package gateway

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// RESOURCE MODEL (vcp_gateway) — hybrid
// ============================================================================

// gatewayModel — the resource model. The public (WAN) interface is
// intrinsic: bandwidth is a gateway-wide property, required at creation.
// Isolated networks are attached via the vcp_gateway_network_attachment
// resource and are not part of the gateway model.
type gatewayModel struct {
	ID            types.String `tfsdk:"id"`
	LocationID    types.String `tfsdk:"location_id"`
	Name          types.String `tfsdk:"name"`
	Tags          types.Set    `tfsdk:"tags"`
	BandwidthMbps types.Int64  `tfsdk:"bandwidth_mbps"`
	PublicIP      types.String `tfsdk:"public_ip"`
	State         types.String `tfsdk:"state"`
	PoweredOn     types.Bool   `tfsdk:"powered_on"`
	Created       types.String `tfsdk:"created"`
}

// mapGatewayScalars fills in the common scalar fields from the API.
// bandwidth_mbps is user-owned and is set by the calling code (echoed from
// plan/state) so that a Required field doesn't produce an "inconsistent
// result after apply".
func mapGatewayScalars(gw *entities.Gateway) gatewayModel {
	return gatewayModel{
		ID:         types.StringValue(gw.ID),
		LocationID: types.StringValue(gw.LocationID),
		Name:       types.StringValue(gw.Name),
		Tags:       tagsSet(gw),
		PublicIP:   wanIP(gw),
		State:      types.StringValue(gw.State),
		PoweredOn:  types.BoolValue(gw.PoweredOn),
		Created:    types.StringValue(gw.Created),
	}
}

// ============================================================================
// DATA SOURCE MODEL (vcp_gateway / vcp_gateways) — shows ALL NICs (incl. WAN)
// ============================================================================

// gatewayDataModel — a read-only reflection of the gateway: lists all NICs,
// split into isolated/public by IP type (the data source has no config).
type gatewayDataModel struct {
	ID              types.String            `tfsdk:"id"`
	LocationID      types.String            `tfsdk:"location_id"`
	Name            types.String            `tfsdk:"name"`
	Tags            types.Set               `tfsdk:"tags"`
	IsolatedNetNICs []isolatedNICModel      `tfsdk:"isolated_net_nics"`
	PublicNetNICs   []publicNICModel        `tfsdk:"public_net_nics"`
	NATRules        []natRuleDataModel      `tfsdk:"nat_rules"`
	FirewallRules   []firewallRuleDataModel `tfsdk:"firewall_rules"`
	State           types.String            `tfsdk:"state"`
	PoweredOn       types.Bool              `tfsdk:"powered_on"`
	Created         types.String            `tfsdk:"created"`
}

// tagsSet — the set of gateway tags.
func tagsSet(gw *entities.Gateway) types.Set {
	vals := make([]attr.Value, 0, len(gw.Tags))
	for _, tag := range gw.Tags {
		vals = append(vals, types.StringValue(tag))
	}
	return types.SetValueMust(types.StringType, vals)
}

func mapGatewayToDataModel(gw *entities.Gateway) gatewayDataModel {
	iso, pub := flattenGatewayNICs(gw)
	return gatewayDataModel{
		ID:              types.StringValue(gw.ID),
		LocationID:      types.StringValue(gw.LocationID),
		Name:            types.StringValue(gw.Name),
		Tags:            tagsSet(gw),
		IsolatedNetNICs: iso,
		PublicNetNICs:   pub,
		NATRules:        flattenNATRulesData(gw.NATRules),
		FirewallRules:   flattenFirewallRulesData(gw.FirewallRules),
		State:           types.StringValue(gw.State),
		PoweredOn:       types.BoolValue(gw.PoweredOn),
		Created:         types.StringValue(gw.Created),
	}
}
