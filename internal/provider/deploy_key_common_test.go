package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

func TestFindDeployKeyByName(t *testing.T) {
	keys := []convex.PlatformDeployKeyResponse{
		{Id: 1, Name: "ci"},
		{Id: 2, Name: "staging"},
	}

	found, err := findDeployKeyByName(keys, "staging")

	if err != nil {
		t.Fatalf("findDeployKeyByName() error = %v", err)
	}

	if found.Id != 2 {
		t.Errorf("Id = %d, want 2", found.Id)
	}

	_, err = findDeployKeyByName(keys, "absent")

	if !errors.Is(err, errDeployKeyNotFound) {
		t.Errorf("error = %v, want it to wrap errDeployKeyNotFound", err)
	}

	_, err = findDeployKeyByName(nil, "ci")

	if !errors.Is(err, errDeployKeyNotFound) {
		t.Errorf("error on empty list = %v, want it to wrap errDeployKeyNotFound", err)
	}
}

// findDeployKeyByName returns a pointer into the slice, so it must point at the
// matching element rather than at a loop variable reused across iterations.
func TestFindDeployKeyByNameReturnsDistinctEntries(t *testing.T) {
	keys := []convex.PlatformDeployKeyResponse{
		{Id: 1, Name: "ci"},
		{Id: 2, Name: "staging"},
	}

	first, err := findDeployKeyByName(keys, "ci")

	if err != nil {
		t.Fatal(err)
	}

	second, err := findDeployKeyByName(keys, "staging")

	if err != nil {
		t.Fatal(err)
	}

	if first.Id == second.Id {
		t.Fatalf("both lookups returned Id %d, want distinct entries", first.Id)
	}

	if first.Name != "ci" || second.Name != "staging" {
		t.Errorf("got names %q and %q, want \"ci\" and \"staging\"", first.Name, second.Name)
	}
}

func TestReadDeployKeyMetadata(t *testing.T) {
	lastUsed := int64(1_700_000_500_000)
	creator := int64(7)

	metadata, diags := readDeployKeyMetadata(t.Context(), &convex.PlatformDeployKeyResponse{
		Id:           99,
		Name:         "ci",
		CreationTime: 1_700_000_000_000,
		LastUsedTime: &lastUsed,
		Creator:      &creator,
		AllowedActions: []convex.PlatformDeployKeyResponseAllowedActions{
			"deployment:deploy",
			"deployment:env:view",
		},
		// ExpiresAt is nil, i.e. the key never expires.
	})

	if diags.HasError() {
		t.Fatalf("readDeployKeyMetadata() diagnostics: %+v", diags)
	}

	if metadata.ID != types.Int64Value(99) {
		t.Errorf("ID = %v, want 99", metadata.ID)
	}

	if metadata.LastUsedTime != types.Int64Value(lastUsed) {
		t.Errorf("LastUsedTime = %v, want %d", metadata.LastUsedTime, lastUsed)
	}

	if metadata.ExpiresAt != types.Int64Null() {
		t.Errorf("ExpiresAt = %v, want null", metadata.ExpiresAt)
	}

	if metadata.Creator != types.Int64Value(creator) {
		t.Errorf("Creator = %v, want %d", metadata.Creator, creator)
	}

	var actions []string

	if diags := metadata.AllowedActions.ElementsAs(t.Context(), &actions, false); diags.HasError() {
		t.Fatalf("ElementsAs() diagnostics: %+v", diags)
	}

	if len(actions) != 2 {
		t.Fatalf("got %d actions, want 2", len(actions))
	}
}

func TestToCreateActions(t *testing.T) {
	ctx := t.Context()

	// An unconfigured set must not be sent at all, so that Convex applies its
	// own default rather than receiving an empty allow-list.
	for _, unset := range []types.Set{types.SetNull(types.StringType), types.SetUnknown(types.StringType)} {
		got, diags := toCreateActions(ctx, unset)

		if diags.HasError() {
			t.Fatalf("toCreateActions() diagnostics: %+v", diags)
		}

		if got != nil {
			t.Errorf("toCreateActions(%v) = %v, want nil", unset, got)
		}
	}

	set, diags := types.SetValueFrom(ctx, types.StringType, []string{"deployment:deploy"})

	if diags.HasError() {
		t.Fatalf("SetValueFrom() diagnostics: %+v", diags)
	}

	got, diags := toCreateActions(ctx, set)

	if diags.HasError() {
		t.Fatalf("toCreateActions() diagnostics: %+v", diags)
	}

	if got == nil || len(*got) != 1 || (*got)[0] != "deployment:deploy" {
		t.Errorf("toCreateActions() = %v, want [deployment:deploy]", got)
	}
}

// The action list is transcribed from the OpenAPI enum, so guard against it
// drifting into duplicates or empty entries.
func TestDeployKeyActionsWellFormed(t *testing.T) {
	seen := map[string]bool{}

	for _, action := range deployKeyActions {
		if action == "" {
			t.Error("deployKeyActions contains an empty entry")
		}

		if seen[action] {
			t.Errorf("deployKeyActions contains %q more than once", action)
		}

		seen[action] = true
	}

	if len(deployKeyActions) != 25 {
		t.Errorf("got %d actions, want the 25 the spec defines", len(deployKeyActions))
	}
}

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
	list := r.lister("playful-otter-123")

	before, err := r.listDeployKeys(ctx, "playful-otter-123")

	if err != nil || len(before) != 1 {
		t.Fatalf("listDeployKeys() = %v, %v; want one key", before, err)
	}

	created, err := listAndSelectCreatedDeployKey(ctx, list, "github-actions", before)

	if err != nil {
		t.Fatalf("listAndSelectCreatedDeployKey() error = %v", err)
	}

	if created.Id != 42 || created.Name != "github-actions (cbd71ace-cdac-417a-b967-78b318339e2e)" {
		t.Errorf("listAndSelectCreatedDeployKey() = %d %q, want 42 and the suffixed name", created.Id, created.Name)
	}

	// By id, the refresh after the create finds the same key.
	found, err := listAndResolveDeployKey(ctx, list, types.Int64Value(42), "github-actions")

	if err != nil || found.Id != 42 {
		t.Errorf("listAndResolveDeployKey(by id) = %v, %v; want Id 42", found, err)
	}

	// Without an id, two keys now answer to the name.
	_, err = listAndResolveDeployKey(ctx, list, types.Int64Null(), "github-actions")

	if !errors.Is(err, errDeployKeyAmbiguous) {
		t.Errorf("listAndResolveDeployKey(by name) error = %v, want errDeployKeyAmbiguous", err)
	}
}

func TestDeployKeyDeleteTarget(t *testing.T) {
	ctx := t.Context()
	keys := []convex.PlatformDeployKeyResponse{{Id: 4, Name: "ci (a)"}}
	var listed int
	list := func(context.Context) ([]convex.PlatformDeployKeyResponse, error) {
		listed++
		return keys, nil
	}

	// A secret in state is used as is, with no lookup.
	target, gone, err := deployKeyDeleteTarget(ctx, list, types.StringValue("prod:abc"), types.Int64Null(), "ci")

	if err != nil || gone || target != "prod:abc" || listed != 0 {
		t.Errorf("with secret = %q, %v, %v after %d lists; want the secret and no list", target, gone, err, listed)
	}

	// Without one, the full listed name, never the configured one.
	target, gone, err = deployKeyDeleteTarget(ctx, list, types.StringNull(), types.Int64Value(4), "ci")

	if err != nil || gone || target != "ci (a)" {
		t.Errorf("without secret = %q, %v, %v; want \"ci (a)\"", target, gone, err)
	}

	// A key that no longer exists is already deleted.
	_, gone, err = deployKeyDeleteTarget(ctx, list, types.StringNull(), types.Int64Value(99), "ci")

	if err != nil || !gone {
		t.Errorf("missing key = gone %v, %v; want gone and no error", gone, err)
	}

	// A failed listing is an error, not "already gone".
	failing := func(context.Context) ([]convex.PlatformDeployKeyResponse, error) {
		return nil, errors.New("boom")
	}

	_, gone, err = deployKeyDeleteTarget(ctx, failing, types.StringNull(), types.Int64Value(4), "ci")

	if err == nil || gone {
		t.Errorf("failed list = gone %v, %v; want an error", gone, err)
	}
}

func TestAdoptedDeployKeyName(t *testing.T) {
	full := "ci (cbd71ace-cdac-417a-b967-78b318339e2e)"
	key := &convex.PlatformDeployKeyResponse{Id: 1, Name: full}

	// Imported by full name: recorded as the short one.
	if got := adoptedDeployKeyName(types.Int64Null(), full, key); got != types.StringValue("ci") {
		t.Errorf("imported full name = %v, want \"ci\"", got)
	}

	// Adopted by short name, or already tracked by id: left alone.
	if got := adoptedDeployKeyName(types.Int64Null(), "ci", key); got != types.StringValue("ci") {
		t.Errorf("short name = %v, want \"ci\"", got)
	}

	if got := adoptedDeployKeyName(types.Int64Value(1), full, key); got != types.StringValue(full) {
		t.Errorf("known id = %v, want the name unchanged", got)
	}
}
