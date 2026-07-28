# Attaches an isolated network to a gateway so it gains external connectivity.
resource "vcp_gateway_network_attachment" "example" {
  gateway_id = vcp_gateway.example.id
  network_id = vcp_network.example.id
}
