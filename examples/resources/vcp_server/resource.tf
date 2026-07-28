resource "vcp_server" "example" {
  name        = "app-server"
  location_id = "kz"
  image_id    = "Ubuntu-22.04-X64"
  cpu         = 2
  ram_mb      = 2048

  ssh_key_ids = [vcp_ssh_key.example.id]

  # At least one volume named "boot" is required. Sizes are in MB and must be a
  # multiple of 10240 (10 GB).
  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    },
  ]

  tags = ["managed-by-terraform"]
}
