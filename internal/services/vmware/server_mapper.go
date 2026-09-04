package vmware

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var nicAttrTypes = map[string]attr.Type{
	"id":             types.Int64Type,
	"number":         types.Int64Type,
	"is_primary":     types.BoolType,
	"network_id":     types.Int64Type,
	"ip":             types.StringType,
	"mac":            types.StringType,
	"bandwidth_mbps": types.Int64Type, // SRV-5
}

var gpuAttrTypes = map[string]attr.Type{
	"model_id":   types.Int64Type,
	"vram_mb":    types.Int64Type,
	"card_count": types.Int64Type,
}

// serverModel is shared between the vmware_server resource and data sources.
type serverModel struct {
	ID         types.Int64  `tfsdk:"id"`
	LocationID types.Int64  `tfsdk:"location_id"`
	Name       types.String `tfsdk:"name"`
	// Create/write-only inputs (not all returned by read — preserved by caller).
	ComputerName         types.String `tfsdk:"computer_name"`
	ImageID              types.Int64  `tfsdk:"image_id"`
	CPU                  types.Int64  `tfsdk:"cpu"`
	RamMB                types.Int64  `tfsdk:"ram_mb"`
	SystemDiskMB         types.Int64  `tfsdk:"system_disk_mb"`
	SystemDiskType       types.String `tfsdk:"system_disk_type"`
	PublicNetworkID      types.Int64  `tfsdk:"public_network_id"`
	NetworkBandwidthMbps types.Int64  `tfsdk:"network_bandwidth_mbps"`
	BackupEnabled        types.Bool   `tfsdk:"backup_enabled"`
	BackupPeriod         types.Int64  `tfsdk:"backup_period"`
	SSHKeyIDs            types.Set    `tfsdk:"ssh_key_ids"`
	NeedSysprep          types.Bool   `tfsdk:"need_sysprep"`
	// CopyFromServerID is the source of a copy. Nothing reports that a machine is
	// a copy, so it is never refreshed — the caller preserves it like the other
	// write-only create inputs.
	CopyFromServerID types.Int64  `tfsdk:"copy_from_server_id"`
	Gpu              types.Object `tfsdk:"gpu"`
	// Volumes are the additional data disks; the boot disk lives in
	// SystemDiskMB/SystemDiskType. See server_volumes.go.
	Volumes []serverVolumeModel `tfsdk:"volumes"`
	// Read-only computed fields.
	State            types.String `tfsdk:"state"`
	IsPowerOn        types.Bool   `tfsdk:"is_power_on"`
	VmToolsInstalled types.Bool   `tfsdk:"vm_tools_installed"`
	Nics             types.List   `tfsdk:"nics"`
	Created          types.String `tfsdk:"created"`
}

// mapServerComputed fills the read-only/computed portion of the model from an
// SDK server. Write-only create inputs are left to the caller to preserve.
func mapServerComputed(m *serverModel, s *entities.VmwareServer) {
	m.ID = types.Int64Value(int64(s.ID))
	m.LocationID = types.Int64Value(int64(s.LocationID))
	m.Name = types.StringValue(s.Name)
	m.ImageID = types.Int64Value(int64(s.ImageID))
	m.CPU = types.Int64Value(int64(s.CPU))
	m.RamMB = types.Int64Value(int64(s.RamMB))
	m.SystemDiskMB = types.Int64Value(int64(s.SystemDiskMB))
	m.State = types.StringValue(s.State)
	m.IsPowerOn = types.BoolValue(s.IsPowerOn)
	m.Created = types.StringValue(s.Created)

	// SRV-5: the primary interface reports its bandwidth, so network_bandwidth_mbps
	// is a real reading rather than an echo of the order. A zero means the field
	// was not populated — keep whatever the caller had instead of recording a
	// bandwidth no interface can have.
	if primary := primaryNIC(s.NICs); primary != nil && primary.BandwidthMbps > 0 {
		m.NetworkBandwidthMbps = types.Int64Value(int64(primary.BandwidthMbps))
	}

	if s.ComputerName != nil {
		m.ComputerName = types.StringValue(*s.ComputerName)
	} else {
		m.ComputerName = types.StringNull()
	}
	// system_disk_type is Optional+Computed+RequiresReplace and read may omit it
	// (SDK field is *string,omitempty). Only overwrite from the response when the
	// API actually returned it; otherwise keep the caller's plan/prior value, so a
	// user-set "ssd" is not clobbered to null (which would cause an inconsistent
	// result and a perpetual replace).
	if s.SystemDiskType != nil {
		m.SystemDiskType = types.StringValue(*s.SystemDiskType)
	}
	if s.VmToolsInstalled != nil {
		m.VmToolsInstalled = types.BoolValue(*s.VmToolsInstalled)
	} else {
		m.VmToolsInstalled = types.BoolNull()
	}

	if s.GPU != nil {
		m.Gpu = types.ObjectValueMust(gpuAttrTypes, map[string]attr.Value{
			"model_id":   types.Int64Value(int64(s.GPU.ModelID)),
			"vram_mb":    types.Int64Value(int64(s.GPU.VramMB)),
			"card_count": types.Int64Value(int64(s.GPU.CardCount)),
		})
	} else {
		m.Gpu = types.ObjectNull(gpuAttrTypes)
	}

	nics := make([]attr.Value, 0, len(s.NICs))
	for _, n := range s.NICs {
		ip := types.StringNull()
		if n.IP != nil {
			ip = types.StringValue(*n.IP)
		}
		nics = append(nics, types.ObjectValueMust(nicAttrTypes, map[string]attr.Value{
			"id":             types.Int64Value(int64(n.ID)),
			"number":         types.Int64Value(int64(n.Number)),
			"is_primary":     types.BoolValue(n.IsPrimary),
			"network_id":     types.Int64Value(int64(n.NetworkID)),
			"ip":             ip,
			"mac":            types.StringValue(n.Mac),
			"bandwidth_mbps": types.Int64Value(int64(n.BandwidthMbps)), // SRV-5
		}))
	}
	m.Nics = types.ListValueMust(types.ObjectType{AttrTypes: nicAttrTypes}, nics)
}

// primaryNIC returns the interface a VMware server is always created with — the
// public one the server resource owns. The interfaces added by
// vcp_vmware_server_network_attachment and vcp_vmware_server_public_interface are
// never primary.
func primaryNIC(nics []entities.VmwareNIC) *entities.VmwareNIC {
	for i := range nics {
		if nics[i].IsPrimary {
			return &nics[i]
		}
	}
	return nil
}
