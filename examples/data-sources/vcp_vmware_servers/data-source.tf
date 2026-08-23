# List VMware servers, optionally filtered by location.
data "vcp_vmware_servers" "in_location" {
  location_id = 5
}

# The machines in the location whose guests may run their own hypervisor.
output "nested_hypervisor_servers" {
  value = [for s in data.vcp_vmware_servers.in_location.servers : s.name if s.nested_hypervisor]
}
