package gateway

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// NAT / FIREWALL RULES — read-only reflection for the data sources.
// The rules themselves are managed by the vcp_gateway_nat and
// vcp_gateway_firewall resources; here they are exposed so that a gateway not
// managed by Terraform can still be inspected (audit, or a look before import).
// GET /gateways/{id} already returns both lists, so this costs no extra call.
// ============================================================================

type natRuleDataModel struct {
	Type            types.String `tfsdk:"type"`
	Protocol        types.String `tfsdk:"protocol"`
	Source          types.String `tfsdk:"source"`
	Destination     types.String `tfsdk:"destination"`
	DestinationPort types.Int64  `tfsdk:"destination_port"`
	Translated      types.String `tfsdk:"translated"`
	TranslatedPort  types.Int64  `tfsdk:"translated_port"`
}

type firewallRuleDataModel struct {
	Action          types.String `tfsdk:"action"`
	Direction       types.String `tfsdk:"direction"`
	Protocol        types.String `tfsdk:"protocol"`
	Source          types.String `tfsdk:"source"`
	SourcePort      types.Int64  `tfsdk:"source_port"`
	Destination     types.String `tfsdk:"destination"`
	DestinationPort types.Int64  `tfsdk:"destination_port"`
}

func natRulesDataSchema() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: "NAT rules of the gateway, in the order the API stores them (which rule wins when " +
			"several of them match is not specified by the API documentation).",
		Computed: true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"type":             schema.StringAttribute{Computed: true, MarkdownDescription: "Rule type (SNAT, DNAT, BINAT)."},
				"protocol":         schema.StringAttribute{Computed: true, MarkdownDescription: "Protocol (TCP, UDP, ICMP, IP)."},
				"source":           schema.StringAttribute{Computed: true, MarkdownDescription: "Source in CIDR notation."},
				"destination":      schema.StringAttribute{Computed: true, MarkdownDescription: "Destination in CIDR notation."},
				"destination_port": schema.Int64Attribute{Computed: true, MarkdownDescription: "Destination port (0 = any)."},
				"translated":       schema.StringAttribute{Computed: true, MarkdownDescription: "Address the traffic is translated to."},
				"translated_port":  schema.Int64Attribute{Computed: true, MarkdownDescription: "Port after translation (0 = any)."},
			},
		},
	}
}

func firewallRulesDataSchema() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: "Firewall rules of the gateway, in evaluation order — the **last** matching rule wins.",
		Computed:            true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"action":           schema.StringAttribute{Computed: true, MarkdownDescription: "Action (Allow, Deny)."},
				"direction":        schema.StringAttribute{Computed: true, MarkdownDescription: "Direction (In, Out)."},
				"protocol":         schema.StringAttribute{Computed: true, MarkdownDescription: "Protocol (TCP, UDP, ICMP, IP)."},
				"source":           schema.StringAttribute{Computed: true, MarkdownDescription: "Source in CIDR notation."},
				"source_port":      schema.Int64Attribute{Computed: true, MarkdownDescription: "Source port (0 = any)."},
				"destination":      schema.StringAttribute{Computed: true, MarkdownDescription: "Destination in CIDR notation."},
				"destination_port": schema.Int64Attribute{Computed: true, MarkdownDescription: "Destination port (0 = any)."},
			},
		},
	}
}

// flattenNATRulesData / flattenFirewallRulesData return an empty (never nil)
// slice for a gateway without rules: an empty list keeps length() and for_each
// working, which is exactly what audit-style configurations do with these.
func flattenNATRulesData(in []entities.NATRule) []natRuleDataModel {
	out := make([]natRuleDataModel, 0, len(in))
	for _, r := range in {
		out = append(out, natRuleDataModel{
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

func flattenFirewallRulesData(in []entities.FirewallRule) []firewallRuleDataModel {
	out := make([]firewallRuleDataModel, 0, len(in))
	for _, r := range in {
		out = append(out, firewallRuleDataModel{
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
