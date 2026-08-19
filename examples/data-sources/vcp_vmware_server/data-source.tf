# Fetch a single VMware server by ID.
data "vcp_vmware_server" "web" {
  id = 5678
}
