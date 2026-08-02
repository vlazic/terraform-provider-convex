package provider

import (
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

func TestProjectResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &fwresource.SchemaResponse{}
	NewProjectResource().Schema(ctx, fwresource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics: %+v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("ValidateImplementation() diagnostics: %+v", diags)
	}
}

// The fallback state written when the post-create read-back fails must not
// contain unknown values, or Terraform rejects it and the project is leaked.
func TestApplyCreateResponseLeavesNothingUnknown(t *testing.T) {
	model := projectResourceModel{
		Name: types.StringValue("My Project"),
		// Computed attributes arrive from the plan as unknown.
		ID:                 types.Int64Unknown(),
		TeamID:             types.Int64Value(7),
		Slug:               types.StringUnknown(),
		TeamSlug:           types.StringUnknown(),
		CreateTime:         types.Int64Unknown(),
		ProdDeploymentName: types.StringUnknown(),
		DevDeploymentName:  types.StringUnknown(),
	}

	applyCreateResponse(&model, 7, &convex.PlatformCreateProjectResponse{
		Id:        42,
		ProjectId: 42,
		Slug:      "my-project",
	})

	unknowns := map[string]bool{
		"id":                   model.ID.IsUnknown(),
		"team_id":              model.TeamID.IsUnknown(),
		"name":                 model.Name.IsUnknown(),
		"slug":                 model.Slug.IsUnknown(),
		"team_slug":            model.TeamSlug.IsUnknown(),
		"create_time":          model.CreateTime.IsUnknown(),
		"prod_deployment_name": model.ProdDeploymentName.IsUnknown(),
		"dev_deployment_name":  model.DevDeploymentName.IsUnknown(),
	}
	for attr, isUnknown := range unknowns {
		if isUnknown {
			t.Errorf("%s is still unknown after applyCreateResponse", attr)
		}
	}

	if model.ID != types.Int64Value(42) {
		t.Errorf("id = %v, want 42", model.ID)
	}
	if model.Slug != types.StringValue("my-project") {
		t.Errorf("slug = %v, want my-project", model.Slug)
	}
}

func TestApplyProject(t *testing.T) {
	prod := "happy-otter-123"

	var model projectResourceModel

	applyProject(&model, &convex.ProjectResponse{
		Id:                 42,
		TeamId:             7,
		Name:               "My Project",
		Slug:               "my-project",
		TeamSlug:           "my-team",
		CreateTime:         1_700_000_000_000,
		ProdDeploymentName: &prod,
		// DevDeploymentName is nil, i.e. no dev deployment exists.
	})

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"id", model.ID, types.Int64Value(42)},
		{"team_id", model.TeamID, types.Int64Value(7)},
		{"name", model.Name, types.StringValue("My Project")},
		{"slug", model.Slug, types.StringValue("my-project")},
		{"team_slug", model.TeamSlug, types.StringValue("my-team")},
		{"create_time", model.CreateTime, types.Int64Value(1_700_000_000_000)},
		{"prod_deployment_name", model.ProdDeploymentName, types.StringValue(prod)},
		{"dev_deployment_name", model.DevDeploymentName, types.StringNull()},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}
