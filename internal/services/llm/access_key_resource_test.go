package llm

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// vcp_llm_access_key driven against the fake. The API hands the secret out
// once, so the resource takes it right after the issue and never asks again;
// a key whose secret was lost is kept, not destroyed or failed.

func keyPlan(group int64, name string) accessKeyModel {
	return accessKeyModel{
		ID: types.Int64Null(), GroupID: types.Int64Value(group), Name: types.StringValue(name),
		Tags: stringSet(nil), Locked: types.BoolValue(false), LockReason: types.StringNull(),
		Secret: types.StringNull(), State: types.StringNull(),
	}
}

func keyAt(group, id int64, locked bool, reason types.String) accessKeyModel {
	return accessKeyModel{
		ID: types.Int64Value(id), GroupID: types.Int64Value(group), Name: types.StringValue("ci"),
		Tags: stringSet(nil), Locked: types.BoolValue(locked), LockReason: reason,
		Secret: types.StringValue("sk-kept"), State: types.StringValue("active"),
	}
}

func createKey(t *testing.T, api *fakeAPI, plan accessKeyModel) (accessKeyModel, resource.CreateResponse) {
	t.Helper()
	res := NewAccessKeyResource()
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planForCreate(t, s, plan)}, &resp)

	var out accessKeyModel
	if !resp.State.Raw.IsNull() {
		assertNoUnknowns(t, resp.State)
		readModel(t, resp.State, &out)
	}
	return out, resp
}

func TestAccessKeyCreateTakesSecretOnce(t *testing.T) {
	api := newFakeAPI(t)
	api.addGroup(10, "team", entities.LLMGroupStateActive)

	plan := keyPlan(10, "ci")
	plan.Locked = types.BoolValue(true)
	plan.LockReason = types.StringValue("rotation")
	state, resp := createKey(t, api, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	if got := api.countCalls("/secret"); got != 1 {
		t.Errorf("asked for the secret %d times, want exactly 1: %v", got, api.calls())
	}
	if state.ID.ValueInt64() != 701 || state.Secret.ValueString() != "sk-701" {
		t.Errorf("state holds key %v with secret %v, want 701 with the secret the API handed out",
			state.ID, state.Secret)
	}
	// The lock came after the issue, with the planned reason, and state reads it back.
	if body := api.bodyOf(t, "/access-keys/701/lock"); !strings.Contains(body, `"reason":"rotation"`) {
		t.Errorf("locked with %q, want the planned reason", body)
	}
	if !state.Locked.ValueBool() || state.LockReason.ValueString() != "rotation" {
		t.Errorf("state locked=%v reason=%v, want true/rotation", state.Locked, state.LockReason)
	}
}

// A failure between the issue and the reveal leaves a live key whose secret is
// gone for good: failing the apply would orphan it outside the state.
func TestAccessKeyCreateWithoutSecret(t *testing.T) {
	api := newFakeAPI(t)
	api.addGroup(10, "team", entities.LLMGroupStateActive)
	api.failOn("POST /secret", http.StatusBadRequest, 1)

	state, resp := createKey(t, api, keyPlan(10, "ci"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed, the issued key would be lost to the state: %v", resp.Diagnostics)
	}
	if resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("want one warning about the missing secret, got %v", resp.Diagnostics)
	}
	if state.ID.ValueInt64() != 701 {
		t.Errorf("state holds key %v, want the issued 701", state.ID)
	}
	if !state.Secret.IsNull() {
		t.Errorf("secret = %v, want null", state.Secret)
	}
	if got := api.countCalls("/secret"); got != 1 {
		t.Errorf("asked for the secret %d times, want 1 — a retry is refused as already revealed", got)
	}
}

func TestAccessKeyImport(t *testing.T) {
	cases := map[string]struct {
		id         string
		group, key int64
		wantErr    bool
	}{
		"group/key":     {id: "10/701", group: 10, key: 701},
		"key only":      {id: "701", wantErr: true},
		"not numeric":   {id: "team/ci", wantErr: true},
		"zero group":    {id: "0/701", wantErr: true},
		"trailing part": {id: "10/701/x", wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := NewAccessKeyResource().(*accessKeyResource)
			s := resourceSchema(t, res)
			resp := resource.ImportStateResponse{State: emptyState(s)}
			res.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.id}, &resp)

			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("import of %q: %v, want error %v", tc.id, resp.Diagnostics, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			var group, key types.Int64
			var secret types.String
			ctx := context.Background()
			resp.State.GetAttribute(ctx, path.Root("group_id"), &group)
			resp.State.GetAttribute(ctx, path.Root("id"), &key)
			resp.State.GetAttribute(ctx, path.Root("secret"), &secret)
			if group.ValueInt64() != tc.group || key.ValueInt64() != tc.key {
				t.Errorf("imported %v/%v, want %d/%d", group, key, tc.group, tc.key)
			}
			if !secret.IsNull() {
				t.Errorf("an imported key has secret %v, want null", secret)
			}
		})
	}
}

// The API refuses to lock a locked key, so every move of the client lock is
// checked by the calls it makes, in order.
func TestAccessKeyUpdateMovesLock(t *testing.T) {
	reason := types.StringValue
	cases := map[string]struct {
		fromLocked bool
		fromReason types.String
		toLocked   bool
		toReason   types.String
		want       []string
	}{
		"lock":   {false, types.StringNull(), true, reason("audit"), []string{"lock"}},
		"unlock": {true, reason("audit"), false, types.StringNull(), []string{"unlock"}},
		"new reason on a locked key": {
			true, reason("audit"), true, reason("rotation"), []string{"unlock", "lock"},
		},
		"unchanged": {true, reason("audit"), true, reason("audit"), nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI(t)
			api.addGroup(10, "team", entities.LLMGroupStateActive)
			locks := &entities.LLMAccessKeyLocks{ByClient: tc.fromLocked}
			if tc.fromLocked {
				r := tc.fromReason.ValueString()
				locks.ClientReason = &r
			}
			api.addKey(701, 10, "ci", locks)

			res := NewAccessKeyResource()
			configure(t, res, api.client(t))
			s := resourceSchema(t, res)

			prior := keyAt(10, 701, tc.fromLocked, tc.fromReason)
			plan := keyAt(10, 701, tc.toLocked, tc.toReason)
			plan.Tags = stringSet([]string{"changed"}) // something reaches Update even when the lock does not move
			resp := resource.UpdateResponse{State: emptyState(s)}
			res.Update(context.Background(), resource.UpdateRequest{
				Plan: planOf(t, s, plan), State: stateOf(t, s, prior),
			}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update failed: %v", resp.Diagnostics)
			}

			var got []string
			for _, call := range api.mutations() {
				switch call {
				case "POST /api/v1/llm/groups/10/access-keys/701/lock":
					got = append(got, "lock")
				case "POST /api/v1/llm/groups/10/access-keys/701/unlock":
					got = append(got, "unlock")
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("lock calls %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("lock calls %v, want %v", got, tc.want)
				}
			}

			var state accessKeyModel
			readModel(t, resp.State, &state)
			if state.Locked.ValueBool() != tc.toLocked || !state.LockReason.Equal(tc.toReason) {
				t.Errorf("state locked=%v reason=%v, want %v/%v",
					state.Locked, state.LockReason, tc.toLocked, tc.toReason)
			}
			if state.Secret.ValueString() != "sk-kept" {
				t.Errorf("the Update lost the secret: %v", state.Secret)
			}
		})
	}
}

// A reason without a lock is refused at plan time, on lock_reason, instead of
// being silently dropped by the apply.
func TestAccessKeyValidateConfigLockReason(t *testing.T) {
	cases := map[string]struct {
		locked  types.Bool
		reason  types.String
		wantErr bool
	}{
		"reason without lock":    {types.BoolValue(false), types.StringValue("audit"), true},
		"reason, locked omitted": {types.BoolNull(), types.StringValue("audit"), true},
		"reason with lock":       {types.BoolValue(true), types.StringValue("audit"), false},
		"lock without reason":    {types.BoolValue(true), types.StringNull(), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := &accessKeyResource{}
			s := resourceSchema(t, res)
			config := keyPlan(10, "ci")
			config.Tags = types.SetNull(types.StringType)
			config.Locked, config.LockReason = tc.locked, tc.reason

			resp := resource.ValidateConfigResponse{}
			res.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: configOf(t, s, config)}, &resp)

			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("errors = %v, want error %v", resp.Diagnostics, tc.wantErr)
			}
			if tc.wantErr && !hasAttributeError(resp.Diagnostics.Errors()[0], path.Root("lock_reason")) {
				t.Errorf("the error is not on lock_reason: %v", resp.Diagnostics)
			}
		})
	}
}

func TestAccessKeyRead(t *testing.T) {
	t.Run("removed — dropped from state", func(t *testing.T) {
		api := newFakeAPI(t)
		api.addGroup(10, "team", entities.LLMGroupStateActive)
		api.addKey(701, 10, "ci", &entities.LLMAccessKeyLocks{})
		api.keys[701].state = entities.LLMAccessKeyStateRemoved

		res := NewAccessKeyResource()
		configure(t, res, api.client(t))
		s := resourceSchema(t, res)
		prior := stateOf(t, s, keyAt(10, 701, false, types.StringNull()))
		resp := resource.ReadResponse{State: prior}
		res.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Read failed: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("a removed key stayed in state")
		}
	})

	// The API never hands the secret out again: a Read that wrote what the API
	// says would blank it on the next refresh.
	t.Run("active — keeps the secret, reads the lock", func(t *testing.T) {
		api := newFakeAPI(t)
		api.addGroup(10, "team", entities.LLMGroupStateActive)
		reason := "by hand"
		api.addKey(701, 10, "ci", &entities.LLMAccessKeyLocks{ByClient: true, ClientReason: &reason})

		res := NewAccessKeyResource()
		configure(t, res, api.client(t))
		s := resourceSchema(t, res)
		prior := stateOf(t, s, keyAt(10, 701, false, types.StringNull()))
		resp := resource.ReadResponse{State: prior}
		res.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)

		var state accessKeyModel
		readModel(t, resp.State, &state)
		if state.Secret.ValueString() != "sk-kept" {
			t.Errorf("secret = %v, want the one in state", state.Secret)
		}
		if !state.Locked.ValueBool() || state.LockReason.ValueString() != reason {
			t.Errorf("locked=%v reason=%v, want the client lock out of band", state.Locked, state.LockReason)
		}
	})
}
