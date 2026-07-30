# List VMware images in a location; optionally only GPU-capable ones.
data "vcp_vmware_images" "in_location" {
  location_id = 5
  gpu_only    = false
}
