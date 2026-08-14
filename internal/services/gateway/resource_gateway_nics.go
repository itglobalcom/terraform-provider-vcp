package gateway

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// DATA SOURCE NIC MODELS + FLATTEN
// Used only by the data sources (vcp_gateway / vcp_gateways) as a read-only
// reflection of all gateway NICs. The vcp_gateway resource does not store
// networks in state — they live in vcp_gateway_network_attachment.
// ============================================================================

// isolatedNICModel — a private network attached to the gateway (data source).
type isolatedNICModel struct {
	NetworkID types.String `tfsdk:"network_id"`
	ID        types.Int64  `tfsdk:"id"`
	IPAddress types.String `tfsdk:"ip_address"`
}

// publicNICModel — an external (WAN) interface (data source).
type publicNICModel struct {
	BandwidthMbps types.Int64  `tfsdk:"bandwidth_mbps"`
	ID            types.Int64  `tfsdk:"id"`
	IPAddress     types.String `tfsdk:"ip_address"`
}

// flattenGatewayNICs splits the API's NICs into private (isolated) and
// external (public) ones by IP type: RFC1918 → isolated, public → public.
// Empty slices → nil (null-vs-empty discipline).
func flattenGatewayNICs(gw *entities.Gateway) ([]isolatedNICModel, []publicNICModel) {
	iso := make([]isolatedNICModel, 0, len(gw.NICs))
	pub := make([]publicNICModel, 0, 1)

	for _, n := range gw.NICs {
		if isPrivateIP(n.IPAddress) {
			iso = append(iso, isolatedNICModel{
				NetworkID: types.StringValue(n.NetworkID),
				ID:        types.Int64Value(int64(n.ID)),
				IPAddress: types.StringValue(n.IPAddress),
			})
		} else {
			pub = append(pub, publicNICModel{
				BandwidthMbps: types.Int64Value(int64(n.BandwidthMbps)),
				ID:            types.Int64Value(int64(n.ID)),
				IPAddress:     types.StringValue(n.IPAddress),
			})
		}
	}

	if len(iso) == 0 {
		iso = nil
	}
	if len(pub) == 0 {
		pub = nil
	}
	return iso, pub
}

// wanIP returns the address of the external (WAN) interface — the first NIC
// with a public IP. Every NAT rule has to name this address (the API requires
// it in `destination` for DNAT and in `translated` for SNAT/BINAT), so the
// gateway publishes it as a computed attribute. Null if the gateway has no
// public NIC.
func wanIP(gw *entities.Gateway) types.String {
	for _, n := range gw.NICs {
		if !isPrivateIP(n.IPAddress) && n.IPAddress != "" {
			return types.StringValue(n.IPAddress)
		}
	}
	return types.StringNull()
}

// wanBandwidth returns the actual bandwidth of the external (WAN) interface
// from the API — the NIC with a public IP. Used in the resource's Read for
// drift detection: if bandwidth was changed outside Terraform, the plan will
// show it. Fallback is the previous value from state (if the WAN NIC isn't
// found, we don't make one up).
func wanBandwidth(gw *entities.Gateway, prior types.Int64) types.Int64 {
	for _, n := range gw.NICs {
		if !isPrivateIP(n.IPAddress) && n.BandwidthMbps > 0 {
			return types.Int64Value(int64(n.BandwidthMbps))
		}
	}
	return prior
}

// isPrivateIP — RFC1918 (used to classify NICs in the data source).
func isPrivateIP(ip string) bool {
	if ip == "" {
		return false
	}
	var a, b int
	if _, err := fmt.Sscanf(ip, "%d.%d.", &a, &b); err != nil {
		return false
	}
	switch {
	case a == 10:
		return true
	case a == 172 && b >= 16 && b <= 31:
		return true
	case a == 192 && b == 168:
		return true
	}
	return false
}
