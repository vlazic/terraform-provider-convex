// Package provider implements the Terraform provider for the Convex
// Management API.
package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// Environment variables consulted when the matching provider attribute is not
// set in configuration.
const (
	envToken    = "CONVEX_ACCESS_TOKEN"
	envEndpoint = "CONVEX_ENDPOINT"
)

// Ensure the implementation satisfies the framework interfaces.
var _ provider.Provider = (*convexProvider)(nil)

type convexProvider struct {
	// version is set at build time and reported to Terraform in the user agent.
	version string
}

// New returns a function that constructs the provider, as expected by
// providerserver.Serve.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &convexProvider{version: version}
	}
}

// providerData is what resources and data sources receive from Configure.
//
// Most of them need only the management client. The ones backed by the
// Deployment Platform API build their own client per deployment, because that
// API is addressed at each deployment's own URL rather than a single host, so
// they need the raw token too.
type providerData struct {
	Management *convex.ClientWithResponses
	Token      string
}

// providerDataFrom pulls the configured clients out of what the framework
// hands a resource or data source. A nil value means the provider has not been
// configured yet, and the framework will call Configure again.
func providerDataFrom(raw any, diags *diag.Diagnostics) (*providerData, bool) {
	if raw == nil {
		return nil, false
	}

	data, ok := raw.(*providerData)

	if !ok {
		diags.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *providerData, got %T. This is a bug in the provider.", raw),
		)

		return nil, false
	}

	return data, true
}

// providerModel maps the provider configuration block.
type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Token    types.String `tfsdk:"token"`
}

func (p *convexProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "convex"
	resp.Version = p.version
}

func (p *convexProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages [Convex](https://convex.dev) projects, deployments and their " +
			"configuration, through the [Management API](https://docs.convex.dev/management-api) and the " +
			"[Deployment Platform API](https://docs.convex.dev/deployment-platform-api). One token " +
			"authenticates both.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Base URL of the Convex Management API. Defaults to `" +
					convex.DefaultBaseURL + "`, or the `" + envEndpoint + "` environment variable when set.",
			},
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Bearer token used to authenticate against the Management API. This may be a " +
					"team access token, a personal access token, or an OAuth token. May also be set with the `" +
					envToken + "` environment variable.",
			},
		},
	}
}

func (p *convexProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Values that are still unknown at this point come from other resources'
	// attributes, which are not resolved yet. Terraform will call Configure
	// again once they are, so defer rather than reading a bogus value.
	if config.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Unknown Convex API endpoint",
			"The endpoint cannot be determined at configuration time. Either set it to a static value or use the "+
				envEndpoint+" environment variable.",
		)
	}

	if config.Token.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Unknown Convex API token",
			"The token cannot be determined at configuration time. Either set it to a static value or use the "+
				envToken+" environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// Configuration wins over the environment.
	endpoint := os.Getenv(envEndpoint)

	if !config.Endpoint.IsNull() {
		endpoint = config.Endpoint.ValueString()
	}

	if endpoint == "" {
		endpoint = convex.DefaultBaseURL
	}

	token := os.Getenv(envToken)

	if !config.Token.IsNull() {
		token = config.Token.ValueString()
	}

	if token == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Missing Convex API token",
			"A bearer token is required to talk to the Convex Management API. Set the provider `token` attribute "+
				"or the "+envToken+" environment variable. Team access tokens are created in the Convex dashboard "+
				"under team settings.",
		)
		return
	}

	client, err := convex.NewAuthenticated(endpoint, token)

	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to create Convex API client",
			"An unexpected error occurred while constructing the API client: "+err.Error(),
		)
		return
	}

	data := &providerData{Management: client, Token: token}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *convexProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewProjectResource,
		NewDeploymentResource,
		NewDeployKeyResource,
		NewPreviewDeployKeyResource,
		NewCustomDomainResource,
		NewDefaultEnvironmentVariableResource,
		NewEnvironmentVariableResource,
		NewLogStreamResource,
	}
}

func (p *convexProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewProjectDataSource,
		NewDeploymentDataSource,
	}
}

func (p *convexProvider) Functions(_ context.Context) []func() function.Function {
	return nil
}
