variable "convex_team_id" {
  type        = number
  description = "Numeric team ID, shown in the Convex dashboard under team settings."
}

resource "convex_project" "minimal" {
  team_id = var.convex_team_id
  name    = "Minimal Project"
}

# Convex derives the slug from the name; set it explicitly to pin the
# dashboard URL.
resource "convex_project" "app" {
  team_id = var.convex_team_id
  name    = "My Application"
  slug    = "my-application"
}

# Creating a project does not create a deployment -- declare one separately.
resource "convex_deployment" "app_prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  is_default      = true
}

output "app_prod_url" {
  value = convex_deployment.app_prod.deployment_url
}
