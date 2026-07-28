# Adds an external (public) interface to a server with the given uplink bandwidth.
resource "vcp_server_public_interface" "example" {
  server_id      = vcp_server.example.id
  bandwidth_mbps = 80
}
