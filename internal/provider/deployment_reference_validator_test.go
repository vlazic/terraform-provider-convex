package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestDeploymentReferenceValidator(t *testing.T) {
	tests := []struct {
		name    string
		value   types.String
		wantErr bool
	}{
		{name: "null is left to the API", value: types.StringNull()},
		{name: "unknown is left to the API", value: types.StringUnknown()},

		{name: "simple", value: types.StringValue("staging")},
		{name: "with digits and dashes", value: types.StringValue("prod-eu-west-2")},
		{name: "with a slash", value: types.StringValue("team/staging")},
		{name: "minimum length", value: types.StringValue("abc")},
		{name: "trailing digits on one word", value: types.StringValue("prod2")},

		// Two words then digits is the deployment-name shape, so a reference
		// that reads naturally like "prod-eu-2" is rejected by the API.
		{name: "word-word-number is reserved", value: types.StringValue("prod-eu-2"), wantErr: true},

		{name: "too short", value: types.StringValue("ab"), wantErr: true},
		{name: "uppercase", value: types.StringValue("Staging"), wantErr: true},
		{name: "underscore", value: types.StringValue("my_ref"), wantErr: true},
		{name: "space", value: types.StringValue("my ref"), wantErr: true},
		{name: "looks like a deployment name", value: types.StringValue("happy-capybara-123"), wantErr: true},
		{name: "local prefix", value: types.StringValue("local-laptop"), wantErr: true},
		{name: "reserved word", value: types.StringValue("prod"), wantErr: true},
		{name: "another reserved word", value: types.StringValue("preview"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &validator.StringResponse{}
			deploymentReferenceValidator{}.ValidateString(
				t.Context(),
				validator.StringRequest{
					Path:        path.Root("reference"),
					ConfigValue: tt.value,
				},
				resp,
			)

			if got := resp.Diagnostics.HasError(); got != tt.wantErr {
				t.Errorf("HasError() = %v, want %v (diagnostics: %+v)", got, tt.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestDeploymentReferenceValidatorLength(t *testing.T) {
	long := make([]byte, referenceMaxLength+1)

	for i := range long {
		long[i] = 'a'
	}

	resp := &validator.StringResponse{}
	deploymentReferenceValidator{}.ValidateString(
		t.Context(),
		validator.StringRequest{
			Path:        path.Root("reference"),
			ConfigValue: types.StringValue(string(long)),
		},
		resp,
	)

	if !resp.Diagnostics.HasError() {
		t.Errorf("a %d character reference was accepted, want an error", len(long))
	}
}
