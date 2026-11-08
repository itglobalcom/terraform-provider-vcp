package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// vcp_llm_connection driven against the fake: a project is connected once and
// never disconnected, so the resource connects only what is not connected,
// takes an existing connection into state without a call, and refuses at plan
// time the names it can no longer apply.

func connectionPlan(group, key string) connectionModel {
	return connectionModel{
		ID: types.StringNull(), GroupName: types.StringValue(group),
		FirstKeyName: types.StringValue(key), State: types.StringNull(),
	}
}

func createConnection(t *testing.T, api *fakeAPI, plan connectionModel) (connectionModel, resource.CreateResponse) {
	t.Helper()
	res := NewConnectionResource()
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planForCreate(t, s, plan)}, &resp)

	var out connectionModel
	if !resp.State.Raw.IsNull() {
		assertNoUnknowns(t, resp.State)
		readModel(t, resp.State, &out)
	}
	return out, resp
}

// assertProjectFromServices checks that the project ID came from the list of
// the project services and the project card was never read: the resource has
// to work with a key that carries only the LLM permission.
func assertProjectFromServices(t *testing.T, api *fakeAPI) {
	t.Helper()
	if got := api.countExact("GET /api/v1/project"); got != 0 {
		t.Errorf("GET /project called %d times, want none", got)
	}
	if got := api.countExact("GET /api/v1/project/services"); got != 1 {
		t.Errorf("GET /project/services called %d times, want once", got)
	}
}

func TestConnectionCreate(t *testing.T) {
	t.Run("not connected — connects with the planned names and awaits the task", func(t *testing.T) {
		api := newFakeAPI(t)

		state, resp := createConnection(t, api, connectionPlan("default", "first"))
		if resp.Diagnostics.HasError() {
			t.Fatalf("Create failed: %v", resp.Diagnostics)
		}
		body := api.bodyOf(t, "POST /api/v1/llm/connection")
		if !strings.Contains(body, `"group_name":"default"`) || !strings.Contains(body, `"first_key_name":"first"`) {
			t.Errorf("the connection was ordered with %s, want the planned names", body)
		}
		if api.countCalls("GET /api/v1/tasks/llm") == 0 {
			t.Error("the connection task was not awaited")
		}
		if state.ID.ValueString() != "4242" {
			t.Errorf("id = %v, want the project id 4242", state.ID)
		}
		assertProjectFromServices(t, api)
		if state.State.ValueString() != string(entities.LLMConnectionStateConnected) {
			t.Errorf("state = %v, want connected", state.State)
		}
	})

	// A second connect is refused by the API (ServiceAlreadyConnected): the
	// resource has to take what is there instead of failing the apply.
	t.Run("connected — taken into state without a call", func(t *testing.T) {
		api := newFakeAPI(t)
		existingGroup, existingKey := "theirs", "their-key"
		api.connection = entities.LLMConnection{
			State: entities.LLMConnectionStateConnected, GroupName: &existingGroup, FirstKeyName: &existingKey,
		}

		state, resp := createConnection(t, api, connectionPlan("default", "first"))
		if resp.Diagnostics.HasError() {
			t.Fatalf("Create failed: %v", resp.Diagnostics)
		}
		if got := api.mutations(); len(got) != 0 {
			t.Errorf("the provider changed something on a connected project: %v", got)
		}
		if resp.Diagnostics.WarningsCount() != 1 {
			t.Errorf("want one warning that the names were not applied, got %v", resp.Diagnostics)
		}
		// The plan's names, not the API's: anything else is "inconsistent result after apply".
		if state.GroupName.ValueString() != "default" || state.FirstKeyName.ValueString() != "first" {
			t.Errorf("state holds %v/%v, want the planned names", state.GroupName, state.FirstKeyName)
		}
		if state.ID.ValueString() != "4242" || state.State.ValueString() != "connected" {
			t.Errorf("state = %+v, want project 4242, connected", state)
		}
		assertProjectFromServices(t, api)
	})

	t.Run("connecting — refused, nothing sent", func(t *testing.T) {
		api := newFakeAPI(t)
		task := "llm77"
		api.connection = entities.LLMConnection{State: entities.LLMConnectionStateConnecting, TaskID: &task}

		_, resp := createConnection(t, api, connectionPlan("default", "first"))
		if !resp.Diagnostics.HasError() {
			t.Fatal("Create succeeded while another request is connecting the project")
		}
		if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), task) {
			t.Errorf("the error does not name the running task: %v", resp.Diagnostics)
		}
		if got := api.mutations(); len(got) != 0 {
			t.Errorf("the provider sent %v while the project is connecting", got)
		}
	})

	t.Run("project services unreadable — refused, nothing sent", func(t *testing.T) {
		api := newFakeAPI(t)
		api.failOn("GET /project/services", 403, 1)

		_, resp := createConnection(t, api, connectionPlan("default", "first"))
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Error Reading Project" {
			t.Fatalf("want Error Reading Project, got %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("a failed create left a state")
		}
		if got := api.mutations(); len(got) != 0 {
			t.Errorf("the provider sent %v without a project", got)
		}
	})
}

// The names are applied only while connecting: a change after that would be a
// perpetual diff the API can never satisfy.
func TestConnectionModifyPlanRefusesNameChange(t *testing.T) {
	connected := func(group, key types.String) connectionModel {
		return connectionModel{
			ID: types.StringValue("4242"), GroupName: group, FirstKeyName: key,
			State: types.StringValue("connected"),
		}
	}
	str := types.StringValue

	cases := map[string]struct {
		state, plan connectionModel
		wantErrorOn string
	}{
		"group_name changed":     {connected(str("a"), str("k")), connected(str("b"), str("k")), "group_name"},
		"first_key_name changed": {connected(str("a"), str("k")), connected(str("a"), str("other")), "first_key_name"},
		"nothing changed":        {connected(str("a"), str("k")), connected(str("a"), str("k")), ""},
		// An import the API reported without names: the configuration fills them.
		"state without names": {
			connected(types.StringNull(), types.StringNull()), connected(str("a"), str("k")), "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := &connectionResource{}
			s := resourceSchema(t, res)
			resp := resource.ModifyPlanResponse{Plan: planOf(t, s, tc.plan)}
			res.ModifyPlan(context.Background(), resource.ModifyPlanRequest{
				Plan: planOf(t, s, tc.plan), State: stateOf(t, s, tc.state),
			}, &resp)

			if tc.wantErrorOn == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("plan refused: %v", resp.Diagnostics)
				}
				return
			}
			if resp.Diagnostics.ErrorsCount() != 1 {
				t.Fatalf("want one error on %s, got %v", tc.wantErrorOn, resp.Diagnostics)
			}
			if !hasAttributeError(resp.Diagnostics.Errors()[0], path.Root(tc.wantErrorOn)) {
				t.Errorf("the error is not on %s: %v", tc.wantErrorOn, resp.Diagnostics)
			}
		})
	}
}

func TestConnectionRead(t *testing.T) {
	t.Run("not connected — removed from state", func(t *testing.T) {
		api := newFakeAPI(t)
		res := NewConnectionResource()
		configure(t, res, api.client(t))
		s := resourceSchema(t, res)

		prior := stateOf(t, s, connectionModel{
			ID: types.StringValue("4242"), GroupName: types.StringValue("a"),
			FirstKeyName: types.StringValue("k"), State: types.StringValue("connected"),
		})
		resp := resource.ReadResponse{State: prior}
		res.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Read failed: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("a project that is not connected stayed in state")
		}
	})

	t.Run("imported — names taken from the API", func(t *testing.T) {
		api := newFakeAPI(t)
		group, key := "default", "first"
		api.connection = entities.LLMConnection{
			State: entities.LLMConnectionStateConnected, GroupName: &group, FirstKeyName: &key,
		}
		res := NewConnectionResource()
		configure(t, res, api.client(t))
		s := resourceSchema(t, res)

		prior := stateOf(t, s, connectionModel{
			ID: types.StringValue("4242"), GroupName: types.StringNull(),
			FirstKeyName: types.StringNull(), State: types.StringNull(),
		})
		resp := resource.ReadResponse{State: prior}
		res.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
		var out connectionModel
		readModel(t, resp.State, &out)
		if out.GroupName.ValueString() != group || out.FirstKeyName.ValueString() != key || out.State.ValueString() != "connected" {
			t.Errorf("state = %+v, want the names and the state the API reports", out)
		}
	})
}

// The service cannot be disconnected: a destroy that called the API would
// either fail or, worse, take the project's keys down with it.
func TestConnectionDeleteCallsNothing(t *testing.T) {
	api := newFakeAPI(t)
	res := NewConnectionResource()
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	state := stateOf(t, s, connectionModel{
		ID: types.StringValue("4242"), GroupName: types.StringValue("a"),
		FirstKeyName: types.StringValue("k"), State: types.StringValue("connected"),
	})
	resp := resource.DeleteResponse{State: state}
	res.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", resp.Diagnostics)
	}
	if got := api.calls(); len(got) != 0 {
		t.Errorf("Delete called the API: %v", got)
	}
	if resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("want a warning that the service stays connected, got %v", resp.Diagnostics)
	}
}

func TestConnectionImport(t *testing.T) {
	cases := map[string]struct {
		id      string
		wantErr bool
	}{
		"the provider key's project": {"4242", false},
		"another project":            {"4243", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI(t)
			res := NewConnectionResource().(*connectionResource)
			configure(t, res, api.client(t))
			s := resourceSchema(t, res)

			resp := resource.ImportStateResponse{State: emptyState(s)}
			res.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.id}, &resp)

			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("import of %s: errors = %v, want error %v", tc.id, resp.Diagnostics, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			var id types.String
			resp.State.GetAttribute(context.Background(), path.Root("id"), &id)
			if id.ValueString() != tc.id {
				t.Errorf("imported id = %v, want %s", id, tc.id)
			}
			assertProjectFromServices(t, api)
		})
	}

	t.Run("project services unreadable — refused", func(t *testing.T) {
		api := newFakeAPI(t)
		api.failOn("GET /project/services", 403, 1)
		res := NewConnectionResource().(*connectionResource)
		configure(t, res, api.client(t))
		s := resourceSchema(t, res)

		resp := resource.ImportStateResponse{State: emptyState(s)}
		res.ImportState(context.Background(), resource.ImportStateRequest{ID: "4242"}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Error Reading Project" {
			t.Fatalf("want Error Reading Project, got %v", resp.Diagnostics)
		}
	})
}
