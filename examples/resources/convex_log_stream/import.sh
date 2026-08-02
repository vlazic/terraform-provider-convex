# Log streams are imported as "<deployment_url>/<log_stream_id>". The URL
# contains slashes, so the id is taken as the final segment.
#
# Credentials (api_key, dsn) are never returned by Convex, so they are empty
# after import -- add them to configuration to match what the stream was
# created with.
terraform import convex_log_stream.axiom 'https://playful-otter-123.convex.cloud/abc123'
