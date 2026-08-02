package convex

import (
	"context"
	"net/http"

	"github.com/srs/terraform-provider-convex/internal/apierr"
)

// DefaultBaseURL is the public Convex Management API endpoint.
const DefaultBaseURL = "https://api.convex.dev/v1"

// APIError is a non-2xx response from the Management API. Aliased from the
// shared package so that callers can keep using convex.APIError.
type APIError = apierr.APIError

// CheckResponse converts a non-2xx response into an *APIError, and returns nil
// for anything in the 2xx range.
func CheckResponse(resp apierr.Response) error {
	return apierr.Check(resp)
}

// IsTransient reports whether an error is worth retrying.
func IsTransient(err error) bool {
	return apierr.IsTransient(err)
}

// NewAuthenticated returns a response-parsing client that attaches the given
// bearer token to every request. The token may be a team access token, a
// personal access token, or an OAuth token -- the API accepts all three on the
// same header.
func NewAuthenticated(baseURL, token string, opts ...ClientOption) (*ClientWithResponses, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	// Put the auth editor first so callers can still override the header.
	opts = append([]ClientOption{WithRequestEditorFn(bearerAuth(token))}, opts...)
	return NewClientWithResponses(baseURL, opts...)
}

func bearerAuth(token string) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}
