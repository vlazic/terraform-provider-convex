package provider

import (
	"context"
	"errors"
	"fmt"

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

// errDeployKeyNotFound reports that no key with the requested name exists.
// The API offers no get-by-id endpoint for deploy keys, so lookups go through
// the list endpoint and this stands in for a 404.
var errDeployKeyNotFound = errors.New("deploy key not found")

// findDeployKeyByName picks a key out of a list response by name. Names are
// unique per deployment or project -- the delete endpoint accepts a name as an
// identifier, which is what makes them usable as the correlation key here.
func findDeployKeyByName(keys []convex.PlatformDeployKeyResponse, name string) (*convex.PlatformDeployKeyResponse, error) {
	for i := range keys {
		if keys[i].Name == name {
			return &keys[i], nil
		}
	}
	return nil, fmt.Errorf("%w: no key named %q", errDeployKeyNotFound, name)
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
