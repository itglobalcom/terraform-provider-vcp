# A DNS zone. The trailing dot is added automatically if omitted.
resource "vcp_dns_domain" "example" {
  name = "example.com."
}
