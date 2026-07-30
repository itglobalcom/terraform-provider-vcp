# Routed network with an edge gateway and bandwidth.
resource "vcp_vmware_network" "routed" {
  type           = "routed"
  location_id    = 5
  name           = "app-routed"
  address        = "10.20.0.0"
  mask           = 24
  enable_dhcp    = true
  bandwidth_mbps = 100
}

# Isolated network (no gateway).
resource "vcp_vmware_network" "isolated" {
  type        = "isolated"
  location_id = 5
  name        = "db-isolated"
  address     = "10.30.0.0"
  mask        = 24
}

# Public IP pool network.
resource "vcp_vmware_network" "public" {
  type           = "public"
  location_id    = 5
  name           = "public-pool"
  capacity       = "1"
  bandwidth_mbps = 100
}
