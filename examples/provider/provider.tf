terraform {
  required_providers {
    convex = {
      source  = "srs/convex"
      version = "0.1.0"
    }
  }
}

# The token may also be supplied with the CONVEX_ACCESS_TOKEN environment
# variable, and the endpoint with CONVEX_ENDPOINT.
provider "convex" {
  token = var.convex_token
}

variable "convex_token" {
  type        = string
  sensitive   = true
  description = "Convex team access token, created in the dashboard under team settings."
}
