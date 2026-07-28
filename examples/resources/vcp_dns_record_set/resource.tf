# A record set (RRset) shares a single TTL across all its values. Names are
# FQDNs; the trailing dot is added automatically.
resource "vcp_dns_record_set" "www" {
  domain = vcp_dns_domain.example.name
  name   = "www.example.com"
  type   = "A"
  ttl    = "1h"
  values = [
    { ip = "10.0.0.1" },
    { ip = "10.0.0.2" },
  ]
}
