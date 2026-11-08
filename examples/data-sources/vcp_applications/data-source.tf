# List preinstalled applications. Optionally filter by location.
data "vcp_applications" "all" {}

data "vcp_applications" "by_location" {
  location_id = "kz"
}

# Parameters an application asks for, and whether a language model key can be
# issued for it, by application ID.
output "application_parameters" {
  value = {
    for app in data.vcp_applications.by_location.applications : app.id => {
      llm_key_enabled = app.llm_key_enabled
      required        = [for p in app.parameters : p.name if p.required && p.default == null]
    }
  }
}
