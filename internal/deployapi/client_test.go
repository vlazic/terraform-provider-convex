package deployapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// noBackoff collapses the retry delay so retry tests stay fast.
func noBackoff(t *testing.T) {
	t.Helper()

	previous := baseDelay
	baseDelay = time.Microsecond
	t.Cleanup(func() { baseDelay = previous })
}

func TestBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain deployment URL", input: "https://playful-otter-123.convex.cloud",
			want: "https://playful-otter-123.convex.cloud/api/v1"},
		{
			// The reason deployment_url exists rather than a name: a regional
			// deployment's host is not derivable from its name.
			name: "regional deployment URL", input: "https://calm-cow-456.eu-west-1.convex.cloud",
			want: "https://calm-cow-456.eu-west-1.convex.cloud/api/v1",
		},
		{name: "trailing slash", input: "https://playful-otter-123.convex.cloud/",
			want: "https://playful-otter-123.convex.cloud/api/v1"},
		{name: "already carries the api path", input: "https://playful-otter-123.convex.cloud/api/v1",
			want: "https://playful-otter-123.convex.cloud/api/v1"},
		{name: "surrounding whitespace", input: "  https://playful-otter-123.convex.cloud  ",
			want: "https://playful-otter-123.convex.cloud/api/v1"},

		{name: "empty", input: "", wantErr: true},
		{name: "bare name", input: "playful-otter-123", wantErr: true},
		{name: "host without scheme", input: "playful-otter-123.convex.cloud", wantErr: true},
		{name: "wrong scheme", input: "ftp://playful-otter-123.convex.cloud", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := baseURL(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("baseURL(%q) = %q, want an error", tt.input, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("baseURL(%q) error = %v", tt.input, err)
			}

			if got != tt.want {
				t.Errorf("baseURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// This API wants "Convex <token>", not the "Bearer <token>" the management API
// takes. Getting it wrong yields a 401 that looks like a bad credential.
func TestAuthorizationHeaderUsesConvexPrefix(t *testing.T) {
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"environmentVariables":{}}`))
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok_123")
	if err != nil {
		t.Fatalf("NewAuthenticated() error = %v", err)
	}

	if _, err := client.ListEnvironmentVariablesWithResponse(t.Context()); err != nil {
		t.Fatalf("ListEnvironmentVariables() error = %v", err)
	}

	if want := "Convex tok_123"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestGetEnvironmentVariable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasSuffix(req.URL.Path, "/api/v1/list_environment_variables") {
			t.Errorf("unexpected path %q", req.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"environmentVariables":{"STRIPE_KEY":"sk_test","LOG_LEVEL":"debug"}}`))
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	value, err := GetEnvironmentVariable(t.Context(), client, "STRIPE_KEY")
	if err != nil {
		t.Fatalf("GetEnvironmentVariable() error = %v", err)
	}

	if value != "sk_test" {
		t.Errorf("value = %q, want sk_test", value)
	}

	if _, err := GetEnvironmentVariable(t.Context(), client, "ABSENT"); err == nil {
		t.Error("GetEnvironmentVariable(ABSENT) = nil error, want ErrEnvVarNotFound")
	}
}

// The same omitempty pitfall as the management API: without an explicit null
// the deletion is silently a no-op.
func TestUnsetEnvironmentVariableSendsExplicitNull(t *testing.T) {
	var body map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	if err := UnsetEnvironmentVariable(t.Context(), client, "STRIPE_KEY"); err != nil {
		t.Fatalf("UnsetEnvironmentVariable() error = %v", err)
	}

	changes, _ := body["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %v", len(changes), body)
	}

	change, _ := changes[0].(map[string]any)
	value, present := change["value"]

	if !present {
		t.Fatalf("the \"value\" key is missing from %v; the deletion would be a no-op", change)
	}

	if value != nil {
		t.Errorf("value = %v, want null", value)
	}
}

func TestSetEnvironmentVariableSendsValue(t *testing.T) {
	var body map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	if err := SetEnvironmentVariable(t.Context(), client, "STRIPE_KEY", "sk_live"); err != nil {
		t.Fatalf("SetEnvironmentVariable() error = %v", err)
	}

	changes, _ := body["changes"].([]any)
	change, _ := changes[0].(map[string]any)

	if change["name"] != "STRIPE_KEY" || change["value"] != "sk_live" {
		t.Errorf("change = %v, want STRIPE_KEY=sk_live", change)
	}
}

func TestCheckResponseSurfacesAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"Unauthorized","message":"bad token"}`))
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	_, err = GetEnvironmentVariable(t.Context(), client, "ANY")
	if err == nil {
		t.Fatal("GetEnvironmentVariable() = nil error, want a 401")
	}

	if got, want := err.Error(), "HTTP 401: Unauthorized: bad token"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// Reproduces a real failure: deleting several variables on one deployment in
// parallel makes Convex return 503 OptimisticConcurrencyControlFailure, since
// it is transactional. The write is idempotent, so it must be retried rather
// than surfaced.
func TestUnsetEnvironmentVariableRetriesOCCFailure(t *testing.T) {
	noBackoff(t)

	calls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++

		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"OptimisticConcurrencyControlFailure",` +
				`"message":"Data read or written in this mutation changed while it was being run."}`))

			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	if err := UnsetEnvironmentVariable(t.Context(), client, "FEATURE_FLAGS"); err != nil {
		t.Fatalf("UnsetEnvironmentVariable() error = %v, want it to survive the OCC failures", err)
	}

	if calls != 3 {
		t.Errorf("server saw %d calls, want 3", calls)
	}
}

// A rejection on the merits must not be retried.
func TestSetEnvironmentVariableDoesNotRetryBadRequest(t *testing.T) {
	calls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"InvalidName","message":"bad name"}`))
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	if err := SetEnvironmentVariable(t.Context(), client, "bad name", "x"); err == nil {
		t.Fatal("SetEnvironmentVariable() = nil error, want the 400")
	}

	if calls != 1 {
		t.Errorf("server saw %d calls, want 1 -- a 400 must not be retried", calls)
	}
}
