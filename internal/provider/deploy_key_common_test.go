package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// UUID-shaped suffixes, as Convex appends them.
const (
	uuidA = "cbd71ace-cdac-417a-b967-78b318339e2e"
	uuidB = "0b0e7c1e-1111-4222-8333-944455556666"
	uuidC = "7f3a2b10-aaaa-4bbb-8ccc-0123456789ab"
)

func TestDeployKeyNameMatches(t *testing.T) {
	tests := []struct {
		listed, configured string
		want               bool
	}{
		// A key made before Convex started suffixing names keeps its own.
		{"ci", "ci", true},
		{"github-actions (" + uuidA + ")", "github-actions", true},
		// Only a UUID-shaped suffix is Convex's: the same rule
		// deployKeyBaseName strips by.
		{"ci (old)", "ci", false},
		{"ci (x)", "ci", false},
		// A different key whose name merely starts with the configured one.
		{"ci-other (" + uuidA + ")", "ci", false},
		{"ci-other", "ci", false},
		{"cix (" + uuidA + ")", "ci", false},
		// A key created as "ci (staging)", not as "ci".
		{"ci (staging) (" + uuidA + ")", "ci", false},
		{"ci (staging) (" + uuidA + ")", "ci (staging)", true},
		// Near misses on the suffix itself.
		{"ci ()", "ci", false},
		{"ci (" + uuidA, "ci", false},
		{"ci (" + uuidA + ") ", "ci", false},
		{"ci(" + uuidA + ")", "ci", false},
		{"ci  (" + uuidA + ")", "ci", false},
		{"ci (" + uuidA[:35] + ")", "ci", false},
		{"CI (" + uuidA + ")", "ci", false},
		// The relation is not symmetric: a short listed name does not match a
		// configured full name.
		{"ci", "ci (" + uuidA + ")", false},
	}

	for _, tt := range tests {
		if got := deployKeyNameMatches(tt.listed, tt.configured); got != tt.want {
			t.Errorf("deployKeyNameMatches(%q, %q) = %v, want %v", tt.listed, tt.configured, got, tt.want)
		}
	}
}

func TestDeployKeyBaseName(t *testing.T) {
	tests := map[string]string{
		"github-actions (" + uuidA + ")": "github-actions",
		"ci (staging) (" + uuidA + ")":   "ci (staging)",
		// Only a UUID-shaped suffix is Convex's; anything else is the user's.
		"deploy (prod)":                      "deploy (prod)",
		"ci":                                 "ci",
		"ci (" + uuidA + ") trailing":        "ci (" + uuidA + ") trailing",
		"(" + uuidA + ")":                    "(" + uuidA + ")",
		"ci (" + uuidA + ") (" + uuidB + ")": "ci (" + uuidA + ")",
	}

	for in, want := range tests {
		if got := deployKeyBaseName(in); got != want {
			t.Errorf("deployKeyBaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindDeployKeyByNameSuffixed(t *testing.T) {
	keys := []convex.PlatformDeployKeyResponse{
		{Id: 1, Name: "ci-other (" + uuidA + ")"},
		{Id: 2, Name: "ci (" + uuidB + ")"},
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
		{Id: 11, Name: "ci (" + uuidA + ")"},
		{Id: 12, Name: "staging (" + uuidB + ")"},
		{Id: 13, Name: "ci (" + uuidC + ")"},
	}

	_, err := findDeployKeyByName(keys, "ci")

	if !errors.Is(err, errDeployKeyAmbiguous) {
		t.Fatalf("error = %v, want it to wrap errDeployKeyAmbiguous", err)
	}

	// The message is what tells the user which keys to import or delete.
	for _, want := range []string{"id 11", "id 13", uuidA} {
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
		{Id: 1, Name: "ci (" + uuidA + ")"},
		{Id: 2, Name: "ci (" + uuidB + ")"},
		{Id: 3, Name: "staging (" + uuidC + ")"},
		// A legacy key with no suffix, beside suffixed siblings.
		{Id: 4, Name: "ci"},
	}

	byName := func(name string) deployKeyRef {
		return deployKeyRef{ID: types.Int64Null(), UniqueName: types.StringNull(), Name: name}
	}

	// A known id wins, even though the name alone is ambiguous here.
	ref := byName("ci")
	ref.ID = types.Int64Value(2)
	found, err := resolveDeployKey(keys, ref)

	if err != nil || found.Id != 2 {
		t.Fatalf("resolve by id = %v, %v; want Id 2", found, err)
	}

	// A known id that is gone means the key was deleted, even if another key
	// still matches the name: that other key is not the one state managed.
	ref = byName("staging")
	ref.ID = types.Int64Value(99)

	if _, err = resolveDeployKey(keys, ref); !errors.Is(err, errDeployKeyNotFound) {
		t.Errorf("resolve by missing id: error = %v, want errDeployKeyNotFound", err)
	}

	// No id (import, or v0.2.0 state): fall back to the name.
	for _, id := range []types.Int64{types.Int64Null(), types.Int64Unknown()} {
		ref = byName("staging")
		ref.ID = id
		found, err = resolveDeployKey(keys, ref)

		if err != nil || found.Id != 3 {
			t.Errorf("resolve by name with id %v = %v, %v; want Id 3", id, found, err)
		}
	}

	if _, err = resolveDeployKey(keys, byName("ci")); !errors.Is(err, errDeployKeyAmbiguous) {
		t.Errorf("resolve ambiguous name: error = %v, want errDeployKeyAmbiguous", err)
	}

	if _, err = resolveDeployKey(keys, byName("absent")); !errors.Is(err, errDeployKeyNotFound) {
		t.Errorf("resolve absent name: error = %v, want errDeployKeyNotFound", err)
	}

	// An exact unique_name picks one key out of an ambiguous name, including
	// the legacy unsuffixed one.
	for want, unique := range map[int64]string{2: "ci (" + uuidB + ")", 4: "ci"} {
		ref = byName("ci")
		ref.UniqueName = types.StringValue(unique)
		found, err = resolveDeployKey(keys, ref)

		if err != nil || found.Id != want {
			t.Errorf("resolve unique_name %q = %v, %v; want Id %d", unique, found, err, want)
		}
	}

	// A unique_name that is not listed falls back to the name match.
	ref = byName("staging")
	ref.UniqueName = types.StringValue("staging")
	found, err = resolveDeployKey(keys, ref)

	if err != nil || found.Id != 3 {
		t.Errorf("resolve stale unique_name = %v, %v; want Id 3", found, err)
	}
}

func TestSelectCreatedDeployKey(t *testing.T) {
	before := []convex.PlatformDeployKeyResponse{
		// Leftovers from earlier applies that answer to the same name.
		{Id: 1, Name: "ci (" + uuidA + ")"},
		{Id: 2, Name: "ci (" + uuidB + ")"},
		{Id: 3, Name: "staging"},
	}
	newKey := func(id int64, name string) []convex.PlatformDeployKeyResponse {
		return append(append([]convex.PlatformDeployKeyResponse{}, before...),
			convex.PlatformDeployKeyResponse{Id: id, Name: name})
	}

	t.Run("one new matching key", func(t *testing.T) {
		found, err := selectCreatedDeployKey(before, newKey(7, "ci ("+uuidC+")"), "ci")

		if err != nil || found.Id != 7 {
			t.Fatalf("selectCreatedDeployKey() = %v, %v; want Id 7", found, err)
		}
	})

	t.Run("new keys with other names are ignored", func(t *testing.T) {
		after := append(newKey(7, "ci-other ("+uuidA+")"),
			convex.PlatformDeployKeyResponse{Id: 8, Name: "ci (" + uuidC + ")"})

		found, err := selectCreatedDeployKey(before, after, "ci")

		if err != nil || found.Id != 8 {
			t.Fatalf("selectCreatedDeployKey() = %v, %v; want Id 8", found, err)
		}
	})

	t.Run("no new key", func(t *testing.T) {
		if _, err := selectCreatedDeployKey(before, before, "ci"); !errors.Is(err, errDeployKeyNotFound) {
			t.Fatalf("error = %v, want errDeployKeyNotFound", err)
		}
	})

	t.Run("two new matching keys", func(t *testing.T) {
		after := append(newKey(7, "ci ("+uuidC+")"),
			convex.PlatformDeployKeyResponse{Id: 8, Name: "ci"})

		if _, err := selectCreatedDeployKey(before, after, "ci"); !errors.Is(err, errDeployKeyAmbiguous) {
			t.Fatalf("error = %v, want errDeployKeyAmbiguous", err)
		}
	})

	t.Run("empty before list", func(t *testing.T) {
		after := []convex.PlatformDeployKeyResponse{{Id: 5, Name: "ci (" + uuidA + ")"}}

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

func TestDeployKeyDeleteTarget(t *testing.T) {
	ctx := t.Context()
	keys := []convex.PlatformDeployKeyResponse{
		{Id: 4, Name: "ci (" + uuidA + ")"},
		{Id: 5, Name: "dup (" + uuidB + ")"},
		{Id: 6, Name: "dup (" + uuidC + ")"},
	}
	list := func(context.Context) ([]convex.PlatformDeployKeyResponse, error) { return keys, nil }
	failWith := func(err error) deployKeyLister {
		return func(context.Context) ([]convex.PlatformDeployKeyResponse, error) { return nil, err }
	}
	ref := func(id types.Int64, name, secret string) deployKeyRef {
		s := types.StringNull()

		if secret != "" {
			s = types.StringValue(secret)
		}

		return deployKeyRef{ID: id, UniqueName: types.StringNull(), Name: name, Secret: s}
	}

	tests := []struct {
		name       string
		list       deployKeyLister
		ref        deployKeyRef
		wantTarget string
		wantGone   bool
		wantErr    bool
	}{
		// Found by id: the listed name, even when the secret is known.
		{"listed by id", list, ref(types.Int64Value(4), "ci", "prod:abc"), "ci (" + uuidA + ")", false, false},
		{"listed by id, no secret", list, ref(types.Int64Value(4), "ci", ""), "ci (" + uuidA + ")", false, false},
		// A known id no longer listed: deleted already.
		{"id gone", list, ref(types.Int64Value(99), "ci", "prod:abc"), "", true, false},
		// No id and the list cannot pin it down: the secret is the fallback.
		{"no id, ambiguous, secret", list, ref(types.Int64Null(), "dup", "prod:abc"), "prod:abc", false, false},
		{"no id, missing, secret", list, ref(types.Int64Null(), "never", "prod:abc"), "prod:abc", false, false},
		{"no id, ambiguous, no secret", list, ref(types.Int64Null(), "dup", ""), "", false, true},
		{"no id, missing, no secret", list, ref(types.Int64Null(), "never", ""), "", true, false},
		{"no id, unique", list, ref(types.Int64Null(), "ci", ""), "ci (" + uuidA + ")", false, false},
		// The deployment itself is gone.
		{"list 404", failWith(&convex.APIError{StatusCode: 404}), ref(types.Int64Value(4), "ci", ""), "", true, false},
		// A failed list is an error, not "already gone" -- unless the secret
		// can name the key without it.
		{"list fails", failWith(errors.New("boom")), ref(types.Int64Value(4), "ci", ""), "", false, true},
		{"list fails, secret", failWith(errors.New("boom")), ref(types.Int64Value(4), "ci", "prod:abc"), "prod:abc", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, gone, err := deployKeyDeleteTarget(ctx, tt.list, tt.ref)

			if target != tt.wantTarget || gone != tt.wantGone || (err != nil) != tt.wantErr {
				t.Errorf("deployKeyDeleteTarget() = %q, gone %v, err %v; want %q, gone %v, err %v",
					target, gone, err, tt.wantTarget, tt.wantGone, tt.wantErr)
			}
		})
	}
}

func TestAdoptedDeployKeyName(t *testing.T) {
	full := "ci (" + uuidA + ")"
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

func TestKeyedMutexSerialisesPerKey(t *testing.T) {
	var locks keyedMutex

	unlockA := locks.lock("a")
	// A different key is independent and must not block.
	locks.lock("b")()

	acquired := make(chan struct{})

	go func() {
		locks.lock("a")()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second lock on the same key acquired while the first was held")
	case <-time.After(50 * time.Millisecond):
	}

	unlockA()
	<-acquired
}
