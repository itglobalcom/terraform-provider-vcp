package gateway_rules

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func natSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewNATResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("vcp_gateway_nat schema is invalid: %v", diags)
	}
	return resp.Schema
}

func firewallSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewFirewallResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("vcp_gateway_firewall schema is invalid: %v", diags)
	}
	return resp.Schema
}

func TestNATResourceSchema(t *testing.T) {
	s := natSchema(t)

	if _, ok := s.Attributes["gateway_id"]; !ok {
		t.Fatal(`missing attribute "gateway_id"`)
	}
	if !s.Attributes["gateway_id"].IsRequired() {
		t.Error(`"gateway_id" must be Required`)
	}
	if !s.Attributes["id"].IsComputed() {
		t.Error(`"id" must be Computed`)
	}

	rules, ok := s.Attributes["rules"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf(`"rules" must be a schema.ListNestedAttribute (order is significant), got %T`, s.Attributes["rules"])
	}
	if !rules.IsRequired() {
		t.Error(`"rules" must be Required (an empty list clears the rule set)`)
	}

	attrs := rules.NestedObject.Attributes
	for _, name := range []string{"type", "protocol", "source", "destination", "translated"} {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("NAT rule is missing attribute %q", name)
			continue
		}
		if !attr.IsRequired() {
			t.Errorf("NAT rule attribute %q must be Required (the API requires it)", name)
		}
	}
	// Ports default to 0 ("any"), which is the only value the API accepts for
	// ICMP/IP/BINAT — writing them out would be noise.
	for _, name := range []string{"destination_port", "translated_port"} {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("NAT rule is missing attribute %q", name)
			continue
		}
		if !attr.IsOptional() || !attr.IsComputed() {
			t.Errorf("NAT rule attribute %q must be Optional+Computed with a default", name)
		}
	}
}

func TestFirewallResourceSchema(t *testing.T) {
	s := firewallSchema(t)

	if !s.Attributes["gateway_id"].IsRequired() {
		t.Error(`"gateway_id" must be Required`)
	}
	if !s.Attributes["id"].IsComputed() {
		t.Error(`"id" must be Computed`)
	}

	rules, ok := s.Attributes["rules"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf(`"rules" must be a schema.ListNestedAttribute (the last matching rule wins), got %T`, s.Attributes["rules"])
	}
	if !rules.IsRequired() {
		t.Error(`"rules" must be Required (an empty list clears the rule set)`)
	}

	attrs := rules.NestedObject.Attributes
	for _, name := range []string{"action", "direction", "protocol", "source", "destination"} {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("firewall rule is missing attribute %q", name)
			continue
		}
		if !attr.IsRequired() {
			t.Errorf("firewall rule attribute %q must be Required (the API requires it)", name)
		}
	}
	for _, name := range []string{"source_port", "destination_port"} {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("firewall rule is missing attribute %q", name)
			continue
		}
		if !attr.IsOptional() || !attr.IsComputed() {
			t.Errorf("firewall rule attribute %q must be Optional+Computed with a default", name)
		}
	}
}

// The rule models must not grow fields the API does not store (a name, an
// enabled flag, a rule number): there would be nothing to read them back from,
// so every refresh would have to guess and every out-of-band edit would drift.
func TestRuleSchemasHaveNoInventedFields(t *testing.T) {
	natAttrs := natSchema(t).Attributes["rules"].(schema.ListNestedAttribute).NestedObject.Attributes
	fwAttrs := firewallSchema(t).Attributes["rules"].(schema.ListNestedAttribute).NestedObject.Attributes

	for _, name := range []string{"name", "enabled", "number", "description", "priority"} {
		if _, ok := natAttrs[name]; ok {
			t.Errorf("NAT rule must not have %q — the API does not store it", name)
		}
		if _, ok := fwAttrs[name]; ok {
			t.Errorf("firewall rule must not have %q — the API does not store it", name)
		}
	}
	if _, ok := firewallSchema(t).Attributes["default_action"]; ok {
		t.Error(`vcp_gateway_firewall must not have "default_action" — the API has no such field`)
	}
}
