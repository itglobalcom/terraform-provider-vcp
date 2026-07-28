# List preinstalled applications. Optionally filter by location.
data "vcp_applications" "all" {}

data "vcp_applications" "by_location" {
  location_id = "kz"
}
