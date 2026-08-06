# VStack Cloud Panel Terraform Provider

The `vcp` provider lets you manage [VStack Cloud Panel](https://itglobal.com/) resources —
servers, isolated networks, gateways, DNS zones, SSH keys, affinity groups, and more —
with [Terraform](https://www.terraform.io/).

## Requirements

- Terraform >= 1.0
- A VStack Cloud Panel account and an API token

## Using the provider

```hcl
terraform {
  required_providers {
    vcp = {
      source  = "itglobalcom/vcp"
      version = ">= 0.1.1"
    }
  }
}

provider "vcp" {
  host = "https://api.example.com" # your VStack Cloud Panel API endpoint
  key  = var.vcp_api_token         # API token
}

resource "vcp_network" "example" {
  name           = "production-network"
  location_id    = "kz"
  network_prefix = "10.100.0.0"
  mask           = 24
}
```

### Authentication

The provider reads its endpoint and token from the `host` / `key` arguments, or — if those
are omitted — from environment variables:

| Argument | Environment variable |
|----------|----------------------|
| `host`   | `VCP_API_URL`        |
| `key`    | `VCP_API_TOKEN`      |

```sh
export VCP_API_URL="https://api.example.com"
export VCP_API_TOKEN="your-api-token"
```

Keep the token out of version control — supply it via an environment variable, a
`*.tfvars` file that is not committed, or a secrets manager.

## Documentation and examples

- Full resource and data-source documentation is published on the
  [Terraform Registry](https://registry.terraform.io/providers/itglobalcom/vcp/latest/docs).
- Runnable configurations live in [`examples/`](examples/).

## Development

Building the provider from source and running the tests is described in
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

Licensed under the [Apache License 2.0](LICENSE).
