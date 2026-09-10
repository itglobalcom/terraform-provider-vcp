package vmware

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// The singleton snapshot driven against the fake API. What matters here is the
// "no snapshot" answer — 200 with an empty body — and the refusal of a second
// snapshot: reaching either on a live stand means ordering a machine and then
// putting it into the wrong state on purpose.

func createSnapshot(t *testing.T, api *fakeAPI, serverID int64, name string) (serverSnapshotModel, resource.CreateResponse) {
	t.Helper()
	res := &serverSnapshotResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	plan := serverSnapshotModel{
		ID:       types.Int64Unknown(),
		ServerID: types.Int64Value(serverID),
		Name:     types.StringValue(name),
		Created:  types.StringUnknown(),
	}
	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)

	var out serverSnapshotModel
	if !resp.State.Raw.IsNull() {
		readModel(t, resp.State, &out)
	}
	return out, resp
}

func readSnapshot(t *testing.T, api *fakeAPI, state serverSnapshotModel) resource.ReadResponse {
	t.Helper()
	res := &serverSnapshotResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	resp := resource.ReadResponse{State: stateOf(t, s, state)}
	res.Read(context.Background(), resource.ReadRequest{State: stateOf(t, s, state)}, &resp)
	return resp
}

func deleteSnapshot(t *testing.T, api *fakeAPI, state serverSnapshotModel) resource.DeleteResponse {
	t.Helper()
	res := &serverSnapshotResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	resp := resource.DeleteResponse{State: stateOf(t, s, state)}
	res.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, s, state)}, &resp)
	return resp
}

// snapshotState is the state a created snapshot leaves behind, for the reads and
// deletes below.
func snapshotState(serverID int64, name string) serverSnapshotModel {
	return serverSnapshotModel{
		ID:       types.Int64Value(serverID),
		ServerID: types.Int64Value(serverID),
		Name:     types.StringValue(name),
		Created:  types.StringValue("2026-09-04T10:00:00Z"),
	}
}

// A first apply takes the snapshot, and the resource is keyed by its server: the
// API gives the snapshot no id of its own.
func TestVmwareServerSnapshotCreate(t *testing.T) {
	api := newFakeAPI(t)
	api.addServer(5678, "web")

	state, resp := createSnapshot(t, api, 5678, "before-upgrade")
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	if state.ID.ValueInt64() != 5678 {
		t.Errorf("id = %v, want the server id", state.ID)
	}
	if got := state.Name.ValueString(); got != "before-upgrade" {
		t.Errorf("name = %q, want the name that was asked for", got)
	}
	if state.Created.IsNull() || state.Created.IsUnknown() {
		t.Error("created was left unknown; the snapshot is read back after the task")
	}
	if api.snapshots[5678] == nil {
		t.Fatal("the API holds no snapshot: the provider recorded one it never took")
	}
	if got := api.countCalls("POST /api/v1/vmware/servers/5678/snapshot"); got != 1 {
		t.Errorf("made %d create calls, want exactly 1", got)
	}
}

// A server that already holds a snapshot is refused before anything is sent: the
// platform allows one, and its own error names neither the existing snapshot nor
// a way out.
func TestVmwareServerSnapshotCreateRefusesSecond(t *testing.T) {
	api := newFakeAPI(t)
	api.addServer(5678, "web")
	api.snapshots[5678] = &entities.VmwareSnapshot{Name: "taken-in-the-panel", Created: "2026-09-01T08:00:00Z"}

	_, resp := createSnapshot(t, api, 5678, "second")
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create succeeded on a server that already holds a snapshot")
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(detail, "taken-in-the-panel") {
		t.Errorf("the error does not name the existing snapshot: %s", detail)
	}
	if !strings.Contains(detail, "terraform import") {
		t.Errorf("the error does not offer the way out (import): %s", detail)
	}
	if got := api.countCalls("POST /api/v1/vmware/servers/5678/snapshot"); got != 0 {
		t.Errorf("made %d create calls after refusing; the request must not be sent at all", got)
	}
	if api.snapshots[5678].Name != "taken-in-the-panel" {
		t.Error("the existing snapshot was replaced; a refusal must leave it alone")
	}
}

// The read a live stand answers with: a server with no snapshot returns 200 and
// the body `{}` — the Public API drops null fields, so there is no "snapshot"
// key rather than a null one. A Read that treated that as a failure would keep a
// deleted snapshot in state forever.
func TestVmwareServerSnapshotReadEmptyBodyRemovesResource(t *testing.T) {
	api := newFakeAPI(t)
	api.addServer(5678, "web")

	resp := readSnapshot(t, api, snapshotState(5678, "gone"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read failed on a server with no snapshot: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("the snapshot is gone from the platform but stayed in state")
	}
}

// A deleted server answers 404, which means the same thing here — its snapshot
// went with it.
func TestVmwareServerSnapshotReadMissingServerRemovesResource(t *testing.T) {
	api := newFakeAPI(t)

	resp := readSnapshot(t, api, snapshotState(4242, "gone"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read failed on a deleted server: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("the server is gone but its snapshot stayed in state")
	}
}

// A snapshot deleted in the panel makes the destroy a no-op rather than an
// error: the delete answers 404, and there is nothing left to remove.
func TestVmwareServerSnapshotDeleteAlreadyGone(t *testing.T) {
	api := newFakeAPI(t)
	api.addServer(5678, "web")

	resp := deleteSnapshot(t, api, snapshotState(5678, "gone"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete failed on a snapshot that was already gone: %v", resp.Diagnostics)
	}
}

// The whole round trip: taken, read back, deleted, and the platform left with no
// snapshot.
func TestVmwareServerSnapshotLifecycle(t *testing.T) {
	api := newFakeAPI(t)
	api.addServer(5678, "web")

	state, createResp := createSnapshot(t, api, 5678, "checkpoint")
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", createResp.Diagnostics)
	}

	readResp := readSnapshot(t, api, state)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}
	var refreshed serverSnapshotModel
	readModel(t, readResp.State, &refreshed)
	if refreshed.Name.ValueString() != "checkpoint" {
		t.Errorf("the refreshed name is %q, want %q", refreshed.Name.ValueString(), "checkpoint")
	}

	if resp := deleteSnapshot(t, api, state); resp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", resp.Diagnostics)
	}
	if api.snapshots[5678] != nil {
		t.Error("the snapshot is still on the platform after a destroy")
	}
}
