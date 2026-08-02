package provider

import (
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestProviderSchema(t *testing.T) {
	ctx := t.Context()
	resp := &fwprovider.SchemaResponse{}
	New("test")().Schema(ctx, fwprovider.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics: %+v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("ValidateImplementation() diagnostics: %+v", diags)
	}
}

func TestProviderMetadata(t *testing.T) {
	resp := &fwprovider.MetadataResponse{}
	New("1.2.3")().Metadata(t.Context(), fwprovider.MetadataRequest{}, resp)

	if resp.TypeName != "convex" {
		t.Errorf("TypeName = %q, want %q", resp.TypeName, "convex")
	}
	if resp.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", resp.Version, "1.2.3")
	}
}

func TestProviderResources(t *testing.T) {
	ctx := t.Context()

	got := map[string]bool{}
	for _, newResource := range New("test")().Resources(ctx) {
		metadata := &fwresource.MetadataResponse{}
		newResource().Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "convex"}, metadata)
		if got[metadata.TypeName] {
			t.Errorf("resource %q is registered more than once", metadata.TypeName)
		}
		got[metadata.TypeName] = true
	}

	want := []string{
		"convex_project",
		"convex_deployment",
		"convex_deploy_key",
		"convex_preview_deploy_key",
		"convex_custom_domain",
		"convex_default_environment_variable",
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("resource %q is not registered", name)
		}
	}
}

// Every registered resource must declare a schema the framework accepts.
func TestProviderResourceSchemas(t *testing.T) {
	ctx := t.Context()

	for _, newResource := range New("test")().Resources(ctx) {
		res := newResource()
		metadata := &fwresource.MetadataResponse{}
		res.Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "convex"}, metadata)

		t.Run(metadata.TypeName, func(t *testing.T) {
			resp := &fwresource.SchemaResponse{}
			res.Schema(ctx, fwresource.SchemaRequest{}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Schema() diagnostics: %+v", resp.Diagnostics)
			}
			if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
				t.Fatalf("ValidateImplementation() diagnostics: %+v", diags)
			}
		})
	}
}
