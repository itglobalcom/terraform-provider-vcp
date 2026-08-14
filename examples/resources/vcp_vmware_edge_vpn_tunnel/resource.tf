# A site-to-site IPsec tunnel joining this network to one subnet on the far side.
# Declare another tunnel for another subnet — one tunnel carries one.
resource "vcp_vmware_edge_vpn_tunnel" "office" {
  network_id = vcp_vmware_network.app.id
  name       = "office"

  peer_endpoint      = "203.0.113.10"    # IPv4, not a hostname
  peer_identificator = "203.0.113.10"    # IKE identity of the far side
  peer_network       = "192.168.10.0/24" # what is reachable through the tunnel

  # 32-128 alphanumeric characters with an upper-case letter, a lower-case one
  # and a digit. Tunnels to the same peer must all carry the same key.
  shared_key = var.vpn_shared_key

  encryption_type         = "aes256"
  diffie_hellman_group    = "dh14"
  mtu                     = 1500
  perfect_forward_secrecy = true
}

# The local side is the platform's to choose, and readable once the tunnel is up.
output "vpn_local_ip" {
  value = vcp_vmware_edge_vpn_tunnel.office.local_ip
}
