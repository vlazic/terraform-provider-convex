# By name, when you already know it.
data "convex_deployment" "by_name" {
  name = "playful-otter-123"
}

# Or pick one out of a project. There is no team+slug lookup for deployments:
# resolve the project first and pass its id here.
data "convex_project" "app" {
  team_slug = "my-team"
  slug      = "my-application"
}

# Exactly one selector is required -- a project usually holds several
# deployments, so there is nothing sensible to default to.
data "convex_deployment" "prod" {
  project_id   = data.convex_project.app.id
  default_prod = true
}

data "convex_deployment" "prod_us" {
  project_id = data.convex_project.app.id
  reference  = "prod-us"
}

# default_dev resolves against whoever's token is in use, so this returns a
# different deployment for a developer's PAT than for a team token in CI.
# Prefer a reference when you need a stable answer.
data "convex_deployment" "mine" {
  project_id  = data.convex_project.app.id
  default_dev = true
}

output "prod_url" {
  value = data.convex_deployment.prod.deployment_url
}

output "prod_region" {
  value = data.convex_deployment.prod.region
}

# Null when the deployment-type default applies rather than an explicit
# override.
output "prod_sends_logs_to_client" {
  value = data.convex_deployment.prod.send_logs_to_client
}
