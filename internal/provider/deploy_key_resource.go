package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// Ensure the implementation satisfies the framework interfaces.
var (
	_ resource.Resource                = (*deployKeyResource)(nil)
	_ resource.ResourceWithConfigure   = (*deployKeyResource)(nil)
	_ resource.ResourceWithImportState = (*deployKeyResource)(nil)
)

type deployKeyResource struct {
	client *convex.ClientWithResponses
}

// NewDeployKeyResource returns the convex_deploy_key resource implementation.
func NewDeployKeyResource() resource.Resource {
	return &deployKeyResource{}
}

// deployKeyResourceModel maps the convex_deploy_key schema.
type deployKeyResourceModel struct {
	DeploymentName types.String `tfsdk:"deployment_name"`
	Name           types.String `tfsdk:"name"`
	AllowedActions types.Set    `tfsdk:"allowed_actions"`
	ExpiresAt      types.Int64  `tfsdk:"expires_at"`

	Key types.String `tfsdk:"key"`

	ID           types.Int64 `tfsdk:"id"`
	CreationTime types.Int64 `tfsdk:"creation_time"`
	LastUsedTime types.Int64 `tfsdk:"last_used_time"`
	Creator      types.Int64 `tfsdk:"creator"`
}

func (r *deployKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deploy_key"
}

func (r *deployKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a deploy key for a Convex deployment.\n\n" +
			"~> **The generated key is stored in Terraform state in plain text.** Convex returns the secret " +
			"only once, at creation, so it cannot be read back and there is no way for the provider to avoid " +
			"persisting it. Treat the state file as a secret, and prefer a remote backend with encryption at " +
			"rest.\n\n" +
			"Convex offers no endpoint for changing a deploy key, so every attribute forces replacement. " +
			"Replacing a key creates the new one and destroys the old one, which will break anything still " +
			"using the old value.\n\n" +
			"Convex has no get-by-id endpoint for deploy keys, so reads list the deployment's keys and match " +
			"on `name`. Names must therefore be unique within the deployment.",
		Attributes: map[string]schema.Attribute{
			"deployment_name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Name of the deployment the key grants access to, such as " +
					"`playful-otter-123`. Changing this forces a new key.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Name of the deploy key. Must be unique within the deployment, since the " +
					"API uses it as an identifier. Changing this forces a new key.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"allowed_actions": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Actions the key is allowed to perform, for example `deployment:deploy`. " +
					"Defaults to whatever Convex grants a key created without restrictions. Changing this " +
					"forces a new key.",
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.OneOf(deployKeyActions...)),
					setvalidator.SizeAtLeast(1),
				},
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.RequiresReplaceIfConfigured(),
					setplanmodifier.UseStateForUnknown(),
				},
			},
			"expires_at": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Timestamp in milliseconds at which the key expires. Must be at least 30 " +
					"minutes in the future. Changing this forces a new key.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplaceIfConfigured(),
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"key": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				MarkdownDescription: "The generated deploy key. Available only on the apply that creates it; " +
					"an imported key has this set to null, because Convex cannot return it again.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Stable numeric identifier of the deploy key.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"creation_time": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp, in milliseconds since the Unix epoch.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"last_used_time": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Timestamp of the last use of this key, or null if it has never been " +
					"used. Changes outside Terraform whenever the key is used.",
			},
			"creator": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Numeric member ID of whoever created the key.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *deployKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *deployKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deployKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	allowedActions, diags := toCreateActions(ctx, plan.AllowedActions)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	args := convex.PlatformCreateDeployKeyArgs{
		Name:           plan.Name.ValueString(),
		AllowedActions: allowedActions,
	}

	if isSet(plan.ExpiresAt) {
		args.ExpiresAt = plan.ExpiresAt.ValueInt64Pointer()
	}

	deploymentName := plan.DeploymentName.ValueString()
	created, err := r.client.CreateDeployKeyWithResponse(ctx, deploymentName, args)

	if err != nil {
		resp.Diagnostics.AddError("Error creating Convex deploy key", err.Error())
		return
	}

	if err := convex.CheckResponse(created); err != nil {
		resp.Diagnostics.AddError(
			"Error creating Convex deploy key",
			fmt.Sprintf("Could not create deploy key %q on deployment %q: %s",
				args.Name, deploymentName, err),
		)
		return
	}

	if created.JSON200 == nil {
		resp.Diagnostics.AddError(
			"Error creating Convex deploy key",
			"The API returned a successful status with no deploy key body.",
		)
		return
	}

	// This is the only moment the secret is available.
	plan.Key = types.StringValue(created.JSON200.DeployKey)
	tflog.Debug(ctx, "created Convex deploy key", map[string]any{
		"name": args.Name, "deployment": deploymentName,
	})

	// The create response carries the secret and nothing else, so the metadata
	// has to come from the list endpoint. The key exists from here on, so a
	// failure must still write state -- losing the secret would mean a key that
	// can never be recovered or cleanly destroyed.
	key, err := r.getDeployKey(ctx, deploymentName, args.Name)

	if err != nil {
		plan.ID = types.Int64Null()
		plan.CreationTime = types.Int64Null()
		plan.LastUsedTime = types.Int64Null()
		plan.Creator = types.Int64Null()

		if plan.AllowedActions.IsUnknown() {
			plan.AllowedActions = types.SetNull(types.StringType)
		}

		if plan.ExpiresAt.IsUnknown() {
			plan.ExpiresAt = types.Int64Null()
		}

		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError(
			"Error reading back created deploy key",
			fmt.Sprintf("Deploy key %q was created on deployment %q but its metadata could not be read: %s. "+
				"The key has been saved to state; the remaining attributes are populated on the next refresh.",
				args.Name, deploymentName, err),
		)
		return
	}

	resp.Diagnostics.Append(applyDeployKey(ctx, &plan, key)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deployKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deployKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	deploymentName := state.DeploymentName.ValueString()
	name := state.Name.ValueString()

	key, err := r.getDeployKey(ctx, deploymentName, name)

	if err != nil {
		var apiErr *convex.APIError

		if errors.Is(err, errDeployKeyNotFound) || (errors.As(err, &apiErr) && apiErr.IsNotFound()) {
			tflog.Debug(ctx, "Convex deploy key no longer exists, removing from state",
				map[string]any{"name": name, "deployment": deploymentName})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error reading Convex deploy key",
			fmt.Sprintf("Could not read deploy key %q on deployment %q: %s", name, deploymentName, err),
		)
		return
	}

	// Note that the secret in state is left untouched: the list endpoint does
	// not return it, and it can never be fetched again.
	resp.Diagnostics.Append(applyDeployKey(ctx, &state, key)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only ever runs for changes the plan did not mark as replacements.
// Every configurable attribute forces replacement, so reaching it means the
// schema and this method have drifted apart.
func (r *deployKeyResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Deploy keys cannot be updated",
		"Convex offers no endpoint for changing a deploy key, so every attribute should force replacement. "+
			"Reaching this point is a bug in the provider.",
	)
}

func (r *deployKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state deployKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	deploymentName := state.DeploymentName.ValueString()
	name := state.Name.ValueString()

	// The delete endpoint accepts either the secret or the name; the name is
	// always available, including on imported keys.
	deleted, err := r.client.DeleteDeployKeyWithResponse(ctx, deploymentName,
		convex.PlatformDeleteDeployKeyArgs{Id: name})

	if err != nil {
		resp.Diagnostics.AddError("Error deleting Convex deploy key", err.Error())
		return
	}

	if err := convex.CheckResponse(deleted); err != nil {
		var apiErr *convex.APIError

		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			// Already gone, which is the desired end state.
			return
		}

		resp.Diagnostics.AddError(
			"Error deleting Convex deploy key",
			fmt.Sprintf("Could not delete deploy key %q on deployment %q: %s", name, deploymentName, err),
		)
	}
}

func (r *deployKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	deploymentName, name, ok := strings.Cut(req.ID, "/")

	if !ok || deploymentName == "" || name == "" {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected \"<deployment_name>/<key_name>\", got %q. Import with: "+
				"terraform import convex_deploy_key.ci playful-otter-123/ci", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deployment_name"), deploymentName)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	// Recording the secret as explicitly absent keeps later plans quiet; an
	// unknown value here would never resolve.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), types.StringNull())...)

	resp.Diagnostics.AddWarning(
		"Imported deploy key has no secret",
		fmt.Sprintf("Convex returns a deploy key's secret only when it is created, so the `key` attribute of "+
			"%q is null and anything referencing it will read as null. To obtain a usable secret, replace the "+
			"key with `terraform apply -replace=...` once nothing depends on the old value.", name),
	)
}

// getDeployKey finds a key by name. The API has no get-by-id endpoint for
// deploy keys, so this lists and filters.
func (r *deployKeyResource) getDeployKey(ctx context.Context, deploymentName, name string) (*convex.PlatformDeployKeyResponse, error) {
	listed, err := r.client.ListDeployKeysWithResponse(ctx, deploymentName)

	if err != nil {
		return nil, err
	}

	if err := convex.CheckResponse(listed); err != nil {
		return nil, err
	}

	if listed.JSON200 == nil {
		return nil, errors.New("the API returned a successful status with no deploy key list")
	}

	return findDeployKeyByName(*listed.JSON200, name)
}

// applyDeployKey copies list metadata onto the model, leaving the secret alone.
func applyDeployKey(ctx context.Context, model *deployKeyResourceModel, key *convex.PlatformDeployKeyResponse) diag.Diagnostics {
	metadata, diags := readDeployKeyMetadata(ctx, key)

	if diags.HasError() {
		return diags
	}

	model.Name = types.StringValue(key.Name)
	model.ID = metadata.ID
	model.CreationTime = metadata.CreationTime
	model.LastUsedTime = metadata.LastUsedTime
	model.ExpiresAt = metadata.ExpiresAt
	model.Creator = metadata.Creator
	model.AllowedActions = metadata.AllowedActions
	return diags
}
