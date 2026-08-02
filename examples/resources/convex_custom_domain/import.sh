# Custom domains are imported as
# "<deployment_name>/<request_destination>/<domain>". The destination is part
# of the identity, since the same domain can be registered for both.
terraform import convex_custom_domain.api playful-otter-123/convexCloud/api.example.com
