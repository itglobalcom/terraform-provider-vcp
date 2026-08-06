# Contributing

**This GitHub repository is a mirror.** The provider and its
[SDK](https://github.com/itglobalcom/vstack-cloud-panel-sdk) are developed in an internal
GitLab and pushed here from it, so a pull request opened on GitHub cannot be merged directly —
the next mirror push would overwrite it.

Contributions are still very welcome; they just travel a slightly different route:

- **Found a bug or want a feature? [Open an issue](https://github.com/itglobalcom/terraform-provider-vcp/issues).**
  This is the most useful thing you can do, and it is read by the same people who write the
  code. Please include the provider and Terraform versions, the location you used, the smallest
  configuration that reproduces the problem, and the relevant part of `TF_LOG=DEBUG` output —
  with API tokens and other secrets removed.
- **Already have a fix?** Open a pull request anyway, or attach a `git diff` to the issue. A
  maintainer applies the change in GitLab, crediting you in the commit, and it lands here with
  the next sync — the pull request is then closed as *merged upstream* rather than merged on
  GitHub. Small, focused patches make this fast; large ones are slow to port, so it is worth
  opening an issue first to agree on the approach.

Everything below is for building and testing the provider locally — useful whether you are
preparing a patch or just running it from source.

## Requirements

- **Go** — [go.mod](go.mod) sets the minimum (`go`) and pins the toolchain CI builds with
  (`toolchain`, mirrored in [.go-version](.go-version) for `asdf`/`goenv`).
- **Terraform** — for examples and acceptance tests.

`golangci-lint` and `tfplugindocs` need no manual install: their versions are pinned in the
[Makefile](Makefile) and fetched into `bin/` on first use, so local results match CI.
`make tools` installs both up front; `make help` lists every target.

The provider depends on the published
[`vstack-cloud-panel-sdk`](https://github.com/itglobalcom/vstack-cloud-panel-sdk), so a plain
`go build` works out of the box — a local SDK checkout is only needed to change the SDK itself.

Copy the environment variables and fill them in:

```sh
cp .env.example .env
```

## Build and installation

```sh
make build               # build the terraform-provider-vcp binary
make install-filesystem  # build + place under ~/.terraform.d/plugins (filesystem mirror)
make setup               # same, plus generate ~/.terraformrc (overwrites it)
```

`install-filesystem` first removes every previously installed version from the plugin mirror:
the runnable examples pin no version, and a leftover higher one would silently win. The mirror stores the
provider like a released one, so after each reinstall an example's `.terraform.lock.hcl` still
holds the *previous* binary's checksum — run `terraform init -upgrade` in that directory (or
`make clean`, which deletes the lock files instead).

## Tests

```sh
make test                                    # unit tests — no credentials, no cloud calls
make testacc                                 # all acceptance tests
make testacc-service SERVICE=server          # tests for a single service
make testacc-test SERVICE=server TEST=TestAccServer_basic
make sweep                                   # delete leftover test-acc-*/tf-acc-* resources
```

Unit tests are enough for most patches, and they are what CI can verify on a pull request.

Acceptance tests create real, billable cloud resources and require `VCP_API_URL`,
`VCP_API_TOKEN`, `VCP_LOCATION_ID`; the server tests additionally require `VCP_IMAGE_ID` (see
[.env.example](.env.example) and [internal/acctest/acctest.go](internal/acctest/acctest.go)).
A few tests that check "changing the location forces a replace" also need `VCP_LOCATION_ID_ALT`
— a second, different location — and skip without it.

Everything named `TestAcc*` is skipped unless `TF_ACC=1` is set, which only the `testacc*`
targets do; `make test` is therefore safe to run against an empty environment. If an acceptance
run dies half-way, `make sweep` tears down whatever it left in the cloud.

`terraform-plugin-testing` runs the provider in reattach mode and needs a Terraform CLI: the
Makefile passes the local `terraform` path in `TF_ACC_TERRAFORM_PATH`. Without one on `PATH` the
framework tries to download Terraform itself, which requires network access and may fail.
Override it to use a specific CLI:

```sh
make testacc TF_ACC_TERRAFORM_PATH=/path/to/terraform
```

## Formatting and checks

```sh
make fmt    # go fmt + terraform fmt
make vet    # go vet
make lint   # golangci-lint at the pinned version, config in .golangci.yml
```

Run these before submitting anything. CI runs `gofmt`, the same pinned `golangci-lint` (which
includes `govet`) and the unit tests, plus two checks with no local `make` target: that
`terraform fmt` is clean under `examples/` (those files are rendered into `docs/`) and that
`docs/` matches `make docs-generate` output. If you touch a schema or an example, run
`make docs-generate` and commit the result.

## Working on the SDK too

Most provider changes need nothing here. But if a fix belongs in the API client rather than the
provider, use **Go workspaces (`go.work`)** to develop both at once — not a `replace` in
`go.mod`:

```sh
git clone https://github.com/itglobalcom/vstack-cloud-panel-sdk.git ../vstack-cloud-panel-sdk
make dev-setup    # writes go.work with a replace → ../vstack-cloud-panel-sdk
```

After that `go build` / `go test` see SDK edits with no publishing and no `go.mod` change.
It's a `replace` rather than `use ../vstack-cloud-panel-sdk` because `replace` wins regardless
of the version pinned in `go.mod`.

### Rules (important)

- **Don't commit `go.work`** — it's a developer's personal file and [.gitignore](.gitignore)d;
  only the template [go.work.example](go.work.example) lives in the repo.
- **Don't commit a local-path `replace` in `go.mod`** — the SDK stays pinned to an actual
  published version in `require`.
- Before submitting, build the way CI does:

  ```sh
  make check-release      # = GOWORK=off go build ./...
  ```

  A failure here means the SDK version in `go.mod` isn't published yet, or a `replace` crept
  back in. This is the safety net against a permanent substitution.

A patch that spans both repositories is fine — describe the SDK part in the issue, and note
that the provider side needs the new SDK version. Releasing that version is a maintainer step:

1. In the SDK repository: `git tag vX.Y.Z && git push --tags`.
2. In the provider: `go get github.com/itglobalcom/vstack-cloud-panel-sdk@vX.Y.Z && go mod tidy`.
3. `make check-release` — make sure the provider builds against the published version.
