terraform {
  required_providers {
    vcp = {
      source  = "itglobalcom/vcp"
      version = ">= 0.1.0"
    }
  }
}

provider "vcp" {
  key  = var.api_key
  host = var.endpoint
}

# ============================================================================
# Two-tier topology:
#
#   internet ── gateway ── app-net ── app1 / app2 / app3
#                                        │
#                                     db-net ── db
#
#   * app-net  is attached to the gateway → the app servers are reachable
#     from the outside.
#   * db-net   is NOT attached to the gateway → the database has no path to or
#     from the internet. The app servers reach it over the internal db-net only.
#
# That is the whole point: you never expose the DB to the outside. The gateway
# only fronts the app tier; the database stays on a private, gateway-less network.
# ============================================================================

locals {
  app_servers = toset(["app1", "app2", "app3"])
}

# Front network: application tier, exposed to the outside via the gateway.
resource "vcp_network" "app" {
  name           = "app-net"
  location_id    = var.location_id
  network_prefix = "10.10.1.0"
  mask           = 24
}

# Back network: database only. Never attached to the gateway.
resource "vcp_network" "db" {
  name           = "db-net"
  location_id    = var.location_id
  network_prefix = "10.10.2.0"
  mask           = 24
}

# Edge gateway: external (WAN) interface only. It takes no network arguments —
# isolated networks are wired up below via attachments.
resource "vcp_gateway" "gw" {
  name           = "edge-gw"
  location_id    = var.location_id
  bandwidth_mbps = 100
}

# Gateway ↔ app-net only. db-net is deliberately never attached, so the database
# has no path to or from the outside.
resource "vcp_gateway_network_attachment" "gw_app" {
  gateway_id = vcp_gateway.gw.id
  network_id = vcp_network.app.id
}

# Application servers.
resource "vcp_server" "app" {
  for_each    = local.app_servers
  name        = each.key
  location_id = var.location_id
  image_id    = var.image_id
  cpu         = 1
  ram_mb      = 2048
  volumes = [
    { number = 0, name = "boot", size_mb = 30720 }
  ]
}

# Database server.
resource "vcp_server" "db" {
  name        = "db"
  location_id = var.location_id
  image_id    = var.image_id
  cpu         = 2
  ram_mb      = 4096
  volumes = [
    { number = 0, name = "boot", size_mb = 30720 }
  ]
}

# Each app server sits on BOTH networks:
#   - app-net: reachable from the outside through the gateway;
#   - db-net:  private link used to talk to the database.
resource "vcp_server_network_attachment" "app_front" {
  for_each   = local.app_servers
  server_id  = vcp_server.app[each.key].id
  network_id = vcp_network.app.id
}

resource "vcp_server_network_attachment" "app_back" {
  for_each   = local.app_servers
  server_id  = vcp_server.app[each.key].id
  network_id = vcp_network.db.id
}

# The database sits only on the internal db-net — no external path.
resource "vcp_server_network_attachment" "db_back" {
  server_id  = vcp_server.db.id
  network_id = vcp_network.db.id
}

# ============================================================================
# Outputs
# ============================================================================
output "gateway_id" {
  value = vcp_gateway.gw.id
}

output "app_server_ids" {
  description = "Application server IDs"
  value       = { for k, s in vcp_server.app : k => s.id }
}

output "app_front_ips" {
  description = "App servers' IPs on app-net (the externally-fronted network)"
  value       = { for k, a in vcp_server_network_attachment.app_front : k => a.ip_address }
}

output "db_internal_ip" {
  description = "Database IP on the private db-net (not reachable from outside)"
  value       = vcp_server_network_attachment.db_back.ip_address
}
