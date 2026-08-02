// Package apierr holds the error handling shared by the two generated Convex
// clients. The management API and the deployment API report failures the same
// way, so the decoding lives here rather than being duplicated per client.
package apierr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// APIError is a non-2xx response from a Convex API.
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

// Response is implemented by every generated *HTTPResponse wrapper, in both
// clients.
type Response interface {
	StatusCode() int
	GetBody() []byte
}

// Check converts a non-2xx response into an *APIError, and returns nil for
// anything in the 2xx range.
func Check(resp Response) error {
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

// IsTransient reports whether an error looks worth retrying: a rate limit or a
// server-side fault, as opposed to a request the server rejected on its
// merits.
func IsTransient(err error) bool {
	var apiErr *APIError

	if !errors.As(err, &apiErr) {
		return false
	}

	return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= http.StatusInternalServerError
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}

	return s[:max] + "..."
}
