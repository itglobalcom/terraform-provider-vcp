terraform {
  required_providers {
    vcp = {
      source  = "itglobalcom/vcp"
      version = ">= 0.1.1"
    }
  }
}

# The host and key can also be supplied via the VCP_API_URL and VCP_API_TOKEN
# environment variables, in which case they can be omitted here.
provider "vcp" {
  host = "https://api.example.com"
  key  = "your-api-token"
}
