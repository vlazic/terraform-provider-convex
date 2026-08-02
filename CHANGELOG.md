# Changelog

## 0.1.0 (unreleased)

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
