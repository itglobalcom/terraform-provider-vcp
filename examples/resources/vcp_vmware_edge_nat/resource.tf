# The resource owns the whole NAT rule set of the network's edge, so declare at
# most one vcp_vmware_edge_nat per network. NAT applies to routed networks only.
resource "vcp_vmware_edge_nat" "app" {
  network_id = vcp_vmware_network.app.id

  rules = [
    # Publish HTTPS: <edge external address>:443 → 10.200.0.10:443.
    #
    # original_ip is deliberately absent: the platform always translates from the
    # edge's own external address and replaces any value sent, so the address it
    # picked appears in state after the apply.
    {
      type            = "dnat"
      protocol        = "tcp"
      description     = "publish https"
      original_port   = "443"
      translated_ip   = "10.200.0.10"
      translated_port = "443"
    },

    # Outbound internet for the whole private network. translated_ip is left to
    # the platform, which uses the edge's external address.
    {
      type        = "snat"
      protocol    = "any"
      description = "outbound"
      original_ip = "10.200.0.0/24"
    },

    # A rule kept in the configuration without passing traffic.
    {
      type            = "dnat"
      protocol        = "tcp"
      description     = "ssh, off for now"
      original_port   = "2222"
      translated_ip   = "10.200.0.10"
      translated_port = "22"
      enabled         = false
    },
  ]
}
