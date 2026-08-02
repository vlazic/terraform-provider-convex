package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// Ensure the implementation satisfies the framework interfaces.
var (
	_ resource.Resource                = (*projectResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectResource)(nil)
	_ resource.ResourceWithImportState = (*projectResource)(nil)
)

type projectResource struct {
	client *convex.ClientWithResponses
}

// NewProjectResource returns the convex_project resource implementation.
func NewProjectResource() resource.Resource {
	return &projectResource{}
}

// projectResourceModel maps the convex_project schema.
type projectResourceModel struct {
	ID       types.Int64  `tfsdk:"id"`
	TeamID   types.Int64  `tfsdk:"team_id"`
	Name     types.String `tfsdk:"name"`
	Slug     types.String `tfsdk:"slug"`
	TeamSlug types.String `tfsdk:"team_slug"`

	CreateTime         types.Int64  `tfsdk:"create_time"`
	ProdDeploymentName types.String `tfsdk:"prod_deployment_name"`
	DevDeploymentName  types.String `tfsdk:"dev_deployment_name"`
}

func (r *projectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Convex project within a team.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Numeric identifier of the project.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"team_id": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "Numeric identifier of the team that owns the project. Changing this forces a " +
					"new project, since Convex cannot move a project between teams.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Full name of the project as it appears in the dashboard. Spaces and " +
					"punctuation are allowed.",
			},
			"slug": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Short name used in Convex dashboard URLs. Generated from `name` when not " +
					"set. Note that Convex assigns the slug at creation time, so setting this on a new project " +
					"results in a second API call to rename it.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_slug": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Slug of the team that owns the project.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"create_time": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp, in milliseconds since the Unix epoch.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"prod_deployment_name": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Name of the project's default production deployment, if one exists " +
					"(for example `happy-otter-123`).",
			},
			"dev_deployment_name": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Name of the requesting member's default development deployment for this " +
					"project, if one exists.",
			},
		},
	}
}

func (r *projectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		// Provider has not been configured yet; the framework calls this again.
		return
	}
	client, ok := req.ProviderData.(*convex.ClientWithResponses)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected resource configure type",
			fmt.Sprintf("Expected *convex.ClientWithResponses, got %T. This is a bug in the provider.", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	args := convex.PlatformCreateProjectArgs{
		ProjectName: plan.Name.ValueString(),
	}
	teamID := plan.TeamID.ValueInt64()
	created, err := r.client.CreateProjectWithResponse(ctx, teamID, args)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Convex project", err.Error())
		return
	}
	if err := convex.CheckResponse(created); err != nil {
		resp.Diagnostics.AddError(
			"Error creating Convex project",
			fmt.Sprintf("Could not create project %q in team %d: %s", args.ProjectName, teamID, err),
		)
		return
	}
	if created.JSON200 == nil {
		resp.Diagnostics.AddError(
			"Error creating Convex project",
			"The API returned a successful status with no project body.",
		)
		return
	}

	projectID := created.JSON200.Id
	tflog.Debug(ctx, "created Convex project", map[string]any{"id": projectID, "slug": created.JSON200.Slug})

	// The create endpoint derives the slug from the name and takes no slug
	// parameter, so an explicitly configured slug needs a follow-up rename.
	var desiredSlug string
	if !plan.Slug.IsNull() && !plan.Slug.IsUnknown() && plan.Slug.ValueString() != created.JSON200.Slug {
		desiredSlug = plan.Slug.ValueString()
	}

	// The project exists from here on, so every remaining failure path still
	// has to leave state behind -- otherwise Terraform forgets the project and
	// leaks it. Seeding the model from the create response leaves no computed
	// attribute unknown, which is what makes that state writable.
	applyCreateResponse(&plan, teamID, created.JSON200)
	saveFallbackState := func() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}

	if desiredSlug != "" {
		updated, err := r.client.UpdateProjectWithResponse(ctx, projectID, convex.PlatformUpdateProjectArgs{
			Slug: &desiredSlug,
		})
		if err == nil {
			err = convex.CheckResponse(updated)
		}
		if err != nil {
			saveFallbackState()
			resp.Diagnostics.AddError(
				"Error setting Convex project slug",
				fmt.Sprintf("The project was created with ID %d as %q but could not be renamed to %q: %s. "+
					"It has been saved to state; re-apply to retry the rename.",
					projectID, created.JSON200.Slug, desiredSlug, err),
			)
			return
		}
	}

	// Read back rather than assembling state from the create response, which
	// omits the team slug, creation time and deployment names.
	project, err := r.getProject(ctx, projectID)
	if err != nil {
		saveFallbackState()
		resp.Diagnostics.AddError(
			"Error reading back created Convex project",
			fmt.Sprintf("The project was created with ID %d but could not be read: %s. "+
				"It has been saved to state; the remaining attributes are populated on the next refresh.",
				projectID, err),
		)
		return
	}

	applyProject(&plan, project)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	project, err := r.getProject(ctx, state.ID.ValueInt64())
	if err != nil {
		var apiErr *convex.APIError
		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			tflog.Debug(ctx, "Convex project no longer exists, removing from state",
				map[string]any{"id": state.ID.ValueInt64()})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading Convex project",
			fmt.Sprintf("Could not read project %d: %s", state.ID.ValueInt64(), err),
		)
		return
	}

	applyProject(&state, project)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Carry over the identifiers, which are computed and absent from the plan.
	plan.ID = state.ID
	projectID := plan.ID.ValueInt64()

	var args convex.PlatformUpdateProjectArgs
	if !plan.Name.Equal(state.Name) {
		args.Name = plan.Name.ValueStringPointer()
	}
	// An unknown slug means the practitioner removed it from configuration; the
	// existing server-side value stands rather than being reset.
	if !plan.Slug.IsUnknown() && !plan.Slug.Equal(state.Slug) {
		args.Slug = plan.Slug.ValueStringPointer()
	}

	if args.Name == nil && args.Slug == nil {
		// Nothing the API can act on -- just refresh the computed attributes.
		project, err := r.getProject(ctx, projectID)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error reading Convex project",
				fmt.Sprintf("Could not read project %d: %s", projectID, err),
			)
			return
		}
		applyProject(&plan, project)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	updated, err := r.client.UpdateProjectWithResponse(ctx, projectID, args)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Convex project", err.Error())
		return
	}
	if err := convex.CheckResponse(updated); err != nil {
		resp.Diagnostics.AddError(
			"Error updating Convex project",
			fmt.Sprintf("Could not update project %d: %s", projectID, err),
		)
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError(
			"Error updating Convex project",
			"The API returned a successful status with no project body.",
		)
		return
	}

	applyProject(&plan, updated.JSON200)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectID := state.ID.ValueInt64()
	deleted, err := r.client.DeleteProjectWithResponse(ctx, projectID)
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Convex project", err.Error())
		return
	}
	if err := convex.CheckResponse(deleted); err != nil {
		var apiErr *convex.APIError
		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			// Already gone, which is the desired end state.
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting Convex project",
			fmt.Sprintf("Could not delete project %d: %s", projectID, err),
		)
	}
}

func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	projectID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected a numeric Convex project ID, got %q. Import with: "+
				"terraform import convex_project.example 12345", req.ID),
		)
		return
	}
	// Read fills in the remaining attributes, including team_id.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), projectID)...)
}

// getProject fetches a project, returning an *convex.APIError for non-2xx
// responses so callers can distinguish a missing project from a real failure.
func (r *projectResource) getProject(ctx context.Context, projectID int64) (*convex.ProjectResponse, error) {
	got, err := r.client.GetProjectByIdWithResponse(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err := convex.CheckResponse(got); err != nil {
		return nil, err
	}
	if got.JSON200 == nil {
		return nil, errors.New("the API returned a successful status with no project body")
	}
	return got.JSON200, nil
}

// applyCreateResponse seeds the model from a create response, so that a state
// written before the read-back has no unknown values left in it. The
// attributes the create endpoint does not report are set to null and pick up
// their real values on the next refresh.
func applyCreateResponse(model *projectResourceModel, teamID int64, created *convex.PlatformCreateProjectResponse) {
	model.ID = types.Int64Value(created.Id)
	model.TeamID = types.Int64Value(teamID)
	model.Slug = types.StringValue(created.Slug)
	model.TeamSlug = types.StringNull()
	model.CreateTime = types.Int64Null()
	model.ProdDeploymentName = types.StringNull()
	model.DevDeploymentName = types.StringNull()
}

// applyProject copies an API response onto the Terraform model.
func applyProject(model *projectResourceModel, project *convex.ProjectResponse) {
	model.ID = types.Int64Value(project.Id)
	model.TeamID = types.Int64Value(project.TeamId)
	model.Name = types.StringValue(project.Name)
	model.Slug = types.StringValue(project.Slug)
	model.TeamSlug = types.StringValue(project.TeamSlug)
	model.CreateTime = types.Int64Value(project.CreateTime)
	model.ProdDeploymentName = types.StringPointerValue(project.ProdDeploymentName)
	model.DevDeploymentName = types.StringPointerValue(project.DevDeploymentName)
}
