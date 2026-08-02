resource "convex_project" "app" {
  team_id = var.convex_team_id
  name    = "My Application"
}

# Preview deploy keys belong to the project, not to a single deployment: they
# let CI or a hosting integration create preview deployments on demand.
resource "convex_preview_deploy_key" "vercel" {
  project_id = convex_project.app.id
  name       = "vercel"
}

# Feed the secret straight into whatever consumes it rather than printing it.
# This lands in Terraform state either way.
output "preview_deploy_key" {
  value     = convex_preview_deploy_key.vercel.key
  sensitive = true
}
