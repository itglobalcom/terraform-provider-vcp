package gateway

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// The data sources flatten rules with their own models, separate from the ones
// the vcp_gateway_nat / vcp_gateway_firewall resources use. Every field gets a
// distinct value here, so a mapping mixed up between two fields of the same type
// (source ↔ destination, either port) fails the test instead of quietly
// reporting the wrong rule.
func TestFlattenNATRulesData(t *testing.T) {
	got := flattenNATRulesData([]entities.NATRule{{
		Type:            "DNAT",
		Protocol:        "TCP",
		Source:          "10.1.0.0/24",
		Destination:     "203.0.113.5/32",
		DestinationPort: 1111,
		Translated:      "10.2.0.7",
		TranslatedPort:  2222,
	}})

	if len(got) != 1 {
		t.Fatalf("flattened %d rule(s), want 1", len(got))
	}
	rule := got[0]

	for _, tc := range []struct {
		field string
		got   string
		want  string
	}{
		{"type", rule.Type.ValueString(), "DNAT"},
		{"protocol", rule.Protocol.ValueString(), "TCP"},
		{"source", rule.Source.ValueString(), "10.1.0.0/24"},
		{"destination", rule.Destination.ValueString(), "203.0.113.5/32"},
		{"translated", rule.Translated.ValueString(), "10.2.0.7"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}
	if rule.DestinationPort.ValueInt64() != 1111 {
		t.Errorf("destination_port = %d, want 1111", rule.DestinationPort.ValueInt64())
	}
	if rule.TranslatedPort.ValueInt64() != 2222 {
		t.Errorf("translated_port = %d, want 2222", rule.TranslatedPort.ValueInt64())
	}
}

func TestFlattenFirewallRulesData(t *testing.T) {
	got := flattenFirewallRulesData([]entities.FirewallRule{{
		Action:          "Deny",
		Direction:       "Out",
		Protocol:        "UDP",
		Source:          "10.3.0.0/24",
		SourcePort:      3333,
		Destination:     "10.4.0.0/24",
		DestinationPort: 4444,
	}})

	if len(got) != 1 {
		t.Fatalf("flattened %d rule(s), want 1", len(got))
	}
	rule := got[0]

	for _, tc := range []struct {
		field string
		got   string
		want  string
	}{
		{"action", rule.Action.ValueString(), "Deny"},
		{"direction", rule.Direction.ValueString(), "Out"},
		{"protocol", rule.Protocol.ValueString(), "UDP"},
		{"source", rule.Source.ValueString(), "10.3.0.0/24"},
		{"destination", rule.Destination.ValueString(), "10.4.0.0/24"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}
	if rule.SourcePort.ValueInt64() != 3333 {
		t.Errorf("source_port = %d, want 3333", rule.SourcePort.ValueInt64())
	}
	if rule.DestinationPort.ValueInt64() != 4444 {
		t.Errorf("destination_port = %d, want 4444", rule.DestinationPort.ValueInt64())
	}
}

// Order is part of the meaning of a rule list (for firewall rules the last match
// wins), so the data source must not reshuffle it.
func TestFlattenRulesDataKeepsOrder(t *testing.T) {
	nat := flattenNATRulesData([]entities.NATRule{
		{Type: "SNAT", Protocol: "IP", Source: "10.0.1.0/24", Destination: "0.0.0.0/0", Translated: "203.0.113.5"},
		{Type: "DNAT", Protocol: "TCP", Source: "0.0.0.0/0", Destination: "203.0.113.5/32", Translated: "10.0.1.7"},
	})
	if nat[0].Type.ValueString() != "SNAT" || nat[1].Type.ValueString() != "DNAT" {
		t.Errorf("NAT order changed: %q, %q", nat[0].Type.ValueString(), nat[1].Type.ValueString())
	}

	fw := flattenFirewallRulesData([]entities.FirewallRule{
		{Action: "Deny", Direction: "In", Protocol: "IP", Source: "0.0.0.0/0", Destination: "0.0.0.0/0"},
		{Action: "Allow", Direction: "In", Protocol: "TCP", Source: "0.0.0.0/0", Destination: "10.0.1.7/32"},
	})
	if fw[0].Action.ValueString() != "Deny" || fw[1].Action.ValueString() != "Allow" {
		t.Errorf("firewall order changed: %q, %q", fw[0].Action.ValueString(), fw[1].Action.ValueString())
	}
}

// A gateway without rules has to come out as an empty list, not null: audit-style
// configurations run length() and for_each over these.
func TestFlattenRulesDataEmptyNotNil(t *testing.T) {
	if got := flattenNATRulesData(nil); got == nil || len(got) != 0 {
		t.Errorf("flattenNATRulesData(nil) = %#v, want empty non-nil slice", got)
	}
	if got := flattenFirewallRulesData(nil); got == nil || len(got) != 0 {
		t.Errorf("flattenFirewallRulesData(nil) = %#v, want empty non-nil slice", got)
	}
}

// The schema attribute names have to match the model's tfsdk tags — a mismatch
// only shows up as a runtime error while reading the data source, which no unit
// test of the flatten functions would catch.
func TestGatewayDataSourceRuleSchemas(t *testing.T) {
	ctx := context.Background()

	var resp datasource.SchemaResponse
	NewGatewayDataSource().Schema(ctx, datasource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("vcp_gateway data source schema is invalid: %v", diags)
	}

	natAttrs := nestedAttributeNames(t, resp.Schema, "nat_rules")
	for _, name := range []string{"type", "protocol", "source", "destination", "destination_port", "translated", "translated_port"} {
		if _, ok := natAttrs[name]; !ok {
			t.Errorf("nat_rules is missing attribute %q", name)
		}
	}
	if len(natAttrs) != 7 {
		t.Errorf("nat_rules has %d attributes, want exactly the 7 fields of a NAT rule", len(natAttrs))
	}

	fwAttrs := nestedAttributeNames(t, resp.Schema, "firewall_rules")
	for _, name := range []string{"action", "direction", "protocol", "source", "source_port", "destination", "destination_port"} {
		if _, ok := fwAttrs[name]; !ok {
			t.Errorf("firewall_rules is missing attribute %q", name)
		}
	}
	if len(fwAttrs) != 7 {
		t.Errorf("firewall_rules has %d attributes, want exactly the 7 fields of a firewall rule", len(fwAttrs))
	}

	// The plural data source reuses the same nested schemas; validating it keeps
	// the two from drifting apart.
	var listResp datasource.SchemaResponse
	NewGatewaysDataSource().Schema(ctx, datasource.SchemaRequest{}, &listResp)
	if diags := listResp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("vcp_gateways data source schema is invalid: %v", diags)
	}
}

func nestedAttributeNames(t *testing.T, s schema.Schema, attribute string) map[string]schema.Attribute {
	t.Helper()
	attr, ok := s.Attributes[attribute]
	if !ok {
		t.Fatalf("data source schema is missing %q", attribute)
	}
	nested, ok := attr.(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("%q must be a schema.ListNestedAttribute, got %T", attribute, attr)
	}
	return nested.NestedObject.Attributes
}
