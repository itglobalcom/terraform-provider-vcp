resource "vcp_vmware_server" "web" {
  location_id       = 5
  name              = "web-01"
  computer_name     = "web01"
  image_id          = 42
  cpu               = 2
  ram_mb            = 4096
  system_disk_mb    = 51200
  system_disk_type  = "ssd"
  public_network_id = 100
  ssh_key_ids       = [7]
}

# GPU server example.
resource "vcp_vmware_server" "gpu" {
  location_id    = 5
  name           = "ml-01"
  computer_name  = "ml01"
  image_id       = 55
  cpu            = 8
  ram_mb         = 32768
  system_disk_mb = 102400

  # The backend selects the slicing policy by the exact triple, so all three
  # values are required — pick them from a vcp_vmware_gpu_models entry.
  gpu = {
    model_id   = 3
    vram_mb    = 8192
    card_count = 1
  }
}

# A copy of an existing machine, disks and all. The copy takes its whole
# specification from the source, so copy_from_server_id and name are the only
# arguments it accepts — anything else is refused at plan time.
#
# Once it exists it is an ordinary server: adding cpu, ram_mb or volumes here
# changes them in place on the next apply.
resource "vcp_vmware_server" "web_clone" {
  copy_from_server_id = vcp_vmware_server.web.id
  name                = "web-01-clone"
}

# Nested hypervisor: the guest is given hardware-assisted CPU virtualization
# and can run a hypervisor of its own. The location has to offer a VDC that
# supports it (nested_hypervisor_supported in vcp_vmware_locations), and gpu
# cannot be used on the same machine. Switching the attribute on an existing
# server restarts it.
resource "vcp_vmware_server" "lab" {
  location_id    = 5
  name           = "lab-01"
  computer_name  = "lab01"
  image_id       = 42
  cpu            = 4
  ram_mb         = 16384
  system_disk_mb = 102400

  nested_hypervisor = true
}
