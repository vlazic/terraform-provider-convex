# Deploy keys are imported as "<deployment_name>/<key_name>".
#
# The secret cannot be recovered: Convex returns it only at creation, so the
# `key` attribute of an imported resource is null. Replace the key if you need
# a usable secret.
terraform import convex_deploy_key.ci playful-otter-123/github-actions
