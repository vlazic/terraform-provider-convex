package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// Convex stores a reference namespaced under the deployment type, so a
// configured "preview-shared" reads back as "preview/preview-shared". Writing
// the server spelling into `reference` would make the applied value differ
// from the configured one, which Terraform rejects outright with "Provider
// produced inconsistent result after apply".
func TestReconcileReference(t *testing.T) {
	tests := []struct {
		name           string
		configured     types.String
		serverValue    string
		deploymentType convex.DeploymentType
		want           types.String
	}{
		{
			name:           "server qualifies the configured value",
			configured:     types.StringValue("preview-shared"),
			serverValue:    "preview/preview-shared",
			deploymentType: convex.DeploymentTypePreview,
			want:           types.StringValue("preview-shared"),
		},
		{
			name:           "prod is qualified the same way",
			configured:     types.StringValue("secondary"),
			serverValue:    "prod/secondary",
			deploymentType: convex.DeploymentTypeProd,
			want:           types.StringValue("secondary"),
		},
		{
			name:           "server returns the value unchanged",
			configured:     types.StringValue("preview/preview-shared"),
			serverValue:    "preview/preview-shared",
			deploymentType: convex.DeploymentTypePreview,
			want:           types.StringValue("preview/preview-shared"),
		},
		{
			name:           "nothing configured takes the server value",
			configured:     types.StringNull(),
			serverValue:    "dev/generated-123",
			deploymentType: convex.DeploymentTypeDev,
			want:           types.StringValue("dev/generated-123"),
		},
		{
			name:           "unknown takes the server value",
			configured:     types.StringUnknown(),
			serverValue:    "prod/main",
			deploymentType: convex.DeploymentTypeProd,
			want:           types.StringValue("prod/main"),
		},
		{
			// A real difference must stay visible rather than being absorbed.
			name:           "genuinely different value is kept verbatim",
			configured:     types.StringValue("staging"),
			serverValue:    "preview/something-else",
			deploymentType: convex.DeploymentTypePreview,
			want:           types.StringValue("preview/something-else"),
		},
		{
			// The prefix must match the deployment type, not just any segment.
			name:           "wrong prefix is not treated as a match",
			configured:     types.StringValue("shared"),
			serverValue:    "dev/shared",
			deploymentType: convex.DeploymentTypePreview,
			want:           types.StringValue("dev/shared"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reconcileReference(tt.configured, tt.serverValue, tt.deploymentType)

			if got != tt.want {
				t.Errorf("reconcileReference(%v, %q, %q) = %v, want %v",
					tt.configured, tt.serverValue, tt.deploymentType, got, tt.want)
			}
		})
	}
}

// applyDeployment must preserve the configured spelling while still reporting
// the stored form.
func TestApplyDeploymentKeepsConfiguredReference(t *testing.T) {
	model := deploymentResourceModel{
		Reference: types.StringValue("preview-shared"),
	}

	applyDeployment(&model, &convex.CloudDeployment{
		Id:             7,
		Name:           "playful-otter-123",
		ProjectId:      42,
		DeploymentType: convex.DeploymentTypePreview,
		Reference:      "preview/preview-shared",
		Region:         convex.AwsEuWest1,
		Class:          "s16",
		DeploymentUrl:  "https://playful-otter-123.convex.cloud",
	})

	if model.Reference != types.StringValue("preview-shared") {
		t.Errorf("reference = %v, want the configured \"preview-shared\"", model.Reference)
	}

	if model.QualifiedReference != types.StringValue("preview/preview-shared") {
		t.Errorf("qualified_reference = %v, want \"preview/preview-shared\"", model.QualifiedReference)
	}
}
