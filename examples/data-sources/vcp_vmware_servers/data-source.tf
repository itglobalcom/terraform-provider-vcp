# List VMware servers, optionally filtered by location.
data "vcp_vmware_servers" "in_location" {
  location_id = 5
}
