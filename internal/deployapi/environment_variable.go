package deployapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// envVarChange mirrors UpdateEnvVarRequest, but without the `omitempty` the
// generator puts on Value.
//
// The API distinguishes an absent `value` from an explicit `value: null`: null
// deletes the variable. The generated struct tags Value as
// `json:"value,omitempty"`, so a nil pointer drops the key entirely and the
// deletion is never requested. This type exists purely to emit that null.
//
// The management API's default environment variables have exactly the same
// shape and the same pitfall -- see internal/convex/environment_variable.go.
type envVarChange struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
}

type envVarChanges struct {
	Changes []envVarChange `json:"changes"`
}

// ErrEnvVarNotFound reports that a deployment has no variable of that name.
var ErrEnvVarNotFound = errors.New("environment variable not found")

// GetEnvironmentVariable returns one variable's value.
//
// The API exposes only a list endpoint, which returns every variable as a
// name/value map, so this reads the whole set and picks one out.
func GetEnvironmentVariable(ctx context.Context, client *ClientWithResponses, name string) (string, error) {
	listed, err := client.ListEnvironmentVariablesWithResponse(ctx)
	if err != nil {
		return "", err
	}

	if err := CheckResponse(listed); err != nil {
		return "", err
	}

	if listed.JSON200 == nil {
		return "", errors.New("the API returned a successful status with no environment variable list")
	}

	value, ok := listed.JSON200.EnvironmentVariables[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrEnvVarNotFound, name)
	}

	return value, nil
}

// SetEnvironmentVariable upserts a variable on the deployment.
func SetEnvironmentVariable(ctx context.Context, client *ClientWithResponses, name, value string) error {
	updated, err := client.UpdateEnvironmentVariablesWithResponse(ctx, UpdateEnvVarsRequest{
		Changes: []UpdateEnvVarRequest{{Name: name, Value: &value}},
	})
	if err != nil {
		return err
	}

	return CheckResponse(updated)
}

// UnsetEnvironmentVariable removes a variable from the deployment. Removing
// one that does not exist is a no-op on the API side.
//
// Named Unset rather than Delete because the generated client already has a
// DeleteEnvironmentVariable audit-log constant.
//
// This goes through the raw-body client method rather than
// UpdateEnvironmentVariablesWithResponse, so that `value: null` is actually
// sent -- see envVarChange.
func UnsetEnvironmentVariable(ctx context.Context, client *ClientWithResponses, name string) error {
	payload, err := json.Marshal(envVarChanges{
		Changes: []envVarChange{{Name: name, Value: nil}},
	})
	if err != nil {
		return fmt.Errorf("encoding environment variable deletion: %w", err)
	}

	deleted, err := client.UpdateEnvironmentVariablesWithBodyWithResponse(
		ctx, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}

	return CheckResponse(deleted)
}
