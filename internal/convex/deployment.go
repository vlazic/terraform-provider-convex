package convex

import (
	"encoding/json"
	"errors"
	"fmt"
)

// CloudDeployment is the cloud variant of a deployment response. The generated
// name (PlatformDeploymentResponse0) carries no meaning, so alias it.
type CloudDeployment = PlatformDeploymentResponse0

const deploymentKindCloud = "cloud"

// ErrNotCloudDeployment reports that a deployment is not a cloud deployment.
// In practice this means a local deployment: a backend the Convex CLI
// registered for a developer's machine, which has no lifecycle to manage.
var ErrNotCloudDeployment = errors.New("deployment is not a cloud deployment")

// AsCloudDeployment narrows a deployment response to its cloud variant.
//
// The generated As... accessors ignore the "kind" discriminator, and
// encoding/json does not enforce required fields, so a non-cloud deployment
// would decode into the cloud struct as mostly zero values instead of failing.
// Hence the explicit check.
func AsCloudDeployment(resp *PlatformDeploymentResponse) (*CloudDeployment, error) {
	raw, err := resp.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("reading deployment response: %w", err)
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("reading deployment kind: %w", err)
	}
	if probe.Kind != deploymentKindCloud {
		return nil, fmt.Errorf("%w: kind is %q", ErrNotCloudDeployment, probe.Kind)
	}

	deployment, err := resp.AsPlatformDeploymentResponse0()
	if err != nil {
		return nil, fmt.Errorf("decoding cloud deployment: %w", err)
	}
	return &deployment, nil
}
