# Keep members apart ("anti-affinity") for fault tolerance, or together
# ("affinity") for low latency between them.
resource "vcp_affinity_group" "example" {
  name        = "web-anti-affinity"
  location_id = "kz"
  policy      = "anti-affinity"
}
