terraform {
  required_providers {
    vcp = {
      source  = "itglobalcom/vcp"
      version = ">= 0.2.0"
    }
  }
}

provider "vcp" {
  key  = var.api_key
  host = var.endpoint
}

# ============================================================================
# A published web application on VMware Cloud:
#
#   internet ──[ edge ]── app-net ── web  (published on :443)
#                            │
#                            └────── db   (no published port at all)
#
#   * app-net is a ROUTED network, which is what gives it an edge of its own.
#     The edge is where NAT and the firewall live.
#   * Both machines are on app-net and talk to each other over it.
#   * Every VMware server also comes with a public interface of its own — that
#     is the platform's doing, not this configuration's, and the server firewall
#     is what keeps it from being a way in.
#
# Read the sizing from the catalog rather than hard-coding it: disk types and
# their limits differ between locations.
# ============================================================================

data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = var.location_id
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == var.location_id])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == var.image_id])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(2048, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)

  app_cidr = "10.90.0.0"
  web_ip   = "10.90.0.10"
  db_ip    = "10.90.0.20"
}

# ---------------------------------------------------------------------------
# The network and its edge
# ---------------------------------------------------------------------------

resource "vcp_vmware_network" "app" {
  type        = "routed"
  location_id = var.location_id
  name        = "app-net"

  address     = local.app_cidr
  mask        = 24
  enable_dhcp = false

  # This is the edge's bandwidth too — the two are one field.
  bandwidth_mbps = 100
}

# ---------------------------------------------------------------------------
# The machines
# ---------------------------------------------------------------------------

resource "vcp_vmware_server" "web" {
  location_id   = var.location_id
  name          = "web-01"
  computer_name = "web01"
  image_id      = var.image_id

  cpu              = 2
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server" "db" {
  location_id   = var.location_id
  name          = "db-01"
  computer_name = "db01"
  image_id      = var.image_id

  cpu              = 2
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title

  # A disk for the data, kept apart from the one the system lives on so it can
  # be grown without touching the boot disk.
  volumes = [
    {
      number    = 0
      name      = "db-data"
      size_mb   = local.disk_type.min_mb
      disk_type = local.disk_type.title
    },
  ]
}

resource "vcp_vmware_server_network_attachment" "web" {
  server_id  = vcp_vmware_server.web.id
  network_id = vcp_vmware_network.app.id
  ip         = local.web_ip
}

resource "vcp_vmware_server_network_attachment" "db" {
  server_id  = vcp_vmware_server.db.id
  network_id = vcp_vmware_network.app.id
  ip         = local.db_ip
}

# ---------------------------------------------------------------------------
# Publishing the application
# ---------------------------------------------------------------------------

# NAT: HTTPS from the outside reaches web, and both machines get a way out.
#
# original_ip is deliberately absent from the DNAT rule — the platform always
# translates from the edge's own external address and reports which one it used.
resource "vcp_vmware_edge_nat" "app" {
  network_id = vcp_vmware_network.app.id

  rules = [
    {
      type            = "dnat"
      protocol        = "tcp"
      description     = "publish https"
      original_port   = "443"
      translated_ip   = local.web_ip
      translated_port = "443"
    },
    {
      type        = "snat"
      protocol    = "any"
      description = "outbound"
      original_ip = "${local.app_cidr}/24"
    },
  ]
}

# The edge firewall is a whitelist: everything not named here is dropped.
resource "vcp_vmware_edge_firewall" "app" {
  network_id     = vcp_vmware_network.app.id
  default_action = "deny"

  rules = [
    {
      name             = "https-in"
      action           = "allow"
      protocol         = "tcp"
      source           = "any"
      destination      = local.web_ip
      destination_port = "443"
    },
    {
      name        = "outbound"
      action      = "allow"
      protocol    = "any"
      source      = "${local.app_cidr}/24"
      destination = "any"
    },
  ]
}

# ---------------------------------------------------------------------------
# The machines' own firewalls
#
# The edge only fronts app-net. Each server also has a public interface of its
# own, so the rules that matter for those live on the machines.
# ---------------------------------------------------------------------------

resource "vcp_vmware_server_firewall" "web" {
  server_id = vcp_vmware_server.web.id

  rules = [
    {
      name              = "https"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      destination_port  = "443"
    },
    {
      name              = "ssh-office"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      source            = var.office_cidr
      destination_port  = "22"
    },
    {
      name              = "drop-the-rest"
      traffic_direction = "incoming"
      action            = "deny"
      protocol          = "any"
    },
  ]
}

# The database answers the application and nobody else.
resource "vcp_vmware_server_firewall" "db" {
  server_id = vcp_vmware_server.db.id

  rules = [
    {
      name              = "postgres-from-web"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      source            = local.web_ip
      destination_port  = "5432"
    },
    {
      name              = "ssh-office"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      source            = var.office_cidr
      destination_port  = "22"
    },
    {
      name              = "drop-the-rest"
      traffic_direction = "incoming"
      action            = "deny"
      protocol          = "any"
    },
  ]
}

# ---------------------------------------------------------------------------

output "published_address" {
  description = "The edge address HTTPS arrives on — chosen by the platform."
  value       = one([for r in vcp_vmware_edge_nat.app.rules : r.original_ip if r.type == "dnat"])
}

output "web_private_ip" {
  value = vcp_vmware_server_network_attachment.web.ip
}
