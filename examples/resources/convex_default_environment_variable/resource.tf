resource "convex_project" "app" {
  team_id = var.convex_team_id
  name    = "My Application"
}

# The same name can carry a different value per deployment type, so each pair
# is its own resource.
resource "convex_default_environment_variable" "sentry_prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  name            = "SENTRY_DSN"
  value           = var.sentry_dsn_prod
}

resource "convex_default_environment_variable" "sentry_dev" {
  project_id      = convex_project.app.id
  deployment_type = "dev"
  name            = "SENTRY_DSN"
  value           = var.sentry_dsn_dev
}

# Spanning several types is a for_each over the types you want.
resource "convex_default_environment_variable" "log_level" {
  for_each = toset(["dev", "preview"])

  project_id      = convex_project.app.id
  deployment_type = each.key
  name            = "LOG_LEVEL"
  value           = "debug"
}

# Defaults apply when a deployment is created, so declare them before the
# deployment that should pick them up.
resource "convex_deployment" "prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  is_default      = true

  depends_on = [convex_default_environment_variable.sentry_prod]
}

variable "sentry_dsn_prod" {
  type      = string
  sensitive = true
}

variable "sentry_dsn_dev" {
  type      = string
  sensitive = true
}
