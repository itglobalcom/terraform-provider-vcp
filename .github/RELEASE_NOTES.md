<!--
Draft description for the next GitHub release. GoReleaser publishes the release
with GitHub's auto-generated notes (commit subjects since the previous tag), so
paste everything below the comment into the release description afterwards:

    gh release edit v0.2.0 --notes-file <(sed '1,/-->/d' .github/RELEASE_NOTES.md)

Replace this file's contents when preparing the next release.
-->

Gateway NAT and firewall rules are now managed by Terraform.

Requires [`vstack-cloud-panel-sdk` v1.1.0](https://github.com/itglobalcom/vstack-cloud-panel-sdk/releases/tag/v1.1.0).

## Features

* **New Resource:** `vcp_gateway_nat` — the NAT rule set of an edge gateway (`SNAT`, `DNAT`, `BINAT`)
* **New Resource:** `vcp_gateway_firewall` — the firewall rule set of an edge gateway
* `vcp_gateway`: new `public_ip` attribute — the gateway's external address, which every NAT rule has to name (`destination` for `DNAT`, `translated` for `SNAT`/`BINAT`)
* `vcp_gateway`, `vcp_gateways`: new `nat_rules` and `firewall_rules` attributes, for inspecting a gateway that Terraform does not manage

Each rule-set resource owns the **whole** rule list of its gateway: the API replaces the
list on every change and rules have no identity beyond their position, so partial
management is not expressible. Declare at most one of each per gateway, and expect rules
created in the panel to be replaced on the first apply. `rules = []` removes every rule,
destroying the resource does the same, and `terraform state rm` lets go of the rules
without touching them.

> **Firewall rule order is the priority, and the last matching rule wins** — the opposite
> of iptables and of security groups. Put the catch-all `Deny` first and the specific
> `Allow` rules below it.

## Validation

Everything the API would otherwise reject after accepting the request is now checked
during `terraform plan`, with a message naming the fix: a non-canonical CIDR
(`10.0.0.5/24`), a bare address where CIDR is required, a mask where a bare address is
required, a port on `ICMP`/`IP`/`BINAT`, a port out of range, and a lower-case enum value.
Duplicate rules are accepted, because the API accepts them.

## Bug Fixes

* `vcp_gateway`: destroying a gateway that was already deleted elsewhere no longer fails — the API answers HTTP 500 rather than 404 in that case
* Applying a network attachment and the rule sets of one gateway together no longer collides: a gateway stays busy for a while after an operation's task completes, and gateway operations now wait for it to accept changes again
* A rule-set change whose backend task fails transiently is retried once; if it still fails, the error says what happened and what to do about it

Nothing to change in existing configurations. Documentation:
https://registry.terraform.io/providers/itglobalcom/vcp/latest/docs
