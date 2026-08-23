package vmware

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Reading a server's nested virtualization back out of the API answer.
//
// A field forgotten in a mapper is silent: nothing fails to compile, nothing
// errors, and the attribute simply arrives null — which reads as "off" on a
// machine that has it on, and makes a plan offer to switch on what is already
// switched on. The resource and both data sources are three separate maps of the
// same server, so all three are checked.

// A bool is the case where "forgotten" and "false" look alike, so both values are
// asserted: a mapper that never touched the field would pass a test that only
// looked at the off case.
func TestMapServerComputedReadsNestedHypervisor(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		// Start from the opposite value: the field has to be written on every read,
		// or a switch made in the panel is echoed back from the previous state
		// instead of showing up as drift.
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

// The data source model is filled in field by field, so the attribute has to be
// named in three places — the model, the schema and this mapper — and the one
// that is easiest to miss is the mapper, because missing it costs nothing until a
// user reads the attribute and gets null.
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
		// The fields either side of it, so a test that passed on a model filled with
		// zero values would be noticed.
		if model.ID.ValueInt64() != 5678 || model.Name.ValueString() != "web-01" {
			t.Errorf("the rest of the server did not come through: %+v", model)
		}
	}
}
