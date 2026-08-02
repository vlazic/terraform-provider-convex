package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// Ensure the implementation satisfies the framework interfaces.
var (
	_ resource.Resource                = (*deploymentResource)(nil)
	_ resource.ResourceWithConfigure   = (*deploymentResource)(nil)
	_ resource.ResourceWithImportState = (*deploymentResource)(nil)
)

type deploymentResource struct {
	client *convex.ClientWithResponses
}

// NewDeploymentResource returns the convex_deployment resource implementation.
func NewDeploymentResource() resource.Resource {
	return &deploymentResource{}
}

// deploymentResourceModel maps the convex_deployment schema.
type deploymentResourceModel struct {
	ID        types.Int64  `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	ProjectID types.Int64  `tfsdk:"project_id"`

	DeploymentType types.String `tfsdk:"deployment_type"`
	Class          types.String `tfsdk:"class"`
	Region         types.String `tfsdk:"region"`
	Reference      types.String `tfsdk:"reference"`
	IsDefault      types.Bool   `tfsdk:"is_default"`

	ExpiresAt                 types.Int64 `tfsdk:"expires_at"`
	SendLogsToClient          types.Bool  `tfsdk:"send_logs_to_client"`
	DashboardEditConfirmation types.Bool  `tfsdk:"dashboard_edit_confirmation"`

	DeploymentURL     types.String `tfsdk:"deployment_url"`
	CreateTime        types.Int64  `tfsdk:"create_time"`
	LastDeployTime    types.Int64  `tfsdk:"last_deploy_time"`
	Creator           types.Int64  `tfsdk:"creator"`
	PreviewIdentifier types.String `tfsdk:"preview_identifier"`
}

func (r *deploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deployment"
}

func (r *deploymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a cloud deployment within a Convex project.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Numeric identifier of the deployment.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Readable identifier assigned by Convex, such as `playful-otter-123`. " +
					"This is the value the API addresses the deployment by, and the value to use when importing.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"project_id": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "Numeric identifier of the project that owns the deployment. Changing this " +
					"forces a new deployment.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"deployment_type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Type of the deployment. One of `dev`, `prod`, `preview` or `custom`. " +
					"This can be changed in place; when changing the type of a default deployment, set " +
					"`is_default` explicitly at the same time.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(convex.DeploymentTypeDev),
						string(convex.DeploymentTypeProd),
						string(convex.DeploymentTypePreview),
						string(convex.DeploymentTypeCustom),
					),
				},
			},
			"class": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Deployment class, for example `s16` or `s256`. Defaults to the team's " +
					"default class. Changing it requires the deployment class selection entitlement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"region": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Hosting region, one of `aws-us-east-1` or `aws-eu-west-1`. Defaults to the " +
					"team's default region. Convex cannot move a deployment between regions, so changing this " +
					"forces a new deployment.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(convex.AwsUsEast1),
						string(convex.AwsEuWest1),
					),
				},
				PlanModifiers: []planmodifier.String{
					// Only replace when the practitioner chose the region. A
					// server-assigned default must not trigger replacement.
					stringplanmodifier.RequiresReplaceIfConfigured(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"reference": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Identifier that uniquely identifies this deployment within the project. " +
					"Supplying one lets a project hold several dev or prod deployments. Generated by Convex " +
					"when not set.",
				Validators: []validator.String{
					deploymentReferenceValidator{},
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"is_default": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "For a `prod` deployment, whether it is the project's default production " +
					"deployment (the one `npx convex deploy` uses). For a `dev` deployment, whether it is the " +
					"default development deployment of the member that created it. Cannot be set on `preview` " +
					"or `custom` deployments.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"expires_at": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Timestamp in milliseconds at which Convex deletes this deployment. Must be " +
					"at least 30 minutes in the future and within the team's preview deployment retention " +
					"entitlement. Preview deployments get one by default.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"send_logs_to_client": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Whether function logs are sent to the client. Defaults to `true` for `dev` " +
					"and `preview` deployments and `false` for `prod`.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"dashboard_edit_confirmation": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Whether the dashboard asks for confirmation before allowing edits during a " +
					"browser session. Defaults to `true` for `prod` deployments and `false` for `dev` and " +
					"`preview`.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"deployment_url": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Backend URL of the deployment, for example " +
					"`https://playful-otter-123.convex.cloud`. This is always a `.convex.cloud` URL, even when " +
					"custom domains are configured.",
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
			"last_deploy_time": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Timestamp of the last push to this deployment, in milliseconds since the " +
					"Unix epoch, or null if nothing has been deployed yet. This changes outside Terraform " +
					"whenever code is deployed.",
			},
			"creator": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Numeric member ID of whoever created the deployment.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"preview_identifier": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Preview identifier, set only on `preview` deployments.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *deploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
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

func (r *deploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deploymentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	args := convex.PlatformCreateDeploymentArgs{
		Type: convex.CreateDeploymentType(plan.DeploymentType.ValueString()),
	}
	// Optional+computed attributes arrive as unknown when not configured, and
	// unknown must not be sent as a value.
	if isSet(plan.Class) {
		args.Class = plan.Class.ValueStringPointer()
	}
	if isSet(plan.Region) {
		region := convex.RegionName(plan.Region.ValueString())
		args.Region = &region
	}
	if isSet(plan.Reference) {
		args.Reference = plan.Reference.ValueStringPointer()
	}
	if isSet(plan.IsDefault) {
		args.IsDefault = plan.IsDefault.ValueBoolPointer()
	}
	if isSet(plan.ExpiresAt) {
		args.ExpiresAt = plan.ExpiresAt.ValueInt64Pointer()
	}

	projectID := plan.ProjectID.ValueInt64()
	created, err := r.client.CreateDeploymentWithResponse(ctx, projectID, args)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Convex deployment", err.Error())
		return
	}
	if err := convex.CheckResponse(created); err != nil {
		resp.Diagnostics.AddError(
			"Error creating Convex deployment",
			fmt.Sprintf("Could not create a %s deployment in project %d: %s",
				plan.DeploymentType.ValueString(), projectID, err),
		)
		return
	}
	if created.JSON200 == nil {
		resp.Diagnostics.AddError(
			"Error creating Convex deployment",
			"The API returned a successful status with no deployment body.",
		)
		return
	}

	deployment, err := convex.AsCloudDeployment(created.JSON200)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Convex deployment", err.Error())
		return
	}
	tflog.Debug(ctx, "created Convex deployment", map[string]any{
		"name": deployment.Name, "project_id": projectID,
	})

	// The create response is a full deployment, so there is nothing to read
	// back. Some attributes still need a follow-up PATCH: the create endpoint
	// accepts neither of these two.
	needsUpdate := isSet(plan.SendLogsToClient) || isSet(plan.DashboardEditConfirmation)

	applyDeployment(&plan, deployment)
	if !needsUpdate {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	// The deployment exists now, so a failure past this point still has to
	// write state or Terraform loses track of it.
	var settings convex.PlatformUpdateDeploymentArgs
	if isSet(plan.SendLogsToClient) {
		settings.SendLogsToClient = plan.SendLogsToClient.ValueBoolPointer()
	}
	if isSet(plan.DashboardEditConfirmation) {
		settings.DashboardEditConfirmation = plan.DashboardEditConfirmation.ValueBoolPointer()
	}

	updated, err := r.updateAndRefresh(ctx, deployment.Name, settings)
	if err != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError(
			"Error applying deployment settings",
			fmt.Sprintf("Deployment %q was created but its settings could not be applied: %s. "+
				"It has been saved to state; re-apply to retry.", deployment.Name, err),
		)
		return
	}

	applyDeployment(&plan, updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deploymentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	deployment, err := r.getDeployment(ctx, name)
	if err != nil {
		var apiErr *convex.APIError
		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			tflog.Debug(ctx, "Convex deployment no longer exists, removing from state",
				map[string]any{"name": name})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading Convex deployment",
			fmt.Sprintf("Could not read deployment %q: %s", name, err),
		)
		return
	}

	applyDeployment(&state, deployment)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *deploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state deploymentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Carry over the identifiers, which are computed and absent from the plan.
	plan.ID = state.ID
	plan.Name = state.Name
	name := plan.Name.ValueString()

	var args convex.PlatformUpdateDeploymentArgs
	var changed bool
	if changedAndSet(plan.DeploymentType, state.DeploymentType) {
		deploymentType := convex.DeploymentType(plan.DeploymentType.ValueString())
		args.DeploymentType = &deploymentType
		changed = true
	}
	if changedAndSet(plan.Class, state.Class) {
		args.Class = plan.Class.ValueStringPointer()
		changed = true
	}
	if changedAndSet(plan.Reference, state.Reference) {
		args.Reference = plan.Reference.ValueStringPointer()
		changed = true
	}
	if changedAndSet(plan.IsDefault, state.IsDefault) {
		args.IsDefault = plan.IsDefault.ValueBoolPointer()
		changed = true
	}
	if changedAndSet(plan.ExpiresAt, state.ExpiresAt) {
		args.ExpiresAt = plan.ExpiresAt.ValueInt64Pointer()
		changed = true
	}
	if changedAndSet(plan.SendLogsToClient, state.SendLogsToClient) {
		args.SendLogsToClient = plan.SendLogsToClient.ValueBoolPointer()
		changed = true
	}
	if changedAndSet(plan.DashboardEditConfirmation, state.DashboardEditConfirmation) {
		args.DashboardEditConfirmation = plan.DashboardEditConfirmation.ValueBoolPointer()
		changed = true
	}

	var deployment *convex.CloudDeployment
	var err error
	if changed {
		deployment, err = r.updateAndRefresh(ctx, name, args)
	} else {
		// Nothing the API can act on -- just refresh the computed attributes.
		deployment, err = r.getDeployment(ctx, name)
	}
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating Convex deployment",
			fmt.Sprintf("Could not update deployment %q: %s", name, err),
		)
		return
	}

	applyDeployment(&plan, deployment)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state deploymentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	deleted, err := r.client.DeleteDeploymentWithResponse(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Convex deployment", err.Error())
		return
	}
	if err := convex.CheckResponse(deleted); err != nil {
		var apiErr *convex.APIError
		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			// Already gone, which is the desired end state.
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting Convex deployment",
			fmt.Sprintf("Could not delete deployment %q: %s", name, err),
		)
	}
}

func (r *deploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			"Expected a Convex deployment name. Import with: "+
				"terraform import convex_deployment.example playful-otter-123",
		)
		return
	}
	// Read fills in the remaining attributes, including project_id.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

// updateAndRefresh applies a patch and re-reads the deployment. The PATCH
// endpoint returns an empty body, so the refreshed state has to come from a
// follow-up GET.
func (r *deploymentResource) updateAndRefresh(ctx context.Context, name string, args convex.PlatformUpdateDeploymentArgs) (*convex.CloudDeployment, error) {
	updated, err := r.client.UpdateDeploymentWithResponse(ctx, name, args)
	if err != nil {
		return nil, err
	}
	if err := convex.CheckResponse(updated); err != nil {
		return nil, err
	}
	return r.getDeployment(ctx, name)
}

// getDeployment fetches a deployment, returning an *convex.APIError for non-2xx
// responses so callers can distinguish a missing deployment from a real failure.
func (r *deploymentResource) getDeployment(ctx context.Context, name string) (*convex.CloudDeployment, error) {
	got, err := r.client.PlatformGetDeploymentWithResponse(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := convex.CheckResponse(got); err != nil {
		return nil, err
	}
	if got.JSON200 == nil {
		return nil, errors.New("the API returned a successful status with no deployment body")
	}
	return convex.AsCloudDeployment(got.JSON200)
}

// applyDeployment copies an API response onto the Terraform model.
func applyDeployment(model *deploymentResourceModel, d *convex.CloudDeployment) {
	model.ID = types.Int64Value(d.Id)
	model.Name = types.StringValue(d.Name)
	model.ProjectID = types.Int64Value(d.ProjectId)
	model.DeploymentType = types.StringValue(string(d.DeploymentType))
	model.Class = types.StringValue(d.Class)
	model.Region = types.StringValue(string(d.Region))
	model.Reference = types.StringValue(d.Reference)
	model.IsDefault = types.BoolValue(d.IsDefault)
	model.ExpiresAt = types.Int64PointerValue(d.ExpiresAt)
	model.SendLogsToClient = types.BoolPointerValue(d.SendLogsToClient)
	model.DashboardEditConfirmation = types.BoolPointerValue(d.DashboardEditConfirmation)
	model.DeploymentURL = types.StringValue(d.DeploymentUrl)
	model.CreateTime = types.Int64Value(d.CreateTime)
	model.LastDeployTime = types.Int64PointerValue(d.LastDeployTime)
	model.Creator = types.Int64PointerValue(d.Creator)
	model.PreviewIdentifier = types.StringPointerValue(d.PreviewIdentifier)
}

// isSet reports whether a value carries something worth sending to the API.
// Optional+computed attributes are unknown in the plan when not configured.
func isSet(v attr.Value) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// changedAndSet reports whether a planned value is known and differs from the
// prior state, i.e. whether it belongs in a patch body.
func changedAndSet(plan, state attr.Value) bool {
	return isSet(plan) && !plan.Equal(state)
}
