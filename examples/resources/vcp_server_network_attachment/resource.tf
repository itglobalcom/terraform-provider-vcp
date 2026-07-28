# Connects a server to an isolated network. ip_address is optional — if omitted,
# an address is assigned automatically.
resource "vcp_server_network_attachment" "example" {
  server_id  = vcp_server.example.id
  network_id = vcp_network.example.id
  ip_address = "10.100.0.11"
}
