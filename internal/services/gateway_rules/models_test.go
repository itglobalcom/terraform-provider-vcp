package gateway_rules

import (
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

func TestNATRulesRoundTrip(t *testing.T) {
	// Deliberately in an order the API would not produce by sorting: the API
	// preserves the order it is given, and so must the provider.
	api := []entities.NATRule{
		{Type: "SNAT", Protocol: "IP", Source: "10.0.0.0/24", Destination: "0.0.0.0/0", Translated: "45.14.48.213"},
		{Type: "DNAT", Protocol: "TCP", Source: "0.0.0.0/0", Destination: "45.14.48.213/32", DestinationPort: 443,
			Translated: "10.0.0.5", TranslatedPort: 8443},
		{Type: "BINAT", Protocol: "IP", Source: "10.0.0.6/32", Destination: "0.0.0.0/0", Translated: "45.14.48.213"},
	}

	got := expandNATRules(flattenNATRules(api))
	if !reflect.DeepEqual(got, api) {
		t.Errorf("NAT round-trip changed the rules:\n got: %+v\nwant: %+v", got, api)
	}
}

func TestFirewallRulesRoundTrip(t *testing.T) {
	api := []entities.FirewallRule{
		{Action: "Deny", Direction: "In", Protocol: "IP", Source: "0.0.0.0/0", Destination: "0.0.0.0/0"},
		{Action: "Allow", Direction: "In", Protocol: "TCP", Source: "0.0.0.0/0", Destination: "10.0.0.5/32", DestinationPort: 8443},
		{Action: "Allow", Direction: "Out", Protocol: "IP", Source: "10.0.0.0/24", Destination: "0.0.0.0/0"},
	}

	got := expandFirewallRules(flattenFirewallRules(api))
	if !reflect.DeepEqual(got, api) {
		t.Errorf("firewall round-trip changed the rules:\n got: %+v\nwant: %+v", got, api)
	}
}

// An empty rule list must stay an empty list, never become null: a configuration
// with `rules = []` has to match what the API reports for a gateway with no
// rules, otherwise every plan would show a diff.
func TestFlattenEmptyRulesIsEmptyNotNil(t *testing.T) {
	if got := flattenNATRules(nil); got == nil || len(got) != 0 {
		t.Errorf("flattenNATRules(nil) = %#v, want empty non-nil slice", got)
	}
	if got := flattenFirewallRules(nil); got == nil || len(got) != 0 {
		t.Errorf("flattenFirewallRules(nil) = %#v, want empty non-nil slice", got)
	}
	if got := expandNATRules(nil); got == nil || len(got) != 0 {
		t.Errorf("expandNATRules(nil) = %#v, want empty non-nil slice (the API rejects a null list)", got)
	}
	if got := expandFirewallRules(nil); got == nil || len(got) != 0 {
		t.Errorf("expandFirewallRules(nil) = %#v, want empty non-nil slice (the API rejects a null list)", got)
	}
}

// A port left out of the configuration is null in the plan and must be sent as
// 0 ("any"), which is how the API reads a missing port.
func TestExpandNullPortsBecomeAny(t *testing.T) {
	nat := expandNATRules([]natRuleModel{{
		Type:            types.StringValue("SNAT"),
		Protocol:        types.StringValue("IP"),
		Source:          types.StringValue("10.0.0.0/24"),
		Destination:     types.StringValue("0.0.0.0/0"),
		DestinationPort: types.Int64Null(),
		Translated:      types.StringValue("45.14.48.213"),
		TranslatedPort:  types.Int64Null(),
	}})
	if nat[0].DestinationPort != 0 || nat[0].TranslatedPort != 0 {
		t.Errorf("null NAT ports must expand to 0, got %d/%d", nat[0].DestinationPort, nat[0].TranslatedPort)
	}

	fw := expandFirewallRules([]firewallRuleModel{{
		Action:          types.StringValue("Allow"),
		Direction:       types.StringValue("In"),
		Protocol:        types.StringValue("ICMP"),
		Source:          types.StringValue("0.0.0.0/0"),
		SourcePort:      types.Int64Null(),
		Destination:     types.StringValue("10.0.0.0/24"),
		DestinationPort: types.Int64Null(),
	}})
	if fw[0].SourcePort != 0 || fw[0].DestinationPort != 0 {
		t.Errorf("null firewall ports must expand to 0, got %d/%d", fw[0].SourcePort, fw[0].DestinationPort)
	}
}
