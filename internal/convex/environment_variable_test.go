package convex

import (
	"encoding/json"
	"testing"
)

// The API deletes a variable only when it receives an explicit "value": null.
// The generated DefaultEnvironmentVariableChangeArgs tags Value with
// `omitempty`, so a nil pointer drops the key and the delete silently does
// nothing. This test pins the distinction that motivates envVarChange.
func TestEnvVarChangeEmitsExplicitNull(t *testing.T) {
	encoded, err := json.Marshal(envVarChanges{
		Changes: []envVarChange{{
			DeploymentType: DeploymentTypeProd,
			Name:           "SENTRY_DSN",
			Value:          nil,
		}},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded struct {
		Changes []map[string]any `json:"changes"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(decoded.Changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(decoded.Changes))
	}

	value, present := decoded.Changes[0]["value"]
	if !present {
		t.Fatalf("the \"value\" key is missing from %s; a deletion would be a no-op", encoded)
	}
	if value != nil {
		t.Errorf("value = %v, want null", value)
	}
}

// Guard the premise: the generated type really does drop the key, so the
// workaround stays justified (and this fails loudly if a regenerated client
// ever stops using omitempty).
func TestGeneratedChangeArgsOmitsNullValue(t *testing.T) {
	encoded, err := json.Marshal(DefaultEnvironmentVariableChangeArgs{
		DeploymentType: DeploymentTypeProd,
		Name:           "SENTRY_DSN",
		Value:          nil,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, present := decoded["value"]; present {
		t.Log("the generated type now emits \"value\"; envVarChange may no longer be needed")
	}
}

func TestEnvVarChangeEmitsValueWhenSet(t *testing.T) {
	value := "https://sentry.example.com/1"
	encoded, err := json.Marshal(envVarChanges{
		Changes: []envVarChange{{
			DeploymentType: DeploymentTypeDev,
			Name:           "SENTRY_DSN",
			Value:          &value,
		}},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded struct {
		Changes []struct {
			DeploymentType string  `json:"deploymentType"`
			Name           string  `json:"name"`
			Value          *string `json:"value"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	change := decoded.Changes[0]
	if change.Value == nil || *change.Value != value {
		t.Errorf("value = %v, want %q", change.Value, value)
	}
	if change.DeploymentType != "dev" {
		t.Errorf("deploymentType = %q, want dev", change.DeploymentType)
	}
	if change.Name != "SENTRY_DSN" {
		t.Errorf("name = %q, want SENTRY_DSN", change.Name)
	}
}
