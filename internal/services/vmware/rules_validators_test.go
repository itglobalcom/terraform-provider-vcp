package vmware

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

func TestRuleAddressProblem(t *testing.T) {
	valid := []string{
		"any",
		"10.0.0.5",
		"10.0.0.0/24",
		"0.0.0.0/0",
		"10.0.0.5-10.0.0.9",
		"10.0.0.5-10.0.0.5",
	}
	for _, s := range valid {
		if problem := ruleAddressProblem(s); problem != "" {
			t.Errorf("ruleAddressProblem(%q) = %q, want no problem", s, problem)
		}
	}

	invalid := map[string]string{
		"":                  "empty",
		"nope":              "not an address",
		"10.0.0.256":        "octet out of range",
		"10.0.0.5/24":       "not the network address of its prefix",
		"::1":               "IPv6",
		"2001:db8::/32":     "IPv6 network",
		"10.0.0.9-10.0.0.5": "reversed range",
		"10.0.0.5-":         "half a range",
	}
	for s, why := range invalid {
		if ruleAddressProblem(s) == "" {
			t.Errorf("ruleAddressProblem(%q) accepted an invalid address (%s)", s, why)
		}
	}
}

// A bare address given where a network is expected is the mistake most easily
// made (an ip attribute of another resource), so the message has to name the fix.
func TestRuleAddressProblemSuggestsTheCanonicalPrefix(t *testing.T) {
	problem := ruleAddressProblem("10.0.0.5/24")
	if problem == "" {
		t.Fatal("ruleAddressProblem(10.0.0.5/24) accepted a non-canonical prefix")
	}
	if !strings.Contains(problem, "10.0.0.0/24") {
		t.Errorf("the message should point at the masked prefix 10.0.0.0/24, got: %s", problem)
	}
}

func TestRulePortProblem(t *testing.T) {
	valid := []string{"any", "1", "443", "65535", "1000-2000", "80,443", "80,1000-2000,8443"}
	for _, s := range valid {
		if problem := rulePortProblem(s); problem != "" {
			t.Errorf("rulePortProblem(%q) = %q, want no problem", s, problem)
		}
	}

	invalid := map[string]string{
		"":          "empty",
		"0":         "port 0 is not a port",
		"65536":     "out of range",
		"-1":        "negative",
		"http":      "not a number",
		"2000-1000": "reversed range",
		"80,":       "trailing separator",
		"80-":       "half a range",
	}
	for s, why := range invalid {
		if rulePortProblem(s) == "" {
			t.Errorf("rulePortProblem(%q) accepted an invalid port specification (%s)", s, why)
		}
	}
}

func natRule(ruleType, originalIP, translatedIP string) edgeNATRuleModel {
	rule := edgeNATRuleModel{
		Type:         types.StringValue(ruleType),
		Protocol:     types.StringValue("tcp"),
		OriginalIP:   types.StringNull(),
		TranslatedIP: types.StringNull(),
	}
	if originalIP != "" {
		rule.OriginalIP = types.StringValue(originalIP)
	}
	if translatedIP != "" {
		rule.TranslatedIP = types.StringValue(translatedIP)
	}
	return rule
}

// A DNAT rule translates from the edge's own external address, which the platform
// substitutes for whatever the request carries (NET-4). Accepting the value would
// mean either a silent substitution or a failure at the end of a long apply.
func TestValidateEdgeNATRulesRejectsOriginalIPOnDNAT(t *testing.T) {
	var diags diag.Diagnostics
	validateEdgeNATRules([]edgeNATRuleModel{natRule(entities.VmwareEdgeNATTypeDNAT, "10.0.0.1", "10.0.0.10")}, &diags)

	if !diags.HasError() {
		t.Fatal("a DNAT rule with original_ip must be rejected")
	}
	if got := diags.Errors()[0].Summary(); got != "Attribute Not Applicable To DNAT" {
		t.Errorf("unexpected error summary: %q", got)
	}
}

func TestValidateEdgeNATRulesRequiredAddresses(t *testing.T) {
	cases := map[string]struct {
		rule      edgeNATRuleModel
		wantError bool
	}{
		"dnat without translated_ip": {natRule(entities.VmwareEdgeNATTypeDNAT, "", ""), true},
		"dnat with translated_ip":    {natRule(entities.VmwareEdgeNATTypeDNAT, "", "10.0.0.10"), false},
		"snat without original_ip":   {natRule(entities.VmwareEdgeNATTypeSNAT, "", ""), true},
		"snat with original_ip":      {natRule(entities.VmwareEdgeNATTypeSNAT, "10.0.0.0/24", ""), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateEdgeNATRules([]edgeNATRuleModel{tc.rule}, &diags)
			if diags.HasError() != tc.wantError {
				t.Fatalf("HasError() = %v, want %v (diags: %v)", diags.HasError(), tc.wantError, diags)
			}
		})
	}
}

// An unknown type (a value taken from another resource) must not be validated as
// if it were one of the two known ones — the rule would be rejected for missing an
// address that the actual type does not need.
func TestValidateEdgeNATRulesSkipsUnknownType(t *testing.T) {
	var diags diag.Diagnostics
	validateEdgeNATRules([]edgeNATRuleModel{{
		Type:         types.StringUnknown(),
		Protocol:     types.StringValue("tcp"),
		OriginalIP:   types.StringNull(),
		TranslatedIP: types.StringNull(),
	}}, &diags)

	if diags.HasError() {
		t.Fatalf("a rule with an unknown type must pass validation, got: %v", diags)
	}
}

func TestValidateServerFirewallRulesRejectsDuplicateName(t *testing.T) {
	rule := func(name string) serverFirewallRuleModel {
		return serverFirewallRuleModel{
			Name:             types.StringValue(name),
			TrafficDirection: types.StringValue(entities.VmwareTrafficDirectionIncoming),
			Action:           types.StringValue(entities.VmwareFirewallActionAllow),
			Protocol:         types.StringValue("tcp"),
		}
	}

	var diags diag.Diagnostics
	validateServerFirewallRules([]serverFirewallRuleModel{rule("ssh"), rule("web"), rule("ssh")}, &diags)
	if !diags.HasError() {
		t.Fatal("two rules with the same name must be rejected — the API identifies a rule by its name")
	}

	diags = nil
	validateServerFirewallRules([]serverFirewallRuleModel{rule("ssh"), rule("web")}, &diags)
	if diags.HasError() {
		t.Fatalf("distinct names must pass, got: %v", diags)
	}
}

// The three failure modes of a rule-set change need different advice, and
// re-applying only helps in two of them. Telling them apart is what the hint is
// for, so each branch is pinned here.
func TestApplyFailureHint(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"a concurrent change is worth waiting out": {
			err:  &sdk.RequestError{StatusCode: http.StatusConflict, Codes: []int{sdk.APICodeConflict}},
			want: "still applying another change",
		},
		"a rejected request will be rejected again": {
			err:  &sdk.RequestError{StatusCode: http.StatusBadRequest, Message: "bad rule"},
			want: "rejected the request",
		},
		"a failed backend task is usually transient": {
			err:  errors.New("task vmw123 failed"),
			want: "backend task then failed",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := applyFailureHint(tc.err, "edge NAT")
			if !strings.Contains(got, tc.want) {
				t.Errorf("applyFailureHint(%v) = %q, want it to mention %q", tc.err, got, tc.want)
			}
		})
	}

	// The subject names the object in the message, so the reader knows which of the
	// two firewalls a failure came from.
	if got := applyFailureHint(&sdk.RequestError{StatusCode: http.StatusBadRequest}, "server firewall"); !strings.Contains(got, "server firewall") {
		t.Errorf("the hint must name its subject, got: %q", got)
	}
}

func TestWarnRuleCount(t *testing.T) {
	var diags diag.Diagnostics
	warnRuleCount(rulesCountWarnThreshold, "NAT", &diags)
	if len(diags) != 0 {
		t.Errorf("a set at the threshold must not warn, got: %v", diags)
	}

	warnRuleCount(rulesCountWarnThreshold+1, "NAT", &diags)
	if diags.WarningsCount() != 1 {
		t.Errorf("a set above the threshold must warn once, got %d warning(s)", diags.WarningsCount())
	}
	if diags.HasError() {
		t.Error("a large set must warn, not fail")
	}
}
