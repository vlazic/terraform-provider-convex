BINARY := terraform-provider-convex

.PHONY: default
default: build

# Refresh the vendored OpenAPI document from the live API.
.PHONY: fetch-spec
fetch-spec:
	curl -fsSL https://api.convex.dev/v1/openapi.json -o api/openapi.json

# Normalize the spec and regenerate the API client.
.PHONY: generate
generate:
	go generate ./...

.PHONY: build
build:
	go build -o $(BINARY) .

.PHONY: test
test:
	go test ./... -count=1

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
