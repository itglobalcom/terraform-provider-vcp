package gateway_rules

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func validateString(v validator.String, value types.String) diag.Diagnostics {
	req := validator.StringRequest{Path: path.Root("attr"), ConfigValue: value}
	var resp validator.StringResponse
	v.ValidateString(context.Background(), req, &resp)
	return resp.Diagnostics
}

func TestCIDRIPv4Validator(t *testing.T) {
	// The API accepts only canonical IPv4 CIDR. It silently rewrites a bare
	// address to /32, which would surface as a permanent diff — so a bare
	// address must fail here, with the fix spelled out.
	tests := []struct {
		value       string
		wantErr     bool
		wantMessage string
	}{
		{value: "0.0.0.0/0"},
		{value: "10.0.0.0/24"},
		{value: "10.0.0.5/32"},
		{value: "10.0.0.5", wantErr: true, wantMessage: `"10.0.0.5/32"`},
		{value: "10.0.0.5/24", wantErr: true, wantMessage: `"10.0.0.0/24"`},
		{value: "10.0.0.0/33", wantErr: true},
		{value: "::/0", wantErr: true, wantMessage: "IPv4 only"},
		{value: "2001:db8::1/128", wantErr: true, wantMessage: "IPv4 only"},
		{value: "not-an-address", wantErr: true},
		{value: "", wantErr: true},
	}

	for _, tc := range tests {
		diags := validateString(cidrIPv4(), types.StringValue(tc.value))
		if got := diags.HasError(); got != tc.wantErr {
			t.Errorf("cidrIPv4(%q): error = %v, want %v (%v)", tc.value, got, tc.wantErr, diags)
			continue
		}
		if tc.wantMessage != "" && !strings.Contains(diags.Errors()[0].Detail(), tc.wantMessage) {
			t.Errorf("cidrIPv4(%q): message %q does not mention %q", tc.value, diags.Errors()[0].Detail(), tc.wantMessage)
		}
	}

	// Null and unknown must pass: a rule may reference an address that is only
	// known after another resource is created.
	if diags := validateString(cidrIPv4(), types.StringNull()); diags.HasError() {
		t.Errorf("cidrIPv4(null) must not error, got %v", diags)
	}
	if diags := validateString(cidrIPv4(), types.StringUnknown()); diags.HasError() {
		t.Errorf("cidrIPv4(unknown) must not error, got %v", diags)
	}
}

func TestIPv4AddressValidator(t *testing.T) {
	tests := []struct {
		value       string
		wantErr     bool
		wantMessage string
	}{
		{value: "10.0.0.5"},
		{value: "45.14.48.213"},
		{value: "10.0.0.5/32", wantErr: true, wantMessage: `"10.0.0.5"`},
		{value: "10.0.0.0/24", wantErr: true},
		{value: "2001:db8::1", wantErr: true, wantMessage: "IPv4 only"},
		{value: "any", wantErr: true},
		{value: "", wantErr: true},
	}

	for _, tc := range tests {
		diags := validateString(ipv4Address(), types.StringValue(tc.value))
		if got := diags.HasError(); got != tc.wantErr {
			t.Errorf("ipv4Address(%q): error = %v, want %v (%v)", tc.value, got, tc.wantErr, diags)
			continue
		}
		if tc.wantMessage != "" && !strings.Contains(diags.Errors()[0].Detail(), tc.wantMessage) {
			t.Errorf("ipv4Address(%q): message %q does not mention %q", tc.value, diags.Errors()[0].Detail(), tc.wantMessage)
		}
	}

	if diags := validateString(ipv4Address(), types.StringUnknown()); diags.HasError() {
		t.Errorf("ipv4Address(unknown) must not error, got %v", diags)
	}
}

// configRules has to survive a `rules` list that is not known at plan time (for
// example built from another resource's attributes): cross-field validation is
// then skipped rather than reported as a type error. A list that IS known but
// holds unknown attribute values — the ordinary case, since NAT rules reference
// vcp_gateway.public_ip — must still be validated.
func TestConfigRulesUnknownHandling(t *testing.T) {
	ctx := context.Background()
	var resp resource.SchemaResponse
	NewNATResource().Schema(ctx, resource.SchemaRequest{}, &resp)
	s := resp.Schema

	objectType := s.Type().TerraformType(ctx).(tftypes.Object)
	rulesType := objectType.AttributeTypes["rules"].(tftypes.List)
	ruleType := rulesType.ElementType.(tftypes.Object)

	config := func(rules tftypes.Value) tfsdk.Config {
		return tfsdk.Config{
			Schema: s,
			Raw: tftypes.NewValue(objectType, map[string]tftypes.Value{
				"id":         tftypes.NewValue(tftypes.String, "gw1"),
				"gateway_id": tftypes.NewValue(tftypes.String, "gw1"),
				"rules":      rules,
			}),
		}
	}

	// The whole list unknown → skip.
	if rules, ok := configRules[natRuleModel](ctx, config(tftypes.NewValue(rulesType, tftypes.UnknownValue))); ok {
		t.Errorf("an unknown rules list must be skipped, got %d rule(s)", len(rules))
	}

	// A known list whose element has an unknown attribute → validate it.
	rule := tftypes.NewValue(ruleType, map[string]tftypes.Value{
		"type":             tftypes.NewValue(tftypes.String, "SNAT"),
		"protocol":         tftypes.NewValue(tftypes.String, "IP"),
		"source":           tftypes.NewValue(tftypes.String, "10.0.0.0/24"),
		"destination":      tftypes.NewValue(tftypes.String, "0.0.0.0/0"),
		"destination_port": tftypes.NewValue(tftypes.Number, 443),
		"translated":       tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"translated_port":  tftypes.NewValue(tftypes.Number, 0),
	})
	rules, ok := configRules[natRuleModel](ctx, config(tftypes.NewValue(rulesType, []tftypes.Value{rule})))
	if !ok || len(rules) != 1 {
		t.Fatalf("a known list must be returned, got ok=%v rules=%d", ok, len(rules))
	}
	if !rules[0].Translated.IsUnknown() {
		t.Error("the unknown attribute must arrive as unknown, not as a zero value")
	}
	// protocol = "IP" with destination_port = 443 is exactly what the API rejects.
	var diags diag.Diagnostics
	validateNATRules(rules, &diags)
	if !diags.HasError() {
		t.Error("a rule with an unknown address must still be checked for port/protocol consistency")
	}
}

func natRule(typ, protocol string, dport, tport types.Int64) natRuleModel {
	return natRuleModel{
		Type:            types.StringValue(typ),
		Protocol:        types.StringValue(protocol),
		Source:          types.StringValue("10.0.0.0/24"),
		Destination:     types.StringValue("0.0.0.0/0"),
		DestinationPort: dport,
		Translated:      types.StringValue("45.14.48.213"),
		TranslatedPort:  tport,
	}
}

func TestValidateNATRules(t *testing.T) {
	zero, port, null, unknown := types.Int64Value(0), types.Int64Value(443), types.Int64Null(), types.Int64Unknown()

	tests := []struct {
		name    string
		rules   []natRuleModel
		wantErr bool
	}{
		{name: "tcp with ports", rules: []natRuleModel{natRule("DNAT", "TCP", port, port)}},
		{name: "icmp without ports", rules: []natRuleModel{natRule("DNAT", "ICMP", zero, zero)}},
		{name: "icmp with null ports", rules: []natRuleModel{natRule("DNAT", "ICMP", null, null)}},
		{name: "icmp with unknown ports", rules: []natRuleModel{natRule("DNAT", "ICMP", unknown, unknown)}},
		{name: "icmp with destination port", rules: []natRuleModel{natRule("DNAT", "ICMP", port, zero)}, wantErr: true},
		{name: "icmp with translated port", rules: []natRuleModel{natRule("DNAT", "ICMP", zero, port)}, wantErr: true},
		{name: "any protocol with port", rules: []natRuleModel{natRule("SNAT", "IP", port, zero)}, wantErr: true},
		{name: "binat without ports", rules: []natRuleModel{natRule("BINAT", "IP", zero, zero)}},
		{name: "binat with tcp port", rules: []natRuleModel{natRule("BINAT", "TCP", port, zero)}, wantErr: true},
		{name: "empty list", rules: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateNATRules(tc.rules, &diags)
			if got := diags.HasError(); got != tc.wantErr {
				t.Errorf("error = %v, want %v (%v)", got, tc.wantErr, diags)
			}
		})
	}
}

func firewallRule(protocol string, sport, dport types.Int64) firewallRuleModel {
	return firewallRuleModel{
		Action:          types.StringValue("Allow"),
		Direction:       types.StringValue("In"),
		Protocol:        types.StringValue(protocol),
		Source:          types.StringValue("0.0.0.0/0"),
		SourcePort:      sport,
		Destination:     types.StringValue("10.0.0.0/24"),
		DestinationPort: dport,
	}
}

func TestValidateFirewallRules(t *testing.T) {
	zero, port, null := types.Int64Value(0), types.Int64Value(80), types.Int64Null()

	tests := []struct {
		name    string
		rules   []firewallRuleModel
		wantErr bool
	}{
		{name: "tcp with ports", rules: []firewallRuleModel{firewallRule("TCP", zero, port)}},
		{name: "icmp without ports", rules: []firewallRuleModel{firewallRule("ICMP", zero, zero)}},
		{name: "icmp with null ports", rules: []firewallRuleModel{firewallRule("ICMP", null, null)}},
		{name: "icmp with source port", rules: []firewallRuleModel{firewallRule("ICMP", port, zero)}, wantErr: true},
		{name: "icmp with destination port", rules: []firewallRuleModel{firewallRule("ICMP", zero, port)}, wantErr: true},
		{name: "any protocol with port", rules: []firewallRuleModel{firewallRule("IP", zero, port)}, wantErr: true},
		// Duplicates are accepted by the API, so the provider must not reject them.
		{name: "duplicate rules", rules: []firewallRuleModel{firewallRule("TCP", zero, port), firewallRule("TCP", zero, port)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateFirewallRules(tc.rules, &diags)
			if got := diags.HasError(); got != tc.wantErr {
				t.Errorf("error = %v, want %v (%v)", got, tc.wantErr, diags)
			}
		})
	}
}

// A very large rule set makes the backend task fail and leaves the gateway Busy
// for a long time, so it is worth a warning — but not an error, because the
// exact limit is not documented.
func TestLargeRuleSetWarns(t *testing.T) {
	rules := make([]firewallRuleModel, rulesCountWarnThreshold+1)
	for i := range rules {
		rules[i] = firewallRule("TCP", types.Int64Value(0), types.Int64Value(int64(1000+i)))
	}

	var diags diag.Diagnostics
	validateFirewallRules(rules, &diags)
	if diags.HasError() {
		t.Errorf("a large rule set must not be an error, got %v", diags)
	}
	if diags.WarningsCount() != 1 {
		t.Errorf("expected exactly one warning, got %d (%v)", diags.WarningsCount(), diags)
	}

	var okDiags diag.Diagnostics
	validateFirewallRules(rules[:rulesCountWarnThreshold], &okDiags)
	if okDiags.WarningsCount() != 0 {
		t.Errorf("%d rules must not warn, got %v", rulesCountWarnThreshold, okDiags)
	}
}
