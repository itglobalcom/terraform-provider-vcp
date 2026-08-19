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
