# The resource owns the whole firewall of the network's edge, so declare at most
# one vcp_vmware_edge_firewall per network. The firewall is on for as long as the
# resource exists — there is no enabled flag to set.
#
# default_action = "deny" makes the list a whitelist: everything not named below
# is dropped.
resource "vcp_vmware_edge_firewall" "app" {
  network_id     = vcp_vmware_network.app.id
  default_action = "deny"

  rules = [
    {
      name             = "allow-https"
      action           = "allow"
      protocol         = "tcp"
      source           = "any"
      destination      = "10.200.0.10"
      destination_port = "443"
    },

    # Attributes left out default to "any", and the value the platform stored is
    # what lands in state.
    {
      name     = "allow-icmp"
      action   = "allow"
      protocol = "icmp"
    },

    {
      name             = "ssh-from-the-office"
      action           = "allow"
      protocol         = "tcp"
      source           = "203.0.113.0/24"
      destination      = "10.200.0.0/24"
      destination_port = "22"
    },
  ]
}
