# Look up a single DNS zone by name (the trailing dot is added automatically).
data "vcp_dns_domain" "example" {
  name = "example.com."
}
