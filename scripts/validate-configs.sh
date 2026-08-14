#!/usr/bin/env bash
#
# Validate every example and every acceptance configuration against the schema
# the provider actually publishes.
#
# `go test` parses the HCL; this runs the real Terraform against the locally
# built binary, which additionally checks that each attribute exists, that its
# type and enum values are right, and that the resources' own ValidateConfig
# rules accept the configuration. No credentials are needed: validate never
# calls the API.
#
# Usage: make validate-configs   (or ./scripts/validate-configs.sh)
set -uo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
binary="$repo/terraform-provider-vcp"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

if ! command -v terraform >/dev/null; then
  echo "terraform is not on PATH — install it or run 'make build' output through your own CLI" >&2
  exit 1
fi
if [ ! -x "$binary" ]; then
  echo "$binary is missing — run 'make build' first" >&2
  exit 1
fi

# dev_overrides points Terraform at the binary that was just built, so no
# registry, no network and no `terraform init` are involved.
cat > "$work/dev.tfrc" <<EOF
provider_installation {
  dev_overrides {
    "itglobalcom/vcp" = "$repo"
  }
  direct {}
}
EOF
export TF_CLI_CONFIG_FILE="$work/dev.tfrc"
export TF_IN_AUTOMATION=1

header='terraform {
  required_providers {
    vcp = { source = "itglobalcom/vcp" }
  }
}
'
ok=0 failed=0 skipped=0

validate() { # label dir
  local label="$1" dir="$2" out
  out="$(cd "$dir" && terraform validate -no-color 2>&1)"
  if grep -q '^Success!' <<<"$out"; then
    ok=$((ok + 1))
    return
  fi
  if grep -q 'Missing required provider' <<<"$out"; then
    skipped=$((skipped + 1))
    echo "SKIP  $label — needs a provider other than vcp, which offline validation cannot install"
    return
  fi
  failed=$((failed + 1))
  echo "FAIL  $label"
  grep -E '^(Error|│)' <<<"$out" | grep -v 'development overrides' | head -12
  echo
}

# Complete examples: they carry their own provider block and variables.
for dir in "$repo"/examples/*/; do
  [ -f "$dir/main.tf" ] || continue
  target="$work/example-$(basename "$dir")"
  mkdir -p "$target"
  cp "$dir"/*.tf "$target"/ 2>/dev/null
  validate "example/$(basename "$dir")" "$target"
done

# The acceptance configurations, taken out of the generated document so that what
# is checked is exactly what the tests apply.
if [ -f "$repo/vmware_acceptance_configs.md" ]; then
  python3 - "$repo/vmware_acceptance_configs.md" "$work" <<'PY'
import pathlib, re, sys

doc = pathlib.Path(sys.argv[1]).read_text()
work = pathlib.Path(sys.argv[2])
for match in re.finditer(r"^## (\S+)\n\n.*?\n\n```hcl\n(.*?)```", doc, re.S | re.M):
    label, config = match.group(1), match.group(2)
    if "/" not in label:
        continue  # the table of contents, not a configuration
    target = work / ("acc-" + label.replace("/", "-"))
    target.mkdir(parents=True, exist_ok=True)
    (target / "main.tf").write_text(config)
PY

  for dir in "$work"/acc-*/; do
    [ -d "$dir" ] || continue
    printf '%s' "$header" > "$dir/versions.tf"
    validate "acceptance/$(basename "${dir%/}" | sed 's/^acc-//')" "$dir"
  done
fi

echo "validated ${ok} configuration(s), ${skipped} skipped, ${failed} failed"
[ "$failed" -eq 0 ]
