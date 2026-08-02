resource "convex_project" "app" {
  team_id = var.convex_team_id
  name    = "My Application"
}

resource "convex_deployment" "prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  is_default      = true
}

# A key scoped to just what CI needs to push code.
resource "convex_deploy_key" "ci" {
  deployment_name = convex_deployment.prod.name
  name            = "github-actions"

  allowed_actions = [
    "deployment:deploy",
    "deployment:env:view",
  ]
}

# Omitting allowed_actions accepts whatever Convex grants by default, which is
# broader than the list above.
resource "convex_deploy_key" "admin" {
  deployment_name = convex_deployment.prod.name
  name            = "ops-admin"
}

# A key with an expiry. expires_at is milliseconds since the Unix epoch and
# must be at least 30 minutes in the future.
#
# Terraform has no function that converts a timestamp to epoch milliseconds, so
# use a literal. Deriving one from timestamp() would change on every plan and
# force the key to be replaced each apply.
resource "convex_deploy_key" "temporary" {
  deployment_name = convex_deployment.prod.name
  name            = "incident-response"

  allowed_actions = ["deployment:logs:view"]
  expires_at      = 1798761600000 # 2027-01-01T00:00:00Z
}

# The secret is only ever returned once, at creation. Referencing it here puts
# it in state and in any output that consumes it.
output "ci_deploy_key" {
  value     = convex_deploy_key.ci.key
  sensitive = true
}
