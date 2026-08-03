package convex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/srs/terraform-provider-convex/internal/apierr"
)

// envVarChange mirrors DefaultEnvironmentVariableChangeArgs, but without the
// `omitempty` the generator puts on Value.
//
// The API distinguishes an absent `value` from an explicit `value: null`: null
// means "delete this variable". The generated struct tags Value as
// `json:"value,omitempty"`, so a nil pointer drops the key entirely and the
// deletion is never requested. This type exists purely to emit that null.
type envVarChange struct {
	DeploymentType DeploymentType `json:"deploymentType"`
	Name           string         `json:"name"`
	Value          *string        `json:"value"`
}

type envVarChanges struct {
	Changes []envVarChange `json:"changes"`
}

// Retry settings for the default environment variable endpoints. Convex has
// been observed returning a 500 from update_default_environment_variables,
// notably when Terraform writes several variables of the same project
// concurrently.
const (
	envVarMaxAttempts = 4
	envVarBaseDelay   = 500 * time.Millisecond
)

// SetDefaultEnvironmentVariable upserts a default environment variable.
func SetDefaultEnvironmentVariable(
	ctx context.Context,
	client *ClientWithResponses,
	projectID ProjectId,
	name string,
	deploymentType DeploymentType,
	value string,
) error {
	return apierr.Retry(ctx, envVarMaxAttempts, envVarBaseDelay, func() error {
		updated, err := client.UpdateDefaultEnvironmentVariablesWithResponse(ctx, projectID,
			UpdateDefaultEnvironmentVariablesArgs{
				Changes: []DefaultEnvironmentVariableChangeArgs{{
					DeploymentType: deploymentType,
					Name:           name,
					Value:          &value,
				}},
			})

		if err != nil {
			return err
		}

		return CheckResponse(updated)
	})
}

// DeleteDefaultEnvironmentVariable removes a default environment variable from
// a project. Deleting a variable that does not exist is a no-op on the API
// side.
//
// This goes through the raw-body client method rather than
// UpdateDefaultEnvironmentVariablesWithResponse, so that `value: null` is
// actually sent -- see envVarChange.
func DeleteDefaultEnvironmentVariable(
	ctx context.Context,
	client *ClientWithResponses,
	projectID ProjectId,
	name string,
	deploymentType DeploymentType,
) error {
	payload, err := json.Marshal(envVarChanges{
		Changes: []envVarChange{{
			DeploymentType: deploymentType,
			Name:           name,
			Value:          nil,
		}},
	})

	if err != nil {
		return fmt.Errorf("encoding environment variable deletion: %w", err)
	}

	return apierr.Retry(ctx, envVarMaxAttempts, envVarBaseDelay, func() error {
		deleted, err := client.UpdateDefaultEnvironmentVariablesWithBodyWithResponse(
			ctx, projectID, "application/json", bytes.NewReader(payload))

		if err != nil {
			return err
		}

		return CheckResponse(deleted)
	})
}
