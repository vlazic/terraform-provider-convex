# Deploy keys are imported as "<deployment_name>/<key_name>".
#
# Convex lists a key created as "github-actions" under a unique name such as
# "github-actions (cbd71ace-cdac-417a-b967-78b318339e2e)". <key_name> may be
# either form. The short form works when exactly one key matches it; if
# several do, import by the full listed name (quoted, it contains a space).
# Either way `name` is recorded as the short name the key was created with, so
# a configuration using it plans no replacement, and the full listed name is
# recorded in `unique_name`.
#
# The secret cannot be recovered: Convex returns it only at creation, so the
# `key` attribute of an imported resource is null. Replace the key if you need
# a usable secret.
terraform import convex_deploy_key.ci playful-otter-123/github-actions
# or, by the full listed name:
terraform import convex_deploy_key.ci 'playful-otter-123/github-actions (cbd71ace-cdac-417a-b967-78b318339e2e)'
