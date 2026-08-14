# An additional public address for a server. The server's first public interface
# is part of vcp_vmware_server (public_network_id / network_bandwidth_mbps) and
# must not be managed here — every interface added by this resource is charged
# separately.
resource "vcp_vmware_server_public_interface" "extra" {
  server_id      = vcp_vmware_server.web.id
  bandwidth_mbps = 100
}

# The platform picks the network and the address; both are readable afterwards.
output "extra_public_ip" {
  value = vcp_vmware_server_public_interface.extra.ip
}
