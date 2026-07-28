resource "vcp_network" "example" {
  name           = "production-network"
  location_id    = "kz"
  description    = "Production environment network"
  network_prefix = "10.100.0.0"
  mask           = 24

  tags = ["production", "managed-by-terraform"]
}
