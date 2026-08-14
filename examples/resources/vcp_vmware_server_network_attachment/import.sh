# Attachments are imported as "<server_id>:<nic_id>" — the ID of the network
# interface, which is listed in the server's `nics` attribute.
terraform import vcp_vmware_server_network_attachment.app 5678:9012
