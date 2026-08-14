# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Terraform provider `vcp` for the VStack Cloud Panel, on terraform-plugin-framework (`sdk/v2` is
rejected by `depguard`). This GitHub repo is a mirror; PRs cannot be merged here.

## Before calling a change done

```sh
gofmt -l . && make vet lint test
make docs-generate      # docs/ must then show no diff
make check-release      # builds as CI does, without go.work
make validate-configs   # terraform validate over the examples and acceptance configs
```

`docs/` is generated from the schema descriptions and `examples/`, which is rendered into them and
must be `terraform fmt`-clean; both are CI failures. A schema description therefore *is* the
documentation: write it for somebody with the panel open, and warn (this replaces the object, this
apply loses data) in a `~>` paragraph, which renders as a callout.

## Rules

- **A fix that belongs in [the SDK](https://github.com/itglobalcom/vstack-cloud-panel-sdk) is made
  there, not worked around here.** No hand-rolled HTTP, no re-declared SDK type, no patching a
  decoded value into the shape the SDK should have returned — a workaround hides the defect from
  every other client and leaves two places to fix. Terraform's half is ours: schema mapping, plan
  modifiers, validation, locking, and turning an *unfixed* API defect into a message a user can act
  on.
- **The SDK is not released for every small change.** Both repositories are edited together:
  `make dev-setup` writes a `go.work` with a `replace` onto a local SDK checkout, and from then on
  `go build` / `go test` see the SDK edits with nothing published and no `go.mod` touched. Only what
  is *committed* has to be a published version pinned in `go.mod` — `go.work` is a personal file and
  gitignored, a `go.mod` `replace` is never committed, and `make check-release` (a build without
  `go.work`) is what proves the pin resolves on its own.
- **Acceptance tests are mandatory** — nothing is finished without them, and they cover the life of
  the resource, not one apply: create → empty plan straight after (a perpetual diff is the commonest
  defect and invisible without this step) → edit in place, asserting the object was *not* replaced →
  assert replacement where it must happen → import → change it out of band and see the drift →
  delete it out of band and see the refresh survive → destroy. Write what a person would write, ids
  and sizes from the catalog data sources rather than hard-coded, and check both sides: state
  agreeing with itself would pass a resource that wrote nothing, so ask the API too. Two resources
  touching one parent in one apply is the only thing that tests the locking. Name them `test-acc-*`
  or `make sweep` cannot clean up; they cost real money, so ask first. See `internal/services/vmware`.
- **Comments carry what the code cannot.** Go conventions first (the name, a full sentence, every
  exported identifier); then why something that looks wrong is right, what breaks if done the obvious
  way, the API defect id (`NET-4`, `SRV-3`) so the workaround can be deleted once the backend is
  fixed, and what was rejected. A test comment names the failure it prevents.

## Architecture

A package per resource family in `internal/services/`; `provider.go` is the only registry — what is
not listed there does not exist. Two products share it: vStack (string ids) and VMware Cloud
(`vcp_vmware_*`, integer ids, own catalog and task family — `*VmwareTaskID` and `*TaskID` must not
be mixed). The API serializes changes per object while Terraform applies in parallel, so every
mutating path locks its **parent** (`internal/locks`). A rule set belongs to one resource whole (an
empty list clears it); `Optional + Computed` is for what the platform supplies; a field the API takes
and ignores is not worth having; a nested attribute's schema and its `attr.Type` map must agree.
