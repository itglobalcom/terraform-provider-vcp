package vmware

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Creating a server as a copy of another one. The interesting halves are the
// plan-time checks — a copy carries a name and nothing else, so an order-time
// argument beside it would be accepted and ignored — and the branch in Create,
// which on a live stand costs a machine and some minutes per run.

// emptyServerModel is a model with every attribute null. The collection
// attributes carry their element types even when null, so the model can be
// written into a plan or a state.
func emptyServerModel() serverModel {
	return serverModel{
		Gpu:       types.ObjectNull(gpuAttrTypes),
		SSHKeyIDs: types.SetNull(types.Int64Type),
		Nics:      types.ListNull(types.ObjectType{AttrTypes: nicAttrTypes}),
	}
}

// orderedServerModel is a configuration that orders a machine from an image.
func orderedServerModel(name string) serverModel {
	m := emptyServerModel()
	m.LocationID = types.Int64Value(7)
	m.Name = types.StringValue(name)
	m.ImageID = types.Int64Value(42)
	m.CPU = types.Int64Value(2)
	m.RamMB = types.Int64Value(4096)
	m.SystemDiskMB = types.Int64Value(51200)
	return m
}

// copyServerModel is a configuration that copies an existing machine.
func copyServerModel(sourceID int64, name string) serverModel {
	m := emptyServerModel()
	m.Name = types.StringValue(name)
	m.CopyFromServerID = types.Int64Value(sourceID)
	return m
}

// addSpecServer registers a source machine with a full specification, so a copy
// of it can be checked to have inherited one.
func addSpecServer(api *fakeAPI, id int, name string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.servers[id] = &entities.VmwareServer{
		ID: id, Name: name, LocationID: 7, ImageID: 42,
		CPU: 2, RamMB: 4096, SystemDiskMB: 51200,
		State: entities.VmwareServerStateActive,
	}
}

func validateServerConfig(t *testing.T, model serverModel) resource.ValidateConfigResponse {
	t.Helper()
	res := &serverResource{}
	s := resourceSchema(t, res)

	resp := resource.ValidateConfigResponse{}
	res.ValidateConfig(context.Background(), resource.ValidateConfigRequest{
		Config: configOf(t, s, model),
	}, &resp)
	return resp
}

// modifyServerPlan runs ModifyPlan. A nil prior means the machine is being
// created; otherwise it already exists.
func modifyServerPlan(t *testing.T, config serverModel, prior *serverModel) resource.ModifyPlanResponse {
	t.Helper()
	res := &serverResource{}
	s := resourceSchema(t, res)

	state := tfsdk.State{Schema: s, Raw: emptyValue(context.Background(), s)}
	if prior != nil {
		state = stateOf(t, s, *prior)
	}

	resp := resource.ModifyPlanResponse{}
	res.ModifyPlan(context.Background(), resource.ModifyPlanRequest{
		Config: configOf(t, s, config),
		Plan:   planOf(t, s, config),
		State:  state,
	}, &resp)
	return resp
}

// A copy takes the source's specification and the name it was asked for, and the
// resource ends up pointing at the new machine — not at the source.
func TestVmwareServerCopyCreate(t *testing.T) {
	api := newFakeAPI(t)
	addSpecServer(api, 5678, "web")

	res := &serverResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	plan := copyServerModel(5678, "web-clone")
	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	var state serverModel
	readModel(t, resp.State, &state)

	if state.ID.ValueInt64() == 5678 {
		t.Error("the resource kept the source's id: a copy is a different machine")
	}
	if got := state.Name.ValueString(); got != "web-clone" {
		t.Errorf("name = %q, want the name the copy was asked for", got)
	}
	// The specification is the source's, read back rather than guessed: without
	// Optional+Computed on these, a copy could not be described at all.
	if state.CPU.ValueInt64() != 2 || state.RamMB.ValueInt64() != 4096 || state.SystemDiskMB.ValueInt64() != 51200 {
		t.Errorf("the copy has cpu=%v ram_mb=%v system_disk_mb=%v, want the source's 2/4096/51200",
			state.CPU, state.RamMB, state.SystemDiskMB)
	}
	if state.LocationID.ValueInt64() != 7 || state.ImageID.ValueInt64() != 42 {
		t.Errorf("the copy has location_id=%v image_id=%v, want the source's 7/42", state.LocationID, state.ImageID)
	}
	// Nothing reports that a machine is a copy, so the attribute has to survive
	// the write-back or the next plan would offer to replace the machine.
	if state.CopyFromServerID.ValueInt64() != 5678 {
		t.Errorf("copy_from_server_id = %v, want it preserved as 5678", state.CopyFromServerID)
	}
	if got := api.countCalls("POST /api/v1/vmware/servers/5678/copy"); got != 1 {
		t.Errorf("made %d copy calls, want exactly 1", got)
	}
	if got := api.countCalls("POST /api/v1/vmware/servers"); got != 1 {
		t.Errorf("made %d POSTs under /servers; a copy must not also order a machine", got)
	}
}

// An order-time argument beside copy_from_server_id is refused at plan time
// rather than sent and ignored — and the message says how to get it applied.
func TestVmwareServerCopyRejectsOrderArguments(t *testing.T) {
	cases := map[string]func(*serverModel){
		"cpu":               func(m *serverModel) { m.CPU = types.Int64Value(4) },
		"image_id":          func(m *serverModel) { m.ImageID = types.Int64Value(42) },
		"location_id":       func(m *serverModel) { m.LocationID = types.Int64Value(7) },
		"system_disk_mb":    func(m *serverModel) { m.SystemDiskMB = types.Int64Value(51200) },
		"computer_name":     func(m *serverModel) { m.ComputerName = types.StringValue("HOST01") },
		"need_sysprep":      func(m *serverModel) { m.NeedSysprep = types.BoolValue(true) },
		"public_network_id": func(m *serverModel) { m.PublicNetworkID = types.Int64Value(11) },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			config := copyServerModel(5678, "web-clone")
			set(&config)

			resp := modifyServerPlan(t, config, nil)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("%s was accepted beside copy_from_server_id; the platform would ignore it", name)
			}
			detail := resp.Diagnostics.Errors()[0].Detail()
			if !strings.Contains(detail, name) {
				t.Errorf("the error does not name %s: %s", name, detail)
			}
		})
	}
}

// A copy on its own is what the plan accepts.
func TestVmwareServerCopyAlonePlansCleanly(t *testing.T) {
	if resp := modifyServerPlan(t, copyServerModel(5678, "web-clone"), nil); resp.Diagnostics.HasError() {
		t.Fatalf("a plain copy was refused: %v", resp.Diagnostics)
	}
	if resp := validateServerConfig(t, copyServerModel(5678, "web-clone")); resp.Diagnostics.HasError() {
		t.Fatalf("a plain copy was refused by ValidateConfig: %v", resp.Diagnostics)
	}
}

// Once the copy exists it is an ordinary server: the create-time restriction must
// not outlive the create, or a copied machine could never be resized —
// copy_from_server_id stays in the configuration for its whole life, because
// removing it would replace the machine.
func TestVmwareServerCopyStaysResizableAfterCreate(t *testing.T) {
	prior := copyServerModel(5678, "web-clone")
	prior.ID = types.Int64Value(5679)
	prior.LocationID = types.Int64Value(7)
	prior.ImageID = types.Int64Value(42)
	prior.CPU = types.Int64Value(2)
	prior.RamMB = types.Int64Value(4096)
	prior.SystemDiskMB = types.Int64Value(51200)

	config := copyServerModel(5678, "web-clone")
	config.CPU = types.Int64Value(4)

	if resp := modifyServerPlan(t, config, &prior); resp.Diagnostics.HasError() {
		t.Fatalf("resizing an existing copy was refused: %v", resp.Diagnostics)
	}
}

// The order path keeps the guarantee Required used to give: the five arguments a
// server cannot be ordered without are reported at plan time, one by one, when
// the configuration is not a copy.
func TestVmwareServerOrderRequiresItsArguments(t *testing.T) {
	if resp := validateServerConfig(t, orderedServerModel("web")); resp.Diagnostics.HasError() {
		t.Fatalf("a complete order was refused: %v", resp.Diagnostics)
	}

	for _, name := range []string{"location_id", "image_id", "cpu", "ram_mb", "system_disk_mb"} {
		t.Run(name, func(t *testing.T) {
			config := orderedServerModel("web")
			switch name {
			case "location_id":
				config.LocationID = types.Int64Null()
			case "image_id":
				config.ImageID = types.Int64Null()
			case "cpu":
				config.CPU = types.Int64Null()
			case "ram_mb":
				config.RamMB = types.Int64Null()
			case "system_disk_mb":
				config.SystemDiskMB = types.Int64Null()
			}

			resp := validateServerConfig(t, config)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("a server was ordered without %s", name)
			}
			if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, name) {
				t.Errorf("the error does not name %s: %s", name, detail)
			}
		})
	}
}
