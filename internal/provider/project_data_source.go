package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// Ensure the implementation satisfies the framework interfaces.
var (
	_ datasource.DataSource                     = (*projectDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*projectDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*projectDataSource)(nil)
)

type projectDataSource struct {
	client *convex.ClientWithResponses
}

// NewProjectDataSource returns the convex_project data source implementation.
func NewProjectDataSource() datasource.DataSource {
	return &projectDataSource{}
}

// projectDataSourceModel maps the convex_project data source schema. Every
// attribute is optional+computed, because most of them can serve either as the
// lookup key or as a result.
type projectDataSourceModel struct {
	ID       types.Int64  `tfsdk:"id"`
	Slug     types.String `tfsdk:"slug"`
	TeamID   types.Int64  `tfsdk:"team_id"`
	TeamSlug types.String `tfsdk:"team_slug"`

	Name               types.String `tfsdk:"name"`
	CreateTime         types.Int64  `tfsdk:"create_time"`
	ProdDeploymentName types.String `tfsdk:"prod_deployment_name"`
	DevDeploymentName  types.String `tfsdk:"dev_deployment_name"`
}

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a Convex project, either by its numeric ID or by its slug within a team.\n\n" +
			"Set `id`, or set `slug` together with one of `team_id` or `team_slug`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Numeric identifier of the project. Set this to look the project up by ID.",
			},
			"slug": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Short name used in Convex dashboard URLs. Set this, along with `team_id` " +
					"or `team_slug`, to look the project up by slug.",
			},
			"team_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Numeric identifier of the team that owns the project. Used together with " +
					"`slug` as a lookup key.",
			},
			"team_slug": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Slug of the team that owns the project. Used together with `slug` as a " +
					"lookup key.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Full project name, including spaces and punctuation.",
			},
			"create_time": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp, in milliseconds since the Unix epoch.",
			},
			"prod_deployment_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the project's default production deployment, if one exists.",
			},
			"dev_deployment_name": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Name of the requesting member's default development deployment for this " +
					"project, if one exists.",
			},
		},
	}
}

func (d *projectDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		// The two lookup keys are mutually exclusive, and one is required.
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("id"),
			path.MatchRoot("slug"),
		),
		// A slug is only unique within a team, so it needs exactly one team
		// identifier alongside it. That "alongside a slug" part cannot be
		// expressed here, so Read checks it.
		datasourcevalidator.Conflicting(
			path.MatchRoot("team_id"),
			path.MatchRoot("team_slug"),
		),
	}
}

func (d *projectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config projectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var project *convex.ProjectResponse
	var err error

	switch {
	case isSet(config.ID):
		project, err = d.getByID(ctx, config.ID.ValueInt64())
	case isSet(config.TeamID):
		project, err = d.getBySlug(ctx, strconv.FormatInt(config.TeamID.ValueInt64(), 10), config.Slug.ValueString())
	case isSet(config.TeamSlug):
		project, err = d.getBySlug(ctx, config.TeamSlug.ValueString(), config.Slug.ValueString())
	default:
		resp.Diagnostics.AddAttributeError(
			path.Root("slug"),
			"Missing team identifier",
			"Looking a project up by slug also needs the team it belongs to. Set either team_id or team_slug.",
		)
		return
	}

	if err != nil {
		resp.Diagnostics.AddError("Error reading Convex project", err.Error())
		return
	}

	config.ID = types.Int64Value(project.Id)
	config.Slug = types.StringValue(project.Slug)
	config.TeamID = types.Int64Value(project.TeamId)
	config.TeamSlug = types.StringValue(project.TeamSlug)
	config.Name = types.StringValue(project.Name)
	config.CreateTime = types.Int64Value(project.CreateTime)
	config.ProdDeploymentName = types.StringPointerValue(project.ProdDeploymentName)
	config.DevDeploymentName = types.StringPointerValue(project.DevDeploymentName)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func (d *projectDataSource) getByID(ctx context.Context, projectID int64) (*convex.ProjectResponse, error) {
	got, err := d.client.GetProjectByIdWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := convex.CheckResponse(got); err != nil {
		return nil, fmt.Errorf("could not read project %d: %w", projectID, err)
	}
	if got.JSON200 == nil {
		return nil, fmt.Errorf("the API returned a successful status with no body for project %d", projectID)
	}
	return got.JSON200, nil
}

func (d *projectDataSource) getBySlug(ctx context.Context, teamIDOrSlug, projectSlug string) (*convex.ProjectResponse, error) {
	got, err := d.client.GetProjectBySlugWithResponse(ctx, teamIDOrSlug, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := convex.CheckResponse(got); err != nil {
		return nil, fmt.Errorf("could not read project %q in team %q: %w", projectSlug, teamIDOrSlug, err)
	}
	if got.JSON200 == nil {
		return nil, fmt.Errorf("the API returned a successful status with no body for project %q", projectSlug)
	}
	return got.JSON200, nil
}
