package deployapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/srs/terraform-provider-convex/internal/apierr"
)

// apiPath is appended to a deployment's URL to reach its management endpoints.
const apiPath = "/api/v1"

// Retry settings for idempotent mutations on this API. Convex is transactional,
// so concurrent writes to one deployment surface as a 503
// OptimisticConcurrencyControlFailure that succeeds on a retry.
const maxAttempts = 5

// A var rather than a const so tests can collapse the backoff.
var baseDelay = 400 * time.Millisecond

// APIError is a non-2xx response from the Deployment Platform API.
type APIError = apierr.APIError

// CheckResponse converts a non-2xx response into an *APIError, and returns nil
// for anything in the 2xx range.
func CheckResponse(resp apierr.Response) error {
	return apierr.Check(resp)
}

// NewAuthenticated returns a response-parsing client bound to one deployment.
//
// Unlike the management API there is no single host: every deployment is
// addressed at its own URL, so a client is per-deployment rather than
// per-provider. Pass the deployment's `deployment_url`; regional deployments
// look like https://calm-cow-456.eu-west-1.convex.cloud, so the URL cannot be
// reconstructed from the deployment name.
//
// The token may be a team access token, a deploy key, or an OAuth token. Note
// the `Convex ` prefix, which is what this API expects instead of `Bearer `.
func NewAuthenticated(deploymentURL, token string, opts ...ClientOption) (*ClientWithResponses, error) {
	base, err := baseURL(deploymentURL)
	if err != nil {
		return nil, err
	}

	// Put the auth editor first so callers can still override the header.
	opts = append([]ClientOption{WithRequestEditorFn(convexAuth(token))}, opts...)
	return NewClientWithResponses(base, opts...)
}

// baseURL turns a deployment URL into the API root, rejecting anything that is
// not an absolute http(s) URL rather than letting it fail later as a confusing
// request error.
func baseURL(deploymentURL string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(deploymentURL), "/")

	if trimmed == "" {
		return "", fmt.Errorf("deployment URL is empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parsing deployment URL %q: %w", deploymentURL, err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("deployment URL %q must be an absolute http or https URL", deploymentURL)
	}

	if parsed.Host == "" {
		return "", fmt.Errorf("deployment URL %q has no host", deploymentURL)
	}

	// Tolerate a URL that already carries the API path.
	if strings.HasSuffix(parsed.Path, apiPath) {
		return trimmed, nil
	}

	return trimmed + apiPath, nil
}

func convexAuth(token string) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Convex "+token)
		return nil
	}
}
