# The resource owns the whole NAT rule set of the gateway: the API replaces the
# list on every change, so declare at most one vcp_gateway_nat per gateway.
#
# Every rule has to name the gateway's external address — the API accepts no
# other value there: `destination` for DNAT, `translated` for SNAT and BINAT.
resource "vcp_gateway_nat" "example" {
  # Referencing the attachment instead of vcp_gateway.example.id makes the rules
  # wait until the gateway is connected to the network (no depends_on needed).
  gateway_id = vcp_gateway_network_attachment.example.gateway_id

  rules = [
    # Publish HTTPS: <gateway public IP>:443 → 10.0.0.5:8443
    {
      type             = "DNAT"
      protocol         = "TCP"
      source           = "0.0.0.0/0"
      destination      = "${vcp_gateway.example.public_ip}/32"
      destination_port = 443
      translated       = "10.0.0.5"
      translated_port  = 8443
    },

    # Outbound internet for the whole private network. protocol = "IP" means any
    # protocol, and then both ports must stay 0 — so they are simply omitted.
    {
      type        = "SNAT"
      protocol    = "IP"
      source      = "10.0.0.0/24"
      destination = "0.0.0.0/0"
      translated  = vcp_gateway.example.public_ip
    },
  ]
}
