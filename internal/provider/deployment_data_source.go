package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// Ensure the implementation satisfies the framework interfaces.
var (
	_ datasource.DataSource                     = (*deploymentDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*deploymentDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*deploymentDataSource)(nil)
)

type deploymentDataSource struct {
	client *convex.ClientWithResponses
}

// NewDeploymentDataSource returns the convex_deployment data source.
func NewDeploymentDataSource() datasource.DataSource {
	return &deploymentDataSource{}
}

// deploymentDataSourceModel maps the convex_deployment data source schema.
type deploymentDataSourceModel struct {
	// Lookup keys.
	Name        types.String `tfsdk:"name"`
	ProjectID   types.Int64  `tfsdk:"project_id"`
	Reference   types.String `tfsdk:"reference"`
	DefaultProd types.Bool   `tfsdk:"default_prod"`
	DefaultDev  types.Bool   `tfsdk:"default_dev"`

	// Results.
	ID                        types.Int64  `tfsdk:"id"`
	DeploymentType            types.String `tfsdk:"deployment_type"`
	Class                     types.String `tfsdk:"class"`
	Region                    types.String `tfsdk:"region"`
	IsDefault                 types.Bool   `tfsdk:"is_default"`
	ExpiresAt                 types.Int64  `tfsdk:"expires_at"`
	SendLogsToClient          types.Bool   `tfsdk:"send_logs_to_client"`
	DashboardEditConfirmation types.Bool   `tfsdk:"dashboard_edit_confirmation"`
	DeploymentURL             types.String `tfsdk:"deployment_url"`
	CreateTime                types.Int64  `tfsdk:"create_time"`
	LastDeployTime            types.Int64  `tfsdk:"last_deploy_time"`
	Creator                   types.Int64  `tfsdk:"creator"`
	PreviewIdentifier         types.String `tfsdk:"preview_identifier"`
}

func (d *deploymentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deployment"
}

func (d *deploymentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a cloud deployment, either by name or by picking one out of a project.\n\n" +
			"Set `name`, or set `project_id` together with one of `reference`, `default_prod` or `default_dev`.\n\n" +
			"To find a deployment starting from a project slug, resolve the project with the `convex_project` " +
			"data source first and pass its `id` here.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Readable identifier of the deployment, such as `playful-otter-123`. Set " +
					"this to look the deployment up directly.",
			},
			"project_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Numeric identifier of the project to search. Combine with `reference`, " +
					"`default_prod` or `default_dev`.",
			},
			"reference": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Select the deployment with this reference within `project_id`.",
			},
			"default_prod": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Select the project's default production deployment.",
			},
			"default_dev": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Select the calling member's default development deployment in the " +
					"project. Which deployment this resolves to depends on the token in use.",
			},
			"id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Numeric identifier of the deployment.",
			},
			"deployment_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Type of the deployment: `dev`, `prod`, `preview` or `custom`.",
			},
			"class": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Deployment class, for example `s16`.",
			},
			"region": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Hosting region of the deployment.",
			},
			"is_default": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether this is the default deployment of its type for the project.",
			},
			"expires_at": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Timestamp in milliseconds at which Convex deletes this deployment, if an " +
					"expiry is set.",
			},
			"send_logs_to_client": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether function logs are sent to the client, or null when the " +
					"deployment-type default applies.",
			},
			"dashboard_edit_confirmation": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether the dashboard asks for confirmation before edits, or null when " +
					"the deployment-type default applies.",
			},
			"deployment_url": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Backend URL of the deployment.",
			},
			"create_time": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp, in milliseconds since the Unix epoch.",
			},
			"last_deploy_time": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Timestamp of the last push to this deployment, or null if nothing has " +
					"been deployed yet.",
			},
			"creator": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Numeric member ID of whoever created the deployment.",
			},
			"preview_identifier": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Preview identifier, set only on `preview` deployments.",
			},
		},
	}
}

func (d *deploymentDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		// Either address the deployment directly or search a project.
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("name"),
			path.MatchRoot("project_id"),
		),
		// The three project-scoped selectors pick different deployments, so at
		// most one may be set.
		datasourcevalidator.Conflicting(
			path.MatchRoot("reference"),
			path.MatchRoot("default_prod"),
			path.MatchRoot("default_dev"),
		),
		datasourcevalidator.Conflicting(
			path.MatchRoot("name"),
			path.MatchRoot("reference"),
		),
		datasourcevalidator.Conflicting(
			path.MatchRoot("name"),
			path.MatchRoot("default_prod"),
		),
		datasourcevalidator.Conflicting(
			path.MatchRoot("name"),
			path.MatchRoot("default_dev"),
		),
	}
}

func (d *deploymentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*convex.ClientWithResponses)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected data source configure type",
			fmt.Sprintf("Expected *convex.ClientWithResponses, got %T. This is a bug in the provider.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *deploymentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config deploymentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var deployment *convex.CloudDeployment
	var err error

	if isSet(config.Name) {
		deployment, err = d.getByName(ctx, config.Name.ValueString())
	} else {
		params := &convex.GetDeploymentInProjectByProjectIdParams{}
		switch {
		case isSet(config.Reference):
			params.Reference = config.Reference.ValueStringPointer()
		case isSet(config.DefaultProd):
			params.DefaultProd = config.DefaultProd.ValueBoolPointer()
		case isSet(config.DefaultDev):
			params.DefaultDev = config.DefaultDev.ValueBoolPointer()
		default:
			resp.Diagnostics.AddAttributeError(
				path.Root("project_id"),
				"Missing deployment selector",
				"A project usually holds more than one deployment, so looking one up by project_id also needs "+
					"a selector. Set one of reference, default_prod or default_dev.",
			)
			return
		}
		deployment, err = d.getInProject(ctx, config.ProjectID.ValueInt64(), params)
	}

	if err != nil {
		resp.Diagnostics.AddError("Error reading Convex deployment", err.Error())
		return
	}

	config.ID = types.Int64Value(deployment.Id)
	config.Name = types.StringValue(deployment.Name)
	config.ProjectID = types.Int64Value(deployment.ProjectId)
	config.Reference = types.StringValue(deployment.Reference)
	config.DeploymentType = types.StringValue(string(deployment.DeploymentType))
	config.Class = types.StringValue(deployment.Class)
	config.Region = types.StringValue(string(deployment.Region))
	config.IsDefault = types.BoolValue(deployment.IsDefault)
	config.ExpiresAt = types.Int64PointerValue(deployment.ExpiresAt)
	config.SendLogsToClient = types.BoolPointerValue(deployment.SendLogsToClient)
	config.DashboardEditConfirmation = types.BoolPointerValue(deployment.DashboardEditConfirmation)
	config.DeploymentURL = types.StringValue(deployment.DeploymentUrl)
	config.CreateTime = types.Int64Value(deployment.CreateTime)
	config.LastDeployTime = types.Int64PointerValue(deployment.LastDeployTime)
	config.Creator = types.Int64PointerValue(deployment.Creator)
	config.PreviewIdentifier = types.StringPointerValue(deployment.PreviewIdentifier)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func (d *deploymentDataSource) getByName(ctx context.Context, name string) (*convex.CloudDeployment, error) {
	got, err := d.client.PlatformGetDeploymentWithResponse(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := convex.CheckResponse(got); err != nil {
		return nil, fmt.Errorf("could not read deployment %q: %w", name, err)
	}
	if got.JSON200 == nil {
		return nil, fmt.Errorf("the API returned a successful status with no body for deployment %q", name)
	}
	return convex.AsCloudDeployment(got.JSON200)
}

func (d *deploymentDataSource) getInProject(ctx context.Context, projectID int64, params *convex.GetDeploymentInProjectByProjectIdParams) (*convex.CloudDeployment, error) {
	got, err := d.client.GetDeploymentInProjectByProjectIdWithResponse(ctx, projectID, params)
	if err != nil {
		return nil, err
	}
	if err := convex.CheckResponse(got); err != nil {
		return nil, fmt.Errorf("could not find a matching deployment in project %d: %w", projectID, err)
	}
	if got.JSON200 == nil {
		return nil, fmt.Errorf("the API returned a successful status with no body for project %d", projectID)
	}
	return convex.AsCloudDeployment(got.JSON200)
}
