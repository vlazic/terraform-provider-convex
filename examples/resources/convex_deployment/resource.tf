resource "convex_project" "app" {
  team_id = var.convex_team_id
  name    = "My Application"
}

# The project's default production deployment.
resource "convex_deployment" "prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  region          = "aws-eu-west-1"
  is_default      = true

  # Production defaults to false. Turning it on streams function logs to
  # connected clients, which is useful while debugging but noisy otherwise.
  send_logs_to_client = true
}

# A second prod deployment in the same project. Anything beyond the default
# one needs its own reference.
resource "convex_deployment" "prod_us" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  region          = "aws-us-east-1"
  reference       = "prod-us"

  # Skip the dashboard's edit confirmation prompt, which is on by default for
  # production deployments.
  dashboard_edit_confirmation = false
}

# Dev deployments default to sending logs to the client; turn it off
# explicitly rather than by removing the attribute, which would keep whatever
# was last applied.
resource "convex_deployment" "dev" {
  project_id      = convex_project.app.id
  deployment_type = "dev"
  reference       = "dev-shared"

  send_logs_to_client = false
}

output "prod_url" {
  value = convex_deployment.prod.deployment_url
}

output "prod_name" {
  # Something like "playful-otter-123".
  value = convex_deployment.prod.name
}
