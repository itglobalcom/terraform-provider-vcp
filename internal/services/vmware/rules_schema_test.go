package vmware

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func ruleSchema(t *testing.T, name string, ctor func() resource.Resource) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	ctor().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("%s schema is invalid: %v", name, diags)
	}
	return resp.Schema
}

// ruleAttrs returns the attributes of one element of the `rules` list, failing
// when `rules` is not a list — the type is part of the contract: an entry is
// paired with an existing rule by position, which a set cannot express.
func ruleAttrs(t *testing.T, s schema.Schema, name string) map[string]schema.Attribute {
	t.Helper()
	rules, ok := s.Attributes["rules"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf(`%s: "rules" must be a schema.ListNestedAttribute, got %T`, name, s.Attributes["rules"])
	}
	if !rules.IsRequired() {
		t.Errorf(`%s: "rules" must be Required (an empty list clears the rule set)`, name)
	}
	return rules.NestedObject.Attributes
}

// requireAttrs asserts that each named attribute exists and is Required.
func requireAttrs(t *testing.T, attrs map[string]schema.Attribute, owner string, names ...string) {
	t.Helper()
	for _, name := range names {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("%s is missing attribute %q", owner, name)
			continue
		}
		if !attr.IsRequired() {
			t.Errorf("%s attribute %q must be Required (the API requires it)", owner, name)
		}
	}
}

// requireOptionalComputed asserts that each named attribute is Optional+Computed:
// the platform fills in what the configuration leaves out, and the value it chose
// has to land in state instead of staying null and diffing forever.
func requireOptionalComputed(t *testing.T, attrs map[string]schema.Attribute, owner string, names ...string) {
	t.Helper()
	for _, name := range names {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("%s is missing attribute %q", owner, name)
			continue
		}
		if !attr.IsOptional() || !attr.IsComputed() {
			t.Errorf("%s attribute %q must be Optional+Computed (the platform supplies a value)", owner, name)
		}
	}
}

func TestEdgeFirewallResourceSchema(t *testing.T) {
	s := ruleSchema(t, "vcp_vmware_edge_firewall", NewEdgeFirewallResource)

	if !s.Attributes["network_id"].IsRequired() {
		t.Error(`"network_id" must be Required`)
	}
	if !s.Attributes["id"].IsComputed() {
		t.Error(`"id" must be Computed`)
	}
	if !s.Attributes["default_action"].IsRequired() {
		t.Error(`"default_action" must be Required — it is the mode the whole list works in`)
	}

	attrs := ruleAttrs(t, s, "vcp_vmware_edge_firewall")
	requireAttrs(t, attrs, "edge firewall rule", "name", "action", "protocol")
	requireOptionalComputed(t, attrs, "edge firewall rule", "source", "source_port", "destination", "destination_port")
}

func TestEdgeNATResourceSchema(t *testing.T) {
	s := ruleSchema(t, "vcp_vmware_edge_nat", NewEdgeNATResource)

	if !s.Attributes["network_id"].IsRequired() {
		t.Error(`"network_id" must be Required`)
	}
	if !s.Attributes["id"].IsComputed() {
		t.Error(`"id" must be Computed`)
	}

	attrs := ruleAttrs(t, s, "vcp_vmware_edge_nat")
	requireAttrs(t, attrs, "NAT rule", "type", "protocol")
	// Both addresses are Optional+Computed: the platform supplies original_ip of a
	// DNAT rule (NET-4) and translated_ip of an SNAT rule, and which one a given
	// rule has to declare is decided by ValidateConfig, not by the schema.
	requireOptionalComputed(t, attrs, "NAT rule",
		"original_ip", "original_port", "translated_ip", "translated_port", "description")

	enabled, ok := attrs["enabled"]
	if !ok {
		t.Fatal(`NAT rule is missing attribute "enabled"`)
	}
	if !enabled.IsOptional() || !enabled.IsComputed() {
		t.Error(`NAT rule attribute "enabled" must be Optional+Computed (rules are created enabled)`)
	}
}

func TestServerFirewallResourceSchema(t *testing.T) {
	s := ruleSchema(t, "vcp_vmware_server_firewall", NewServerFirewallResource)

	if !s.Attributes["server_id"].IsRequired() {
		t.Error(`"server_id" must be Required`)
	}
	if !s.Attributes["id"].IsComputed() {
		t.Error(`"id" must be Computed`)
	}
	if _, ok := s.Attributes["default_action"]; ok {
		t.Error(`vcp_vmware_server_firewall must not have "default_action" — the server firewall has no such mode`)
	}

	attrs := ruleAttrs(t, s, "vcp_vmware_server_firewall")
	requireAttrs(t, attrs, "server firewall rule", "name", "traffic_direction", "action", "protocol")
	requireOptionalComputed(t, attrs, "server firewall rule", "source", "source_port", "destination", "destination_port")
}

// The edge firewall rule must not offer `enabled` or `description`: the API takes
// both and ignores them (NET-7 — it forces enabled to true and derives the
// description from the name), so a configuration that set them would silently
// mean nothing. Nor may any rule carry an id or a number: the resource pairs a
// configuration entry with an API rule by position, and an id in the
// configuration would be a second, conflicting key.
func TestRuleSchemasHaveNoInventedFields(t *testing.T) {
	edgeFW := ruleAttrs(t, ruleSchema(t, "vcp_vmware_edge_firewall", NewEdgeFirewallResource), "edge firewall")
	for _, name := range []string{"enabled", "description", "id", "number", "priority"} {
		if _, ok := edgeFW[name]; ok {
			t.Errorf("edge firewall rule must not have %q — the API does not round-trip it", name)
		}
	}

	nat := ruleAttrs(t, ruleSchema(t, "vcp_vmware_edge_nat", NewEdgeNATResource), "edge NAT")
	for _, name := range []string{"id", "vcloud_id", "number", "priority"} {
		if _, ok := nat[name]; ok {
			t.Errorf("NAT rule must not have %q — rules are matched by position", name)
		}
	}

	serverFW := ruleAttrs(t, ruleSchema(t, "vcp_vmware_server_firewall", NewServerFirewallResource), "server firewall")
	for _, name := range []string{"id", "number", "priority", "enabled"} {
		if _, ok := serverFW[name]; ok {
			t.Errorf("server firewall rule must not have %q — the API does not store it", name)
		}
	}
}

func TestServerNICResourceSchemas(t *testing.T) {
	attach := ruleSchema(t, "vcp_vmware_server_network_attachment", NewServerNetworkAttachmentResource)
	for _, name := range []string{"server_id", "network_id"} {
		if !attach.Attributes[name].IsRequired() {
			t.Errorf("vcp_vmware_server_network_attachment: %q must be Required", name)
		}
	}
	if !attach.Attributes["ip"].IsOptional() || !attach.Attributes["ip"].IsComputed() {
		t.Error(`vcp_vmware_server_network_attachment: "ip" must be Optional+Computed (the platform assigns it)`)
	}

	pubif := ruleSchema(t, "vcp_vmware_server_public_interface", NewServerPublicInterfaceResource)
	if !pubif.Attributes["server_id"].IsRequired() {
		t.Error(`vcp_vmware_server_public_interface: "server_id" must be Required`)
	}
	// The bandwidth is the one thing this resource sets and the API edits in place.
	if !pubif.Attributes["bandwidth_mbps"].IsRequired() {
		t.Error(`vcp_vmware_server_public_interface: "bandwidth_mbps" must be Required`)
	}
	for _, name := range []string{"network_id", "ip", "mac", "number"} {
		if !pubif.Attributes[name].IsComputed() {
			t.Errorf("vcp_vmware_server_public_interface: %q must be Computed (the platform picks it)", name)
		}
	}
}

// The `nics` and `gpu` attributes are described twice: once as a schema, and
// once as the attr.Type map the mapper builds values with. They have to agree
// exactly — an attribute in one and not the other makes every apply fail while
// converting the value, and no schema check on its own would notice.
//
// This is not hypothetical: `bandwidth_mbps` was added to the data source and
// the mapper but missed in the resource, which broke every server apply until
// this test was written.
func TestNestedAttributeTypesMatchTheSchemas(t *testing.T) {
	nested := func(t *testing.T, attrs map[string]schema.Attribute, name string) map[string]schema.Attribute {
		t.Helper()
		attr, ok := attrs[name]
		if !ok {
			t.Fatalf("no %q attribute", name)
		}
		switch typed := attr.(type) {
		case schema.ListNestedAttribute:
			return typed.NestedObject.Attributes
		case schema.SingleNestedAttribute:
			return typed.Attributes
		default:
			t.Fatalf("%q is a %T, which carries no nested attributes", name, attr)
			return nil
		}
	}

	sameKeys := func(t *testing.T, what string, attrs map[string]schema.Attribute, types map[string]attr.Type) {
		t.Helper()
		for name := range types {
			if _, ok := attrs[name]; !ok {
				t.Errorf("%s: the mapper builds %q but the schema does not declare it — "+
					"every apply would fail converting the value", what, name)
			}
		}
		for name := range attrs {
			if _, ok := types[name]; !ok {
				t.Errorf("%s: the schema declares %q but the mapper never fills it in", what, name)
			}
		}
	}

	serverAttrs := ruleSchema(t, "vcp_vmware_server", NewServerResource).Attributes
	sameKeys(t, "vcp_vmware_server.nics", nested(t, serverAttrs, "nics"), nicAttrTypes)
	sameKeys(t, "vcp_vmware_server.gpu", nested(t, serverAttrs, "gpu"), gpuAttrTypes)
}
