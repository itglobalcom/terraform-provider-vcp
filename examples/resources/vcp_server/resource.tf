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

# A server ordered with an application from the catalog. Parameter names come
# from the catalog entry; issue_llm_key is accepted only where the entry has
# llm_key_enabled.
data "vcp_applications" "kz" {
  location_id = "kz"
}

resource "vcp_server" "with_app" {
  name        = "assistant"
  location_id = "kz"
  image_id    = data.vcp_applications.kz.applications[0].images[0]
  cpu         = 4
  ram_mb      = 8192

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 51200
    },
  ]

  applications = [
    {
      id            = data.vcp_applications.kz.applications[0].id
      issue_llm_key = data.vcp_applications.kz.applications[0].llm_key_enabled
      parameters = {
        ADMIN_EMAIL = "admin@example.com"
      }
    },
  ]
}

output "application_installations" {
  value = vcp_server.with_app.application_installations
}
