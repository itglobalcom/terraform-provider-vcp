# Fetch a single VMware network by ID.
data "vcp_vmware_network" "routed" {
  id = 1234
}
