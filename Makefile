BINARY := terraform-provider-convex

# Version used when installing into the local plugin mirror. Terraform needs a
# concrete version there, so this is what `version =` constraints resolve
# against locally.
VERSION ?= 0.1.0

# The "implied local mirror" Terraform searches without any CLI configuration.
PLUGIN_HOST      := registry.terraform.io
PLUGIN_NAMESPACE := srs
PLUGIN_TYPE      := convex
PLATFORM         := $(shell go env GOOS)_$(shell go env GOARCH)
MIRROR_DIR       := $(HOME)/.terraform.d/plugins/$(PLUGIN_HOST)/$(PLUGIN_NAMESPACE)/$(PLUGIN_TYPE)/$(VERSION)/$(PLATFORM)

.PHONY: default
default: build

# Refresh the vendored OpenAPI documents.
#
# The management spec is served by the API itself. The deployment spec is not
# published anywhere servable, so it comes from the convex-backend repository.
.PHONY: fetch-spec
fetch-spec:
	curl -fsSL https://api.convex.dev/v1/openapi.json -o api/management-openapi.json
	curl -fsSL https://raw.githubusercontent.com/get-convex/convex-backend/main/npm-packages/@convex-dev/platform/deployment-openapi.json -o api/deployment-openapi.json

# Normalize the spec and regenerate the API client.
.PHONY: generate
generate:
	go generate ./...

# Generate the docs/ tree the Terraform Registry renders. Reads the provider
# schemas plus examples/ and templates/, so schema descriptions are the single
# source of truth for the published documentation.
.PHONY: docs
docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate \
		--provider-name convex --rendered-provider-name Convex

.PHONY: build
build:
	go build -o $(BINARY) -ldflags "-X main.version=$(VERSION)" .

# Install into the local plugin mirror, so `terraform init` finds the provider
# with no CLI configuration at all -- no dev_overrides, no -plugin-dir.
# Configs then pin it the ordinary way:
#
#     convex = { source = "srs/convex", version = "0.1.0" }
#
# Unlike dev_overrides this behaves like a published provider: init works,
# version constraints apply and a .terraform.lock.hcl is written. The tradeoff
# is that you must re-run `make install` after every code change, and bump
# VERSION when a config pins an exact version.
.PHONY: install
install: build
	mkdir -p $(MIRROR_DIR)
	cp $(BINARY) $(MIRROR_DIR)/$(BINARY)_v$(VERSION)
	@echo "installed $(BINARY) $(VERSION) to $(MIRROR_DIR)"

.PHONY: uninstall
uninstall:
	rm -rf $(HOME)/.terraform.d/plugins/$(PLUGIN_HOST)/$(PLUGIN_NAMESPACE)/$(PLUGIN_TYPE)
	@echo "removed $(PLUGIN_NAMESPACE)/$(PLUGIN_TYPE) from the local plugin mirror"

.PHONY: test
test:
	go test ./... -count=1

# Offline end-to-end check: plans test/smoke through real Terraform against a
# locally built binary. Needs no credentials and no network, and applies
# nothing. Skipped with a message when terraform is not installed.
.PHONY: smoke
smoke:
	./scripts/smoke.sh

# What CI should run.
.PHONY: check
check: lint test smoke

# Acceptance tests talk to the real Convex API and create billable resources.
.PHONY: testacc
testacc:
	TF_ACC=1 go test ./... -v -count=1 -timeout 30m

.PHONY: fmt
fmt:
	gofmt -w .
	terraform fmt -recursive ./examples

.PHONY: lint
lint:
	gofmt -l .
	go vet ./...
