# Fetch a single VMware server by ID.
data "vcp_vmware_server" "web" {
  id = 5678
}

# Whether the guest of that machine may run its own hypervisor.
output "web_nested_hypervisor" {
  value = data.vcp_vmware_server.web.nested_hypervisor
}
