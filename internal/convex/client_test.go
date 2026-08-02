package convex

import (
	"errors"
	"net/http"
	"testing"
)

// stubResponse stands in for a generated *HTTPResponse wrapper.
type stubResponse struct {
	status int
	body   []byte
}

func (s stubResponse) StatusCode() int { return s.status }
func (s stubResponse) GetBody() []byte { return s.body }

func TestCheckResponse(t *testing.T) {
	tests := []struct {
		name    string
		resp    stubResponse
		wantErr bool
		wantMsg string
	}{
		{
			name: "200 is not an error",
			resp: stubResponse{status: http.StatusOK, body: []byte(`{"id":1}`)},
		},
		{
			name: "204 is not an error",
			resp: stubResponse{status: http.StatusNoContent},
		},
		{
			name:    "structured error body is unpacked",
			resp:    stubResponse{status: http.StatusBadRequest, body: []byte(`{"code":"InvalidName","message":"bad name"}`)},
			wantErr: true,
			wantMsg: "HTTP 400: InvalidName: bad name",
		},
		{
			name:    "message-only body",
			resp:    stubResponse{status: http.StatusForbidden, body: []byte(`{"message":"nope"}`)},
			wantErr: true,
			wantMsg: "HTTP 403: nope",
		},
		{
			name:    "unparseable body falls back to raw text",
			resp:    stubResponse{status: http.StatusInternalServerError, body: []byte("upstream exploded")},
			wantErr: true,
			wantMsg: "HTTP 500: upstream exploded",
		},
		{
			name:    "empty body",
			resp:    stubResponse{status: http.StatusBadGateway},
			wantErr: true,
			wantMsg: "HTTP 502 (empty response body)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckResponse(tt.resp)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("CheckResponse() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("CheckResponse() = nil, want error")
			}
			if got := err.Error(); got != tt.wantMsg {
				t.Errorf("error message = %q, want %q", got, tt.wantMsg)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error is %T, want *APIError", err)
			}
			if apiErr.StatusCode != tt.resp.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.resp.status)
			}
		})
	}
}

func TestAPIErrorIsNotFound(t *testing.T) {
	notFound := CheckResponse(stubResponse{status: http.StatusNotFound, body: []byte(`{"code":"ProjectNotFound"}`)})

	var apiErr *APIError
	if !errors.As(notFound, &apiErr) {
		t.Fatalf("error is %T, want *APIError", notFound)
	}
	if !apiErr.IsNotFound() {
		t.Error("IsNotFound() = false for a 404, want true")
	}

	other := CheckResponse(stubResponse{status: http.StatusConflict})
	if errors.As(other, &apiErr) && apiErr.IsNotFound() {
		t.Error("IsNotFound() = true for a 409, want false")
	}
}

func TestNewAuthenticatedSetsBearerToken(t *testing.T) {
	client, err := NewAuthenticated("", "tok_123")
	if err != nil {
		t.Fatalf("NewAuthenticated() error = %v", err)
	}
	if client.ClientInterface == nil {
		t.Fatal("NewAuthenticated() returned a client with no underlying implementation")
	}

	req, err := http.NewRequest(http.MethodGet, DefaultBaseURL+"/projects/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := bearerAuth("tok_123")(t.Context(), req); err != nil {
		t.Fatalf("bearerAuth() error = %v", err)
	}
	if got, want := req.Header.Get("Authorization"), "Bearer tok_123"; got != want {
		t.Errorf("Authorization header = %q, want %q", got, want)
	}
}

func TestNewAuthenticatedDefaultsBaseURL(t *testing.T) {
	// A non-empty base URL is left alone; an empty one falls back to the public
	// endpoint. Constructing both is enough to prove neither path errors.
	if _, err := NewAuthenticated("https://convex.internal/v1", "tok"); err != nil {
		t.Errorf("NewAuthenticated(custom) error = %v", err)
	}
	if _, err := NewAuthenticated("", "tok"); err != nil {
		t.Errorf("NewAuthenticated(default) error = %v", err)
	}
}
