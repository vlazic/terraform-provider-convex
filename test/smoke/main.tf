# Offline smoke test configuration, driven by scripts/smoke.sh.
#
# DO NOT APPLY THIS. It is planned, never applied: the values below are
# placeholders (team 1234, api.example.com) and applying it against a real
# token creates real, billable Convex resources under a project literally
# named "Smoke Test".
#
# scripts/smoke.sh plans a copy of this directory, so any state left here by a
# stray apply is ignored rather than silently refreshed.
#
# It exercises every resource and data source the provider registers. That keeps it hermetic: no credentials
# and no network access are required, because every resource is new and every
# data source depends on one of them, so Terraform defers the reads to apply.
#
# Keep this in step with provider.Resources and provider.DataSources: the
# script asserts that the plan covers every registered type.

terraform {
  required_version = ">= 1.8"

  required_providers {
    convex = {
      source  = "srs/convex"
      version = "0.1.0"
    }
  }
}

provider "convex" {
  # Supplied by the script as CONVEX_ACCESS_TOKEN. Never used, since nothing
  # here reaches the API during a plan.
}

resource "convex_project" "smoke" {
  team_id = 392742
  name    = "Smoke Test"
  slug    = "smoke-test"
}

resource "convex_default_environment_variable" "log_level" {
  for_each = toset(["dev", "prod"])

  project_id      = convex_project.smoke.id
  deployment_type = each.key
  name            = "LOG_LEVEL"
  value           = each.key == "prod" ? "info" : "debug"
}

resource "convex_deployment" "prod" {
  project_id      = convex_project.smoke.id
  deployment_type = "prod"
  is_default      = true
  region          = "aws-eu-west-1"

  send_logs_to_client         = false
  dashboard_edit_confirmation = true

  depends_on = [convex_default_environment_variable.log_level]
}

# A second deployment needs its own reference. "prod-eu-2" would be rejected
# at plan time: two words then digits is the deployment-name shape, which
# Convex reserves. That the plan succeeds proves the validator accepts this.
resource "convex_deployment" "secondary" {
  project_id      = convex_project.smoke.id
  deployment_type = "preview"
  reference       = "preview-shared"
}

resource "convex_deploy_key" "ci" {
  deployment_name = convex_deployment.prod.name
  name            = "ci"

  allowed_actions = [
    "deployment:deploy",
    "deployment:env:view",
  ]
}

resource "convex_preview_deploy_key" "previews" {
  project_id = convex_project.smoke.id
  name       = "previews"
}

resource "convex_custom_domain" "api" {
  deployment_name     = convex_deployment.prod.name
  domain              = "api.example.com"
  request_destination = "convexCloud"
}

# Both data sources key off attributes that are unknown until apply, which is
# what keeps the plan offline.
data "convex_project" "by_slug" {
  team_slug = convex_project.smoke.team_slug
  slug      = convex_project.smoke.slug
}

data "convex_deployment" "default_prod" {
  project_id   = convex_project.smoke.id
  default_prod = true

  depends_on = [convex_deployment.prod]
}

output "sensitive_values_are_masked" {
  value = {
    deploy_key         = convex_deploy_key.ci.key
    preview_deploy_key = convex_preview_deploy_key.previews.key
  }
  sensitive = true
}
