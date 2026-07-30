# List VMware networks, optionally filtered by location.
data "vcp_vmware_networks" "in_location" {
  location_id = 5
}
