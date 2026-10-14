package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// deployKeyActions lists every action a deploy key can be granted. Generated
// from the PlatformCreateDeployKeyArgs.allowedActions enum in the OpenAPI
// document; keep it in sync when refreshing the spec.
var deployKeyActions = []string{
	"deployment:deploy",
	"deployment:env:view",
	"deployment:env:write",
	"deployment:pause",
	"deployment:unpause",
	"deployment:logs:view",
	"deployment:metrics:view",
	"deployment:integrations:view",
	"deployment:integrations:write",
	"deployment:data:view",
	"deployment:data:write",
	"deployment:backups:view",
	"deployment:backups:create",
	"deployment:backups:download",
	"deployment:backups:delete",
	"deployment:backups:import",
	"deployment:functions:actAsUser",
	"deployment:functions:runInternalQueries",
	"deployment:functions:runInternalMutations",
	"deployment:functions:runInternalActions",
	"deployment:functions:runTestQuery",
	"deployment:auditLog:view",
	"deployment:usageLimits:view",
	"deployment:usageLimits:write",
	"deployment:usage:view",
}

// errDeployKeyNotFound reports that no listed key is the one being looked for.
// The API offers no get-by-id endpoint for deploy keys, so lookups go through
// the list endpoint and this stands in for a 404.
var errDeployKeyNotFound = errors.New("deploy key not found")

// errDeployKeyAmbiguous reports that several listed keys answer to one
// configured name, so the provider cannot tell which of them it manages.
var errDeployKeyAmbiguous = errors.New("deploy key name is ambiguous")

// deployKeyNameMatches reports whether a name from the list endpoint belongs to
// a key created under the configured name.
//
// Convex makes deploy key names unique by appending " (<uuid>)": a key created
// as "github-actions" lists back as "github-actions (cbd71ace-...)". Keys made
// before that change keep their plain name, so an exact match counts too.
//
// It accepts any parenthesised suffix, unlike deployKeyBaseName, so a key
// still matches if Convex changes what it appends.
//
// The suffix check is otherwise strict. The text between the parentheses
// may not itself contain one, so "ci" matches neither "ci-other (x)" (wrong
// prefix) nor "ci (staging) (x)" -- the latter is a key created as
// "ci (staging)", not as "ci".
func deployKeyNameMatches(listed, configured string) bool {
	if listed == configured {
		return true
	}

	rest, ok := strings.CutPrefix(listed, configured+" (")

	if !ok {
		return false
	}

	inner, ok := strings.CutSuffix(rest, ")")

	return ok && inner != "" && !strings.ContainsAny(inner, "()")
}

// uniqueSuffixPattern matches the " (<uuid>)" Convex appends to a deploy key's
// name.
var uniqueSuffixPattern = regexp.MustCompile(` \([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\)$`)

// deployKeyBaseName strips the uniqueness suffix Convex appends, returning the
// name the key was created with. It is used when a key is imported by its full
// listed name: recording that full name as `name` would make every plan want
// to replace a resource configured with the short one. Only a UUID-shaped
// suffix is stripped, so a user's own name such as "deploy (prod)" is kept.
// That is narrower than deployKeyNameMatches on purpose: if Convex changes its
// suffix, an import by full name shows up as a planned replacement, visibly,
// rather than silently rewriting a name.
func deployKeyBaseName(listed string) string {
	return uniqueSuffixPattern.ReplaceAllString(listed, "")
}

// findDeployKeyByID picks a key out of a list response by its numeric id,
// which is stable and, unlike the name, never rewritten by Convex.
func findDeployKeyByID(keys []convex.PlatformDeployKeyResponse, id int64) (*convex.PlatformDeployKeyResponse, error) {
	for i := range keys {
		if keys[i].Id == id {
			return &keys[i], nil
		}
	}

	return nil, fmt.Errorf("%w: no key with id %d", errDeployKeyNotFound, id)
}

// findDeployKeyByName picks the one key whose listed name matches the
// configured name (see deployKeyNameMatches). Several matches are an error
// rather than a guess: adopting the wrong key would hand Terraform one it did
// not create, and destroying it later would delete someone else's key.
func findDeployKeyByName(keys []convex.PlatformDeployKeyResponse, name string) (*convex.PlatformDeployKeyResponse, error) {
	var matches []*convex.PlatformDeployKeyResponse

	for i := range keys {
		if deployKeyNameMatches(keys[i].Name, name) {
			matches = append(matches, &keys[i])
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w: no key named %q", errDeployKeyNotFound, name)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("%w: %d keys match the name %q: %s",
			errDeployKeyAmbiguous, len(matches), name, describeDeployKeys(matches))
	}
}

// resolveDeployKey finds the key a resource's state refers to. A known id is
// authoritative. Without one -- an imported key, or state written by v0.2.0,
// which saved a null id when it could not read a created key back -- the name
// is the only handle left, so the key is adopted by name match.
func resolveDeployKey(keys []convex.PlatformDeployKeyResponse, id types.Int64, name string) (*convex.PlatformDeployKeyResponse, error) {
	if isSet(id) {
		return findDeployKeyByID(keys, id.ValueInt64())
	}

	return findDeployKeyByName(keys, name)
}

// selectCreatedDeployKey identifies the key a create call just made, by
// diffing the key lists taken before and after it.
//
// The create response carries only the secret, and the name cannot identify
// the key on its own: Convex appends a unique suffix, and earlier keys with
// the same configured name (from failed applies, or made by hand) match it
// too. A key that is new since the "before" list and matches the name is the
// one -- unless something else created a same-named key in between, in which
// case this refuses to guess.
func selectCreatedDeployKey(before, after []convex.PlatformDeployKeyResponse, name string) (*convex.PlatformDeployKeyResponse, error) {
	existing := make(map[int64]bool, len(before))

	for i := range before {
		existing[before[i].Id] = true
	}

	var candidates []convex.PlatformDeployKeyResponse

	for i := range after {
		if !existing[after[i].Id] {
			candidates = append(candidates, after[i])
		}
	}

	key, err := findDeployKeyByName(candidates, name)

	if errors.Is(err, errDeployKeyNotFound) {
		return nil, fmt.Errorf("%w: no new key matching the name %q appeared in the list after creation",
			errDeployKeyNotFound, name)
	}

	return key, err
}

// describeDeployKeys renders keys as `id 12 ("ci (...)")` for error messages,
// so a user can see which ids to import or delete.
func describeDeployKeys(keys []*convex.PlatformDeployKeyResponse) string {
	parts := make([]string, 0, len(keys))

	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("id %d (%q)", key.Id, key.Name))
	}

	return strings.Join(parts, ", ")
}

// deployKeySecret returns the secret held in state, if it has one. An imported
// key has none.
func deployKeySecret(secret types.String) (string, bool) {
	if !isSet(secret) || secret.ValueString() == "" {
		return "", false
	}

	return secret.ValueString(), true
}

// deployKeyLister lists every key of one deployment or project. Each resource
// passes a closure over its own list endpoint, so the lookups below are shared.
type deployKeyLister func(context.Context) ([]convex.PlatformDeployKeyResponse, error)

// listAndResolveDeployKey lists the keys and finds the one state refers to
// (see resolveDeployKey).
func listAndResolveDeployKey(ctx context.Context, list deployKeyLister, id types.Int64, name string) (*convex.PlatformDeployKeyResponse, error) {
	keys, err := list(ctx)

	if err != nil {
		return nil, err
	}

	return resolveDeployKey(keys, id, name)
}

// listAndSelectCreatedDeployKey lists the keys again after a create and picks
// out the one that call made (see selectCreatedDeployKey).
func listAndSelectCreatedDeployKey(ctx context.Context, list deployKeyLister, name string, before []convex.PlatformDeployKeyResponse) (*convex.PlatformDeployKeyResponse, error) {
	after, err := list(ctx)

	if err != nil {
		return nil, err
	}

	return selectCreatedDeployKey(before, after, name)
}

// deployKeyDeleteTarget picks the identifier to pass to a delete endpoint, or
// reports that the key is already gone.
//
// The delete endpoints accept either the secret or the key's full listed name.
// The secret is preferred: it names exactly one key with no lookup. Without it
// (an imported key) the key is looked up and its full listed name used; the
// configured name would miss, since Convex stores it with a suffix.
func deployKeyDeleteTarget(ctx context.Context, list deployKeyLister, secret types.String, id types.Int64, name string) (target string, gone bool, err error) {
	if target, ok := deployKeySecret(secret); ok {
		return target, false, nil
	}

	key, err := listAndResolveDeployKey(ctx, list, id, name)

	if errors.Is(err, errDeployKeyNotFound) {
		return "", true, nil
	}

	if err != nil {
		return "", false, err
	}

	return key.Name, false, nil
}

// adoptedDeployKeyName returns the name to keep in state after a read. A key
// imported by its full listed name has that name in state with no id; it is
// recorded as the name the key was created with, so a configuration using the
// short name does not plan a replacement. Otherwise the name is unchanged.
func adoptedDeployKeyName(id types.Int64, name string, key *convex.PlatformDeployKeyResponse) types.String {
	if !isSet(id) && key.Name == name {
		return types.StringValue(deployKeyBaseName(name))
	}

	return types.StringValue(name)
}

// ambiguousDeployKeyDetail explains how to get out of an ambiguous match.
func ambiguousDeployKeyDetail(err error, importExample string) string {
	return fmt.Sprintf("%s. The provider cannot tell which of these keys this resource manages. Delete the "+
		"extra keys in the Convex dashboard, or remove this resource from state and import the one you want "+
		"by its full listed name, for example: %s", err, importExample)
}

// deployKeyMetadata holds the attributes every deploy key resource shares.
// The secret itself is deliberately absent: it is returned only at creation
// and can never be read back.
type deployKeyMetadata struct {
	ID             types.Int64
	CreationTime   types.Int64
	LastUsedTime   types.Int64
	ExpiresAt      types.Int64
	Creator        types.Int64
	AllowedActions types.Set
}

// readDeployKeyMetadata converts a list entry into the shared attributes.
func readDeployKeyMetadata(ctx context.Context, key *convex.PlatformDeployKeyResponse) (deployKeyMetadata, diag.Diagnostics) {
	actions := make([]string, 0, len(key.AllowedActions))

	for _, action := range key.AllowedActions {
		actions = append(actions, string(action))
	}

	allowedActions, diags := types.SetValueFrom(ctx, types.StringType, actions)

	return deployKeyMetadata{
		ID:             types.Int64Value(key.Id),
		CreationTime:   types.Int64Value(key.CreationTime),
		LastUsedTime:   types.Int64PointerValue(key.LastUsedTime),
		ExpiresAt:      types.Int64PointerValue(key.ExpiresAt),
		Creator:        types.Int64PointerValue(key.Creator),
		AllowedActions: allowedActions,
	}, diags
}

// toCreateActions converts a configured set of actions into the enum type the
// create endpoint expects.
func toCreateActions(ctx context.Context, set types.Set) (*[]convex.PlatformCreateDeployKeyArgsAllowedActions, diag.Diagnostics) {
	if !isSet(set) {
		return nil, nil
	}

	var actions []string
	diags := set.ElementsAs(ctx, &actions, false)

	if diags.HasError() {
		return nil, diags
	}

	converted := make([]convex.PlatformCreateDeployKeyArgsAllowedActions, 0, len(actions))

	for _, action := range actions {
		converted = append(converted, convex.PlatformCreateDeployKeyArgsAllowedActions(action))
	}

	return &converted, diags
}
