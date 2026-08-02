resource "convex_project" "app" {
  team_id = var.convex_team_id
  name    = "My Application"
}

resource "convex_deployment" "prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  is_default      = true
}

# Fronts the deployment's .convex.cloud API URL.
resource "convex_custom_domain" "api" {
  deployment_name     = convex_deployment.prod.name
  domain              = "api.example.com"
  request_destination = "convexCloud"
}

# Fronts the deployment's .convex.site HTTP actions URL. A deployment can have
# one domain per destination, so both can coexist.
resource "convex_custom_domain" "http_actions" {
  deployment_name     = convex_deployment.prod.name
  domain              = "hooks.example.com"
  request_destination = "convexSite"
}

# Convex verifies asynchronously once DNS resolves, so this is false right
# after the first apply and flips to true on a later refresh.
output "api_domain_verified" {
  value = convex_custom_domain.api.verified
}
