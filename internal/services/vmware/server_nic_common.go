package vmware

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Helpers shared by vcp_vmware_server_network_attachment and
// vcp_vmware_server_public_interface. Both add an interface to a server through
// an endpoint that answers with the server's whole NIC list rather than with the
// interface it just created, so both have to work out which one is new.

// findVmwareNIC returns the interface with the given id, or nil.
func findVmwareNIC(nics []*entities.VmwareNIC, id int) *entities.VmwareNIC {
	for _, nic := range nics {
		if nic != nil && nic.ID == id {
			return nic
		}
	}
	return nil
}

// findNewVmwareNIC returns the interface that appeared since the `before`
// snapshot and satisfies match, or nil. Comparing against a snapshot is what
// keeps a second attachment to the same network from being mistaken for the
// first one.
func findNewVmwareNIC(before, after []*entities.VmwareNIC, match func(*entities.VmwareNIC) bool) *entities.VmwareNIC {
	known := make(map[int]bool, len(before))
	for _, nic := range before {
		if nic != nil {
			known[nic.ID] = true
		}
	}
	for _, nic := range after {
		if nic == nil || known[nic.ID] || !match(nic) {
			continue
		}
		return nic
	}
	return nil
}

// snapshotVmwareNICs reads the current interfaces of a server. A failure is not
// fatal — it only means a NIC created by a call that then fails to complete
// cannot be recovered, so it is logged rather than reported.
func snapshotVmwareNICs(ctx context.Context, client *sdk.CloudClient, serverID int) []*entities.VmwareNIC {
	nics, err := client.GetVmwareServerNICs(ctx, serverID)
	if err != nil {
		tflog.Warn(ctx, "Could not snapshot server NICs before attaching a new one", map[string]any{
			"server_id": serverID, "error": err.Error(),
		})
		return nil
	}
	return nics
}

// nicRemovalHint explains the one refusal a user can act on: detaching an
// interface from a running machine needs the image to support NIC hot-remove,
// and the API's own message does not say so.
func nicRemovalHint(serverID int) string {
	return fmt.Sprintf("If the server is running, check whether its image supports removing an interface without a "+
		"reboot (nic_hot_remove in vcp_vmware_images). When it does not, power server %d off and apply again.", serverID)
}

// nicIP returns the interface's address as a plain string ("" when unset).
func nicIP(nic *entities.VmwareNIC) string {
	if nic.IP == nil {
		return ""
	}
	return *nic.IP
}
