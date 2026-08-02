package provider

import (
	"testing"

	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestProviderDataSources(t *testing.T) {
	ctx := t.Context()

	got := map[string]bool{}

	for _, newDataSource := range New("test")().DataSources(ctx) {
		metadata := &fwdatasource.MetadataResponse{}
		newDataSource().Metadata(ctx, fwdatasource.MetadataRequest{ProviderTypeName: "convex"}, metadata)

		if got[metadata.TypeName] {
			t.Errorf("data source %q is registered more than once", metadata.TypeName)
		}

		got[metadata.TypeName] = true
	}

	for _, want := range []string{"convex_project", "convex_deployment"} {
		if !got[want] {
			t.Errorf("data source %q is not registered", want)
		}
	}
}

// Every registered data source must declare a schema the framework accepts.
func TestProviderDataSourceSchemas(t *testing.T) {
	ctx := t.Context()

	for _, newDataSource := range New("test")().DataSources(ctx) {
		ds := newDataSource()
		metadata := &fwdatasource.MetadataResponse{}
		ds.Metadata(ctx, fwdatasource.MetadataRequest{ProviderTypeName: "convex"}, metadata)

		t.Run(metadata.TypeName, func(t *testing.T) {
			resp := &fwdatasource.SchemaResponse{}
			ds.Schema(ctx, fwdatasource.SchemaRequest{}, resp)

			if resp.Diagnostics.HasError() {
				t.Fatalf("Schema() diagnostics: %+v", resp.Diagnostics)
			}

			if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
				t.Fatalf("ValidateImplementation() diagnostics: %+v", diags)
			}
		})
	}
}

// Config validators reference attribute paths by name, so a renamed attribute
// would leave them silently pointing at nothing. Check every path resolves.
func TestDataSourceConfigValidatorPaths(t *testing.T) {
	ctx := t.Context()

	cases := []struct {
		name       string
		dataSource fwdatasource.DataSource
		paths      []string
	}{
		{
			name:       "convex_project",
			dataSource: NewProjectDataSource(),
			paths:      []string{"id", "slug", "team_id", "team_slug"},
		},
		{
			name:       "convex_deployment",
			dataSource: NewDeploymentDataSource(),
			paths:      []string{"name", "project_id", "reference", "default_prod", "default_dev"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &fwdatasource.SchemaResponse{}
			tc.dataSource.Schema(ctx, fwdatasource.SchemaRequest{}, resp)

			for _, name := range tc.paths {
				if _, ok := resp.Schema.Attributes[name]; !ok {
					t.Errorf("config validators reference attribute %q, which the schema does not define", name)
				}
			}

			withValidators, ok := tc.dataSource.(fwdatasource.DataSourceWithConfigValidators)

			if !ok {
				t.Fatal("data source does not implement DataSourceWithConfigValidators")
			}

			if len(withValidators.ConfigValidators(ctx)) == 0 {
				t.Error("ConfigValidators() is empty, want the lookup-key constraints")
			}
		})
	}
}
