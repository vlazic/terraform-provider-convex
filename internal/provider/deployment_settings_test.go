package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

func TestPostCreateSettings(t *testing.T) {
	tests := []struct {
		name              string
		plan              deploymentResourceModel
		wantNeeded        bool
		wantSendLogs      *bool
		wantDashboardWant *bool
	}{
		{
			name: "nothing configured needs no patch",
			plan: deploymentResourceModel{
				SendLogsToClient:          types.BoolUnknown(),
				DashboardEditConfirmation: types.BoolUnknown(),
			},
			wantNeeded: false,
		},
		{
			// The regression this guards: false is a legitimate value, and
			// skipping it would leave the API reporting null against a
			// configured false.
			name: "false must still be sent",
			plan: deploymentResourceModel{
				SendLogsToClient:          types.BoolValue(false),
				DashboardEditConfirmation: types.BoolUnknown(),
			},
			wantNeeded:   true,
			wantSendLogs: boolPtr(false),
		},
		{
			name: "true is sent",
			plan: deploymentResourceModel{
				SendLogsToClient:          types.BoolValue(true),
				DashboardEditConfirmation: types.BoolUnknown(),
			},
			wantNeeded:   true,
			wantSendLogs: boolPtr(true),
		},
		{
			name: "both are sent together",
			plan: deploymentResourceModel{
				SendLogsToClient:          types.BoolValue(false),
				DashboardEditConfirmation: types.BoolValue(true),
			},
			wantNeeded:        true,
			wantSendLogs:      boolPtr(false),
			wantDashboardWant: boolPtr(true),
		},
		{
			name: "null is treated as unconfigured",
			plan: deploymentResourceModel{
				SendLogsToClient:          types.BoolNull(),
				DashboardEditConfirmation: types.BoolNull(),
			},
			wantNeeded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, needed := postCreateSettings(tt.plan)

			if needed != tt.wantNeeded {
				t.Fatalf("needed = %v, want %v", needed, tt.wantNeeded)
			}

			assertBoolPtr(t, "send_logs_to_client", settings.SendLogsToClient, tt.wantSendLogs)
			assertBoolPtr(t, "dashboard_edit_confirmation", settings.DashboardEditConfirmation, tt.wantDashboardWant)
		})
	}
}

// The bug this reproduces: deriving the patch after applyDeployment has run
// reads the API's view, where an unset attribute is null, so a configured
// false silently turns into "send nothing".
func TestPostCreateSettingsMustReadTheConfiguredPlan(t *testing.T) {
	plan := deploymentResourceModel{
		SendLogsToClient:          types.BoolValue(false),
		DashboardEditConfirmation: types.BoolUnknown(),
	}

	// What the caller must do: capture the patch first.
	settings, needed := postCreateSettings(plan)

	// Then the create response overwrites the model, as applyDeployment does.
	applyDeployment(&plan, &convex.CloudDeployment{
		Id:             1,
		Name:           "playful-otter-123",
		ProjectId:      42,
		DeploymentType: convex.DeploymentTypeProd,
		Region:         convex.AwsEuWest1,
		Class:          "s16",
		DeploymentUrl:  "https://playful-otter-123.convex.cloud",
		// The create endpoint does not carry these, so they come back nil.
		SendLogsToClient: nil,
	})

	if plan.SendLogsToClient != types.BoolNull() {
		t.Fatalf("precondition failed: applyDeployment left %v, expected null", plan.SendLogsToClient)
	}

	// The captured patch must be unaffected by that.
	if !needed {
		t.Fatal("needed = false, want true")
	}

	assertBoolPtr(t, "send_logs_to_client", settings.SendLogsToClient, boolPtr(false))

	// And deriving it now, after the overwrite, would produce nothing -- which
	// is precisely the failure mode.
	if _, stillNeeded := postCreateSettings(plan); stillNeeded {
		t.Error("deriving the patch after applyDeployment produced one; the test no longer models the bug")
	}
}

func boolPtr(v bool) *bool { return &v }

func assertBoolPtr(t *testing.T, name string, got, want *bool) {
	t.Helper()

	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %v, want it to be omitted", name, *got)
	case want != nil && got == nil:
		t.Errorf("%s was omitted, want %v", name, *want)
	case want != nil && got != nil && *got != *want:
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}
