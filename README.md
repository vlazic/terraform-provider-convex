# terraform-provider-convex

A Terraform provider for the [Convex](https://convex.dev) platform, built on
[terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework)
and the [Convex Management API](https://docs.convex.dev/management-api).

## Status

Early. Two resources and two data sources are implemented:

| Resource            | Create | Read | Update | Delete | Import        |
| ------------------- | ------ | ---- | ------ | ------ | ------------- |
| `convex_project`    | yes    | yes  | yes    | yes    | by numeric ID |
| `convex_deployment` | yes    | yes  | yes    | yes    | by name       |

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
