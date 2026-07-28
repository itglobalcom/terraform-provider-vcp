# Examples

Terraform configurations for the `vcp` provider (VStack Cloud Panel).

## Layout

The per-resource and per-data-source snippets follow the
[Terraform Registry conventions](https://developer.hashicorp.com/terraform/registry/providers/docs)
and feed the generated documentation:

| Path | Contents |
|------|----------|
| `provider/` | Provider configuration block. |
| `resources/vcp_*/resource.tf` | Minimal usage snippet for each resource. |
| `data-sources/vcp_*/data-source.tf` | Minimal usage snippet for each data source. |

Three complete, runnable end-to-end scenarios live alongside them:

| Example | What it shows |
|---------|---------------|
| `gateway_with_servers/` | Two-tier topology: an edge gateway fronting an app network, with a private database network that has no external path. |
| `servers_with_isolated_net/` | A server on an isolated network, using a `tls`-generated throwaway SSH key. |
| `web_servers_with_dns/` | Two public web servers behind a DNS zone: round-robin A records at the apex, per-host records, a `www` CNAME, MX and TXT sets, and the NS records to delegate the zone. |

## Credentials

Every example reads credentials from the environment (recommended):

```sh
export VCP_API_URL="https://api.example.com"
export VCP_API_TOKEN="your-api-token"
```

The runnable scenarios also accept `-var 'api_key=…' -var 'endpoint=…'` if you
prefer not to use environment variables.

## Running a scenario locally

Before the provider is published to the registry, build and install it into the
local filesystem mirror (this also writes `~/.terraformrc`):

```sh
make setup
```

Then run a scenario:

```sh
cd examples/gateway_with_servers
terraform init
terraform apply
```
