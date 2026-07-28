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
# Public web servers behind a DNS zone:
#
#   example.com       A     → web1 + web2 public IPs (round-robin, short TTL)
#   www.example.com   CNAME → example.com
#   web1.example.com  A     → web1 public IP (per-host records for SSH/debug)
#   web2.example.com  A     → web2 public IP
#   example.com       MX    → external mail provider
#   example.com       TXT   → SPF policy + domain-verification token
#
# Each vcp_dns_record_set manages one RRset — every record sharing a name and
# type — so the apex A set holds both server IPs and one shared TTL.
#
# Set var.zone to a domain you control, apply, then delegate the domain to the
# name servers from the `zone_name_servers` output at your registrar.
# ============================================================================

locals {
  web_servers = toset(["web1", "web2"])
}

# ----------------------------------------------------------------------------
# Infrastructure: two web servers, each with a public (external) interface.
# ----------------------------------------------------------------------------

resource "vcp_server" "web" {
  for_each    = local.web_servers
  name        = each.key
  location_id = var.location_id
  image_id    = var.image_id
  cpu         = 1
  ram_mb      = 2048
  volumes = [
    { number = 0, name = "boot", size_mb = 30720 }
  ]
}

# The public interface is what the DNS records point at.
resource "vcp_server_public_interface" "web" {
  for_each       = local.web_servers
  server_id      = vcp_server.web[each.key].id
  bandwidth_mbps = 50
}

# ----------------------------------------------------------------------------
# DNS zone and records.
# ----------------------------------------------------------------------------

# Creating the zone auto-provisions system NS records; they are not managed by
# Terraform and are surfaced through the data source at the bottom.
resource "vcp_dns_domain" "zone" {
  name = var.zone
}

# Zone apex: a single A record set carrying both web servers' public IPs
# (DNS round-robin). The TTL is shared by the whole set; keep it short so
# traffic moves quickly when a server is replaced.
resource "vcp_dns_record_set" "apex" {
  domain = vcp_dns_domain.zone.name
  name   = var.zone
  type   = "A"
  ttl    = "5m"
  values = [
    for k in local.web_servers : { ip = vcp_server_public_interface.web[k].ip_address }
  ]
}

# Per-host records, one record set per server: handy for SSH and debugging a
# specific instance behind the round-robin apex.
resource "vcp_dns_record_set" "host" {
  for_each = local.web_servers
  domain   = vcp_dns_domain.zone.name
  name     = "${each.key}.${var.zone}"
  type     = "A"
  ttl      = "1h"
  values = [
    { ip = vcp_server_public_interface.web[each.key].ip_address }
  ]
}

# www is an alias for the apex. A CNAME set must contain exactly one value.
resource "vcp_dns_record_set" "www" {
  domain = vcp_dns_domain.zone.name
  name   = "www.${var.zone}"
  type   = "CNAME"
  ttl    = "1h"
  values = [
    { canonical_name = var.zone }
  ]
}

# Mail is hosted externally: two MX records with different priorities in one
# set. (.example is a reserved TLD — replace with your provider's hosts.)
resource "vcp_dns_record_set" "mx" {
  domain = vcp_dns_domain.zone.name
  name   = var.zone
  type   = "MX"
  ttl    = "1d"
  values = [
    { priority = 10, mail_host = "mx1.mail-provider.example" },
    { priority = 20, mail_host = "mx2.mail-provider.example" },
  ]
}

# Apex TXT set: SPF policy plus a domain-verification token as two values of
# the same record set.
resource "vcp_dns_record_set" "txt" {
  domain = vcp_dns_domain.zone.name
  name   = var.zone
  type   = "TXT"
  ttl    = "1d"
  values = [
    { text = "v=spf1 mx -all" },
    { text = "site-verification=0123456789abcdef" },
  ]
}

# Read the zone back to expose the system NS records provisioned with it —
# these are the name servers to delegate var.zone to at your registrar.
data "vcp_dns_domain" "zone" {
  name = vcp_dns_domain.zone.name
}

# ============================================================================
# Outputs
# ============================================================================
output "zone" {
  description = "Canonical zone name"
  value       = vcp_dns_domain.zone.id
}

output "zone_name_servers" {
  description = "Delegate the zone to these name servers at your registrar"
  value = distinct(flatten([
    for rs in data.vcp_dns_domain.zone.record_sets :
    [for v in rs.values : v.name_server_host] if rs.type == "NS"
  ]))
}

output "web_public_ips" {
  description = "Public IPs the DNS records point at"
  value       = { for k, i in vcp_server_public_interface.web : k => i.ip_address }
}

output "site_urls" {
  description = "Names that resolve to the web servers once the zone is delegated"
  value = concat(
    ["http://${trimsuffix(vcp_dns_domain.zone.id, ".")}"],
    ["http://www.${trimsuffix(vcp_dns_domain.zone.id, ".")}"],
    [for k in local.web_servers : "http://${k}.${trimsuffix(vcp_dns_domain.zone.id, ".")}"],
  )
}
