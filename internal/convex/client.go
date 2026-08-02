package convex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// DefaultBaseURL is the public Convex Management API endpoint.
const DefaultBaseURL = "https://api.convex.dev/v1"

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

// APIError is a non-2xx response from the Management API.
type APIError struct {
	StatusCode int
	// Code is the machine-readable error code, when the body carries one.
	Code string
	// Message is the human-readable message, when the body carries one.
	Message string
	// Body is the raw response body, always populated.
	Body []byte
}

func (e *APIError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("HTTP %d: %s: %s", e.StatusCode, e.Code, e.Message)
	case e.Message != "":
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	case len(e.Body) > 0:
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, truncate(strings.TrimSpace(string(e.Body)), 512))
	default:
		return fmt.Sprintf("HTTP %d (empty response body)", e.StatusCode)
	}
}

// IsNotFound reports whether the API said the resource does not exist, which
// callers treat as "remove from state" rather than as a failure.
func (e *APIError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

// apiResponse is implemented by every generated *Response wrapper.
type apiResponse interface {
	StatusCode() int
	GetBody() []byte
}

// CheckResponse converts a non-2xx response into an *APIError, and returns nil
// for anything in the 2xx range.
func CheckResponse(resp apiResponse) error {
	status := resp.StatusCode()
	if status >= 200 && status < 300 {
		return nil
	}

	body := resp.GetBody()
	apiErr := &APIError{StatusCode: status, Body: body}

	// Convex reports errors as {"code": "...", "message": "..."}. Anything that
	// does not parse falls back to the raw body in Error().
	var parsed struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		apiErr.Code = parsed.Code
		apiErr.Message = parsed.Message
	}
	return apiErr
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
