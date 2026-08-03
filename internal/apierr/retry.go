package apierr

import (
	"context"
	"fmt"
	"time"
)

// Retry runs fn, retrying with linear backoff while the API reports a
// transient failure. It gives up after attempts tries and returns the last
// error, wrapped so callers can still inspect the *APIError.
//
// Convex returns two kinds of transient failure worth retrying:
//
//   - plain 5xx faults, which the management API has been seen emitting from
//     update_default_environment_variables
//   - 503 OptimisticConcurrencyControlFailure, which the deployment API raises
//     when concurrent mutations touch the same data. Terraform applies
//     independent resources in parallel, so several writes against one
//     deployment collide readily.
//
// Only ever wrap idempotent operations in this. Retrying a create can produce
// duplicates, since a failure does not prove the resource was not made.
func Retry(ctx context.Context, attempts int, baseDelay time.Duration, fn func() error) error {
	var err error

	for attempt := 1; attempt <= attempts; attempt++ {
		err = fn()

		if err == nil || !IsTransient(err) {
			return err
		}

		if attempt == attempts {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * baseDelay):
		}
	}

	return fmt.Errorf("giving up after %d attempts: %w", attempts, err)
}
