resource "convex_project" "app" {
  team_id = var.convex_team_id
  name    = "My Application"
}

resource "convex_deployment" "prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  is_default      = true
}

# Sets the variable on the running deployment, taking effect immediately.
#
# Always feed it deployment_url rather than building a URL from the deployment
# name: regional deployments look like
# https://calm-cow-456.eu-west-1.convex.cloud.
resource "convex_environment_variable" "stripe_key" {
  deployment_url = convex_deployment.prod.deployment_url
  name           = "STRIPE_KEY"
  value          = var.stripe_key
}

# Works against a deployment Terraform does not manage, too.
data "convex_deployment" "existing" {
  name = "playful-otter-123"
}

resource "convex_environment_variable" "sentry_dsn" {
  deployment_url = data.convex_deployment.existing.deployment_url
  name           = "SENTRY_DSN"
  value          = var.sentry_dsn
}

# Compare with convex_default_environment_variable, which does not touch any
# existing deployment -- it only seeds deployments created afterwards.
resource "convex_default_environment_variable" "log_level" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  name            = "LOG_LEVEL"
  value           = "info"
}

variable "stripe_key" {
  type      = string
  sensitive = true
}

variable "sentry_dsn" {
  type      = string
  sensitive = true
}
