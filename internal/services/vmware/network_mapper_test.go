package vmware

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// coarseNetworkType is the hinge of the network resource: the configuration says
// "isolated" and the API answers "private_client", and every apply compares the
// two. A type that fell through the mapping unchanged would make the apply end in
// "inconsistent result after apply" — for a value the user never chose.
func TestCoarseNetworkType(t *testing.T) {
	cases := map[string]string{
		entities.VmwareNetworkTypePrivateClient:    networkTypeIsolated,
		entities.VmwareNetworkTypeRoutedClient:     networkTypeRouted,
		entities.VmwareNetworkTypePublicClient:     networkTypePublic,
		entities.VmwareNetworkTypePublicShared:     networkTypePublic,
		entities.VmwareNetworkTypePublicSharedIPv6: networkTypePublic,
	}
	for apiType, want := range cases {
		if got := coarseNetworkType(apiType); got != want {
			t.Errorf("coarseNetworkType(%q) = %q, want %q", apiType, got, want)
		}
	}

	// Every type the API publishes today is covered above. One it may publish
	// tomorrow is surfaced as it came rather than quietly mapped to a flavour it
	// might not be — the difference shows up, and nobody has to guess.
	if got := coarseNetworkType("something_new"); got != "something_new" {
		t.Errorf("an unknown type must be surfaced as-is, got %q", got)
	}
}

// The API omits what does not apply to a network: an isolated one reports no
// bandwidth (NET-3), and a network that has not been given a gateway reports
// none. Null is the honest answer for all of them — a zero would read as "the
// bandwidth is zero".
func TestMapNetworkToModelKeepsAbsentFieldsNull(t *testing.T) {
	model := mapNetworkToModel(&entities.VmwareNetwork{
		ID:         42,
		LocationID: 5,
		Type:       entities.VmwareNetworkTypePrivateClient,
		Name:       "app-private",
		State:      entities.VmwareNetworkStateActive,
		NICsCount:  0,
	})

	for name, value := range map[string]interface{ IsNull() bool }{
		"address":        model.Address,
		"mask":           model.Mask,
		"gateway":        model.Gateway,
		"bandwidth_mbps": model.BandwidthMbps,
		"is_dhcp":        model.IsDhcp,
		"shared":         model.Shared,
		// Write-only create inputs the API never returns; the caller restores them.
		"capacity":    model.Capacity,
		"enable_dhcp": model.EnableDhcp,
	} {
		if !value.IsNull() {
			t.Errorf("%s must be null when the API does not report it", name)
		}
	}

	if model.ID.ValueInt64() != 42 || model.Name.ValueString() != "app-private" {
		t.Errorf("the fields the API does report must come through: %+v", model)
	}
}

func TestMapNetworkToModelReadsReportedFields(t *testing.T) {
	address, gateway := "10.100.0.0", "10.100.0.1"
	mask, bandwidth := 24, 20
	dhcp, shared := true, false

	model := mapNetworkToModel(&entities.VmwareNetwork{
		ID: 7, Type: entities.VmwareNetworkTypeRoutedClient, Name: "app",
		Address: &address, Mask: &mask, Gateway: &gateway,
		BandwidthMbps: &bandwidth, IsDhcp: &dhcp, Shared: &shared,
	})

	if model.Address.ValueString() != address || model.Gateway.ValueString() != gateway {
		t.Errorf("addresses did not come through: %+v", model)
	}
	if model.Mask.ValueInt64() != 24 || model.BandwidthMbps.ValueInt64() != 20 {
		t.Errorf("numbers did not come through: %+v", model)
	}
	if !model.IsDhcp.ValueBool() || model.Shared.ValueBool() {
		t.Errorf("flags did not come through: %+v", model)
	}
}

// SRV-3: the backend upper-cases the guest hostname, so a configuration written
// in lower case has to be recognised as the same value or it diffs for ever.
func TestPrimaryNICAndComputedServerFields(t *testing.T) {
	hostname := "WEB01"
	ip := "203.0.113.5"
	diskType := "ssd"

	var model serverModel
	mapServerComputed(&model, &entities.VmwareServer{
		ID: 5678, LocationID: 5, Name: "web-01", ComputerName: &hostname,
		CPU: 2, RamMB: 4096, SystemDiskMB: 51200, SystemDiskType: &diskType,
		State: entities.VmwareServerStateActive, IsPowerOn: true,
		NICs: []entities.VmwareNIC{
			{ID: 1, Number: 1, IsPrimary: false, NetworkID: 200, BandwidthMbps: 0},
			{ID: 2, Number: 0, IsPrimary: true, NetworkID: 100, IP: &ip, BandwidthMbps: 100},
		},
	})

	// SRV-5: the bandwidth of the primary interface is what network_bandwidth_mbps
	// reports, so a change made in the panel shows up as a difference.
	if model.NetworkBandwidthMbps.ValueInt64() != 100 {
		t.Errorf("network_bandwidth_mbps = %v, want 100 read from the primary interface",
			model.NetworkBandwidthMbps)
	}
	if model.ComputerName.ValueString() != "WEB01" {
		t.Errorf("computer_name = %q, want the platform's spelling", model.ComputerName.ValueString())
	}
	if model.VmToolsInstalled.IsNull() != true {
		t.Error("vm_tools_installed must stay null when the API omits it (SRV-4: absent from list responses)")
	}
}

// A server whose primary interface reports no bandwidth must not have the
// attribute overwritten with a zero — that would be a value no interface has.
func TestMapServerComputedKeepsBandwidthWhenNotReported(t *testing.T) {
	model := serverModel{NetworkBandwidthMbps: types.Int64Value(100)}
	mapServerComputed(&model, &entities.VmwareServer{
		ID: 1, NICs: []entities.VmwareNIC{{ID: 2, IsPrimary: true, BandwidthMbps: 0}},
	})

	if model.NetworkBandwidthMbps.ValueInt64() != 100 {
		t.Errorf("network_bandwidth_mbps = %v, want the prior value kept", model.NetworkBandwidthMbps)
	}
}

func TestPrimaryNIC(t *testing.T) {
	nics := []entities.VmwareNIC{
		{ID: 1, IsPrimary: false},
		{ID: 2, IsPrimary: true},
		{ID: 3, IsPrimary: false},
	}
	if got := primaryNIC(nics); got == nil || got.ID != 2 {
		t.Errorf("primaryNIC = %v, want the interface with id 2", got)
	}
	if got := primaryNIC([]entities.VmwareNIC{{ID: 1}}); got != nil {
		t.Errorf("primaryNIC = %v, want nil when no interface is primary", got)
	}
}

// Pairing NAT rules by position only holds while the API keeps the order it was
// given. If it ever stops, the plan never settles — so the provider says so once
// instead of leaving the user to work it out from a diff that keeps returning.
func TestWarnNATOrderMismatch(t *testing.T) {
	dnat := func(port, target string) edgeNATRuleModel {
		return edgeNATRuleModel{
			Type:         types.StringValue(entities.VmwareEdgeNATTypeDNAT),
			OriginalPort: types.StringValue(port),
			TranslatedIP: types.StringValue(target),
		}
	}

	planned := []edgeNATRuleModel{dnat("443", "10.0.0.10"), dnat("8080", "10.0.0.11")}

	var quiet diag.Diagnostics
	warnNATOrderMismatch(planned, []edgeNATRuleModel{dnat("443", "10.0.0.10"), dnat("8080", "10.0.0.11")}, &quiet)
	if len(quiet) != 0 {
		t.Fatalf("the order the rules were written in must not warn, got: %v", quiet)
	}

	var swapped diag.Diagnostics
	warnNATOrderMismatch(planned, []edgeNATRuleModel{dnat("8080", "10.0.0.11"), dnat("443", "10.0.0.10")}, &swapped)
	if swapped.WarningsCount() != 1 {
		t.Fatalf("a reordered rule set must warn once, got: %v", swapped)
	}
	if got := swapped.Warnings()[0].Summary(); got != "NAT Rules Are Not In The Order They Were Written" {
		t.Errorf("unexpected warning: %q", got)
	}

	// A rule the API has not created yet is not evidence of anything.
	var short diag.Diagnostics
	warnNATOrderMismatch(planned, []edgeNATRuleModel{dnat("443", "10.0.0.10")}, &short)
	if len(short) != 0 {
		t.Errorf("a shorter answer must not warn about order, got: %v", short)
	}
}
