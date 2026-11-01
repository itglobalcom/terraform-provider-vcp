package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// consumptionFake answers the three consumption routes with fixed wire bodies —
// JSON as the API sends it, fields without a value left out rather than null —
// and records the query of every call.
type consumptionFake struct {
	*httptest.Server

	mu      sync.Mutex
	bodies  map[string]string
	queries map[string][]url.Values
}

func newConsumptionFake(t *testing.T, bodies map[string]string) *consumptionFake {
	t.Helper()
	f := &consumptionFake{bodies: bodies, queries: map[string][]url.Values{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.route))
	t.Cleanup(f.Close)
	return f
}

func (f *consumptionFake) route(w http.ResponseWriter, r *http.Request) {
	for suffix, body := range f.bodies {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, suffix) {
			f.mu.Lock()
			f.queries[suffix] = append(f.queries[suffix], r.URL.Query())
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
	}
	http.NotFound(w, r)
}

func (f *consumptionFake) calls(suffix string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.queries[suffix]...)
}

func (f *consumptionFake) client(t *testing.T) *sdk.CloudClient {
	t.Helper()
	config, err := sdk.NewConfig("test-token", f.URL,
		sdk.WithPollingInterval(time.Millisecond),
		sdk.WithPollingTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("building the SDK config: %v", err)
	}
	client, err := sdk.NewClient(config)
	if err != nil {
		t.Fatalf("building the SDK client: %v", err)
	}
	return client
}

// readConsumption drives a data source against the fake with the given
// configuration attributes (unset ones stay null) and returns the Read response.
func readConsumption(t *testing.T, f *consumptionFake, ctor func() datasource.DataSource, config map[string]string) datasource.ReadResponse {
	t.Helper()
	ctx := context.Background()

	ds, ok := ctor().(datasource.DataSourceWithConfigure)
	if !ok {
		t.Fatal("the data source cannot be configured")
	}
	configureResp := datasource.ConfigureResponse{}
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: f.client(t)}, &configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("configuring the data source: %v", configureResp.Diagnostics)
	}

	schemaResp := datasource.SchemaResponse{}
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("reading the schema: %v", schemaResp.Diagnostics)
	}
	s := schemaResp.Schema
	objectType, ok := s.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatal("the schema is not an object")
	}

	values := map[string]tftypes.Value{}
	for name, typ := range objectType.AttributeTypes {
		if v, set := config[name]; set {
			values[name] = tftypes.NewValue(typ, v)
		} else {
			values[name] = tftypes.NewValue(typ, nil)
		}
	}
	raw := tftypes.NewValue(objectType, values)

	resp := datasource.ReadResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(objectType, nil)}}
	ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: raw}}, &resp)
	return resp
}

func getState(t *testing.T, resp datasource.ReadResponse, into any) {
	t.Helper()
	if resp.Diagnostics.HasError() {
		t.Fatalf("reading the data source: %v", resp.Diagnostics)
	}
	if diags := resp.State.Get(context.Background(), into); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
}

const consumptionEnvelopeJSON = `"from":"2026-10-01","to":"2026-10-31","time_zone":"Europe/Moscow",` +
	`"currency":"RUB","is_final":true,"total":1500.25`

func checkEnvelope(t *testing.T, e consumptionEnvelope) {
	t.Helper()
	if e.From.ValueString() != "2026-10-01" || e.To.ValueString() != "2026-10-31" ||
		e.TimeZone.ValueString() != "Europe/Moscow" || e.Currency.ValueString() != "RUB" ||
		!e.IsFinal.ValueBool() || e.Total.ValueFloat64() != 1500.25 {
		t.Errorf("the envelope did not come through: %+v", e)
	}
}

// A resource the API cannot address arrives without "id", and one without a
// name of its own without "name": both must land in state as null, not as an
// empty string a configuration would compare against.
func TestProjectConsumptionResourcesReadLeavesMissingIDAndNameNull(t *testing.T) {
	f := newConsumptionFake(t, map[string]string{
		"/project/consumption/resources": `{"consumption":{` + consumptionEnvelopeJSON + `,"resources":[` +
			`{"service":"vstack","type":"vstack_server","id":"l1s42","name":"web-1","total":1000.25},` +
			`{"service":"vstack","type":"vstack_edge","total":500}]}}`,
	})

	resp := readConsumption(t, f, NewProjectConsumptionResourcesDataSource, map[string]string{
		"from": "2026-10-01", "to": "2026-10-31", "service": "vstack",
	})
	var state projectConsumptionResourcesModel
	getState(t, resp, &state)

	checkEnvelope(t, state.consumptionEnvelope)
	if state.Service.ValueString() != "vstack" {
		t.Errorf("service = %v, want the configured vstack", state.Service)
	}
	if len(state.Resources) != 2 {
		t.Fatalf("read %d resources, want 2: %+v", len(state.Resources), state.Resources)
	}
	named := state.Resources[0]
	if named.Service.ValueString() != "vstack" || named.Type.ValueString() != "vstack_server" ||
		named.ID.ValueString() != "l1s42" || named.Name.ValueString() != "web-1" || named.Total.ValueFloat64() != 1000.25 {
		t.Errorf("resources[0] did not come through: %+v", named)
	}
	bare := state.Resources[1]
	if !bare.ID.IsNull() || !bare.Name.IsNull() {
		t.Errorf("resources[1] id = %v, name = %v, want both null", bare.ID, bare.Name)
	}
	if bare.Type.ValueString() != "vstack_edge" || bare.Total.ValueFloat64() != 500 {
		t.Errorf("resources[1] did not come through: %+v", bare)
	}

	calls := f.calls("/project/consumption/resources")
	if len(calls) != 1 {
		t.Fatalf("made %d calls, want 1", len(calls))
	}
	q := calls[0]
	if q.Get("from") != "2026-10-01" || q.Get("to") != "2026-10-31" || q.Get("service") != "vstack" {
		t.Errorf("query = %v, want the configured period and service", q)
	}
}

// Unset service reads every service: a filter sent as an empty string would be
// a 400 from the API.
func TestProjectConsumptionResourcesReadWithoutServiceSendsNoFilter(t *testing.T) {
	f := newConsumptionFake(t, map[string]string{
		"/project/consumption/resources": `{"consumption":{` + consumptionEnvelopeJSON + `,"resources":[]}}`,
	})

	resp := readConsumption(t, f, NewProjectConsumptionResourcesDataSource, nil)
	var state projectConsumptionResourcesModel
	getState(t, resp, &state)

	if !state.Service.IsNull() {
		t.Errorf("service = %v, want null as configured", state.Service)
	}
	if state.Resources == nil || len(state.Resources) != 0 {
		t.Errorf("resources = %#v, want an empty list", state.Resources)
	}
	calls := f.calls("/project/consumption/resources")
	if len(calls) != 1 || len(calls[0]) != 0 {
		t.Errorf("calls = %v, want one call without query", calls)
	}
}

// An unset period takes the API defaults, and the period the API resolved is
// what from/to hold afterwards: a Read that left them null would hide which
// month the totals belong to.
func TestProjectConsumptionReadFillsTheResolvedPeriod(t *testing.T) {
	f := newConsumptionFake(t, map[string]string{
		"/project/consumption": `{"consumption":{` + consumptionEnvelopeJSON + `,"services":[` +
			`{"service":"vstack","total":1000.25},{"service":"vmware","total":500}]}}`,
	})

	resp := readConsumption(t, f, NewProjectConsumptionDataSource, nil)
	var state projectConsumptionModel
	getState(t, resp, &state)

	checkEnvelope(t, state.consumptionEnvelope)
	if len(state.Services) != 2 ||
		state.Services[0].Service.ValueString() != "vstack" || state.Services[0].Total.ValueFloat64() != 1000.25 ||
		state.Services[1].Service.ValueString() != "vmware" || state.Services[1].Total.ValueFloat64() != 500 {
		t.Errorf("services did not come through: %+v", state.Services)
	}
	calls := f.calls("/project/consumption")
	if len(calls) != 1 || len(calls[0]) != 0 {
		t.Errorf("calls = %v, want one call without query", calls)
	}
}

func TestProjectConsumptionDailyReadMapsDays(t *testing.T) {
	f := newConsumptionFake(t, map[string]string{
		"/project/consumption/daily": `{"consumption":{` + consumptionEnvelopeJSON + `,"days":[` +
			`{"date":"2026-10-01","total":0},{"date":"2026-10-02","total":1500.25}]}}`,
	})

	resp := readConsumption(t, f, NewProjectConsumptionDailyDataSource, map[string]string{"from": "2026-10-01"})
	var state projectConsumptionDailyModel
	getState(t, resp, &state)

	checkEnvelope(t, state.consumptionEnvelope)
	if len(state.Days) != 2 ||
		state.Days[0].Date.ValueString() != "2026-10-01" || state.Days[0].Total.IsNull() || state.Days[0].Total.ValueFloat64() != 0 ||
		state.Days[1].Date.ValueString() != "2026-10-02" || state.Days[1].Total.ValueFloat64() != 1500.25 {
		t.Errorf("days did not come through: %+v", state.Days)
	}
	calls := f.calls("/project/consumption/daily")
	if len(calls) != 1 || calls[0].Get("from") != "2026-10-01" || calls[0].Has("to") {
		t.Errorf("calls = %v, want one call with from only", calls)
	}
}

// A period the configuration alone shows to be wrong is refused in Read, before
// any call reaches the API.
func TestProjectConsumptionReadRefusesInvalidPeriod(t *testing.T) {
	cases := map[string]map[string]string{
		"to before from":   {"from": "2026-10-31", "to": "2026-10-01"},
		"not a YYYY-MM-DD": {"from": "01.10.2026"},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			f := newConsumptionFake(t, map[string]string{
				"/project/consumption/daily": `{"consumption":{` + consumptionEnvelopeJSON + `,"days":[]}}`,
			})
			resp := readConsumption(t, f, NewProjectConsumptionDailyDataSource, config)
			if !resp.Diagnostics.HasError() {
				t.Fatal("the Read accepted the period")
			}
			if calls := f.calls("/project/consumption/daily"); len(calls) != 0 {
				t.Errorf("made %d calls, want none", len(calls))
			}
		})
	}
}

// from and to are Optional + Computed: the API resolves an unset period, and a
// plain Optional attribute could not hold the value it resolved to. service is
// only echoed, so it stays plain Optional; everything else is read-only.
func TestProjectConsumptionSchemas(t *testing.T) {
	ctx := context.Background()
	for _, ctor := range []func() datasource.DataSource{
		NewProjectConsumptionDataSource,
		NewProjectConsumptionResourcesDataSource,
		NewProjectConsumptionDailyDataSource,
	} {
		ds := ctor()
		meta := datasource.MetadataResponse{}
		ds.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "vcp"}, &meta)
		t.Run(meta.TypeName, func(t *testing.T) {
			resp := datasource.SchemaResponse{}
			ds.Schema(ctx, datasource.SchemaRequest{}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("schema: %v", resp.Diagnostics)
			}
			if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
				t.Fatalf("schema implementation: %v", diags)
			}
			for name, a := range resp.Schema.Attributes {
				if a.GetMarkdownDescription() == "" {
					t.Errorf("%s has no description, and the description is the documentation", name)
				}
				switch name {
				case "from", "to":
					if !a.IsOptional() || !a.IsComputed() {
						t.Errorf("%s: optional=%v computed=%v, want both", name, a.IsOptional(), a.IsComputed())
					}
				case "service":
					if !a.IsOptional() || a.IsComputed() {
						t.Errorf("service: optional=%v computed=%v, want optional only", a.IsOptional(), a.IsComputed())
					}
				default:
					if a.IsOptional() || a.IsRequired() || !a.IsComputed() {
						t.Errorf("%s must be read-only", name)
					}
				}
			}
		})
	}
}
