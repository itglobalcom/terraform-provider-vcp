# List all VMware Cloud locations.
data "vcp_vmware_locations" "all" {}

# Where a server with nested_hypervisor can be ordered. The capability is derived
# from the VDCs this project may be provisioned in, so it is read per project
# rather than known in advance.
output "nested_hypervisor_locations" {
  value = [for l in data.vcp_vmware_locations.all.locations : l.tech_title if l.nested_hypervisor_supported]
}
