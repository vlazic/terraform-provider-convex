package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// Rules the Convex API documents for a deployment reference. Uniqueness within
// the project is the one rule that can only be checked server-side.
var (
	referenceCharset        = regexp.MustCompile(`^[a-z0-9/-]+$`)
	referenceLooksLikeAName = regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9]+$`)
	referenceReserved       = []string{
		"cloud", "default", "deployment", "dev", "existing",
		"local", "name", "new", "preview", "prod",
	}
)

const (
	referenceMinLength = 3
	referenceMaxLength = 100
)

// deploymentReferenceValidator checks a deployment reference against the rules
// the API documents, so that a bad value fails at plan time with a specific
// message rather than as an opaque API error mid-apply.
type deploymentReferenceValidator struct{}

func (v deploymentReferenceValidator) Description(_ context.Context) string {
	return fmt.Sprintf("must be %d-%d characters of lowercase letters, numbers, \"-\" or \"/\"; "+
		"must not look like a deployment name (e.g. \"happy-capybara-123\"), start with \"local-\", "+
		"or be one of %v", referenceMinLength, referenceMaxLength, referenceReserved)
}

func (v deploymentReferenceValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v deploymentReferenceValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	value := req.ConfigValue.ValueString()

	addError := func(detail string) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid deployment reference", detail)
	}

	switch {
	case len(value) < referenceMinLength || len(value) > referenceMaxLength:
		addError(fmt.Sprintf("A reference must be between %d and %d characters, got %d.",
			referenceMinLength, referenceMaxLength, len(value)))
	case !referenceCharset.MatchString(value):
		addError("A reference may only contain lowercase letters, numbers, \"-\" and \"/\".")
	case referenceLooksLikeAName.MatchString(value):
		addError(fmt.Sprintf("%q has the shape of a Convex deployment name "+
			"(word-word-number), which is not allowed as a reference.", value))
	case len(value) > len("local-") && value[:len("local-")] == "local-":
		addError("A reference may not start with \"local-\", which is reserved for local deployments.")
	case slices.Contains(referenceReserved, value):
		addError(fmt.Sprintf("%q is a reserved keyword. Reserved keywords are %v.", value, referenceReserved))
	}
}
