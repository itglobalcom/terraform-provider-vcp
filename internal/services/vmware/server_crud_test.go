package vmware

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The server resource driven against the fake API for the one setting that is
// both an order option and an editable one: nested virtualization.
//
// What an acceptance run proves here costs a machine and several minutes per
// step, and it cannot see the request at all — only its effect. These tests check
// the call itself: that the order carries the flag, that switching it afterwards
// is one POST and no replacement, and above all that an apply which changes
// nothing sends nothing, because this endpoint power-cycles a running guest.

// createServer runs a Create and returns the state it left behind.
func createServer(t *testing.T, api *fakeAPI, plan serverModel) (serverModel, resource.CreateResponse) {
	t.Helper()
	res := &serverResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)

	var out serverModel
	if !resp.State.Raw.IsNull() {
		readModel(t, resp.State, &out)
	}
	return out, resp
}

// updateServer runs an Update from prior state to a plan.
func updateServer(t *testing.T, api *fakeAPI, state, plan serverModel) (serverModel, resource.UpdateResponse) {
	t.Helper()
	res := &serverResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	resp := resource.UpdateResponse{State: emptyState(s)}
	res.Update(context.Background(), resource.UpdateRequest{
		Plan: planOf(t, s, plan), State: stateOf(t, s, state),
	}, &resp)

	var out serverModel
	if !resp.State.Raw.IsNull() {
		readModel(t, resp.State, &out)
	}
	return out, resp
}

// serverAt turns the fake's server into the state Terraform would hold for it.
func serverAt(id int64, enabled bool) serverModel {
	model := nestedHypervisorModel(enabled)
	model.ID = types.Int64Value(id)
	return model
}

// The order carries the attribute only when it was asked for: the platform's
// default is off, and an order that always sent a value would take the choice
// away from the caller — and, for a `false` it invented, would send a field to an
// API that need never have seen it.
func TestServerCreateSendsNestedHypervisor(t *testing.T) {
	cases := map[string]struct {
		planned  types.Bool
		wantSent *bool
	}{
		"asked for":     {types.BoolValue(true), boolPtr(true)},
		"asked against": {types.BoolValue(false), boolPtr(false)},
		// Optional+Computed: an attribute left out of the configuration reaches
		// Create unknown, not null.
		"left out": {types.BoolUnknown(), nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI(t)

			plan := nestedHypervisorModel(false)
			plan.ID = types.Int64Unknown()
			plan.NestedHypervisor = tc.planned

			state, resp := createServer(t, api, plan)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Create failed: %v", resp.Diagnostics)
			}

			if len(api.serverOrders) != 1 {
				t.Fatalf("the provider sent %d orders, want 1", len(api.serverOrders))
			}
			sent := api.serverOrders[0].NestedHypervisor
			switch {
			case tc.wantSent == nil && sent != nil:
				t.Errorf("the order carried nested_hypervisor=%v; an attribute the configuration "+
					"does not mention must be left out", *sent)
			case tc.wantSent != nil && sent == nil:
				t.Error("the order left nested_hypervisor out; it was asked for")
			case tc.wantSent != nil && *sent != *tc.wantSent:
				t.Errorf("the order carried nested_hypervisor=%v, want %v", *sent, *tc.wantSent)
			}

			// State is the machine's reading, not an echo of the plan: a server
			// ordered without the attribute has to end up with the platform's answer
			// rather than staying unknown.
			want := tc.wantSent != nil && *tc.wantSent
			if state.NestedHypervisor.IsNull() || state.NestedHypervisor.IsUnknown() {
				t.Fatalf("state holds nested_hypervisor=%v, want the value read back from the machine",
					state.NestedHypervisor)
			}
			if got := state.NestedHypervisor.ValueBool(); got != want {
				t.Errorf("state holds nested_hypervisor=%v, want %v", got, want)
			}
		})
	}
}

// Switching the setting is an edit of the machine that is already there: one POST
// to the action the change calls for, and the state that comes out is what the
// API then reports — checked against the fake rather than against the provider's
// own bookkeeping, which would agree with itself even if nothing had been sent.
func TestServerUpdateSwitchesNestedHypervisorInPlace(t *testing.T) {
	const serverID = 5678

	cases := map[string]struct {
		from, to bool
		wantCall string
	}{
		"switched on":  {false, true, "POST /api/v1/vmware/servers/5678/nested-hypervisor/enable"},
		"switched off": {true, false, "POST /api/v1/vmware/servers/5678/nested-hypervisor/disable"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI(t)
			api.addServerWithNestedHypervisor(serverID, "web-01", tc.from)

			state, resp := updateServer(t, api,
				serverAt(serverID, tc.from), serverAt(serverID, tc.to))
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update failed: %v", resp.Diagnostics)
			}

			if got := api.countCalls(tc.wantCall); got != 1 {
				t.Errorf("made %d calls to %s, want exactly 1 (calls: %v)", got, tc.wantCall, api.calls())
			}
			// The machine is edited, not reordered: an Update that replaced it would
			// show up here as an order or a delete.
			if len(api.serverOrders) != 0 {
				t.Error("the Update ordered a new machine; the setting is edited in place")
			}
			if got := api.countCalls("DELETE"); got != 0 {
				t.Errorf("the Update deleted something (%d calls); the setting is edited in place", got)
			}

			if got := api.nestedHypervisorOf(serverID); got != tc.to {
				t.Errorf("the API holds nested_hypervisor=%v, want %v", got, tc.to)
			}
			if got := state.NestedHypervisor.ValueBool(); got != tc.to {
				t.Errorf("state holds nested_hypervisor=%v, want %v", got, tc.to)
			}
		})
	}
}

// An apply that does not change the setting must not touch the endpoint at all.
// The switch is a saga that powers a running guest off and back on, so a spurious
// call is not a wasted request — it is an unannounced reboot of somebody's server.
func TestServerUpdateLeavesNestedHypervisorAlone(t *testing.T) {
	const serverID = 5678

	t.Run("nothing changed", func(t *testing.T) {
		api := newFakeAPI(t)
		api.addServerWithNestedHypervisor(serverID, "web-01", true)

		// Something else changed, so the Update has work to do: backup_period is a
		// write-only order option, which is why changing it reaches no endpoint of
		// its own and leaves this test about one call only.
		state := serverAt(serverID, true)
		plan := serverAt(serverID, true)
		plan.BackupPeriod = types.Int64Value(7)

		if _, resp := updateServer(t, api, state, plan); resp.Diagnostics.HasError() {
			t.Fatalf("Update failed: %v", resp.Diagnostics)
		}
		if got := api.countCalls("nested-hypervisor"); got != 0 {
			t.Errorf("made %d calls to the nested-hypervisor endpoint, want none: %v", got, api.calls())
		}
	})

	// An attribute dropped from the configuration arrives unknown, and "I no longer
	// say" is not "switch it off".
	t.Run("dropped from the configuration", func(t *testing.T) {
		api := newFakeAPI(t)
		api.addServerWithNestedHypervisor(serverID, "web-01", true)

		plan := serverAt(serverID, true)
		plan.NestedHypervisor = types.BoolUnknown()

		state, resp := updateServer(t, api, serverAt(serverID, true), plan)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Update failed: %v", resp.Diagnostics)
		}
		if got := api.countCalls("nested-hypervisor"); got != 0 {
			t.Errorf("made %d calls to the nested-hypervisor endpoint, want none: %v", got, api.calls())
		}
		if !state.NestedHypervisor.ValueBool() {
			t.Error("state lost the setting the machine still has")
		}
	})
}

// The API answers a request that matches the current state with 200 and no task
// id — there is nothing to do. That is the shape of a drift the platform resolved
// on its own (somebody switched it in the panel to what Terraform was about to
// ask for), and awaiting a task that does not exist would hang the apply.
func TestServerUpdateAcceptsNestedHypervisorAlreadyInPlace(t *testing.T) {
	const serverID = 5678

	api := newFakeAPI(t)
	api.addServerWithNestedHypervisor(serverID, "web-01", true)

	// State says off, the machine says on, the plan asks for on.
	state, resp := updateServer(t, api, serverAt(serverID, false), serverAt(serverID, true))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update failed on an idempotent switch: %v", resp.Diagnostics)
	}
	if got := api.countCalls("nested-hypervisor/enable"); got != 1 {
		t.Errorf("made %d calls to the enable endpoint, want 1", got)
	}
	if !state.NestedHypervisor.ValueBool() {
		t.Error("state did not record the setting the machine reports")
	}
}
