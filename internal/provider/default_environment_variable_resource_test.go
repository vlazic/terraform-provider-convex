package provider

import (
	"errors"
	"testing"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// The list response groups by name and value, listing every deployment type
// that shares them, so a lookup has to confirm the type is actually present.
func TestFindEnvVarMatchesDeploymentType(t *testing.T) {
	items := []convex.DefaultEnvironmentVariableResponse{
		{
			Name:            "SENTRY_DSN",
			Value:           "shared-value",
			DeploymentTypes: []convex.DeploymentType{convex.DeploymentTypeDev, convex.DeploymentTypePreview},
		},
		{
			Name:            "SENTRY_DSN",
			Value:           "prod-value",
			DeploymentTypes: []convex.DeploymentType{convex.DeploymentTypeProd},
		},
	}

	tests := []struct {
		deploymentType convex.DeploymentType
		wantValue      string
	}{
		{convex.DeploymentTypeDev, "shared-value"},
		{convex.DeploymentTypePreview, "shared-value"},
		{convex.DeploymentTypeProd, "prod-value"},
	}

	for _, tt := range tests {
		t.Run(string(tt.deploymentType), func(t *testing.T) {
			found, err := findEnvVar(items, "SENTRY_DSN", tt.deploymentType)

			if err != nil {
				t.Fatalf("findEnvVar() error = %v", err)
			}

			if found.Value != tt.wantValue {
				t.Errorf("value = %q, want %q", found.Value, tt.wantValue)
			}
		})
	}
}

func TestFindEnvVarNotFound(t *testing.T) {
	items := []convex.DefaultEnvironmentVariableResponse{
		{
			Name:            "SENTRY_DSN",
			Value:           "dev-value",
			DeploymentTypes: []convex.DeploymentType{convex.DeploymentTypeDev},
		},
	}

	// Right name, a type it is not set for.
	if _, err := findEnvVar(items, "SENTRY_DSN", convex.DeploymentTypeProd); !errors.Is(err, errEnvVarNotFound) {
		t.Errorf("error = %v, want it to wrap errEnvVarNotFound", err)
	}

	if _, err := findEnvVar(items, "OTHER", convex.DeploymentTypeDev); !errors.Is(err, errEnvVarNotFound) {
		t.Errorf("error = %v, want it to wrap errEnvVarNotFound", err)
	}

	if _, err := findEnvVar(nil, "SENTRY_DSN", convex.DeploymentTypeDev); !errors.Is(err, errEnvVarNotFound) {
		t.Errorf("error on empty list = %v, want it to wrap errEnvVarNotFound", err)
	}
}

// A variable with no deployment types must not match anything, rather than
// falling through to the first entry.
func TestFindEnvVarIgnoresEmptyDeploymentTypes(t *testing.T) {
	items := []convex.DefaultEnvironmentVariableResponse{
		{Name: "SENTRY_DSN", Value: "orphan", DeploymentTypes: nil},
	}

	if _, err := findEnvVar(items, "SENTRY_DSN", convex.DeploymentTypeProd); !errors.Is(err, errEnvVarNotFound) {
		t.Errorf("error = %v, want it to wrap errEnvVarNotFound", err)
	}
}
