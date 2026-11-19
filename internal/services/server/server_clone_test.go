package vstack_server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// A fake of the part of the vStack API a clone goes through: the clone request,
// the task it starts, and the read of the clone the task reports. It answers the
// shapes the SDK reads — paths, envelopes, task resources — and nothing else.

const (
	cloneSourceID = "l1s1"
	cloneID       = "l1s9"
	cloneTaskID   = "l1t9"
)

type cloneFakeAPI struct {
	*httptest.Server

	mu sync.Mutex
	// requests records every call as "METHOD /path body".
	requests []string
	// refuseClone makes the clone request fail with a 400, as the platform does
	// for a source that is running.
	refuseClone bool
	// clone is what the API answers for the clone once the task has finished.
	clone entities.Server
}

func newCloneFakeAPI(t *testing.T) *cloneFakeAPI {
	t.Helper()
	api := &cloneFakeAPI{clone: entities.Server{
		ID:         cloneID,
		Name:       "test-acc-clone",
		LocationID: "loc-src",
		ImageID:    "img-src",
		CPU:        4,
		RamMB:      8192,
		// Volume ids out of the order the source declared them in, so that a
		// number taken from the id rather than the position shows.
		Volumes: []entities.Volume{
			{ID: 905, ServerID: cloneID, Name: "boot", SizeMB: 30720, Created: "2026-11-10T09:00:00Z"},
			{ID: 903, ServerID: cloneID, Name: "data", SizeMB: 10240, Created: "2026-11-10T09:00:00Z"},
			{ID: 907, ServerID: cloneID, Name: "logs", SizeMB: 20480, Created: "2026-11-10T09:00:00Z"},
		},
		IsPowerOn: true,
		Login:     "root",
		Password:  "secret",
		// The source's keys come along with its disks; the plan of a clone holds
		// none, so state must not take them from here.
		SSHKeyIDs: []int{7},
		State:     entities.ServerStateActive,
		Created:   "2026-11-10T09:00:00Z",
	}}
	api.Server = httptest.NewServer(http.HandlerFunc(api.route))
	t.Cleanup(api.Close)
	return api
}

func (a *cloneFakeAPI) client(t *testing.T) *sdk.CloudClient {
	t.Helper()
	config, err := sdk.NewConfig("test-token", a.URL,
		sdk.WithPollingInterval(time.Millisecond),
		sdk.WithPollingTimeout(5*time.Second),
		sdk.WithMaxRetries(0),
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

func (a *cloneFakeAPI) calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.requests...)
}

func (a *cloneFakeAPI) route(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.requests = append(a.requests, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(body))))
	refuse, clone := a.refuseClone, a.clone
	a.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/servers/"+cloneSourceID+"/clone":
		if refuse {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "the server must be stopped"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": cloneTaskID})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/"+cloneTaskID:
		_ = json.NewEncoder(w).Encode(map[string]any{"task": entities.TaskResponse{
			ID: cloneTaskID, Type: "CloneServer", IsCompleted: entities.TaskStateCompleted,
			Resources: []entities.TaskResource{
				{Type: entities.TaskResourceServer, ID: clone.ID},
			},
		}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/servers/"+clone.ID:
		_ = json.NewEncoder(w).Encode(map[string]any{"server": clone})
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "no such endpoint: " + r.URL.Path})
	}
}

// cloneModel is the configuration of a clone that declares nothing but what a
// clone accepts.
func cloneModel() ServerResourceModel {
	return ServerResourceModel{
		Name:                     types.StringValue("test-acc-clone"),
		CloneFromServerID:        types.StringValue(cloneSourceID),
		Tags:                     types.SetNull(types.StringType),
		Applications:             applicationsNull(),
		ApplicationInstallations: types.ListNull(types.ObjectType{AttrTypes: applicationInstallationAttrTypes}),
	}
}

func objectOf(t *testing.T, s schema.Schema, model ServerResourceModel) tftypes.Value {
	t.Helper()
	ctx := context.Background()
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("building the object: %v", diags)
	}
	return state.Raw
}

// createPlan is the plan the framework hands Create: every Computed attribute
// the configuration leaves null is unknown until the apply settles it.
func createPlan(t *testing.T, s schema.Schema, model ServerResourceModel) tfsdk.Plan {
	t.Helper()
	attributes := map[string]tftypes.Value{}
	if err := objectOf(t, s, model).As(&attributes); err != nil {
		t.Fatalf("reading the plan back: %v", err)
	}
	for name, attribute := range s.Attributes {
		if attribute.IsComputed() && attributes[name].IsNull() {
			attributes[name] = tftypes.NewValue(attributes[name].Type(), tftypes.UnknownValue)
		}
	}
	return tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(context.Background()), attributes)}
}

// createClone runs Create on the plan of a clone against the fake.
func createClone(t *testing.T, api *cloneFakeAPI) resource.CreateResponse {
	t.Helper()
	ctx := context.Background()
	s := serverSchema(t)

	res := NewServerResource()
	cr := resource.ConfigureResponse{}
	res.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: api.client(t)}, &cr)
	if cr.Diagnostics.HasError() {
		t.Fatalf("configuring: %v", cr.Diagnostics)
	}

	resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	res.Create(ctx, resource.CreateRequest{Plan: createPlan(t, s, cloneModel())}, &resp)
	return resp
}

// TestServerCloneCreate pins the clone branch of Create: the platform is asked
// for a clone of the source carrying the name and nothing else — an order sent
// instead would create a server from an image — and state describes the clone
// the task reported, with clone_from_server_id kept from the plan (the API never
// reports it) and volumes numbered by position. What the API cannot report and
// the plan holds null — ssh_key_ids, init_script, the applications — stays
// null, or the apply fails as an inconsistent result.
func TestServerCloneCreate(t *testing.T) {
	ctx := context.Background()
	api := newCloneFakeAPI(t)

	resp := createClone(t, api)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	for _, call := range api.calls() {
		if strings.HasPrefix(call, "POST ") && call != `POST /api/v1/servers/l1s1/clone {"name":"test-acc-clone"}` {
			t.Errorf("unexpected mutating call %q: a clone sends its name to the clone of the source and nothing else", call)
		}
	}
	if !contains(api.calls(), `POST /api/v1/servers/l1s1/clone {"name":"test-acc-clone"}`) {
		t.Errorf("the clone request was not sent; calls: %v", api.calls())
	}

	var got ServerResourceModel
	if diags := resp.State.Get(ctx, &got); diags.HasError() {
		t.Fatalf("reading the state: %v", diags)
	}

	for name, pair := range map[string][2]string{
		"id":                   {got.ID.ValueString(), cloneID},
		"clone_from_server_id": {got.CloneFromServerID.ValueString(), cloneSourceID},
		"name":                 {got.Name.ValueString(), "test-acc-clone"},
		"location_id":          {got.LocationID.ValueString(), "loc-src"},
		"image_id":             {got.ImageID.ValueString(), "img-src"},
		"state":                {got.State.ValueString(), entities.ServerStateActive},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	if got.CPU.ValueInt64() != 4 || got.RamMB.ValueInt64() != 8192 {
		t.Errorf("cpu, ram_mb = %s, %s; want the clone's 4, 8192", got.CPU, got.RamMB)
	}

	wantVolumes := []struct {
		id   int64
		name string
	}{{905, "boot"}, {903, "data"}, {907, "logs"}}
	if len(got.Volumes) != len(wantVolumes) {
		t.Fatalf("volumes: %d, want %d", len(got.Volumes), len(wantVolumes))
	}
	for i, want := range wantVolumes {
		v := got.Volumes[i]
		if v.Number.ValueInt64() != int64(i) || v.ID.ValueInt64() != want.id || v.Name.ValueString() != want.name {
			t.Errorf("volumes[%d] = number %s id %s name %s; want number %d id %d name %s",
				i, v.Number, v.ID, v.Name, i, want.id, want.name)
		}
	}

	if got.SSHKeyIDs != nil {
		t.Errorf("ssh_key_ids = %v, want null: it is create-only and the plan of a clone holds none", got.SSHKeyIDs)
	}
	if !got.InitScript.IsNull() {
		t.Errorf("init_script = %s, want null", got.InitScript)
	}
	if got.ApplicationsIDs != nil {
		t.Errorf("applications_ids = %v, want null", got.ApplicationsIDs)
	}
	if !got.Applications.IsNull() {
		t.Errorf("applications = %s, want null", got.Applications)
	}

	t.Run("refused by the platform", func(t *testing.T) {
		api := newCloneFakeAPI(t)
		api.refuseClone = true

		resp := createClone(t, api)
		if !hasError(resp.Diagnostics, "Error Cloning Server") {
			t.Errorf("errors %v, want Error Cloning Server", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("a refused clone must leave no state behind")
		}
	})

	// An imported server cannot tell where it came from: the API never reports
	// the source, and a value made up here would replace the server on the next
	// plan of a configuration that does not declare it.
	t.Run("import leaves clone_from_server_id null", func(t *testing.T) {
		api := newCloneFakeAPI(t)
		s := serverSchema(t)
		res := NewServerResource()
		cr := resource.ConfigureResponse{}
		res.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: api.client(t)}, &cr)

		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		res.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: cloneID}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("ImportState: %v", resp.Diagnostics)
		}
		var source types.String
		if diags := resp.State.GetAttribute(ctx, path.Root("clone_from_server_id"), &source); diags.HasError() {
			t.Fatalf("reading clone_from_server_id: %v", diags)
		}
		if !source.IsNull() {
			t.Errorf("imported clone_from_server_id = %s, want null", source)
		}
	})
}

// TestServerCloneLeavesNoUnknownState walks the state a clone leaves behind:
// everything the plan of a clone leaves unknown — cpu, ram_mb, volumes and the
// other Optional + Computed arguments — must be settled by the apply, or
// Terraform aborts it with "Provider produced inconsistent result after apply"
// after the clone already exists on the platform.
func TestServerCloneLeavesNoUnknownState(t *testing.T) {
	resp := createClone(t, newCloneFakeAPI(t))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("the clone left no state behind")
	}
	err := tftypes.Walk(resp.State.Raw, func(p *tftypes.AttributePath, value tftypes.Value) (bool, error) {
		if !value.IsKnown() {
			t.Errorf("%s is still unknown after the apply", p)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("walking the state: %v", err)
	}
}

// modifyPlan runs ModifyPlan for a configuration; prior is the state of a server
// that exists, or nil while it is being created.
func modifyPlan(t *testing.T, config ServerResourceModel, prior *ServerResourceModel) resource.ModifyPlanResponse {
	t.Helper()
	ctx := context.Background()
	s := serverSchema(t)

	raw := objectOf(t, s, config)
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if prior != nil {
		state.Raw = objectOf(t, s, *prior)
	}
	plan := tfsdk.Plan{Schema: s, Raw: raw}

	resp := resource.ModifyPlanResponse{Plan: plan}
	NewServerResource().(resource.ResourceWithModifyPlan).ModifyPlan(ctx, resource.ModifyPlanRequest{
		Config: tfsdk.Config{Schema: s, Raw: raw}, Plan: plan, State: state,
	}, &resp)
	return resp
}

func planErrors(resp resource.ModifyPlanResponse) []orderError {
	var got []orderError
	for _, d := range resp.Diagnostics.Errors() {
		e := orderError{summary: d.Summary()}
		if withPath, ok := d.(interface{ Path() path.Path }); ok {
			e.path = withPath.Path().String()
		}
		got = append(got, e)
	}
	return got
}

// TestServerCloneRejectsOrderArguments pins the refusal of every order argument
// beside clone_from_server_id at plan time, on that argument: the clone request
// carries a name only, so an argument let through would be accepted and then
// silently not applied. A source known only at apply is still a clone; an
// order without clone_from_server_id is not touched.
func TestServerCloneRejectsOrderArguments(t *testing.T) {
	rejected := func(arg string) []orderError {
		return []orderError{{summary: "Argument Not Accepted By A Clone", path: arg}}
	}
	cases := map[string]struct {
		edit func(*ServerResourceModel)
		want []orderError
	}{
		"location_id": {edit: func(m *ServerResourceModel) { m.LocationID = types.StringValue("loc-1") }, want: rejected("location_id")},
		"image_id":    {edit: func(m *ServerResourceModel) { m.ImageID = types.StringValue("img-1") }, want: rejected("image_id")},
		"cpu":         {edit: func(m *ServerResourceModel) { m.CPU = types.Int64Value(2) }, want: rejected("cpu")},
		"ram_mb":      {edit: func(m *ServerResourceModel) { m.RamMB = types.Int64Value(2048) }, want: rejected("ram_mb")},
		"volumes": {
			edit: func(m *ServerResourceModel) { m.Volumes = orderModel().Volumes },
			want: rejected("volumes"),
		},
		// An empty list is declared: the argument is there.
		"empty volumes": {edit: func(m *ServerResourceModel) { m.Volumes = []VolumeAttrModel{} }, want: rejected("volumes")},
		"applications_ids": {
			edit: func(m *ServerResourceModel) { m.ApplicationsIDs = []types.String{types.StringValue("app-1")} },
			want: rejected("applications_ids"),
		},
		"applications": {
			edit: func(m *ServerResourceModel) { m.Applications = applicationsList(application(t, "app-1", nil, false)) },
			want: rejected("applications"),
		},
		"init_script": {edit: func(m *ServerResourceModel) { m.InitScript = types.StringValue("#!/bin/sh") }, want: rejected("init_script")},
		"ssh_key_ids": {
			edit: func(m *ServerResourceModel) { m.SSHKeyIDs = []types.Int64{types.Int64Value(7)} },
			want: rejected("ssh_key_ids"),
		},
		"affinity_group_id": {
			edit: func(m *ServerResourceModel) { m.AffinityGroupID = types.StringValue("ag-1") },
			want: rejected("affinity_group_id"),
		},
		"tags": {
			edit: func(m *ServerResourceModel) {
				m.Tags = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("t")})
			},
			want: rejected("tags"),
		},
		"a source known only at apply": {
			edit: func(m *ServerResourceModel) {
				m.CloneFromServerID = types.StringUnknown()
				m.CPU = types.Int64Value(2)
			},
			want: rejected("cpu"),
		},
		"the whole order": {
			edit: func(m *ServerResourceModel) {
				order := orderModel()
				m.LocationID, m.ImageID, m.CPU, m.RamMB, m.Volumes =
					order.LocationID, order.ImageID, order.CPU, order.RamMB, order.Volumes
			},
			want: []orderError{
				{summary: "Argument Not Accepted By A Clone", path: "location_id"},
				{summary: "Argument Not Accepted By A Clone", path: "image_id"},
				{summary: "Argument Not Accepted By A Clone", path: "cpu"},
				{summary: "Argument Not Accepted By A Clone", path: "ram_mb"},
				{summary: "Argument Not Accepted By A Clone", path: "volumes"},
			},
		},
		"an order without clone_from_server_id": {
			edit: func(m *ServerResourceModel) { *m = orderModel() },
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			config := cloneModel()
			tc.edit(&config)

			got := planErrors(modifyPlan(t, config, nil))
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("errors %v, want %v", got, tc.want)
			}
		})
	}
}

// TestServerCloneAlonePlansCleanly: a clone that declares only name and
// clone_from_server_id is neither refused as an order missing its arguments
// (ValidateConfig) nor as a clone with arguments it does not accept
// (ModifyPlan) — whether the source is known or only known at apply.
func TestServerCloneAlonePlansCleanly(t *testing.T) {
	for name, source := range map[string]types.String{
		"known source":          types.StringValue(cloneSourceID),
		"source known at apply": types.StringUnknown(),
	} {
		t.Run(name, func(t *testing.T) {
			config := cloneModel()
			config.CloneFromServerID = source

			if diags := validateModel(t, config).Diagnostics; diags.HasError() {
				t.Errorf("ValidateConfig: %v", diags)
			}
			if diags := modifyPlan(t, config, nil).Diagnostics; diags.HasError() {
				t.Errorf("ModifyPlan: %v", diags)
			}
		})
	}
}

// TestServerCloneStaysResizableAfterCreate: clone_from_server_id stays in the
// configuration for the life of the server, so the refusal holds only while the
// clone is created. Once it exists, cpu, ram_mb and volumes added beside it are
// an ordinary in-place change, and a destroy is not refused at all.
func TestServerCloneStaysResizableAfterCreate(t *testing.T) {
	config := cloneModel()
	config.CPU, config.RamMB = types.Int64Value(8), types.Int64Value(16384)
	config.Volumes = orderModel().Volumes

	prior := cloneModel()
	prior.ID = types.StringValue(cloneID)
	prior.CPU, prior.RamMB = types.Int64Value(4), types.Int64Value(8192)

	if diags := modifyPlan(t, config, &prior).Diagnostics; diags.HasError() {
		t.Errorf("ModifyPlan of an existing clone: %v", diags)
	}

	t.Run("destroy", func(t *testing.T) {
		ctx := context.Background()
		s := serverSchema(t)
		null := tftypes.NewValue(s.Type().TerraformType(ctx), nil)

		resp := resource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: s, Raw: null}}
		NewServerResource().(resource.ResourceWithModifyPlan).ModifyPlan(ctx, resource.ModifyPlanRequest{
			Config: tfsdk.Config{Schema: s, Raw: objectOf(t, s, config)},
			Plan:   tfsdk.Plan{Schema: s, Raw: null},
			State:  tfsdk.State{Schema: s, Raw: objectOf(t, s, prior)},
		}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("ModifyPlan of a destroy: %v", resp.Diagnostics)
		}
	})
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func hasError(diags diag.Diagnostics, summary string) bool {
	for _, d := range diags.Errors() {
		if d.Summary() == summary {
			return true
		}
	}
	return false
}
