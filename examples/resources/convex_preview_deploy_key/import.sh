# Preview deploy keys are imported as "<project_id>/<key_name>".
#
# As with regular deploy keys, <key_name> may be the name the key was created
# with or its full listed name, such as
# "vercel (cbd71ace-cdac-417a-b967-78b318339e2e)"; use the full name when
# several keys match the short one.
#
# The secret cannot be recovered, so the `key` attribute of an imported
# resource is null.
terraform import convex_preview_deploy_key.vercel 12345/vercel
terraform import convex_preview_deploy_key.vercel '12345/vercel (cbd71ace-cdac-417a-b967-78b318339e2e)'
