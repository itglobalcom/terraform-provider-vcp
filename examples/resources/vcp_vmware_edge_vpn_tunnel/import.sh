# Tunnels are imported as "<network_id>:<tunnel_id>".
#
# The pre-shared key is write-only — the API never returns it — so an imported
# tunnel takes the key from the configuration, and the first apply writes it to
# the tunnel. Make sure it is the key the far side expects.
terraform import vcp_vmware_edge_vpn_tunnel.office 2479:501
