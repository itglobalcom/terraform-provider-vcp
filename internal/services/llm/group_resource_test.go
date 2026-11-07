package llm

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// vcp_llm_group driven against the fake: the model set is applied whole in one
// operation, a group disabled out of band stays readable, and a destroy
// disables only what is still active.

func int64Set(values ...int64) types.Set {
	elems := make([]attr.Value, len(values))
	for i, v := range values {
		elems[i] = types.Int64Value(v)
	}
	return types.SetValueMust(types.Int64Type, elems)
}

func groupAt(id int64, name string, models types.Set, tags types.Set) groupModel {
	return groupModel{
		ID: types.Int64Value(id), Name: types.StringValue(name), ModelIDs: models, Tags: tags,
		State: types.StringValue("active"),
	}
}

func modelIDsOf(t *testing.T, set types.Set) []int64 {
	t.Helper()
	var out []int64
	if diags := set.ElementsAs(context.Background(), &out, false); diags.HasError() {
		t.Fatalf("reading model_ids: %v", diags)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestGroupCreate(t *testing.T) {
	api := newFakeAPI(t)
	res := NewGroupResource()
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	plan := groupModel{
		ID: types.Int64Null(), Name: types.StringValue("team"),
		ModelIDs: int64Set(3, 5), Tags: stringSet([]string{"prod"}), State: types.StringNull(),
	}
	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planForCreate(t, s, plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}
	assertNoUnknowns(t, resp.State)

	var state groupModel
	readModel(t, resp.State, &state)
	// The id comes from the task's resources[], not from the plan.
	if state.ID.ValueInt64() != 701 {
		t.Errorf("id = %v, want 701, the group the task reported", state.ID)
	}
	if got := modelIDsOf(t, state.ModelIDs); len(got) != 2 || got[0] != 3 || got[1] != 5 {
		t.Errorf("model_ids = %v, want [3 5] read back from the API", got)
	}
	if got := api.groups[701].tags; len(got) != 1 || got[0] != "prod" {
		t.Errorf("the API holds tags %v, want [prod]", got)
	}
	if state.State.ValueString() != "active" {
		t.Errorf("state = %v, want active", state.State)
	}
}

// Granting one model and revoking another is one replacement of the whole set:
// a per-model call would leave the group half-changed when the second fails.
func TestGroupUpdateSetsModelsInOneOperation(t *testing.T) {
	api := newFakeAPI(t)
	api.addGroup(701, "team", entities.LLMGroupStateActive, 3, 5)
	res := NewGroupResource()
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	empty := stringSet(nil)
	prior := groupAt(701, "team", int64Set(3, 5), empty)
	plan := groupAt(701, "team", int64Set(5, 8, 9), empty)

	resp := resource.UpdateResponse{State: emptyState(s)}
	res.Update(context.Background(), resource.UpdateRequest{
		Plan: planOf(t, s, plan), State: stateOf(t, s, prior),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", resp.Diagnostics)
	}

	if got := api.mutations(); len(got) != 1 || got[0] != "PUT /api/v1/llm/groups/701/models" {
		t.Fatalf("the Update sent %v, want exactly one PUT of the models", got)
	}
	body := api.bodyOf(t, "PUT /api/v1/llm/groups/701/models")
	if !strings.Contains(body, `"model_ids":[5,8,9]`) {
		t.Errorf("the models were set with %s, want the whole planned set [5,8,9]", body)
	}
	var state groupModel
	readModel(t, resp.State, &state)
	if got := modelIDsOf(t, state.ModelIDs); len(got) != 3 || got[0] != 5 || got[2] != 9 {
		t.Errorf("state model_ids = %v, want [5 8 9]", got)
	}
}

func TestGroupRead(t *testing.T) {
	t.Run("disabled out of band — stays with state disabled", func(t *testing.T) {
		api := newFakeAPI(t)
		api.addGroup(701, "team", entities.LLMGroupStateDisabled, 3)
		res := NewGroupResource()
		configure(t, res, api.client(t))
		s := resourceSchema(t, res)

		prior := stateOf(t, s, groupAt(701, "team", int64Set(3), stringSet(nil)))
		resp := resource.ReadResponse{State: prior}
		res.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Read failed: %v", resp.Diagnostics)
		}
		var state groupModel
		readModel(t, resp.State, &state)
		if state.State.ValueString() != "disabled" || state.ID.ValueInt64() != 701 {
			t.Errorf("state = %+v, want group 701 kept with state disabled", state)
		}
	})

	t.Run("gone — removed from state", func(t *testing.T) {
		api := newFakeAPI(t)
		res := NewGroupResource()
		configure(t, res, api.client(t))
		s := resourceSchema(t, res)

		prior := stateOf(t, s, groupAt(701, "team", int64Set(3), stringSet(nil)))
		resp := resource.ReadResponse{State: prior}
		res.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Read failed: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("a group the API does not know stayed in state")
		}
	})
}

// Disabling cannot be undone and the API refuses it twice: a destroy of a group
// already disabled must not call it.
func TestGroupDelete(t *testing.T) {
	cases := map[string]struct {
		state    entities.LLMGroupState
		wantCall bool
	}{
		"active":   {entities.LLMGroupStateActive, true},
		"disabled": {entities.LLMGroupStateDisabled, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI(t)
			api.addGroup(701, "team", tc.state)
			res := NewGroupResource()
			configure(t, res, api.client(t))
			s := resourceSchema(t, res)

			state := stateOf(t, s, groupAt(701, "team", int64Set(), stringSet(nil)))
			resp := resource.DeleteResponse{State: state}
			res.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete failed: %v", resp.Diagnostics)
			}

			got := api.countCalls("POST /api/v1/llm/groups/701/disable")
			if tc.wantCall && got != 1 {
				t.Errorf("made %d disable calls, want 1", got)
			}
			if !tc.wantCall && got != 0 {
				t.Errorf("disabled an already disabled group (%d calls)", got)
			}
			if api.groups[701].state != entities.LLMGroupStateDisabled {
				t.Errorf("the API holds the group %s, want disabled", api.groups[701].state)
			}
		})
	}
}

// Tags go after the models: a refused tag set fails the apply with the group
// still in state.
func TestGroupUpdateTagsRefused(t *testing.T) {
	api := newFakeAPI(t)
	api.addGroup(701, "team", entities.LLMGroupStateActive, 3)
	api.failOn("PUT /tags", http.StatusBadRequest, 1)
	res := NewGroupResource()
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	prior := groupAt(701, "team", int64Set(3), stringSet(nil))
	plan := groupAt(701, "team", int64Set(3), stringSet([]string{"prod"}))
	resp := resource.UpdateResponse{State: emptyState(s)}
	res.Update(context.Background(), resource.UpdateRequest{
		Plan: planOf(t, s, plan), State: stateOf(t, s, prior),
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatalf("Update passed with the tags refused: %v", resp.Diagnostics)
	}
	var state groupModel
	readModel(t, resp.State, &state)
	if state.ID.ValueInt64() != 701 {
		t.Errorf("state holds group %v, want 701 kept", state.ID)
	}
}

func TestGroupImport(t *testing.T) {
	cases := map[string]struct {
		id      string
		want    int64
		wantErr bool
	}{
		"numeric":     {id: "701", want: 701},
		"not numeric": {id: "team", wantErr: true},
		"zero":        {id: "0", wantErr: true},
		"negative":    {id: "-1", wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := NewGroupResource().(*groupResource)
			s := resourceSchema(t, res)
			resp := resource.ImportStateResponse{State: emptyState(s)}
			res.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.id}, &resp)

			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("import of %q: %v, want error %v", tc.id, resp.Diagnostics, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			var id types.Int64
			resp.State.GetAttribute(context.Background(), path.Root("id"), &id)
			if id.ValueInt64() != tc.want {
				t.Errorf("imported %v, want %d", id, tc.want)
			}
		})
	}
}
