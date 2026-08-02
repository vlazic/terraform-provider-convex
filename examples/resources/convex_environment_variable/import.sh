# Environment variables are imported as "<deployment_url>/<name>". The URL
# contains slashes, so the variable name is taken as the final segment.
terraform import convex_environment_variable.stripe_key 'https://playful-otter-123.convex.cloud/STRIPE_KEY'
