package gateway

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// TestGatewayNICs_flattenClassifiesByIP — data source flatten: private IPs →
// isolated_net_nics, public ones → public_net_nics (with bandwidth from the API).
func TestGatewayNICs_flattenClassifiesByIP(t *testing.T) {
	gw := &entities.Gateway{
		NICs: []entities.GatewayNIC{
			{ID: 1, NetworkID: "netA", IPAddress: "10.0.0.2", BandwidthMbps: 0},         // private
			{ID: 2, NetworkID: "wan-sys", IPAddress: "203.0.113.5", BandwidthMbps: 100}, // WAN
		},
	}

	iso, pub := flattenGatewayNICs(gw)

	if len(iso) != 1 {
		t.Fatalf("expected 1 isolated NIC, got %d", len(iso))
	}
	if iso[0].NetworkID.ValueString() != "netA" {
		t.Errorf("isolated NIC network_id = %q, want netA", iso[0].NetworkID.ValueString())
	}
	if iso[0].ID.ValueInt64() != 1 || iso[0].IPAddress.ValueString() != "10.0.0.2" {
		t.Errorf("isolated NIC computed fields not mapped from API: %+v", iso[0])
	}
	if len(pub) != 1 {
		t.Fatalf("expected 1 public NIC (WAN), got %d", len(pub))
	}
	if pub[0].IPAddress.ValueString() != "203.0.113.5" || pub[0].ID.ValueInt64() != 2 {
		t.Errorf("public NIC computed fields not mapped from API: %+v", pub[0])
	}
	if pub[0].BandwidthMbps.ValueInt64() != 100 {
		t.Errorf("public NIC bandwidth = %d, want 100 (from API)", pub[0].BandwidthMbps.ValueInt64())
	}
}

// TestGatewayNICs_flattenEmpty — an empty NIC list → nil slices (null-vs-empty).
func TestGatewayNICs_flattenEmpty(t *testing.T) {
	iso, pub := flattenGatewayNICs(&entities.Gateway{})
	if iso != nil || pub != nil {
		t.Errorf("expected nil slices for a gateway with no NICs, got iso=%v pub=%v", iso, pub)
	}
}

// TestGatewayNICs_wanBandwidth — drift detection: bandwidth is read from the
// WAN NIC; if there's no WAN NIC, fallback to the previous value.
func TestGatewayNICs_wanBandwidth(t *testing.T) {
	gw := &entities.Gateway{
		NICs: []entities.GatewayNIC{
			{ID: 1, NetworkID: "netA", IPAddress: "10.0.0.2", BandwidthMbps: 0},
			{ID: 2, NetworkID: "wan-sys", IPAddress: "203.0.113.5", BandwidthMbps: 250},
		},
	}
	if got := wanBandwidth(gw, types.Int64Value(100)); got.ValueInt64() != 250 {
		t.Errorf("wanBandwidth = %d, want 250 (from WAN NIC, not prior)", got.ValueInt64())
	}
	// No WAN NIC → prior.
	gwNoWAN := &entities.Gateway{
		NICs: []entities.GatewayNIC{{ID: 1, NetworkID: "netA", IPAddress: "10.0.0.2"}},
	}
	if got := wanBandwidth(gwNoWAN, types.Int64Value(100)); got.ValueInt64() != 100 {
		t.Errorf("wanBandwidth fallback = %d, want 100 (prior)", got.ValueInt64())
	}
}

// TestGatewayNICs_wanIP — the external address published as public_ip. Every
// NAT rule has to name it, so a gateway without a WAN NIC must yield null
// rather than an empty string that would be sent to the API verbatim.
func TestGatewayNICs_wanIP(t *testing.T) {
	gw := &entities.Gateway{
		NICs: []entities.GatewayNIC{
			{ID: 1, NetworkID: "netA", IPAddress: "10.0.0.2"},
			{ID: 2, NetworkID: "wan-sys", IPAddress: "203.0.113.5", BandwidthMbps: 250},
		},
	}
	if got := wanIP(gw); got.ValueString() != "203.0.113.5" {
		t.Errorf("wanIP = %q, want 203.0.113.5", got.ValueString())
	}

	gwNoWAN := &entities.Gateway{
		NICs: []entities.GatewayNIC{{ID: 1, NetworkID: "netA", IPAddress: "10.0.0.2"}},
	}
	if got := wanIP(gwNoWAN); !got.IsNull() {
		t.Errorf("wanIP without a WAN NIC = %q, want null", got.ValueString())
	}
}

func TestGatewayNICs_isPrivateIP(t *testing.T) {
	cases := map[string]bool{
		"10.0.0.2":    true,
		"172.16.5.1":  true,
		"192.168.1.1": true,
		"203.0.113.5": false,
		"8.8.8.8":     false,
		"172.32.0.1":  false, // outside 172.16/12
		"":            false,
	}
	for ip, want := range cases {
		if got := isPrivateIP(ip); got != want {
			t.Errorf("isPrivateIP(%q) = %v, want %v", ip, got, want)
		}
	}
}
