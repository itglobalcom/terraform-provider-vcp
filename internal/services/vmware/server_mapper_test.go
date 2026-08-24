package vmware

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// A field forgotten in a mapper is silent: the attribute arrives null, which
// reads as "off" on a machine that has it on. The resource and both data
// sources map the same server separately, so all three are checked.

// Both values are asserted: a mapper that never touched the field would pass on
// the off case alone.
func TestMapServerComputedReadsNestedHypervisor(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		// Start from the opposite value: the field must be rewritten on every
		// read, or a panel switch is echoed back from the previous state.
		model := serverModel{NestedHypervisor: types.BoolValue(!enabled)}
		mapServerComputed(&model, &entities.VmwareServer{
			ID: 5678, Name: "web-01", State: entities.VmwareServerStateActive,
			NestedHypervisor: enabled,
		})

		if model.NestedHypervisor.IsNull() || model.NestedHypervisor.IsUnknown() {
			t.Fatalf("nested_hypervisor = %v, want the value the API reported (%v) — "+
				"the mapper never wrote it", model.NestedHypervisor, enabled)
		}
		if got := model.NestedHypervisor.ValueBool(); got != enabled {
			t.Errorf("nested_hypervisor = %v, want %v", got, enabled)
		}
	}
}

// The data source model is filled in field by field; a field missed in the
// mapper costs nothing until a user reads the attribute and gets null.
func TestMapServerToDSModelReadsNestedHypervisor(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		model := mapServerToDSModel(&entities.VmwareServer{
			ID: 5678, Name: "web-01", State: entities.VmwareServerStateActive,
			NestedHypervisor: enabled,
		})

		if model.NestedHypervisor.IsNull() || model.NestedHypervisor.IsUnknown() {
			t.Fatalf("data.vcp_vmware_server.nested_hypervisor = %v, want %v — "+
				"mapServerToDSModel does not copy the field", model.NestedHypervisor, enabled)
		}
		if got := model.NestedHypervisor.ValueBool(); got != enabled {
			t.Errorf("data.vcp_vmware_server.nested_hypervisor = %v, want %v", got, enabled)
		}
		// Neighbouring fields: catches a model filled with zero values.
		if model.ID.ValueInt64() != 5678 || model.Name.ValueString() != "web-01" {
			t.Errorf("the rest of the server did not come through: %+v", model)
		}
	}
}
