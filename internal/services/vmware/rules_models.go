package vmware

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Models of the three rule-set resources — vcp_vmware_edge_firewall,
// vcp_vmware_edge_nat and vcp_vmware_server_firewall. Each of them owns the
// complete rule set of one parent object, the same contract the vStack
// vcp_gateway_nat / vcp_gateway_firewall resources have.
//
// Field names, values and formats mirror the API exactly (no normalisation on
// the provider side), which keeps state identical to what the API returns and
// leaves no room for drift. Where the API does not round-trip a value the field
// is either absent from the schema (edge firewall `enabled`/`description`,
// NET-7) or Optional+Computed so the platform's value lands in state
// (NAT addresses, NET-4).

// fromStringPtr converts an optional API string into a model value.
func fromStringPtr(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// fromBoolPtr converts an optional API bool into a model value.
func fromBoolPtr(b *bool) types.Bool {
	if b == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*b)
}

// preferPlanned keeps the value the plan asked for when the API answered without
// one. It is used on the apply path only: a Required attribute the API omits
// would otherwise land in state as null, which Terraform rejects outright
// ("Provider produced inconsistent result after apply"), and an Optional one the
// user did set would be silently dropped. Read takes the API at its word, so a
// value the platform really did change still surfaces as drift on the next
// refresh.
func preferPlanned(fresh, planned types.String) types.String {
	if !fresh.IsNull() || planned.IsNull() || planned.IsUnknown() {
		return fresh
	}
	return planned
}

// preferPlannedBool is preferPlanned for a bool attribute.
func preferPlannedBool(fresh, planned types.Bool) types.Bool {
	if !fresh.IsNull() || planned.IsNull() || planned.IsUnknown() {
		return fresh
	}
	return planned
}

// ============================================================================
// vcp_vmware_edge_firewall
// ============================================================================

type edgeFirewallModel struct {
	ID            types.Int64             `tfsdk:"id"`
	NetworkID     types.Int64             `tfsdk:"network_id"`
	DefaultAction types.String            `tfsdk:"default_action"`
	Rules         []edgeFirewallRuleModel `tfsdk:"rules"`
}

// edgeFirewallRuleModel deliberately has no `enabled` and no `description`:
// NET-7 — the backend accepts both and ignores them (it forces enabled to true
// and derives the description from the rule name), so the write type of the SDK
// has no such fields either. Offering them would promise a round trip the API
// does not perform.
type edgeFirewallRuleModel struct {
	Name            types.String `tfsdk:"name"`
	Action          types.String `tfsdk:"action"`
	Protocol        types.String `tfsdk:"protocol"`
	Source          types.String `tfsdk:"source"`
	SourcePort      types.String `tfsdk:"source_port"`
	Destination     types.String `tfsdk:"destination"`
	DestinationPort types.String `tfsdk:"destination_port"`
}

func expandEdgeFirewallRules(in []edgeFirewallRuleModel) []entities.VmwareUpdateEdgeFirewallRule {
	out := make([]entities.VmwareUpdateEdgeFirewallRule, 0, len(in))
	for _, r := range in {
		out = append(out, entities.VmwareUpdateEdgeFirewallRule{
			Name:            optionalString(r.Name),
			Action:          r.Action.ValueString(),
			Protocol:        optionalString(r.Protocol),
			Source:          optionalString(r.Source),
			SourcePort:      optionalString(r.SourcePort),
			Destination:     optionalString(r.Destination),
			DestinationPort: optionalString(r.DestinationPort),
		})
	}
	return out
}

// flattenEdgeFirewallRules maps the API response back into the model. The result
// is always a non-nil slice: an empty rule list is an empty list in state, never
// null, which is what a configuration with `rules = []` produces.
func flattenEdgeFirewallRules(in []entities.VmwareEdgeFirewallRule) []edgeFirewallRuleModel {
	out := make([]edgeFirewallRuleModel, 0, len(in))
	for _, r := range in {
		out = append(out, edgeFirewallRuleModel{
			Name:            fromStringPtr(r.Name),
			Action:          fromStringPtr(r.Action),
			Protocol:        fromStringPtr(r.Protocol),
			Source:          fromStringPtr(r.Source),
			SourcePort:      fromStringPtr(r.SourcePort),
			Destination:     fromStringPtr(r.Destination),
			DestinationPort: fromStringPtr(r.DestinationPort),
		})
	}
	return out
}

// mergeEdgeFirewallRules fills the gaps of a read-back rule set from the plan —
// see preferPlanned.
func mergeEdgeFirewallRules(planned, fresh []edgeFirewallRuleModel) []edgeFirewallRuleModel {
	for i := range fresh {
		if i >= len(planned) {
			break
		}
		p := planned[i]
		fresh[i].Name = preferPlanned(fresh[i].Name, p.Name)
		fresh[i].Action = preferPlanned(fresh[i].Action, p.Action)
		fresh[i].Protocol = preferPlanned(fresh[i].Protocol, p.Protocol)
		fresh[i].Source = preferPlanned(fresh[i].Source, p.Source)
		fresh[i].SourcePort = preferPlanned(fresh[i].SourcePort, p.SourcePort)
		fresh[i].Destination = preferPlanned(fresh[i].Destination, p.Destination)
		fresh[i].DestinationPort = preferPlanned(fresh[i].DestinationPort, p.DestinationPort)
	}
	return fresh
}

// ============================================================================
// vcp_vmware_edge_nat
// ============================================================================

type edgeNATModel struct {
	ID        types.Int64        `tfsdk:"id"`
	NetworkID types.Int64        `tfsdk:"network_id"`
	Rules     []edgeNATRuleModel `tfsdk:"rules"`
}

// edgeNATRuleModel carries no rule id: the resource owns the whole set and
// matches configuration entries to API rules by position, so the API's internal
// ids never have to appear in the configuration. See natResource.reconcile.
type edgeNATRuleModel struct {
	Type           types.String `tfsdk:"type"`
	Protocol       types.String `tfsdk:"protocol"`
	Description    types.String `tfsdk:"description"`
	OriginalIP     types.String `tfsdk:"original_ip"`
	OriginalPort   types.String `tfsdk:"original_port"`
	TranslatedIP   types.String `tfsdk:"translated_ip"`
	TranslatedPort types.String `tfsdk:"translated_port"`
	Enabled        types.Bool   `tfsdk:"enabled"`
}

// expandEdgeNATRule builds the upsert request for one rule. ruleID selects the
// existing API rule to overwrite; pass nil to create a new one.
//
// The API requires both addresses to be non-empty, while the resource lets each
// of them be omitted where the platform supplies the value: original_ip of a
// DNAT rule (always the edge's external address, NET-4) and translated_ip of an
// SNAT rule. "any" is the form the API accepts for that — verified live.
func expandEdgeNATRule(r edgeNATRuleModel, ruleID *int) *entities.VmwareUpsertNATRuleRequest {
	return &entities.VmwareUpsertNATRuleRequest{
		RuleID:         ruleID,
		Type:           r.Type.ValueString(),
		Protocol:       r.Protocol.ValueString(),
		Description:    r.Description.ValueString(),
		OriginalIP:     addressOrAny(r.OriginalIP),
		OriginalPort:   r.OriginalPort.ValueString(),
		TranslatedIP:   addressOrAny(r.TranslatedIP),
		TranslatedPort: r.TranslatedPort.ValueString(),
		Enabled:        optionalBool(r.Enabled),
	}
}

// addressOrAny substitutes the "any" wildcard for an address the configuration
// left out, which is what the API takes for "the platform decides".
func addressOrAny(v types.String) string {
	if !isSet(v) || v.ValueString() == "" {
		return entities.VmwareEdgeFirewallAny
	}
	return v.ValueString()
}

func flattenEdgeNATRules(in []entities.VmwareEdgeNATRule) []edgeNATRuleModel {
	out := make([]edgeNATRuleModel, 0, len(in))
	for _, r := range in {
		out = append(out, edgeNATRuleModel{
			Type:           fromStringPtr(r.Type),
			Protocol:       fromStringPtr(r.Protocol),
			Description:    fromStringPtr(r.Description),
			OriginalIP:     fromStringPtr(r.OriginalIP),
			OriginalPort:   fromStringPtr(r.OriginalPort),
			TranslatedIP:   fromStringPtr(r.TranslatedIP),
			TranslatedPort: fromStringPtr(r.TranslatedPort),
			Enabled:        fromBoolPtr(r.Enabled),
		})
	}
	return out
}

// mergeEdgeNATRules fills the gaps of a read-back rule set from the plan — see
// preferPlanned. Rules the API added beyond the plan are kept as they came.
func mergeEdgeNATRules(planned, fresh []edgeNATRuleModel) []edgeNATRuleModel {
	for i := range fresh {
		if i >= len(planned) {
			break
		}
		p := planned[i]
		fresh[i].Type = preferPlanned(fresh[i].Type, p.Type)
		fresh[i].Protocol = preferPlanned(fresh[i].Protocol, p.Protocol)
		fresh[i].Description = preferPlanned(fresh[i].Description, p.Description)
		fresh[i].OriginalIP = preferPlanned(fresh[i].OriginalIP, p.OriginalIP)
		fresh[i].OriginalPort = preferPlanned(fresh[i].OriginalPort, p.OriginalPort)
		fresh[i].TranslatedIP = preferPlanned(fresh[i].TranslatedIP, p.TranslatedIP)
		fresh[i].TranslatedPort = preferPlanned(fresh[i].TranslatedPort, p.TranslatedPort)
		fresh[i].Enabled = preferPlannedBool(fresh[i].Enabled, p.Enabled)
	}
	return fresh
}

// natReconcilePlan says how a configured rule list maps onto the rules the API
// already holds.
type natReconcilePlan struct {
	// UpsertIDs[i] is the id of the API rule that configuration entry i
	// overwrites, or nil when the entry has no counterpart and is created.
	UpsertIDs []*int
	// DeleteIDs are the rules the configuration no longer has, in API order.
	DeleteIDs []int
}

// planNATReconcile pairs configuration entries with existing rules by position:
// entry i overwrites the rule the API holds at position i, entries past the end
// are created, and rules past the end of the configuration are deleted.
//
// An existing rule the API listed without an id (0 here) cannot be addressed, so
// the entry facing it is created instead of overwriting it and the rule itself is
// left to the caller to report.
func planNATReconcile(existingIDs []int, ruleCount int) natReconcilePlan {
	plan := natReconcilePlan{UpsertIDs: make([]*int, ruleCount)}

	for i := 0; i < ruleCount && i < len(existingIDs); i++ {
		if existingIDs[i] <= 0 {
			continue
		}
		id := existingIDs[i]
		plan.UpsertIDs[i] = &id
	}
	if len(existingIDs) > ruleCount {
		plan.DeleteIDs = existingIDs[ruleCount:]
	}
	return plan
}

// natRuleIDs returns the API ids of a rule set, in the order the API listed
// them. A rule the API reported without an id cannot be addressed, so it is
// reported as 0 and skipped by the reconciler.
func natRuleIDs(in []entities.VmwareEdgeNATRule) []int {
	out := make([]int, 0, len(in))
	for _, r := range in {
		if r.ID == nil {
			out = append(out, 0)
			continue
		}
		out = append(out, *r.ID)
	}
	return out
}

// ============================================================================
// vcp_vmware_server_firewall
// ============================================================================

type serverFirewallModel struct {
	ID       types.Int64               `tfsdk:"id"`
	ServerID types.Int64               `tfsdk:"server_id"`
	Rules    []serverFirewallRuleModel `tfsdk:"rules"`
}

type serverFirewallRuleModel struct {
	Name             types.String `tfsdk:"name"`
	TrafficDirection types.String `tfsdk:"traffic_direction"`
	Action           types.String `tfsdk:"action"`
	Protocol         types.String `tfsdk:"protocol"`
	Source           types.String `tfsdk:"source"`
	SourcePort       types.String `tfsdk:"source_port"`
	Destination      types.String `tfsdk:"destination"`
	DestinationPort  types.String `tfsdk:"destination_port"`
}

func expandServerFirewallRules(in []serverFirewallRuleModel) []entities.VmwareServerFirewallRule {
	out := make([]entities.VmwareServerFirewallRule, 0, len(in))
	for _, r := range in {
		out = append(out, entities.VmwareServerFirewallRule{
			Name:             r.Name.ValueString(),
			TrafficDirection: r.TrafficDirection.ValueString(),
			Action:           r.Action.ValueString(),
			Protocol:         r.Protocol.ValueString(),
			Source:           optionalString(r.Source),
			SourcePort:       optionalString(r.SourcePort),
			Destination:      optionalString(r.Destination),
			DestinationPort:  optionalString(r.DestinationPort),
		})
	}
	return out
}

// mergeServerFirewallRules fills the gaps of a read-back rule set from the plan —
// see preferPlanned. Only the optional attributes can be null here: the API
// models the rest as plain strings.
func mergeServerFirewallRules(planned, fresh []serverFirewallRuleModel) []serverFirewallRuleModel {
	for i := range fresh {
		if i >= len(planned) {
			break
		}
		p := planned[i]
		fresh[i].Source = preferPlanned(fresh[i].Source, p.Source)
		fresh[i].SourcePort = preferPlanned(fresh[i].SourcePort, p.SourcePort)
		fresh[i].Destination = preferPlanned(fresh[i].Destination, p.Destination)
		fresh[i].DestinationPort = preferPlanned(fresh[i].DestinationPort, p.DestinationPort)
	}
	return fresh
}

func flattenServerFirewallRules(in []entities.VmwareServerFirewallRule) []serverFirewallRuleModel {
	out := make([]serverFirewallRuleModel, 0, len(in))
	for _, r := range in {
		out = append(out, serverFirewallRuleModel{
			Name:             types.StringValue(r.Name),
			TrafficDirection: types.StringValue(r.TrafficDirection),
			Action:           types.StringValue(r.Action),
			Protocol:         types.StringValue(r.Protocol),
			Source:           fromStringPtr(r.Source),
			SourcePort:       fromStringPtr(r.SourcePort),
			Destination:      fromStringPtr(r.Destination),
			DestinationPort:  fromStringPtr(r.DestinationPort),
		})
	}
	return out
}
