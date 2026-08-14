# The resource owns the whole firewall rule set of the gateway: the API replaces
# the list on every change, so declare at most one vcp_gateway_firewall per
# gateway.
#
# Order matters, and the LAST matching rule wins — the opposite of the usual
# first-match-wins. The catch-all deny therefore comes first, and the specific
# allow rules below it.
resource "vcp_gateway_firewall" "example" {
  # Referencing the attachment instead of vcp_gateway.example.id makes the rules
  # wait until the gateway is connected to the network (no depends_on needed).
  gateway_id = vcp_gateway_network_attachment.example.gateway_id

  rules = [
    # Default: drop everything in both directions.
    {
      action      = "Deny"
      direction   = "In"
      protocol    = "IP"
      source      = "0.0.0.0/0"
      destination = "0.0.0.0/0"
    },
    {
      action      = "Deny"
      direction   = "Out"
      protocol    = "IP"
      source      = "0.0.0.0/0"
      destination = "0.0.0.0/0"
    },

    # Published service. After a DNAT rule the destination is already the
    # private address and port of the server.
    {
      action           = "Allow"
      direction        = "In"
      protocol         = "TCP"
      source           = "0.0.0.0/0"
      destination      = "10.0.0.5/32"
      destination_port = 8443
    },

    # ICMP from the admin network only. With ICMP both ports must be 0, so they
    # are omitted.
    {
      action      = "Allow"
      direction   = "In"
      protocol    = "ICMP"
      source      = "192.0.2.0/24"
      destination = "10.0.0.0/24"
    },

    # Outbound traffic from the private network.
    {
      action      = "Allow"
      direction   = "Out"
      protocol    = "IP"
      source      = "10.0.0.0/24"
      destination = "0.0.0.0/0"
    },
  ]
}
