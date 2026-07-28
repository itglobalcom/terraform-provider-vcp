# The external (WAN) interface is intrinsic to the gateway: bandwidth_mbps is a
# gateway-wide property. Attach isolated networks with
# vcp_gateway_network_attachment.
resource "vcp_gateway" "example" {
  name           = "edge-gateway"
  location_id    = "kz"
  bandwidth_mbps = 100

  tags = ["managed-by-terraform"]
}
