package vmware

import (
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func intPtr(i int) *int       { return &i }

// A rule whose optional attributes are absent must reach the API as absent, not
// as an empty string: the API stores "" as a literal value and the rule then
// matches nothing.
func TestExpandEdgeFirewallRulesSubstitutesAnyForUnsetAttributes(t *testing.T) {
	out := expandEdgeFirewallRules([]edgeFirewallRuleModel{{
		Name:            types.StringValue("ssh"),
		Action:          types.StringValue(entities.VmwareEdgeFirewallActionAllow),
		Protocol:        types.StringValue("tcp"),
		Source:          types.StringNull(),
		SourcePort:      types.StringUnknown(),
		Destination:     types.StringValue("10.0.0.10"),
		DestinationPort: types.StringValue("22"),
	}})

	if len(out) != 1 {
		t.Fatalf("expandEdgeFirewallRules returned %d rules, want 1", len(out))
	}
	rule := out[0]
	if rule.Action != entities.VmwareEdgeFirewallActionAllow {
		t.Errorf("action = %q, want %q", rule.Action, entities.VmwareEdgeFirewallActionAllow)
	}
	// The contract requires every address and port of a rule, so an unset one
	// goes as "any"; omitting it fails the request outright.
	if rule.Source == nil || *rule.Source != entities.VmwareEdgeFirewallAny {
		t.Errorf("source = %v, want %q", rule.Source, entities.VmwareEdgeFirewallAny)
	}
	if rule.SourcePort == nil || *rule.SourcePort != entities.VmwareEdgeFirewallAny {
		t.Errorf("source_port = %v, want %q", rule.SourcePort, entities.VmwareEdgeFirewallAny)
	}
	if rule.Destination == nil || *rule.Destination != "10.0.0.10" {
		t.Errorf("destination = %v, want 10.0.0.10", rule.Destination)
	}
}

// An empty rule set must flatten to an empty list, never to nil: a configuration
// with `rules = []` produces an empty list, and null there would be a diff that
// no apply can settle.
func TestFlattenRulesNeverReturnsNil(t *testing.T) {
	if got := flattenEdgeFirewallRules(nil); got == nil || len(got) != 0 {
		t.Errorf("flattenEdgeFirewallRules(nil) = %v, want an empty non-nil slice", got)
	}
	if got := flattenEdgeNATRules(nil); got == nil || len(got) != 0 {
		t.Errorf("flattenEdgeNATRules(nil) = %v, want an empty non-nil slice", got)
	}
	if got := flattenServerFirewallRules(nil); got == nil || len(got) != 0 {
		t.Errorf("flattenServerFirewallRules(nil) = %v, want an empty non-nil slice", got)
	}
}

func TestFlattenEdgeFirewallRulesDropsServerDerivedFields(t *testing.T) {
	out := flattenEdgeFirewallRules([]entities.VmwareEdgeFirewallRule{{
		// NET-7: the API reports these two whatever the request said. They have no
		// place in the model, so flattening must simply not look at them.
		Enabled:         boolPtr(true),
		Description:     strPtr("ssh"),
		Name:            strPtr("ssh"),
		Action:          strPtr(entities.VmwareEdgeFirewallActionAllow),
		Protocol:        strPtr("tcp"),
		Source:          strPtr("any"),
		DestinationPort: strPtr("22"),
	}})

	if len(out) != 1 {
		t.Fatalf("flattenEdgeFirewallRules returned %d rules, want 1", len(out))
	}
	rule := out[0]
	if rule.Name.ValueString() != "ssh" {
		t.Errorf("name = %q, want ssh", rule.Name.ValueString())
	}
	// destination was absent from the response: the model has to say null rather
	// than invent "any", so Terraform can tell "not set" from "set to any".
	if !rule.Destination.IsNull() {
		t.Errorf("an absent destination must flatten to null, got %q", rule.Destination.ValueString())
	}
	if rule.DestinationPort.ValueString() != "22" {
		t.Errorf("destination_port = %q, want 22", rule.DestinationPort.ValueString())
	}
}

// The API requires both NAT addresses to be non-empty, so an address the
// configuration leaves out has to travel as the "any" wildcard — that is what
// tells the platform to fill the value in.
func TestExpandEdgeNATRuleSubstitutesAnyForUnsetAddresses(t *testing.T) {
	req := expandEdgeNATRule(edgeNATRuleModel{
		Type:           types.StringValue(entities.VmwareEdgeNATTypeDNAT),
		Protocol:       types.StringValue("tcp"),
		Description:    types.StringNull(),
		OriginalIP:     types.StringNull(),
		OriginalPort:   types.StringValue("443"),
		TranslatedIP:   types.StringValue("10.0.0.10"),
		TranslatedPort: types.StringValue("443"),
		Enabled:        types.BoolNull(),
	}, nil)

	if req.OriginalIP != entities.VmwareEdgeFirewallAny {
		t.Errorf("an unset original_ip must be sent as %q, got %q", entities.VmwareEdgeFirewallAny, req.OriginalIP)
	}
	if req.TranslatedIP != "10.0.0.10" {
		t.Errorf("translated_ip = %q, want 10.0.0.10", req.TranslatedIP)
	}
	if req.RuleID != nil {
		t.Errorf("a rule with no id must be created, not updated (rule_id = %d)", *req.RuleID)
	}
	if req.Enabled != nil {
		t.Error("an unset enabled must be omitted so the platform's default (enabled) applies")
	}
	if err := req.Validate(); err != nil {
		t.Errorf("the request must pass the SDK's own validation, got: %v", err)
	}
}

func TestExpandEdgeNATRuleCarriesRuleIDForUpdate(t *testing.T) {
	req := expandEdgeNATRule(edgeNATRuleModel{
		Type:         types.StringValue(entities.VmwareEdgeNATTypeSNAT),
		Protocol:     types.StringValue("any"),
		OriginalIP:   types.StringValue("10.0.0.0/24"),
		TranslatedIP: types.StringNull(),
		Enabled:      types.BoolValue(false),
	}, intPtr(663))

	if req.RuleID == nil || *req.RuleID != 663 {
		t.Fatalf("rule_id = %v, want 663", req.RuleID)
	}
	if req.Enabled == nil || *req.Enabled {
		t.Error("enabled = false must be sent, so the rule stays in the configuration without passing traffic")
	}
	if err := req.Validate(); err != nil {
		t.Errorf("the request must pass the SDK's own validation, got: %v", err)
	}
}

// The ids are what pairs a configuration entry with an existing rule, so their
// order must be the order the API listed the rules in, gaps included.
func TestNATRuleIDs(t *testing.T) {
	ids := natRuleIDs([]entities.VmwareEdgeNATRule{
		{ID: intPtr(663)},
		{ID: nil},
		{ID: intPtr(664)},
	})

	want := []int{663, 0, 664}
	if len(ids) != len(want) {
		t.Fatalf("natRuleIDs returned %d ids, want %d", len(ids), len(want))
	}
	for i, id := range want {
		if ids[i] != id {
			t.Errorf("ids[%d] = %d, want %d", i, ids[i], id)
		}
	}
}

// planNATReconcile is the heart of the resource: the API has no bulk endpoint, so
// which rule gets overwritten, created or deleted is decided here rather than by
// the platform.
func TestPlanNATReconcile(t *testing.T) {
	ids := func(in []*int) []string {
		out := make([]string, len(in))
		for i, id := range in {
			if id == nil {
				out[i] = "create"
				continue
			}
			out[i] = strconv.Itoa(*id)
		}
		return out
	}

	cases := map[string]struct {
		existing    []int
		ruleCount   int
		wantUpserts []string
		wantDeletes []int
	}{
		"first apply creates every rule": {
			existing: nil, ruleCount: 2,
			wantUpserts: []string{"create", "create"},
		},
		"same length overwrites in place": {
			existing: []int{10, 11}, ruleCount: 2,
			wantUpserts: []string{"10", "11"},
		},
		"a rule appended is created, the rest overwritten": {
			existing: []int{10, 11}, ruleCount: 3,
			wantUpserts: []string{"10", "11", "create"},
		},
		"a rule dropped leaves the last one to delete": {
			existing: []int{10, 11, 12}, ruleCount: 1,
			wantUpserts: []string{"10"}, wantDeletes: []int{11, 12},
		},
		"an empty configuration deletes everything": {
			existing: []int{10, 11}, ruleCount: 0,
			wantUpserts: []string{}, wantDeletes: []int{10, 11},
		},
		// A rule the API listed without an id cannot be addressed, so the entry
		// facing it is created instead of overwriting it.
		"an unaddressable rule is not overwritten": {
			existing: []int{10, 0, 12}, ruleCount: 3,
			wantUpserts: []string{"10", "create", "12"},
		},
		"nothing to do": {
			existing: nil, ruleCount: 0,
			wantUpserts: []string{},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			plan := planNATReconcile(tc.existing, tc.ruleCount)

			got := ids(plan.UpsertIDs)
			if len(got) != len(tc.wantUpserts) {
				t.Fatalf("upserts = %v, want %v", got, tc.wantUpserts)
			}
			for i := range got {
				if got[i] != tc.wantUpserts[i] {
					t.Errorf("upserts[%d] = %s, want %s", i, got[i], tc.wantUpserts[i])
				}
			}

			if len(plan.DeleteIDs) != len(tc.wantDeletes) {
				t.Fatalf("deletes = %v, want %v", plan.DeleteIDs, tc.wantDeletes)
			}
			for i := range plan.DeleteIDs {
				if plan.DeleteIDs[i] != tc.wantDeletes[i] {
					t.Errorf("deletes[%d] = %d, want %d", i, plan.DeleteIDs[i], tc.wantDeletes[i])
				}
			}
		})
	}
}

// A rule created by the pass must never be scheduled for deletion — that would
// apply the configuration and then undo it.
func TestPlanNATReconcileNeverDeletesWhatItCreates(t *testing.T) {
	for existing := 0; existing <= 4; existing++ {
		for ruleCount := 0; ruleCount <= 4; ruleCount++ {
			ids := make([]int, existing)
			for i := range ids {
				ids[i] = 10 + i
			}

			plan := planNATReconcile(ids, ruleCount)
			if len(plan.DeleteIDs) == 0 {
				continue
			}
			for _, id := range plan.UpsertIDs {
				if id == nil {
					t.Fatalf("existing=%d rules=%d: a rule is created while %v are deleted in the same pass",
						existing, ruleCount, plan.DeleteIDs)
				}
			}
		}
	}
}

// The API may answer without a value for an attribute the configuration set. For
// a Required one that would be an "inconsistent result after apply" and for an
// Optional one a silent loss, so the planned value stands in — and only then.
func TestPreferPlanned(t *testing.T) {
	cases := map[string]struct {
		fresh, planned, want types.String
	}{
		"the API value wins": {
			types.StringValue("any"), types.StringValue("10.0.0.0/24"), types.StringValue("any"),
		},
		"a missing API value falls back to the plan": {
			types.StringNull(), types.StringValue("10.0.0.0/24"), types.StringValue("10.0.0.0/24"),
		},
		"both absent stays absent": {
			types.StringNull(), types.StringNull(), types.StringNull(),
		},
		// An unknown plan value must never reach state.
		"an unknown plan value is not used": {
			types.StringNull(), types.StringUnknown(), types.StringNull(),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := preferPlanned(tc.fresh, tc.planned); !got.Equal(tc.want) {
				t.Errorf("preferPlanned = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMergeEdgeNATRulesKeepsPlannedValuesTheAPIOmits(t *testing.T) {
	planned := []edgeNATRuleModel{{
		Type:           types.StringValue(entities.VmwareEdgeNATTypeDNAT),
		Protocol:       types.StringValue("tcp"),
		Description:    types.StringValue("publish https"),
		OriginalIP:     types.StringUnknown(), // the platform supplies it (NET-4)
		OriginalPort:   types.StringValue("443"),
		TranslatedIP:   types.StringValue("10.0.0.10"),
		TranslatedPort: types.StringValue("443"),
		Enabled:        types.BoolUnknown(),
	}}
	// The API answered with the substituted address but without the description,
	// the ports or the enabled flag.
	fresh := []edgeNATRuleModel{{
		Type:           types.StringValue(entities.VmwareEdgeNATTypeDNAT),
		Protocol:       types.StringValue("tcp"),
		Description:    types.StringNull(),
		OriginalIP:     types.StringValue("203.0.113.5"),
		OriginalPort:   types.StringNull(),
		TranslatedIP:   types.StringValue("10.0.0.10"),
		TranslatedPort: types.StringNull(),
		Enabled:        types.BoolNull(),
	}}

	out := mergeEdgeNATRules(planned, fresh)
	if len(out) != 1 {
		t.Fatalf("merge returned %d rules, want 1", len(out))
	}
	if got := out[0].OriginalIP.ValueString(); got != "203.0.113.5" {
		t.Errorf("original_ip = %q, want the address the platform substituted", got)
	}
	if got := out[0].Description.ValueString(); got != "publish https" {
		t.Errorf("description = %q, want the planned value", got)
	}
	if got := out[0].OriginalPort.ValueString(); got != "443" {
		t.Errorf("original_port = %q, want the planned value", got)
	}
	// The plan had no value either, so nothing to fall back on.
	if !out[0].Enabled.IsNull() {
		t.Errorf("enabled = %v, want null (the plan had no value to fall back on)", out[0].Enabled)
	}
}

// A rule the API holds beyond the configuration (or one the platform added) has
// no planned counterpart and must survive the merge untouched.
func TestMergeRulesLeavesExtraAPIRulesAlone(t *testing.T) {
	fresh := []edgeFirewallRuleModel{
		{Name: types.StringValue("ssh"), Action: types.StringNull()},
		{Name: types.StringValue("extra"), Action: types.StringValue("deny")},
	}
	planned := []edgeFirewallRuleModel{
		{Name: types.StringValue("ssh"), Action: types.StringValue("allow")},
	}

	out := mergeEdgeFirewallRules(planned, fresh)
	if got := out[0].Action.ValueString(); got != "allow" {
		t.Errorf("rules[0].action = %q, want the planned value", got)
	}
	if got := out[1].Action.ValueString(); got != "deny" {
		t.Errorf("rules[1].action = %q, want the API value untouched", got)
	}
}

// The server firewall reads and writes the same type, so a rule read back and
// sent again must survive the round trip unchanged.
func TestServerFirewallRulesRoundTrip(t *testing.T) {
	original := []entities.VmwareServerFirewallRule{{
		Name:             "ssh-office",
		TrafficDirection: entities.VmwareTrafficDirectionIncoming,
		Action:           entities.VmwareFirewallActionAllow,
		Protocol:         "tcp",
		Source:           strPtr("203.0.113.0/24"),
		DestinationPort:  strPtr("22"),
	}}

	back := expandServerFirewallRules(flattenServerFirewallRules(original))
	if len(back) != 1 {
		t.Fatalf("round trip returned %d rules, want 1", len(back))
	}
	rule := back[0]
	if rule.Name != "ssh-office" || rule.TrafficDirection != entities.VmwareTrafficDirectionIncoming {
		t.Errorf("name/direction changed: %+v", rule)
	}
	if rule.Source == nil || *rule.Source != "203.0.113.0/24" {
		t.Errorf("source = %v, want 203.0.113.0/24", rule.Source)
	}
	// An absent port is sent as "any" — the contract has no way to say "unset".
	if rule.SourcePort == nil || *rule.SourcePort != entities.VmwareEdgeFirewallAny {
		t.Errorf("source_port = %v, want %q", rule.SourcePort, entities.VmwareEdgeFirewallAny)
	}
	if rule.DestinationPort == nil || *rule.DestinationPort != "22" {
		t.Errorf("destination_port = %v, want 22", rule.DestinationPort)
	}
}

// Every address and port of a firewall rule is required by the contract
// (ServerFirewallRuleDto marks all four [EncodedRequired]), so an attribute the
// configuration leaves out goes on the wire as "any". Omitting it fails the
// request with "The firewall source is required" — which is what the schema's
// "Defaults to any" was meant to prevent.
func TestExpandFirewallRulesSendsAnyForUnsetAddressesAndPorts(t *testing.T) {
	t.Run("edge", func(t *testing.T) {
		out := expandEdgeFirewallRules([]edgeFirewallRuleModel{{
			Action: types.StringValue("allow"),
		}})

		for name, got := range map[string]*string{
			"source":           out[0].Source,
			"source_port":      out[0].SourcePort,
			"destination":      out[0].Destination,
			"destination_port": out[0].DestinationPort,
		} {
			if got == nil {
				t.Errorf("%s omitted; the API requires it", name)
				continue
			}
			if *got != entities.VmwareEdgeFirewallAny {
				t.Errorf("%s = %q, want %q", name, *got, entities.VmwareEdgeFirewallAny)
			}
		}
	})

	t.Run("server", func(t *testing.T) {
		out := expandServerFirewallRules([]serverFirewallRuleModel{{
			Name: types.StringValue("ssh"), Action: types.StringValue("allow"),
			TrafficDirection: types.StringValue("incoming"), Protocol: types.StringValue("tcp"),
			DestinationPort: types.StringValue("22"),
		}})

		if out[0].Source == nil || *out[0].Source != entities.VmwareEdgeFirewallAny {
			t.Errorf("source = %v, want %q", out[0].Source, entities.VmwareEdgeFirewallAny)
		}
		if out[0].DestinationPort == nil || *out[0].DestinationPort != "22" {
			t.Errorf("destination_port = %v, want the configured 22", out[0].DestinationPort)
		}
	})
}
