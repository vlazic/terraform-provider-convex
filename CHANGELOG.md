# Changelog

Notable changes, newest first. Entries accumulate under **Unreleased** and get
a version heading and date when a release is tagged.

Grouped as Added / Changed / Fixed / Removed, following
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions follow
[semantic versioning](https://semver.org/spec/v2.0.0.html), with the usual 0.x
caveat that minor releases may still break.

## Unreleased

### Added

- `convex_environment_variable` — sets an environment variable on a **running**
  deployment, through the
  [Deployment Platform API](https://docs.convex.dev/deployment-platform-api).
  This is distinct from `convex_default_environment_variable`, which only seeds
  deployments created afterwards and has no effect on existing ones. Takes a
  `deployment_url`, since regional deployments cannot be addressed by name.

### Changed

- The provider now wraps two Convex APIs. `internal/convex` covers the
  management API; the new `internal/deployapi` covers the Deployment Platform
  API, which is addressed at each deployment's own URL and takes a `Convex `
  authorization prefix instead of `Bearer `. The same team access token
  authenticates both, so provider configuration is unchanged.
- Error decoding shared by both clients moved to `internal/apierr`.
  `convex.APIError` remains as a type alias.
- `api/openapi.json` renamed to `api/management-openapi.json` now that a second
  spec is vendored. `make fetch-spec` refreshes both; the deployment spec comes
  from the convex-backend repository, which is the only place it is published.
- CI actions updated to their latest major versions.

## 0.1.0 (2026-08-03)

Initial release.

### Features

- **Resources:** `convex_project`, `convex_deployment`, `convex_deploy_key`,
  `convex_preview_deploy_key`, `convex_custom_domain`,
  `convex_default_environment_variable`.
- **Data sources:** `convex_project` (by ID, or by slug within a team) and
  `convex_deployment` (by name, or by project plus a selector).
- Authentication with a team access token, personal access token or OAuth
  token, via the `token` attribute or `CONVEX_ACCESS_TOKEN`.
- Every resource supports import.

### Known limitations

- Deploy key secrets are returned by Convex only at creation, so they are
  stored in Terraform state in plain text and cannot be recovered by import.
- Default environment variables apply only to deployments created afterwards;
  Convex does not sync them to existing deployments.
- Custom domain verification is asynchronous, so `verified` is `false` until a
  later refresh picks up the DNS check.
- No acceptance test suite yet, so resource behaviour is verified by unit tests
  and an offline plan rather than against a live API.
