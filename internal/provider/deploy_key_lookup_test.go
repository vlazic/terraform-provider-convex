package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

func TestDeployKeyNameMatches(t *testing.T) {
	tests := []struct {
		listed, configured string
		want               bool
	}{
		// A key made before Convex started suffixing names keeps its own.
		{"ci", "ci", true},
		{"github-actions (cbd71ace-cdac-417a-b967-78b318339e2e)", "github-actions", true},
		// The suffix content is not checked for UUID shape, in case Convex
		// changes what it appends.
		{"ci (x)", "ci", true},
		// A different key whose name merely starts with the configured one.
		{"ci-other (x)", "ci", false},
		{"ci-other", "ci", false},
		{"cix (x)", "ci", false},
		// A key created as "ci (staging)", not as "ci".
		{"ci (staging) (x)", "ci", false},
		// But it does belong to a key configured as "ci (staging)".
		{"ci (staging) (x)", "ci (staging)", true},
		// Near misses on the suffix itself.
		{"ci ()", "ci", false},
		{"ci (x", "ci", false},
		{"ci (x) ", "ci", false},
		{"ci(x)", "ci", false},
		{"ci  (x)", "ci", false},
		{"CI (x)", "ci", false},
		// The relation is not symmetric: a short listed name does not match a
		// configured full name.
		{"ci", "ci (x)", false},
	}

	for _, tt := range tests {
		if got := deployKeyNameMatches(tt.listed, tt.configured); got != tt.want {
			t.Errorf("deployKeyNameMatches(%q, %q) = %v, want %v", tt.listed, tt.configured, got, tt.want)
		}
	}
}

func TestDeployKeyBaseName(t *testing.T) {
	tests := map[string]string{
		"github-actions (cbd71ace-cdac-417a-b967-78b318339e2e)": "github-actions",
		"ci (staging) (cbd71ace-cdac-417a-b967-78b318339e2e)":   "ci (staging)",
		// Only a UUID-shaped suffix is Convex's; anything else is the user's.
		"deploy (prod)": "deploy (prod)",
		"ci":            "ci",
		"ci (cbd71ace-cdac-417a-b967-78b318339e2e) trailing": "ci (cbd71ace-cdac-417a-b967-78b318339e2e) trailing",
	}

	for in, want := range tests {
		if got := deployKeyBaseName(in); got != want {
			t.Errorf("deployKeyBaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindDeployKeyByNameSuffixed(t *testing.T) {
	keys := []convex.PlatformDeployKeyResponse{
		{Id: 1, Name: "ci-other (a)"},
		{Id: 2, Name: "ci (b)"},
	}

	found, err := findDeployKeyByName(keys, "ci")

	if err != nil {
		t.Fatalf("findDeployKeyByName() error = %v", err)
	}

	if found.Id != 2 {
		t.Errorf("Id = %d, want 2", found.Id)
	}
}

func TestFindDeployKeyByNameAmbiguous(t *testing.T) {
	keys := []convex.PlatformDeployKeyResponse{
		{Id: 11, Name: "ci (a)"},
		{Id: 12, Name: "staging (b)"},
		{Id: 13, Name: "ci (c)"},
	}

	_, err := findDeployKeyByName(keys, "ci")

	if !errors.Is(err, errDeployKeyAmbiguous) {
		t.Fatalf("error = %v, want it to wrap errDeployKeyAmbiguous", err)
	}

	// The message is what tells the user which keys to import or delete.
	for _, want := range []string{"id 11", "id 13", `"ci (a)"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	if strings.Contains(err.Error(), "id 12") {
		t.Errorf("error %q mentions id 12, which does not match", err)
	}
}

func TestResolveDeployKey(t *testing.T) {
	keys := []convex.PlatformDeployKeyResponse{
		{Id: 1, Name: "ci (a)"},
		{Id: 2, Name: "ci (b)"},
		{Id: 3, Name: "staging (c)"},
	}

	// A known id wins, even though the name alone is ambiguous here.
	found, err := resolveDeployKey(keys, types.Int64Value(2), "ci")

	if err != nil || found.Id != 2 {
		t.Fatalf("resolve by id = %v, %v; want Id 2", found, err)
	}

	// A known id that is gone means the key was deleted, even if another key
	// still matches the name: that other key is not the one state managed.
	_, err = resolveDeployKey(keys, types.Int64Value(99), "staging")

	if !errors.Is(err, errDeployKeyNotFound) {
		t.Errorf("resolve by missing id: error = %v, want errDeployKeyNotFound", err)
	}

	// No id (import, or v0.2.0 state): fall back to the name.
	for _, id := range []types.Int64{types.Int64Null(), types.Int64Unknown()} {
		found, err = resolveDeployKey(keys, id, "staging")

		if err != nil || found.Id != 3 {
			t.Errorf("resolve by name with id %v = %v, %v; want Id 3", id, found, err)
		}
	}

	_, err = resolveDeployKey(keys, types.Int64Null(), "ci")

	if !errors.Is(err, errDeployKeyAmbiguous) {
		t.Errorf("resolve ambiguous name: error = %v, want errDeployKeyAmbiguous", err)
	}

	_, err = resolveDeployKey(keys, types.Int64Null(), "absent")

	if !errors.Is(err, errDeployKeyNotFound) {
		t.Errorf("resolve absent name: error = %v, want errDeployKeyNotFound", err)
	}

	// An imported full listed name matches exactly that key.
	found, err = resolveDeployKey(keys, types.Int64Null(), "ci (b)")

	if err != nil || found.Id != 2 {
		t.Errorf("resolve full name = %v, %v; want Id 2", found, err)
	}
}

func TestSelectCreatedDeployKey(t *testing.T) {
	before := []convex.PlatformDeployKeyResponse{
		// Leftovers from earlier applies that answer to the same name.
		{Id: 1, Name: "ci (a)"},
		{Id: 2, Name: "ci (b)"},
		{Id: 3, Name: "staging"},
	}

	t.Run("one new matching key", func(t *testing.T) {
		after := append(append([]convex.PlatformDeployKeyResponse{}, before...),
			convex.PlatformDeployKeyResponse{Id: 7, Name: "ci (new)"})

		found, err := selectCreatedDeployKey(before, after, "ci")

		if err != nil || found.Id != 7 {
			t.Fatalf("selectCreatedDeployKey() = %v, %v; want Id 7", found, err)
		}
	})

	t.Run("new keys with other names are ignored", func(t *testing.T) {
		after := append(append([]convex.PlatformDeployKeyResponse{}, before...),
			convex.PlatformDeployKeyResponse{Id: 7, Name: "ci-other (x)"},
			convex.PlatformDeployKeyResponse{Id: 8, Name: "ci (new)"})

		found, err := selectCreatedDeployKey(before, after, "ci")

		if err != nil || found.Id != 8 {
			t.Fatalf("selectCreatedDeployKey() = %v, %v; want Id 8", found, err)
		}
	})

	t.Run("no new key", func(t *testing.T) {
		_, err := selectCreatedDeployKey(before, before, "ci")

		if !errors.Is(err, errDeployKeyNotFound) {
			t.Fatalf("error = %v, want errDeployKeyNotFound", err)
		}
	})

	t.Run("two new matching keys", func(t *testing.T) {
		after := append(append([]convex.PlatformDeployKeyResponse{}, before...),
			convex.PlatformDeployKeyResponse{Id: 7, Name: "ci (x)"},
			convex.PlatformDeployKeyResponse{Id: 8, Name: "ci (y)"})

		_, err := selectCreatedDeployKey(before, after, "ci")

		if !errors.Is(err, errDeployKeyAmbiguous) {
			t.Fatalf("error = %v, want errDeployKeyAmbiguous", err)
		}
	})

	t.Run("empty before list", func(t *testing.T) {
		after := []convex.PlatformDeployKeyResponse{{Id: 5, Name: "ci (x)"}}

		found, err := selectCreatedDeployKey(nil, after, "ci")

		if err != nil || found.Id != 5 {
			t.Fatalf("selectCreatedDeployKey() = %v, %v; want Id 5", found, err)
		}
	})
}

func TestDeployKeySecret(t *testing.T) {
	for _, absent := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("")} {
		if _, ok := deployKeySecret(absent); ok {
			t.Errorf("deployKeySecret(%v) reported a secret", absent)
		}
	}

	if got, ok := deployKeySecret(types.StringValue("prod:abc")); !ok || got != "prod:abc" {
		t.Errorf("deployKeySecret() = %q, %v; want \"prod:abc\", true", got, ok)
	}
}

// The list body below is the shape the live API returned on 2026-10-14, so
// this exercises decoding as well as selection, against a fake server.
func TestDeployKeyResourceListsAndResolves(t *testing.T) {
	var calls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/deployments/playful-otter-123/list_deploy_keys" {
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}

		calls++
		w.Header().Set("Content-Type", "application/json")

		body := `{"allowedActions":["deployment:deploy"],"allowedOperations":null,"creationTime":1760400000000,` +
			`"creator":7,"expiresAt":null,"id":41,"lastUsedTime":null,` +
			`"name":"github-actions (0b0e7c1e-1111-4222-8333-944455556666)"}`

		// The second listing is the one taken after the create.
		if calls > 1 {
			body += `,{"allowedActions":["deployment:deploy"],"allowedOperations":null,"creationTime":1760400100000,` +
				`"creator":7,"expiresAt":null,"id":42,"lastUsedTime":null,` +
				`"name":"github-actions (cbd71ace-cdac-417a-b967-78b318339e2e)"}`
		}

		_, _ = w.Write([]byte("[" + body + "]"))
	}))
	defer server.Close()

	client, err := convex.NewAuthenticated(server.URL, "fake-token")

	if err != nil {
		t.Fatal(err)
	}

	r := &deployKeyResource{client: client}
	ctx := t.Context()

	before, err := r.listDeployKeys(ctx, "playful-otter-123")

	if err != nil || len(before) != 1 {
		t.Fatalf("listDeployKeys() = %v, %v; want one key", before, err)
	}

	created, err := r.findCreatedDeployKey(ctx, "playful-otter-123", "github-actions", before)

	if err != nil {
		t.Fatalf("findCreatedDeployKey() error = %v", err)
	}

	if created.Id != 42 || created.Name != "github-actions (cbd71ace-cdac-417a-b967-78b318339e2e)" {
		t.Errorf("findCreatedDeployKey() = %d %q, want 42 and the suffixed name", created.Id, created.Name)
	}

	// By id, the refresh after the create finds the same key.
	found, err := r.resolveDeployKey(ctx, "playful-otter-123", types.Int64Value(42), "github-actions")

	if err != nil || found.Id != 42 {
		t.Errorf("resolveDeployKey(by id) = %v, %v; want Id 42", found, err)
	}

	// Without an id, two keys now answer to the name.
	_, err = r.resolveDeployKey(ctx, "playful-otter-123", types.Int64Null(), "github-actions")

	if !errors.Is(err, errDeployKeyAmbiguous) {
		t.Errorf("resolveDeployKey(by name) error = %v, want errDeployKeyAmbiguous", err)
	}
}
