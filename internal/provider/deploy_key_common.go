package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/apierr"
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

// uniqueSuffix matches the " (<uuid>)" Convex appends to a deploy key's name,
// as the whole of the string it is applied to.
var uniqueSuffix = regexp.MustCompile(`^ \([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\)$`)

// deployKeyBaseName strips the uniqueness suffix Convex appends, returning the
// name the key was created with.
//
// Convex makes deploy key names unique by appending " (<uuid>)": a key created
// as "github-actions" lists back as "github-actions (cbd71ace-...)". Only a
// UUID-shaped suffix counts, so a user's own name such as "deploy (prod)" is
// kept whole. Should Convex change the shape of its suffix, keys stop matching
// by name and that surfaces as an error or a planned replacement, rather than
// as a silent match against some other key.
func deployKeyBaseName(listed string) string {
	if i := strings.LastIndex(listed, " ("); i >= 0 && uniqueSuffix.MatchString(listed[i:]) {
		return listed[:i]
	}

	return listed
}

// deployKeyNameMatches reports whether a name from the list endpoint belongs to
// a key created under the configured name: it is that name, or that name plus
// the suffix deployKeyBaseName strips. Keys made before Convex started
// suffixing names keep their plain name, hence the exact match. One rule
// serves both functions, so "ci" matches neither "ci-other (<uuid>)" nor
// "ci (staging) (<uuid>)", the latter being a key created as "ci (staging)".
func deployKeyNameMatches(listed, configured string) bool {
	return listed == configured || deployKeyBaseName(listed) == configured
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

// deployKeyRef is what a resource's state knows about the key it manages.
type deployKeyRef struct {
	ID         types.Int64
	UniqueName types.String
	Name       string
	Secret     types.String
}

// resolveDeployKey finds the key a resource's state refers to. A known id is
// authoritative. Without one -- an imported key, or state written by v0.2.0,
// which saved a null id when it could not read a created key back -- the
// recorded unique name is tried as an exact match first, which is how a key
// imported by its full listed name (including a legacy unsuffixed one next to
// suffixed siblings) is found. Failing that, the key is adopted by name match.
func resolveDeployKey(keys []convex.PlatformDeployKeyResponse, ref deployKeyRef) (*convex.PlatformDeployKeyResponse, error) {
	if isSet(ref.ID) {
		return findDeployKeyByID(keys, ref.ID.ValueInt64())
	}

	if isSet(ref.UniqueName) {
		for i := range keys {
			if keys[i].Name == ref.UniqueName.ValueString() {
				return &keys[i], nil
			}
		}
	}

	return findDeployKeyByName(keys, ref.Name)
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
	s := secret.ValueString()
	return s, s != ""
}

// deployKeyLister lists every key of one deployment or project.
type deployKeyLister func(context.Context) ([]convex.PlatformDeployKeyResponse, error)

// deployKeyScope is what the shared lifecycle below needs to know about the
// deployment or project a key belongs to.
type deployKeyScope struct {
	// lockKey identifies the key list, so creates against it can be
	// serialised (see createDeployKey).
	lockKey string
	list    deployKeyLister

	// kind and where make messages read naturally: "preview deploy key" and
	// "in project 12".
	kind  string
	where string

	// importCommand is a full import command for this resource, shown when a
	// name is ambiguous.
	importCommand string
}

// Retry settings for listing keys. Listing is a read, so retrying it is always
// safe; it is the call every create, read and delete depends on.
const (
	deployKeyListAttempts  = 4
	deployKeyListBaseDelay = 500 * time.Millisecond
)

// retryDeployKeyList wraps a list call in apierr.Retry.
func retryDeployKeyList(ctx context.Context, list deployKeyLister) ([]convex.PlatformDeployKeyResponse, error) {
	var keys []convex.PlatformDeployKeyResponse

	err := apierr.Retry(ctx, deployKeyListAttempts, deployKeyListBaseDelay, func() error {
		var err error
		keys, err = list(ctx)
		return err
	})

	return keys, err
}

// keyedMutex hands out one mutex per key.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// lock locks the mutex for key and returns its unlock function.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()

	if k.locks == nil {
		k.locks = map[string]*sync.Mutex{}
	}

	m, ok := k.locks[key]

	if !ok {
		m = &sync.Mutex{}
		k.locks[key] = m
	}

	k.mu.Unlock()
	m.Lock()

	return m.Unlock
}

// deployKeyCreateLocks serialises creates per deployment or project. It is
// package-level rather than per resource instance, because the framework may
// build a fresh resource value per call and provider aliases may point at the
// same deployment.
var deployKeyCreateLocks keyedMutex

// createDeployKey runs a create call between two listings of the scope's keys
// and identifies the created key as the one new entry matching the name.
//
// The whole sequence holds the scope's lock. Without it, two same-named keys
// created in parallel -- two resources, or parallel creation in one apply --
// would each see both new keys in their "after" listing and neither could
// tell which was its own. Other writers (another Terraform run, the
// dashboard) are not covered; selectCreatedDeployKey then refuses to guess.
//
// created reports whether the create call succeeded. When it did, secret is
// set even if err is not nil, and the caller must save it: it is never
// returned again.
func createDeployKey(ctx context.Context, scope deployKeyScope, name string, create func(context.Context) (string, error)) (secret string, key *convex.PlatformDeployKeyResponse, created bool, err error) {
	unlock := deployKeyCreateLocks.lock(scope.lockKey)
	defer unlock()

	// Nothing has been created yet, so failing here is safe.
	before, err := retryDeployKeyList(ctx, scope.list)

	if err != nil {
		return "", nil, false, fmt.Errorf("could not list the existing keys before creating: %w", err)
	}

	secret, err = create(ctx)

	if err != nil {
		return "", nil, false, err
	}

	after, err := retryDeployKeyList(ctx, scope.list)

	if err != nil {
		return secret, nil, true, err
	}

	key, err = selectCreatedDeployKey(before, after, name)
	return secret, key, true, err
}

// isNotFound reports whether err is a 404 from the API.
func isNotFound(err error) bool {
	var apiErr *convex.APIError
	return errors.As(err, &apiErr) && apiErr.IsNotFound()
}

// readDeployKey finds the key state refers to for a refresh. A nil key with
// remove set means the key is gone and the resource leaves state; a nil key
// without it means state is kept as it is, with whatever diagnostics say why.
//
// A key with no id but a secret in state was created by Terraform and could
// not be identified afterwards (see createDeployKey). It is never dropped and
// never fails the refresh: dropping it would orphan a live key, and the secret
// still lets destroy delete it. It gets a warning instead.
func readDeployKey(ctx context.Context, scope deployKeyScope, ref deployKeyRef) (key *convex.PlatformDeployKeyResponse, remove bool, diags diag.Diagnostics) {
	keys, err := retryDeployKeyList(ctx, scope.list)

	if err != nil {
		if isNotFound(err) {
			// The deployment or project itself is gone, and its keys with it.
			return nil, true, diags
		}

		diags.AddError("Error reading Convex "+scope.kind,
			fmt.Sprintf("Could not list the keys to read %s %q %s: %s", scope.kind, ref.Name, scope.where, err))
		return nil, false, diags
	}

	key, err = resolveDeployKey(keys, ref)

	if err == nil {
		return key, false, diags
	}

	if _, hasSecret := deployKeySecret(ref.Secret); hasSecret && !isSet(ref.ID) {
		diags.AddWarning("Convex "+scope.kind+" could not be identified",
			fmt.Sprintf("%s. The %s %q %s was created by Terraform, but it could not be told apart in the "+
				"key list. It is kept in state, and destroying the resource deletes it by the secret held in "+
				"state. Deleting any extra keys of this name in the Convex dashboard lets the next refresh "+
				"identify it.", err, scope.kind, ref.Name, scope.where))
		return nil, false, diags
	}

	if errors.Is(err, errDeployKeyNotFound) {
		return nil, true, diags
	}

	diags.AddError("Ambiguous Convex "+scope.kind, fmt.Sprintf("%s. The provider cannot tell which of these "+
		"keys this resource manages. Delete the extra keys in the Convex dashboard, or remove this resource "+
		"from state (it holds no secret) and import the one you want by its full listed name, for example: %s",
		err, scope.importCommand))

	return nil, false, diags
}

// deployKeyDeleteTarget picks the identifier to pass to a delete endpoint, or
// reports that the key is already gone.
//
// The delete endpoints accept either the secret or the key's full listed
// name; the configured name would miss, since Convex stores it with a suffix.
// The listed name of the key found by id (or by unique name match) is used
// first. The secret is the fallback for a key with no id that the list cannot
// pin down -- one created but never identified. A key whose known id is no
// longer listed is gone, as is everything when the list itself is a 404.
func deployKeyDeleteTarget(ctx context.Context, list deployKeyLister, ref deployKeyRef) (target string, gone bool, err error) {
	secret, hasSecret := deployKeySecret(ref.Secret)
	keys, err := retryDeployKeyList(ctx, list)

	switch {
	case isNotFound(err):
		return "", true, nil
	case err != nil && hasSecret:
		// The secret identifies the key without the list, so a failing list
		// need not block destroy.
		return secret, false, nil
	case err != nil:
		return "", false, err
	}

	key, err := resolveDeployKey(keys, ref)

	switch {
	case err == nil:
		return key.Name, false, nil
	case isSet(ref.ID):
		return "", true, nil
	case hasSecret:
		return secret, false, nil
	case errors.Is(err, errDeployKeyNotFound):
		return "", true, nil
	default:
		return "", false, err
	}
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
