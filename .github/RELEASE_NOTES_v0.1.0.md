<!--
Draft description for the v0.1.0 GitHub release. GitHub's auto-generated
notes are useless for the very first tag (no previous tag to diff against),
so paste this into the release description after goreleaser publishes it,
then delete this file.
-->

Initial release of the VStack Cloud Panel provider.

## Features

* **New Resource:** `vcp_server` — virtual servers with volumes, SSH keys, tags, and optional affinity group placement
* **New Resource:** `vcp_network` — isolated networks
* **New Resource:** `vcp_server_network_attachment` — connects a server to an isolated network
* **New Resource:** `vcp_server_public_interface` — a server's external (public) network interface
* **New Resource:** `vcp_gateway` — edge gateways providing external connectivity to isolated networks
* **New Resource:** `vcp_gateway_network_attachment` — connects a gateway to an isolated network
* **New Resource:** `vcp_dns_domain` — DNS zones
* **New Resource:** `vcp_dns_record_set` — DNS record sets (all records sharing one name and type)
* **New Resource:** `vcp_ssh_key` — SSH keys for server authentication
* **New Resource:** `vcp_affinity_group` — affinity / anti-affinity server placement groups
* **New Data Source:** `vcp_server`, `vcp_servers`
* **New Data Source:** `vcp_network`, `vcp_networks`
* **New Data Source:** `vcp_gateway`, `vcp_gateways`
* **New Data Source:** `vcp_dns_domain`, `vcp_dns_domains`
* **New Data Source:** `vcp_ssh_key`, `vcp_ssh_keys`
* **New Data Source:** `vcp_affinity_group`, `vcp_affinity_groups`
* **New Data Source:** `vcp_locations` — available datacenter locations and their limits
* **New Data Source:** `vcp_images` — available OS images
* **New Data Source:** `vcp_applications` — available applications
* **New Data Source:** `vcp_project` — current project information

All resources support `terraform import`. Documentation:
https://registry.terraform.io/providers/itglobalcom/vcp/latest/docs
