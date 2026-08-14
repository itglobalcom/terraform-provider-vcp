# The firewall of the machine itself, independent of the edge firewall in front
# of the network. The resource owns the whole rule set, so declare at most one
# vcp_vmware_server_firewall per server.
#
# Traffic no rule matches is passed — this firewall has no default action, so a
# whitelist needs a closing deny rule of its own.
resource "vcp_vmware_server_firewall" "web" {
  server_id = vcp_vmware_server.web.id

  rules = [
    {
      name              = "ssh-from-the-office"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      source            = "203.0.113.0/24"
      destination_port  = "22"
    },
    {
      name              = "https"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      destination_port  = "443"
    },
    {
      name              = "drop-the-rest"
      traffic_direction = "incoming"
      action            = "deny"
      protocol          = "any"
    },
  ]
}
