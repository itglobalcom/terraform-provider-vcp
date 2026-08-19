package vmware

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// networkModel is shared between the vmware_network resource and data sources.
type networkModel struct {
	ID            types.Int64  `tfsdk:"id"`
	LocationID    types.Int64  `tfsdk:"location_id"`
	Type          types.String `tfsdk:"type"`
	Name          types.String `tfsdk:"name"`
	Address       types.String `tfsdk:"address"`
	Mask          types.Int64  `tfsdk:"mask"`
	Gateway       types.String `tfsdk:"gateway"`
	BandwidthMbps types.Int64  `tfsdk:"bandwidth_mbps"`
	Capacity      types.String `tfsdk:"capacity"`
	EnableDhcp    types.Bool   `tfsdk:"enable_dhcp"`
	IsDhcp        types.Bool   `tfsdk:"is_dhcp"`
	Shared        types.Bool   `tfsdk:"shared"`
	State         types.String `tfsdk:"state"`
	NicsCount     types.Int64  `tfsdk:"nics_count"`
}

// mapNetworkToModel converts an SDK network to the resource/data-source model.
// enableDhcp/capacity are write-only create inputs (not returned by read) and
// are preserved by the caller from plan/state.
func mapNetworkToModel(n *entities.VmwareNetwork) networkModel {
	m := networkModel{
		ID:         types.Int64Value(int64(n.ID)),
		LocationID: types.Int64Value(int64(n.LocationID)),
		Type:       types.StringValue(n.Type),
		Name:       types.StringValue(n.Name),
		State:      types.StringValue(n.State),
		NicsCount:  types.Int64Value(int64(n.NICsCount)),
		Address:    types.StringNull(),
		Mask:       types.Int64Null(),
		Gateway:    types.StringNull(),
		Capacity:   types.StringNull(),
		EnableDhcp: types.BoolNull(),
	}
	if n.Address != nil {
		m.Address = types.StringValue(*n.Address)
	}
	if n.Mask != nil {
		m.Mask = types.Int64Value(int64(*n.Mask))
	}
	if n.Gateway != nil {
		m.Gateway = types.StringValue(*n.Gateway)
	}
	if n.BandwidthMbps != nil {
		m.BandwidthMbps = types.Int64Value(int64(*n.BandwidthMbps))
	} else {
		m.BandwidthMbps = types.Int64Null()
	}
	if n.IsDhcp != nil {
		m.IsDhcp = types.BoolValue(*n.IsDhcp)
	} else {
		m.IsDhcp = types.BoolNull()
	}
	if n.Shared != nil {
		m.Shared = types.BoolValue(*n.Shared)
	} else {
		m.Shared = types.BoolNull()
	}
	return m
}
