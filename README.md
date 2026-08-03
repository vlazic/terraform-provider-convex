# terraform-provider-convex

A Terraform provider for the [Convex](https://convex.dev) platform, built on
[terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework).

It wraps both of Convex's management APIs — the
[Management API](https://docs.convex.dev/management-api) for provisioning
projects and deployments, and the
[Deployment Platform API](https://docs.convex.dev/deployment-platform-api) for
configuring a deployment that already exists. One token authenticates both, so
the provider takes a single `token`.

## Status

Version **0.2.0**. Eight resources and two data sources, all with import
support.

The surface is complete enough to manage projects, deployments, deploy keys,
custom domains, environment variables and log streams end to end. Being 0.x,
the API surface may still change, and there is no acceptance test suite yet —
behaviour is covered by unit tests and an offline plan rather than against a
live API.

| Resource                              | Update           | API        |
| ------------------------------------- | ---------------- | ---------- |
| `convex_project`                      | yes              | Management |
| `convex_deployment`                   | yes              | Management |
| `convex_deploy_key`                   | no, replace only | Management |
| `convex_preview_deploy_key`           | no, replace only | Management |
| `convex_custom_domain`                | no, replace only | Management |
| `convex_default_environment_variable` | value only       | Management |
| `convex_environment_variable`         | value only       | Deployment |
| `convex_log_stream`                   | yes              | Deployment |

Resources on the Deployment Platform API take a `deployment_url` rather than a
name, because each deployment is served from its own host. Pass
`convex_deployment.<name>.deployment_url`: a regional deployment looks like
`https://calm-cow-456.eu-west-1.convex.cloud`, so the URL cannot be built from
the deployment name.

| Data source         | Lookup keys                                  |
| ------------------- | -------------------------------------------- |
| `convex_project`    | `id`, or `slug` + (`team_id` \| `team_slug`) |
| `convex_deployment` | `name`, or `project_id` + one selector       |

## Usage

```hcl
terraform {
  required_providers {
    convex = {
      source  = "srs/convex"
      version = "0.2.0"
    }
  }
}

provider "convex" {
  token = var.convex_token # or the CONVEX_ACCESS_TOKEN environment variable
}

resource "convex_project" "app" {
  team_id = 1234
  name    = "My Application"
}

resource "convex_deployment" "prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  region          = "aws-eu-west-1"
  is_default      = true
}
```

### Authentication

The provider authenticates with a bearer token, which may be a team access
token, a personal access token, or an OAuth token. Team access tokens are
created in the Convex dashboard under team settings, which is also where you
find the numeric team ID that `convex_project.team_id` expects.

| Attribute  | Environment variable  | Default                     |
| ---------- | --------------------- | --------------------------- |
| `token`    | `CONVEX_ACCESS_TOKEN` | none, required              |
| `endpoint` | `CONVEX_ENDPOINT`     | `https://api.convex.dev/v1` |

`endpoint` overrides the Management API base URL only. Deployment Platform API
calls go to whatever `deployment_url` the resource was given, so they are not
affected by it.

## Documentation

Per-resource reference lives in [`docs/`](docs/), generated from the provider
schemas — that is also what the Terraform Registry renders. Runnable
configuration is in [`examples/`](examples/).

The registry's overview page comes from [`templates/index.md.tmpl`](templates/index.md.tmpl)
rather than from a schema description, so prose for that page belongs there.
Everything else is generated from schemas plus `examples/`; `make docs` rebuilds
the tree and CI fails if it is stale.

Each resource's page documents its own constraints. A few are worth knowing up
front:

- **Deploy key secrets are stored in Terraform state in plain text.** Convex
  returns them only once, at creation, so no provider can avoid it and import
  cannot recover them. Treat state as a secret.
- **Log streams need a Convex Pro plan**, and start asynchronously — `status`
  is `pending` until a later refresh reports `active` or `failed`.
- **Convex is transactional, so parallel writes to one deployment conflict.**
  Several environment variables applied at once can return a 503
  `OptimisticConcurrencyControlFailure`. Idempotent writes are retried
  automatically; if it still bites, `terraform apply -parallelism=1` serialises
  them. Creates are deliberately not retried, since a failure does not prove
  nothing was made.
- **Two kinds of environment variable.** `convex_environment_variable` changes
  a running deployment. `convex_default_environment_variable` only seeds
  deployments created *afterwards* — Convex does not sync it to existing ones.
- **Custom domain verification is asynchronous.** `verified` stays `false`
  until a later refresh picks up the DNS check, and this resource does not
  manage DNS records.
- **Several attributes are tri-state** (`expires_at`, `send_logs_to_client`,
  `dashboard_edit_confirmation`), where null means "use the default". Removing
  one from configuration keeps the last applied value; set it explicitly.

## Development

Two Convex APIs are wrapped, each with its own generated client:

| Package | API | Addressed at | Auth header |
| --- | --- | --- | --- |
| `internal/convex` | Management | `api.convex.dev/v1` | `Bearer <token>` |
| `internal/deployapi` | Deployment Platform | each deployment's own URL | `Convex <token>` |

The same team access token authenticates both, so the provider takes a single
`token`. Shared error decoding lives in `internal/apierr`. The deployment spec
is not served anywhere, so `make fetch-spec` pulls it from the convex-backend
repository rather than from the API.

```bash
make fetch-spec   # refresh both vendored OpenAPI documents
make generate     # normalize the specs, then regenerate both API clients
make docs         # regenerate docs/ from the provider schemas
make test         # unit tests
make smoke        # offline end-to-end plan through real Terraform
make check        # lint + test + smoke, i.e. what CI runs
make build        # build the provider binary
make install      # install into the local plugin mirror
```

Generated artifacts (`api/management-openapi.normalized.json`, both
`client.gen.go` files and `docs/`) are committed, so a checkout builds without
running codegen. CI fails if they are stale.

### The smoke test

`make smoke` builds the provider, points Terraform at the binary with a
`dev_overrides` block, and plans [`test/smoke`](test/smoke) against it. That
runs the actual plugin protocol, so schemas, plan modifiers, validators and
attribute sensitivity are exercised the way Terraform exercises them — which
the Go unit tests cannot do on their own.

It needs no credentials and no network, and applies nothing: every resource in
the config is new and both data sources depend on one, so Terraform defers the
reads to apply. It plans a *copy* of the directory, so state left behind by a
stray apply cannot turn the check into a live refresh.

The script asserts against the plan JSON that every registered resource and
data source appears, deriving the expected list from `provider.go` itself, so
adding a resource without adding it to the smoke config fails the build. It
also checks that deploy key secrets are marked sensitive.

If `terraform` is not installed the script exits 0 with a message, so it does
not break a Go-only checkout.

### Why the spec is normalized before codegen

Thirteen path parameters in the upstream OpenAPI document declare neither
`schema` nor `content`, which is invalid and makes `oapi-codegen` fail
outright. `internal/tools/normalizespec` backfills each one from a well-formed
occurrence of the same parameter elsewhere in the document, refusing to guess
if those occurrences disagree. The vendored `api/management-openapi.json` stays
byte-identical to upstream, so refreshing it is a clean overwrite.

Generated per-operation response wrappers carry an `HTTPResponse` suffix,
because several schema components in the spec are themselves named
`<Something>Response` and would otherwise collide.

### Running against a local build

The source address is `srs/convex`, which the registry maps to
`github.com/srs/terraform-provider-convex`. Until a release is published it
resolves only to a local build, via one of two mechanisms.

**Local plugin mirror.** `make install` copies the binary into Terraform's
implied local mirror, under the deliberately unresolvable address
**`local/srs/convex`**. Nothing else is needed — no CLI configuration, no flags
— and `terraform init` resolves it offline and writes a real lock file, so the
workflow matches a published provider.

The `local/` host matters: a config pinning `local/srs/convex` can only ever
load your build. Pinning the real `srs/convex` risks silently falling back to
the published release from the registry, which is exactly the mistake you would
not notice. The released binary keeps its real address; Terraform does not
cross-check the two, so the same build serves both.

Two things bite during development. The mirror holds a *copy*, so `make build`
is not enough: you must `make install`, or Terraform silently keeps running the
previous binary. And reinstalling invalidates `.terraform.lock.hcl`, which
records a checksum of a binary that changed while the version stayed put — the
next `terraform init` then fails on the checksum, and the fix is to delete the
lock and re-init. The sandbox Makefile does both automatically.

**`dev_overrides`.** Points the address straight at a build directory: no copy,
no lock, and `terraform init` is neither needed nor supported. Easier for a
tight edit-and-plan loop, and what the smoke test uses.

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "srs/convex" = "/path/to/terraform-provider-convex"
  }
  direct {}
}
```

Note that a provider `source` is always `[host/]namespace/type` — unlike module
sources it can never be a filesystem path. Both mechanisms work by mapping that
address to a local directory.

### Sandbox

`../convex-tf-sandbox` is a scratch config covering the whole surface. Unlike
the smoke test it talks to the real API and creates real, possibly billable
resources, so it needs a token and a team ID.

```bash
cd ../convex-tf-sandbox
cp terraform.tfvars.example terraform.tfvars   # then fill it in
export CONVEX_ACCESS_TOKEN=...
make plan       # rebuilds the provider, then plans
```

It loads the provider through `dev_overrides` under the address
`local/srs/convex`, from a `dev.tfrc` its Makefile generates. There is no plugin
mirror copy, no lock file and no `terraform init`, so a rebuild can never leave
a stale checksum behind — which the mirror did on every provider change. Bare
`terraform` commands work there too, as long as `TF_CLI_CONFIG_FILE` points at
that `dev.tfrc`. `make destroy` cleans up.

### Cutting a release

The steps are order-sensitive: tagging before the signing secrets exist
produces a failed release run, and re-running a tag is awkward.

1. **Create a GPG signing key** and add the *public* key to your registry
   namespace at registry.terraform.io. The registry verifies that the checksums
   file in each release is signed by it.
2. **Add repository secrets** in GitHub: `GPG_PRIVATE_KEY` (ASCII-armored) and
   `PASSPHRASE`.
3. **Publish the provider** on registry.terraform.io — sign in with GitHub,
   authorize the account, select this repository.
4. **Update `CHANGELOG.md`** and bump `VERSION` in the `Makefile` if the local
   mirror should match.
5. **Tag and push:** `git tag v0.2.0 && git push origin v0.2.0`. The
   [release workflow](.github/workflows/release.yml) builds every platform with
   GoReleaser, signs the checksums and creates the GitHub release; the registry
   picks it up from the tag.

Watch the workflow finish before assuming a release landed — a missing or
mistyped secret fails at the GPG import step, and the registry will simply
never see a release for that tag.

## License

Copyright 2026 Sten Roger Sandvik <stenrs@gmail.com>.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
