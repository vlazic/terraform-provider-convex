package convex

import (
	"encoding/json"
	"errors"
	"testing"
)

func mustDeploymentResponse(t *testing.T, body string) *PlatformDeploymentResponse {
	t.Helper()
	var resp PlatformDeploymentResponse

	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshalling fixture: %v", err)
	}

	return &resp
}

func TestAsCloudDeployment(t *testing.T) {
	resp := mustDeploymentResponse(t, `{
		"kind": "cloud",
		"id": 99,
		"name": "playful-otter-123",
		"createTime": 1700000000000,
		"deploymentType": "prod",
		"projectId": 42,
		"region": "aws-eu-west-1",
		"isDefault": true,
		"reference": "prod-main",
		"deploymentUrl": "https://playful-otter-123.convex.cloud",
		"class": "s16"
	}`)

	deployment, err := AsCloudDeployment(resp)

	if err != nil {
		t.Fatalf("AsCloudDeployment() error = %v", err)
	}

	if deployment.Id != 99 {
		t.Errorf("Id = %d, want 99", deployment.Id)
	}

	if deployment.Name != "playful-otter-123" {
		t.Errorf("Name = %q, want playful-otter-123", deployment.Name)
	}

	if deployment.Region != AwsEuWest1 {
		t.Errorf("Region = %q, want %q", deployment.Region, AwsEuWest1)
	}

	if !deployment.IsDefault {
		t.Error("IsDefault = false, want true")
	}
}

// A local deployment shares no required fields with the cloud variant, and
// encoding/json would decode it into the cloud struct as zero values. The kind
// discriminator is the only thing standing between that and silent corruption.
func TestAsCloudDeploymentRejectsLocal(t *testing.T) {
	resp := mustDeploymentResponse(t, `{
		"kind": "local",
		"name": "local-laptop",
		"createTime": 1700000000000,
		"deploymentType": "dev",
		"projectId": 42,
		"creator": 7,
		"port": 3210,
		"deviceName": "laptop",
		"isActive": true
	}`)

	deployment, err := AsCloudDeployment(resp)

	if err == nil {
		t.Fatalf("AsCloudDeployment() = %+v, want error", deployment)
	}

	if !errors.Is(err, ErrNotCloudDeployment) {
		t.Errorf("error = %v, want it to wrap ErrNotCloudDeployment", err)
	}
}

func TestAsCloudDeploymentRejectsMissingKind(t *testing.T) {
	resp := mustDeploymentResponse(t, `{"name": "mystery-deployment"}`)

	if _, err := AsCloudDeployment(resp); err == nil {
		t.Fatal("AsCloudDeployment() = nil error, want error for a response with no kind")
	}
}
