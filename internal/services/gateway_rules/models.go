// Package gateway_rules implements the vcp_gateway_nat and vcp_gateway_firewall
// resources — the NAT and firewall rule lists of an edge gateway.
//
// The API has no per-rule endpoints and no rule IDs: PUT replaces the whole
// list ("deletes existing rules and creates passed rules"), so each resource
// owns the complete rule set of one gateway. Field names, values and formats
// mirror the API exactly (no normalisation on the provider side), which keeps
// state byte-identical to what the API returns and leaves no room for drift.
package gateway_rules

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// vcp_gateway_nat
// ============================================================================

type natModel struct {
	ID        types.String   `tfsdk:"id"`
	GatewayID types.String   `tfsdk:"gateway_id"`
	Rules     []natRuleModel `tfsdk:"rules"`
}

type natRuleModel struct {
	Type            types.String `tfsdk:"type"`
	Protocol        types.String `tfsdk:"protocol"`
	Source          types.String `tfsdk:"source"`
	Destination     types.String `tfsdk:"destination"`
	DestinationPort types.Int64  `tfsdk:"destination_port"`
	Translated      types.String `tfsdk:"translated"`
	TranslatedPort  types.Int64  `tfsdk:"translated_port"`
}

func expandNATRules(in []natRuleModel) []entities.NATRule {
	out := make([]entities.NATRule, 0, len(in))
	for _, r := range in {
		out = append(out, entities.NATRule{
			Type:            r.Type.ValueString(),
			Protocol:        r.Protocol.ValueString(),
			Source:          r.Source.ValueString(),
			Destination:     r.Destination.ValueString(),
			DestinationPort: int(r.DestinationPort.ValueInt64()),
			Translated:      r.Translated.ValueString(),
			TranslatedPort:  int(r.TranslatedPort.ValueInt64()),
		})
	}
	return out
}

// flattenNATRules maps the API response back into the model. The result is
// always a non-nil slice: an empty rule list is an empty list in state, never
// null, which is what a configuration with `rules = []` produces.
func flattenNATRules(in []entities.NATRule) []natRuleModel {
	out := make([]natRuleModel, 0, len(in))
	for _, r := range in {
		out = append(out, natRuleModel{
			Type:            types.StringValue(r.Type),
			Protocol:        types.StringValue(r.Protocol),
			Source:          types.StringValue(r.Source),
			Destination:     types.StringValue(r.Destination),
			DestinationPort: types.Int64Value(int64(r.DestinationPort)),
			Translated:      types.StringValue(r.Translated),
			TranslatedPort:  types.Int64Value(int64(r.TranslatedPort)),
		})
	}
	return out
}

// ============================================================================
// vcp_gateway_firewall
// ============================================================================

type firewallModel struct {
	ID        types.String        `tfsdk:"id"`
	GatewayID types.String        `tfsdk:"gateway_id"`
	Rules     []firewallRuleModel `tfsdk:"rules"`
}

type firewallRuleModel struct {
	Action          types.String `tfsdk:"action"`
	Direction       types.String `tfsdk:"direction"`
	Protocol        types.String `tfsdk:"protocol"`
	Source          types.String `tfsdk:"source"`
	SourcePort      types.Int64  `tfsdk:"source_port"`
	Destination     types.String `tfsdk:"destination"`
	DestinationPort types.Int64  `tfsdk:"destination_port"`
}

func expandFirewallRules(in []firewallRuleModel) []entities.FirewallRule {
	out := make([]entities.FirewallRule, 0, len(in))
	for _, r := range in {
		out = append(out, entities.FirewallRule{
			Action:          r.Action.ValueString(),
			Direction:       r.Direction.ValueString(),
			Protocol:        r.Protocol.ValueString(),
			Source:          r.Source.ValueString(),
			SourcePort:      int(r.SourcePort.ValueInt64()),
			Destination:     r.Destination.ValueString(),
			DestinationPort: int(r.DestinationPort.ValueInt64()),
		})
	}
	return out
}

func flattenFirewallRules(in []entities.FirewallRule) []firewallRuleModel {
	out := make([]firewallRuleModel, 0, len(in))
	for _, r := range in {
		out = append(out, firewallRuleModel{
			Action:          types.StringValue(r.Action),
			Direction:       types.StringValue(r.Direction),
			Protocol:        types.StringValue(r.Protocol),
			Source:          types.StringValue(r.Source),
			SourcePort:      types.Int64Value(int64(r.SourcePort)),
			Destination:     types.StringValue(r.Destination),
			DestinationPort: types.Int64Value(int64(r.DestinationPort)),
		})
	}
	return out
}
