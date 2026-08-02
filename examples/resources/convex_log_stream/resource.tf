resource "convex_project" "app" {
  team_id = var.convex_team_id
  name    = "My Application"
}

resource "convex_deployment" "prod" {
  project_id      = convex_project.app.id
  deployment_type = "prod"
  is_default      = true
}

# Log streams require a Convex Pro plan.
#
# Exactly one sink block per resource. Declare several resources to send the
# same deployment's events to more than one destination.

resource "convex_log_stream" "axiom" {
  deployment_url = convex_deployment.prod.deployment_url

  axiom {
    api_key      = var.axiom_api_key
    dataset_name = "convex"

    # Extra fields attached to every event.
    attributes = {
      env     = "production"
      service = "api"
    }

    # Omit topics to receive everything, including topics Convex adds later.
    topics = ["console", "function_execution", "exception"]
  }
}

resource "convex_log_stream" "datadog" {
  deployment_url = convex_deployment.prod.deployment_url

  datadog {
    site_location = "EU"
    api_key       = var.datadog_api_key
    service       = "convex-api"
    tags          = ["team:core", "env:production"]
  }
}

# Convex generates the HMAC secret and returns it once, at creation. Use it to
# verify that incoming requests really came from Convex.
resource "convex_log_stream" "webhook" {
  deployment_url = convex_deployment.prod.deployment_url

  webhook {
    url    = "https://example.com/convex-logs"
    format = "jsonl"
  }
}

resource "convex_log_stream" "sentry" {
  deployment_url = convex_deployment.prod.deployment_url

  sentry {
    dsn = var.sentry_dsn

    tags = {
      env = "production"
    }
  }
}

output "webhook_hmac_secret" {
  value     = convex_log_stream.webhook.webhook.hmac_secret
  sensitive = true
}

# Asynchronous: usually "pending" right after the first apply, then "active".
output "axiom_status" {
  value = convex_log_stream.axiom.status
}

variable "axiom_api_key" {
  type      = string
  sensitive = true
}

variable "datadog_api_key" {
  type      = string
  sensitive = true
}

variable "sentry_dsn" {
  type      = string
  sensitive = true
}
