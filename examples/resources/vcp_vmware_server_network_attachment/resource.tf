# Private connectivity between two servers. Each server already has a public
# interface of its own — that one belongs to vcp_vmware_server.
resource "vcp_vmware_network" "private" {
  type        = "isolated"
  location_id = 5
  name        = "app-private"
  address     = "10.100.0.0"
  mask        = 24
  enable_dhcp = false
}

resource "vcp_vmware_server_network_attachment" "app" {
  server_id  = vcp_vmware_server.app.id
  network_id = vcp_vmware_network.private.id

  # A static address needs the network to have DHCP switched off. Leave it out on
  # a DHCP network and the platform assigns one.
  ip = "10.100.0.10"
}

resource "vcp_vmware_server_network_attachment" "db" {
  server_id  = vcp_vmware_server.db.id
  network_id = vcp_vmware_network.private.id
  ip         = "10.100.0.11"
}
