# By numeric ID.
data "convex_project" "by_id" {
  id = 12345
}

# By slug within a team. Slugs are only unique per team, so one of team_id or
# team_slug is required alongside.
data "convex_project" "by_slug" {
  team_slug = "my-team"
  slug      = "my-application"
}

data "convex_project" "by_team_id" {
  team_id = 1234
  slug    = "my-application"
}

# Attach a new deployment to a project that Terraform does not manage.
resource "convex_deployment" "preview" {
  project_id      = data.convex_project.by_slug.id
  deployment_type = "preview"
}

output "project_name" {
  value = data.convex_project.by_slug.name
}

# Null when the project has no default production deployment yet.
output "project_prod_deployment" {
  value = data.convex_project.by_slug.prod_deployment_name
}
