package convex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
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
const envVarMaxAttempts = 4

// Overridden in tests so they do not actually sleep.
var envVarBaseDelay = 500 * time.Millisecond

// SetDefaultEnvironmentVariable upserts a default environment variable.
func SetDefaultEnvironmentVariable(
	ctx context.Context,
	client *ClientWithResponses,
	projectID ProjectId,
	name string,
	deploymentType DeploymentType,
	value string,
) error {
	return retryTransient(ctx, func() error {
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

	return retryTransient(ctx, func() error {
		deleted, err := client.UpdateDefaultEnvironmentVariablesWithBodyWithResponse(
			ctx, projectID, "application/json", bytes.NewReader(payload))

		if err != nil {
			return err
		}

		return CheckResponse(deleted)
	})
}

// retryTransient runs fn, retrying with linear backoff while the API reports a
// transient failure.
//
// This is applied only to the default environment variable writes, which are
// idempotent: setting the same value twice, or deleting something already
// gone, changes nothing. Retrying is deliberately not wired into the HTTP
// client globally, because a 500 from a create endpoint may well mean the
// resource was created, and a blind retry would duplicate it.
func retryTransient(ctx context.Context, fn func() error) error {
	var err error

	for attempt := 1; attempt <= envVarMaxAttempts; attempt++ {
		err = fn()

		if err == nil || !IsTransient(err) {
			return err
		}

		if attempt == envVarMaxAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * envVarBaseDelay):
		}
	}

	return fmt.Errorf("giving up after %d attempts: %w", envVarMaxAttempts, err)
}

// IsTransient reports whether an error looks worth retrying: a rate limit or a
// server-side fault, as opposed to a request the server rejected on its
// merits.
func IsTransient(err error) bool {
	var apiErr *APIError

	if !errors.As(err, &apiErr) {
		return false
	}

	return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= http.StatusInternalServerError
}
