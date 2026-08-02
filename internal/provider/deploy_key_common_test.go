package provider

import (
	"errors"
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
