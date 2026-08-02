# Default environment variables are imported as
# "<project_id>/<deployment_type>/<name>". Only the first two separators are
# significant, so a name containing "/" survives.
terraform import convex_default_environment_variable.sentry_prod 12345/prod/SENTRY_DSN
