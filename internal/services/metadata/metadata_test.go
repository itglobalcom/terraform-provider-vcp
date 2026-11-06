package metadata_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	"github.com/itglobalcom/terraform-provider-vcp/internal/services/metadata"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// ----------------------------------------------------------------------
// project
// ----------------------------------------------------------------------

// TestAccProjectDataSource_basic verifies the project data source exposes the
// account fields (id, balance, currency, state, created).
func TestAccProjectDataSource_basic(t *testing.T) {
	dataSourceName := "data.vcp_project.current"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "vcp_project" "current" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that all required fields are set
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("balance"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("currency"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("state"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("created"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// ----------------------------------------------------------------------
// locations
// ----------------------------------------------------------------------

// TestAccLocationsDataSource_basic verifies the locations list is non-empty and
// elements carry the fields consumers rely on (id, system_volume_min).
func TestAccLocationsDataSource_basic(t *testing.T) {
	dataSourceName := "data.vcp_locations.all"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "vcp_locations" "all" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the locations list is not empty
					acctest.CheckListNotEmpty(dataSourceName, "locations"),

					// Verify that the first element has the required fields
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("locations").AtSliceIndex(0).AtMapKey("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("locations").AtSliceIndex(0).AtMapKey("system_volume_min"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// ----------------------------------------------------------------------
// images
// ----------------------------------------------------------------------

// TestAccImagesDataSource_basic verifies the OS images list is non-empty and
// elements carry id and os_version.
func TestAccImagesDataSource_basic(t *testing.T) {
	dataSourceName := "data.vcp_images.all"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "vcp_images" "all" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the images list is not empty
					acctest.CheckListNotEmpty(dataSourceName, "images"),

					// Verify that the first element has the required fields
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("images").AtSliceIndex(0).AtMapKey("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("images").AtSliceIndex(0).AtMapKey("os_version"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// TestAccApplicationsDataSource_all verifies the unfiltered applications list is
// non-empty and elements carry id, location_id and images.
func TestAccApplicationsDataSource_all(t *testing.T) {
	dataSourceName := "data.vcp_applications.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccApplicationsDataSourceConfig_all(),
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the applications list is not empty (a plain
					// NotNull would pass vacuously for an empty list, and the
					// index-0 checks below would fail with an obscure path error)
					acctest.CheckListNotEmpty(dataSourceName, "applications"),

					// Verify that the first element has all required fields
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("applications").AtSliceIndex(0).AtMapKey("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("applications").AtSliceIndex(0).AtMapKey("location_id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("applications").AtSliceIndex(0).AtMapKey("images"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// TestAccApplicationsDataSource_filteredByLocation verifies the location_id
// filter: the result is non-empty and every application belongs to the location.
func TestAccApplicationsDataSource_filteredByLocation(t *testing.T) {
	dataSourceName := "data.vcp_applications.test"
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccApplicationsDataSourceConfig_filtered(locationID),
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the filter is applied
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID),
					),

					// Verify that the list is not empty — otherwise the
					// per-application location check below passes vacuously
					acctest.CheckListNotEmpty(dataSourceName, "applications"),

					// Custom check: all applications must be from the specified location
					checkAllApplicationsHaveLocation{
						dataSourceAddress:  dataSourceName,
						expectedLocationID: locationID,
					},
				},
			},
		},
	})
}

// checkAllApplicationsHaveLocation - custom state check
type checkAllApplicationsHaveLocation struct {
	dataSourceAddress  string
	expectedLocationID string
}

func (c checkAllApplicationsHaveLocation) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	var resource *tfjson.StateResource
	for _, r := range req.State.Values.RootModule.Resources {
		if r.Address == c.dataSourceAddress {
			resource = r
			break
		}
	}

	if resource == nil {
		resp.Error = fmt.Errorf("Resource not found: %s", c.dataSourceAddress)
		return
	}

	applicationsRaw, ok := resource.AttributeValues["applications"]
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'applications' not found")
		return
	}

	applications, ok := applicationsRaw.([]interface{})
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'applications' is not a list")
		return
	}

	for i, appRaw := range applications {
		app, ok := appRaw.(map[string]interface{})
		if !ok {
			continue
		}

		locationID, _ := app["location_id"].(string)
		if locationID != c.expectedLocationID {
			resp.Error = fmt.Errorf(
				"Application at index %d has location_id %s, expected %s",
				i,
				locationID,
				c.expectedLocationID,
			)
			return
		}
	}
}

func testAccApplicationsDataSourceConfig_all() string {
	return `
data "vcp_applications" "test" {}
`
}

func testAccApplicationsDataSourceConfig_filtered(locationID string) string {
	return fmt.Sprintf(`
data "vcp_applications" "test" {
  location_id = %q
}
`, locationID)
}

// applicationsCatalogJSON is the catalog as the API sends it: one application
// offered a key, with an ordinary, a defaulted, a key and an endpoint parameter,
// and one in the minimal shape, where every optional field is left out.
const applicationsCatalogJSON = `{"applications":[
	{"id":"n8n","location_id":"dc-1","images":["img-ubuntu-24"],"llm_key_enabled":true,
	 "credentials_mode":"ServicePassword",
	 "parameters":[
		{"name":"N8N_ENCRYPTION_KEY","required":true,"secret":true},
		{"name":"N8N_HOST","required":false,"default":"localhost","secret":false},
		{"name":"N8N_LLM_KEY","required":true,"secret":true,"llm_kind":"Key"},
		{"name":"N8N_LLM_ENDPOINT","required":true,"secret":false,"llm_kind":"Endpoint"}
	 ]},
	{"id":"redis","location_id":"dc-1","images":["img-debian-12"],"llm_key_enabled":false,"parameters":[]}
]}`

// readApplications reads vcp_applications against a fake answering the
// catalog route with body.
func readApplications(t *testing.T, body string) metadata.ApplicationsDataSourceModel {
	t.Helper()
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/applications") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	config, err := sdk.NewConfig("test-token", srv.URL)
	if err != nil {
		t.Fatalf("building the SDK config: %v", err)
	}
	client, err := sdk.NewClient(config)
	if err != nil {
		t.Fatalf("building the SDK client: %v", err)
	}

	ds := metadata.NewApplicationsDataSource().(datasource.DataSourceWithConfigure)
	var configureResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("configuring the data source: %v", configureResp.Diagnostics)
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	objectType := s.Type().TerraformType(ctx).(tftypes.Object)
	values := map[string]tftypes.Value{}
	for name, typ := range objectType.AttributeTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}

	resp := datasource.ReadResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(objectType, nil)}}
	ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: tftypes.NewValue(objectType, values)}}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("reading the data source: %v", resp.Diagnostics)
	}

	var got metadata.ApplicationsDataSourceModel
	if diags := resp.State.Get(ctx, &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	return got
}

// TestApplicationsDataSourceMapsParametersAndLLMKey maps the catalog literal
// into state: llm_key_enabled is what decides whether issue_llm_key is
// accepted, the parameter flags must not be swapped, and a missing default or
// llm_kind must be null rather than "", or a config comparing them to null
// would never match.
func TestApplicationsDataSourceMapsParametersAndLLMKey(t *testing.T) {
	got := readApplications(t, applicationsCatalogJSON)

	if len(got.Applications) != 2 {
		t.Fatalf("got %d applications, want 2", len(got.Applications))
	}
	n8n, redis := got.Applications[0], got.Applications[1]

	if !n8n.LLMKeyEnabled.Equal(types.BoolValue(true)) {
		t.Errorf("n8n llm_key_enabled = %s, want true", n8n.LLMKeyEnabled)
	}
	if !redis.LLMKeyEnabled.Equal(types.BoolValue(false)) {
		t.Errorf("redis llm_key_enabled = %s, want false", redis.LLMKeyEnabled)
	}

	want := []metadata.ApplicationParameterModel{
		{Name: types.StringValue("N8N_ENCRYPTION_KEY"), Required: types.BoolValue(true), Secret: types.BoolValue(true), Default: types.StringNull(), LLMKind: types.StringNull()},
		{Name: types.StringValue("N8N_HOST"), Required: types.BoolValue(false), Secret: types.BoolValue(false), Default: types.StringValue("localhost"), LLMKind: types.StringNull()},
		{Name: types.StringValue("N8N_LLM_KEY"), Required: types.BoolValue(true), Secret: types.BoolValue(true), Default: types.StringNull(), LLMKind: types.StringValue("Key")},
		{Name: types.StringValue("N8N_LLM_ENDPOINT"), Required: types.BoolValue(true), Secret: types.BoolValue(false), Default: types.StringNull(), LLMKind: types.StringValue("Endpoint")},
	}
	if len(n8n.Parameters) != len(want) {
		t.Fatalf("n8n has %d parameters, want %d", len(n8n.Parameters), len(want))
	}
	for i, w := range want {
		p := n8n.Parameters[i]
		if !p.Name.Equal(w.Name) || !p.Required.Equal(w.Required) || !p.Secret.Equal(w.Secret) ||
			!p.Default.Equal(w.Default) || !p.LLMKind.Equal(w.LLMKind) {
			t.Errorf("parameter %d = %+v, want %+v", i, p, w)
		}
	}

	if redis.Parameters == nil || len(redis.Parameters) != 0 {
		t.Errorf("redis parameters = %#v, want an empty list", redis.Parameters)
	}
}

// ----------------------------------------------------------------------
// project consumption
// ----------------------------------------------------------------------

// lastMonth returns the first and the last day of the previous month: a closed
// period, so is_final is true and the amounts no longer move between steps.
func lastMonth() (string, string) {
	firstOfThisMonth := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
	from := firstOfThisMonth.AddDate(0, -1, 0)
	to := firstOfThisMonth.AddDate(0, 0, -1)
	return from.Format(time.DateOnly), to.Format(time.DateOnly)
}

// TestAccProjectConsumption_defaultPeriod reads the three data sources without
// a period: from/to must come back resolved, not null, and the currency must be
// the project's own.
func TestAccProjectConsumption_defaultPeriod(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
data "vcp_project" "current" {}
data "vcp_project_consumption" "test" {}
data "vcp_project_consumption_resources" "test" {}
data "vcp_project_consumption_daily" "test" {}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.vcp_project_consumption.test", tfjsonpath.New("from"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.vcp_project_consumption.test", tfjsonpath.New("to"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.vcp_project_consumption.test", tfjsonpath.New("time_zone"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.vcp_project_consumption.test", tfjsonpath.New("total"), knownvalue.NotNull()),
					// The period reaches today, so the last day is still being billed.
					statecheck.ExpectKnownValue("data.vcp_project_consumption.test", tfjsonpath.New("is_final"), knownvalue.Bool(false)),
					statecheck.CompareValuePairs(
						"data.vcp_project_consumption.test", tfjsonpath.New("currency"),
						"data.vcp_project.current", tfjsonpath.New("currency"),
						compare.ValuesSame(),
					),
					statecheck.ExpectKnownValue("data.vcp_project_consumption_resources.test", tfjsonpath.New("resources"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.vcp_project_consumption_daily.test", tfjsonpath.New("days"), knownvalue.NotNull()),
				},
			},
		},
	})
}

// TestAccProjectConsumption_closedPeriod reads last month: the period comes back
// as configured, is_final is true, every day of the month is listed, and the
// per-service and per-day reads agree on the total.
func TestAccProjectConsumption_closedPeriod(t *testing.T) {
	from, to := lastMonth()
	end, _ := time.Parse(time.DateOnly, to)
	days := end.Day()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "vcp_project_consumption" "test" {
  from = %[1]q
  to   = %[2]q
}

data "vcp_project_consumption_resources" "test" {
  from = %[1]q
  to   = %[2]q
}

data "vcp_project_consumption_daily" "test" {
  from = %[1]q
  to   = %[2]q
}
`, from, to),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.vcp_project_consumption.test", tfjsonpath.New("from"), knownvalue.StringExact(from)),
					statecheck.ExpectKnownValue("data.vcp_project_consumption.test", tfjsonpath.New("to"), knownvalue.StringExact(to)),
					statecheck.ExpectKnownValue("data.vcp_project_consumption.test", tfjsonpath.New("is_final"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("data.vcp_project_consumption_daily.test", tfjsonpath.New("days"), knownvalue.ListSizeExact(days)),
					statecheck.ExpectKnownValue(
						"data.vcp_project_consumption_daily.test",
						tfjsonpath.New("days").AtSliceIndex(0).AtMapKey("date"),
						knownvalue.StringExact(from),
					),
					statecheck.CompareValuePairs(
						"data.vcp_project_consumption.test", tfjsonpath.New("total"),
						"data.vcp_project_consumption_daily.test", tfjsonpath.New("total"),
						compare.ValuesSame(),
					),
					statecheck.CompareValuePairs(
						"data.vcp_project_consumption.test", tfjsonpath.New("total"),
						"data.vcp_project_consumption_resources.test", tfjsonpath.New("total"),
						compare.ValuesSame(),
					),
				},
			},
		},
	})
}

// TestAccProjectConsumption_resourcesByService filters the resources by one
// service: the filter stays as configured and every listed resource belongs to it.
func TestAccProjectConsumption_resourcesByService(t *testing.T) {
	dataSourceName := "data.vcp_project_consumption_resources.vstack"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
data "vcp_project_consumption_resources" "vstack" {
  service = "vstack"
}

output "foreign_services" {
  value = [for r in data.vcp_project_consumption_resources.vstack.resources : r.service if r.service != "vstack"]
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("service"), knownvalue.StringExact("vstack")),
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("total"), knownvalue.NotNull()),
					statecheck.ExpectKnownOutputValue("foreign_services", knownvalue.ListSizeExact(0)),
				},
			},
		},
	})
}
