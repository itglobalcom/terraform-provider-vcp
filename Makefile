-include .env
export

# ── Config ─────────────────────────────────────────────────
HOSTNAME     := registry.terraform.io
NAMESPACE    := itglobalcom
NAME         := vcp
VERSION      ?= 0.1.0
OS_ARCH      ?= $(shell go env GOOS)_$(shell go env GOARCH)
BINARY       := terraform-provider-$(NAME)
PLUGINS_DIR  := $(HOME)/.terraform.d/plugins
EXAMPLES_DIR := examples
BUILD_DIR    := bin
SERVICES_DIR := ./internal/services

# Pinned dev tools, installed into bin/ on demand so local and CI agree.
# CI reads these pins with: make -s print-GOLANGCI_VERSION
BIN_DIR              := $(CURDIR)/$(BUILD_DIR)
GOLANGCI_VERSION     ?= v2.12.2
TFPLUGINDOCS_VERSION ?= v0.25.0

# Local SDK checkout for go.work (see CONTRIBUTING.md)
SDK_DIR    ?= ../vstack-cloud-panel-sdk
SDK_MODULE ?= github.com/itglobalcom/vstack-cloud-panel-sdk

# Sweepers (delete leftover test resources)
SWEEP_TIMEOUT ?= 360m
SWEEP_RUN     ?=

# Acceptance-test CLI.
# Override: make ... TF_ACC_TERRAFORM_PATH=/path/to/cli
TERRAFORM_BIN := $(shell command -v terraform)
TF_ACC_TERRAFORM_PATH ?= $(TERRAFORM_BIN)
export TF_ACC_TERRAFORM_PATH

.DEFAULT_GOAL := install-filesystem
.PHONY: help build release install-filesystem setup-terraformrc-filesystem setup \
        dev-setup check-release deps fmt vet lint tools clean clean-all \
        test testacc testacc-service testacc-test \
        sweep docs-generate docs-validate

help: ## Show the list of targets
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | \
		awk -F':.*## ' '{printf "  \033[36m%-26s\033[0m %s\n", $$1, $$2}'

# ── Build & install ────────────────────────────────────────
build: ## Build the provider
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY)

release: ## Build binaries for all platforms into bin/
	@mkdir -p $(BUILD_DIR)
	@for p in darwin/amd64 darwin/arm64 linux/386 linux/amd64 linux/arm linux/arm64 \
	          freebsd/386 freebsd/amd64 freebsd/arm openbsd/386 openbsd/amd64 \
	          solaris/amd64 windows/386 windows/amd64; do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		echo "→ $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY)_$(VERSION)_$${os}_$${arch}$$ext || exit 1; \
	done

# Drop every previously installed version first: examples pin ">= 0.1.0", so a
# leftover higher version would win over the build we just made.
install-filesystem: build ## Install the provider into filesystem_mirror
	@rm -rf $(PLUGINS_DIR)/$(HOSTNAME)/$(NAMESPACE)/$(NAME)
	@dir=$(PLUGINS_DIR)/$(HOSTNAME)/$(NAMESPACE)/$(NAME)/$(VERSION)/$(OS_ARCH); \
		mkdir -p $$dir && cp $(BINARY) $$dir/ && echo "✓ installed → $$dir/$(BINARY)"
	@echo "  note: run 'terraform init -upgrade' in an example — its .terraform.lock.hcl still holds the previous binary's hash"

setup-terraformrc-filesystem: ## Create ~/.terraformrc for filesystem_mirror
	@printf 'provider_installation {\n  filesystem_mirror {\n    path    = "%s"\n    include = ["%s/%s/*"]\n  }\n  direct {\n    exclude = ["%s/%s/*"]\n  }\n}\n' \
		'$(PLUGINS_DIR)' '$(HOSTNAME)' '$(NAMESPACE)' '$(HOSTNAME)' '$(NAMESPACE)' > ~/.terraformrc
	@echo "✓ ~/.terraformrc written"

setup: install-filesystem setup-terraformrc-filesystem ## Initial setup (install + terraformrc)
	@echo "✓ setup complete; provider source: $(HOSTNAME)/$(NAMESPACE)/$(NAME)"

# ── Dev workflow ───────────────────────────────────────────
dev-setup: ## Configure go.work to use the local SDK (see CONTRIBUTING.md)
	@test -d $(SDK_DIR) || { echo "SDK not found in $(SDK_DIR) — clone it there"; exit 1; }
	@test -f go.work || go work init .
	@go work edit -replace $(SDK_MODULE)=$(SDK_DIR)
	@echo "✓ go.work → $(SDK_DIR)"

check-release: ## Build without go.work (as in CI/release, SDK from go.mod)
	GOWORK=off go build ./...

deps: ## go mod download + tidy
	go mod download && go mod tidy

fmt: ## go fmt + terraform fmt
	go fmt ./...
	@terraform fmt -recursive $(EXAMPLES_DIR)/ 2>/dev/null || true

vet: ## go vet
	go vet ./...

lint: $(BIN_DIR)/golangci-lint ## golangci-lint (pinned version, auto-installed into bin/)
	$(BIN_DIR)/golangci-lint run ./...

# ── Pinned tools ───────────────────────────────────────────
# GOWORK=off: `go install pkg@version` must not be affected by a local go.work.
$(BIN_DIR)/golangci-lint:
	@mkdir -p $(BIN_DIR)
	GOWORK=off GOBIN=$(BIN_DIR) go install \
		github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

$(BIN_DIR)/tfplugindocs:
	@mkdir -p $(BIN_DIR)
	GOWORK=off GOBIN=$(BIN_DIR) go install \
		github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@$(TFPLUGINDOCS_VERSION)

tools: $(BIN_DIR)/golangci-lint $(BIN_DIR)/tfplugindocs ## Install the pinned dev tools into bin/

# Lets CI read a pin without duplicating it: make -s print-GOLANGCI_VERSION
print-%:
	@echo $($*)

# ── Tests ──────────────────────────────────────────────────
# Unit tests only: without TF_ACC the framework skips every TestAcc* before it
# reaches PreCheck, so this needs no credentials and touches no cloud resources.
test: ## Unit tests (no cloud calls, no credentials needed)
	go test ./... $(TESTARGS)

# ── Acceptance tests (need VCP_* env; see .env.example) ────
# -p 1: run test packages serially — all packages share one cloud account,
# parallel apply/destroy across services trips backend quotas and races.
testacc: ## All acceptance tests
	TF_ACC=1 go test $(if $(TEST),$(TEST),./...) -v -p 1 $(TESTARGS) -timeout 120m

testacc-service: ## Tests for a single service (SERVICE=…)
	@test -n "$(SERVICE)" || { echo "usage: make testacc-service SERVICE=server"; exit 1; }
	TF_ACC=1 go test $(SERVICES_DIR)/$(SERVICE) -v -timeout 120m

testacc-test: ## A single test (SERVICE=… TEST=…)
	@test -n "$(SERVICE)" -a -n "$(TEST)" || { echo "usage: make testacc-test SERVICE=server TEST=TestAccServer_basic"; exit 1; }
	TF_ACC=1 go test $(SERVICES_DIR)/$(SERVICE) -v -run $(TEST) -timeout 120m

# ── Sweepers (delete leftover test resources by name prefix test-acc-*/tf-acc-*) ──
# make sweep                        — tear down all test resources
# make sweep SWEEP_RUN=vcp_server   — servers only (names: vcp_server, vcp_network)
sweep: ## Tear down test resources (SWEEP_RUN=vcp_server — servers only)
	TF_ACC=1 go test ./internal/acctest -v -timeout $(SWEEP_TIMEOUT) -sweep=all $(if $(SWEEP_RUN),-sweep-run=$(SWEEP_RUN),) $(SWEEPARGS)

# ── Docs ───────────────────────────────────────────────────
docs-generate: $(BIN_DIR)/tfplugindocs ## tfplugindocs generate
	$(BIN_DIR)/tfplugindocs generate --provider-name $(NAME)

docs-validate: $(BIN_DIR)/tfplugindocs ## tfplugindocs validate
	$(BIN_DIR)/tfplugindocs validate --provider-name $(NAME)

# ── Clean ──────────────────────────────────────────────────
# Removes build output but keeps bin/'s pinned tools — re-fetching them after
# every clean costs a full rebuild of golangci-lint. clean-all drops them too.
clean: ## Remove build output and example artifacts (keeps the pinned tools)
	rm -rf $(BINARY)
	@rm -f $(BUILD_DIR)/$(BINARY)_*
	@find $(EXAMPLES_DIR) -type d -name .terraform -exec rm -rf {} + 2>/dev/null || true
	@find $(EXAMPLES_DIR) -type f \( -name 'terraform.tfstate*' -o -name '.terraform.lock.hcl' \) -delete 2>/dev/null || true

# The second rm clears a leftover from the pre-rename layout, when the provider
# was installed as localhost/main/vcp.
clean-all: clean ## clean + remove the pinned tools and the installed provider
	rm -rf $(BUILD_DIR)
	rm -rf $(PLUGINS_DIR)/$(HOSTNAME)/$(NAMESPACE)
	@rm -rf $(PLUGINS_DIR)/localhost/main/$(NAME)
