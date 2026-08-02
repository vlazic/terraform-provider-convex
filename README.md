# terraform-provider-convex

A Terraform provider for the [Convex](https://convex.dev) platform, built on
[terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework)
and the [Convex Management API](https://docs.convex.dev/management-api).

## Status

Early. Two resources and two data sources are implemented:

| Resource                    | Create | Read | Update           | Delete | Import                        |
| --------------------------- | ------ | ---- | ---------------- | ------ | ----------------------------- |
| `convex_project`            | yes    | yes  | yes              | yes    | by numeric ID                 |
| `convex_deployment`         | yes    | yes  | yes              | yes    | by name                       |
| `convex_deploy_key`         | yes    | yes  | no, replace only | yes    | `<deployment_name>/<key_name>` |
| `convex_preview_deploy_key` | yes    | yes  | no, replace only | yes    | `<project_id>/<key_name>`      |
| `convex_custom_domain`      | yes    | yes  | no, replace only | yes    | `<deployment_name>/<request_destination>/<domain>` |

| Data source         | Lookup keys                                            |
| ------------------- | ------------------------------------------------------ |
| `convex_project`    | `id`, or `slug` + (`team_id` \| `team_slug`)            |
| `convex_deployment` | `name`, or `project_id` + one selector                  |

## Usage

```hcl
terraform {
  required_providers {
    convex = {
      source = "srs/convex"
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

More in [`examples/`](examples/).

### Authentication

The provider authenticates with a bearer token, which may be a team access
token, a personal access token, or an OAuth token. Team access tokens are
created in the Convex dashboard under team settings, which is also where you
find the numeric team ID that `convex_project.team_id` expects.

| Attribute  | Environment variable   | Default                     |
| ---------- | ---------------------- | --------------------------- |
| `token`    | `CONVEX_ACCESS_TOKEN`  | none, required              |
| `endpoint` | `CONVEX_ENDPOINT`      | `https://api.convex.dev/v1` |

### Notes on `convex_project`

- `team_id` forces replacement. Convex has no API for moving a project between
  teams.
- `slug` is generated from `name` by Convex. Setting it explicitly on a new
  project costs a second API call, because the create endpoint has no slug
  parameter and the rename has to follow.
- Creating a project does not create a deployment. Declare a
  `convex_deployment` for that.
- Import takes the numeric project ID: `terraform import convex_project.app 12345`.

### Notes on `convex_deployment`

- Manages cloud deployments. Local deployments — backends the Convex CLI
  registers for a developer's machine — are rejected with a clear error rather
  than half-decoded, since they share almost no fields with cloud ones.
- Import takes the deployment **name**, not a numeric ID:
  `terraform import convex_deployment.prod playful-otter-123`. The name is what
  the API addresses deployments by.
- `region` forces replacement, but only when you set it explicitly — a
  server-assigned default region does not.
- `deployment_type`, `class`, `reference` and `is_default` are all updatable in
  place. Changing the type of a default deployment requires setting
  `is_default` in the same apply.
- `reference` is validated at plan time against the rules the API documents.
  Watch out for one non-obvious rule: a reference must not have the shape of a
  deployment name (word-word-number), so a natural-looking `prod-eu-2` is
  rejected while `prod-eu-west-2` is fine.
- `expires_at`, `send_logs_to_client` and `dashboard_edit_confirmation` are
  tri-state in the API, where null means "use the deployment-type default".
  They are modelled as optional+computed, so setting and changing them works,
  but **removing one from configuration keeps the last applied value** rather
  than resetting it to the default. Set it explicitly to the default instead.
- `last_deploy_time` changes whenever code is pushed, entirely outside
  Terraform, so it is refreshed on every read and never used to plan changes.

### Notes on the deploy key resources

**The generated secret is written to Terraform state in plain text.** Convex
returns a deploy key's value exactly once, in the create response, and no
endpoint can fetch it again. A provider cannot avoid persisting it, so treat
the state file as a secret: use a remote backend with encryption at rest and
restricted access. This applies to both `convex_deploy_key` and
`convex_preview_deploy_key`.

- **Nothing is updatable.** Convex has no endpoint for modifying a deploy key,
  so every configurable attribute forces replacement. Replacing means the new
  key is created and the old one destroyed — anything still holding the old
  value breaks at that moment. Plan key rotation accordingly.
- **Reads go through the list endpoint.** There is no get-by-id for deploy
  keys, so the provider lists the deployment's or project's keys and matches on
  `name`. Names must therefore be unique within their scope, which the API
  already assumes — its delete endpoint accepts a name as an identifier.
- **Importing cannot recover the secret.** An imported key has `key` set to
  null, and the import emits a warning saying so. Everything else refreshes
  normally. To get a usable secret you have to replace the key.
- `allowed_actions` is optional on `convex_deploy_key`. Leaving it out accepts
  whatever Convex grants by default, which is broader than most CI jobs need —
  `["deployment:deploy"]` is usually the right scope for a deploy pipeline.
  Preview deploy keys have no equivalent setting, so there the attribute is
  read-only.
- `last_used_time` changes whenever the key is used, entirely outside
  Terraform.

### Notes on `convex_custom_domain`

- **Verification is asynchronous and Terraform does not wait for it.** Creating
  the resource registers the domain and returns immediately; Convex verifies it
  once the DNS records resolve. So `verified` is normally `false` right after
  the first apply and flips to `true` on a later refresh. Point your DNS at the
  deployment separately — this resource does not manage DNS records.
- `request_destination` is part of the identity, not a detail. `convexCloud`
  fronts the deployment's `.convex.cloud` API URL and `convexSite` fronts its
  `.convex.site` HTTP actions URL, and the same domain can be registered for
  both. It is required rather than defaulted, because silently picking the
  wrong one would be hard to notice.
- Nothing is updatable, so every attribute forces replacement. Reads go through
  the list endpoint and match on domain + destination, since there is no
  get-by-id.
- `verified` is a convenience for `verification_time != null`; both are
  exposed.
- Note that `convex_deployment.deployment_url` always reports the
  `.convex.cloud` URL, even once a custom domain is in place.

### Notes on the data sources

- `data.convex_project` takes either `id`, or `slug` plus one of `team_id` /
  `team_slug`. A slug is only unique within a team, so it cannot stand alone.
- `data.convex_deployment` takes either `name`, or `project_id` plus exactly
  one selector: `reference`, `default_prod` or `default_dev`. A project
  normally holds several deployments, so the selector is required rather than
  defaulted — guessing would be worse than asking.
- There is no team+slug lookup for deployments. Resolve the project with
  `data.convex_project` and feed its `id` to `data.convex_deployment`, which
  composes to the same thing without a second lookup path to maintain.
- `default_dev` resolves against the calling member, so which deployment it
  returns depends on the token in use. Prefer `reference` when you need a
  stable answer across machines and CI.

## Development

The API client in `internal/convex/client.gen.go` is generated from the
vendored OpenAPI document at `api/openapi.json`.

```bash
make fetch-spec   # refresh api/openapi.json from https://api.convex.dev/v1/openapi.json
make generate     # normalize the spec, then regenerate the client
make test         # unit tests
make build        # build the provider binary
```

Both generated artifacts (`api/openapi.normalized.json` and
`internal/convex/client.gen.go`) are committed, so a checkout builds without
running codegen.

### Why the spec is normalized before codegen

Thirteen path parameters in the upstream document declare neither `schema` nor
`content`, which is invalid OpenAPI and makes `oapi-codegen` fail outright.
`internal/tools/normalizespec` backfills each one from a well-formed occurrence
of the same parameter elsewhere in the document, refusing to guess if those
occurrences disagree. The vendored `api/openapi.json` stays byte-identical to
upstream so that refreshing it is a clean overwrite.

The generated per-operation response wrappers also carry an `HTTPResponse`
suffix, because several schema components in the spec are themselves named
`<Something>Response` and would otherwise collide.

### Running against a local build

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "srs/convex" = "/path/to/terraform-provider-convex"
  }
  direct {}
}
```

With a dev override in place, run `terraform plan` / `terraform apply`
directly; `terraform init` is neither needed nor supported.
