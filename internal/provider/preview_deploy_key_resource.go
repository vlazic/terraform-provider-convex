package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
	_ resource.Resource                = (*previewDeployKeyResource)(nil)
	_ resource.ResourceWithConfigure   = (*previewDeployKeyResource)(nil)
	_ resource.ResourceWithImportState = (*previewDeployKeyResource)(nil)
)

type previewDeployKeyResource struct {
	client *convex.ClientWithResponses
}

// NewPreviewDeployKeyResource returns the convex_preview_deploy_key resource.
func NewPreviewDeployKeyResource() resource.Resource {
	return &previewDeployKeyResource{}
}

// previewDeployKeyResourceModel maps the convex_preview_deploy_key schema.
type previewDeployKeyResourceModel struct {
	ProjectID types.Int64  `tfsdk:"project_id"`
	Name      types.String `tfsdk:"name"`
	ExpiresAt types.Int64  `tfsdk:"expires_at"`

	Key types.String `tfsdk:"key"`

	ID             types.Int64 `tfsdk:"id"`
	CreationTime   types.Int64 `tfsdk:"creation_time"`
	LastUsedTime   types.Int64 `tfsdk:"last_used_time"`
	Creator        types.Int64 `tfsdk:"creator"`
	AllowedActions types.Set   `tfsdk:"allowed_actions"`
}

func (r *previewDeployKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_preview_deploy_key"
}

func (r *previewDeployKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a preview deploy key for a Convex project. Preview deploy keys let a CI or " +
			"hosting integration spin up preview deployments, so they belong to a project rather than to any " +
			"one deployment.\n\n" +
			"~> **The generated key is stored in Terraform state in plain text.** Convex returns the secret " +
			"only once, at creation, so it cannot be read back. Treat the state file as a secret.\n\n" +
			"Convex offers no endpoint for changing a preview deploy key, so every attribute forces " +
			"replacement.\n\n" +
			"Convex has no get-by-id endpoint, so reads list the project's keys and match on `name`. Names " +
			"must therefore be unique within the project.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "Numeric identifier of the project the key belongs to. Changing this " +
					"forces a new key.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Name of the preview deploy key. Must be unique within the project, since " +
					"the API uses it as an identifier. Changing this forces a new key.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
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
				MarkdownDescription: "The generated preview deploy key. Available only on the apply that " +
					"creates it; an imported key has this set to null.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Stable numeric identifier of the preview deploy key.",
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
			"allowed_actions": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Actions the key is allowed to perform. Preview deploy keys are not " +
					"configurable in this respect, so this reports whatever Convex granted.",
			},
		},
	}
}

func (r *previewDeployKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data, ok := providerDataFrom(req.ProviderData, &resp.Diagnostics)

	if !ok {
		return
	}

	r.client = data.Management
}

func (r *previewDeployKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan previewDeployKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	args := convex.PlatformCreatePreviewDeployKeyArgs{
		Name: plan.Name.ValueString(),
	}

	if isSet(plan.ExpiresAt) {
		args.ExpiresAt = plan.ExpiresAt.ValueInt64Pointer()
	}

	projectID := plan.ProjectID.ValueInt64()
	created, err := r.client.CreatePreviewDeployKeyWithResponse(ctx, projectID, args)

	if err != nil {
		resp.Diagnostics.AddError("Error creating Convex preview deploy key", err.Error())
		return
	}

	if err := convex.CheckResponse(created); err != nil {
		resp.Diagnostics.AddError(
			"Error creating Convex preview deploy key",
			fmt.Sprintf("Could not create preview deploy key %q in project %d: %s", args.Name, projectID, err),
		)
		return
	}

	if created.JSON200 == nil {
		resp.Diagnostics.AddError(
			"Error creating Convex preview deploy key",
			"The API returned a successful status with no deploy key body.",
		)
		return
	}

	// This is the only moment the secret is available.
	plan.Key = types.StringValue(created.JSON200.PreviewDeployKey)
	tflog.Debug(ctx, "created Convex preview deploy key", map[string]any{
		"name": args.Name, "project_id": projectID,
	})

	// The create response carries the secret and nothing else. The key exists
	// from here on, so a failure must still write state -- losing the secret
	// would strand a key that cannot be recovered.
	key, err := r.getPreviewDeployKey(ctx, projectID, args.Name)

	if err != nil {
		plan.ID = types.Int64Null()
		plan.CreationTime = types.Int64Null()
		plan.LastUsedTime = types.Int64Null()
		plan.Creator = types.Int64Null()
		plan.AllowedActions = types.SetNull(types.StringType)

		if plan.ExpiresAt.IsUnknown() {
			plan.ExpiresAt = types.Int64Null()
		}

		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError(
			"Error reading back created preview deploy key",
			fmt.Sprintf("Preview deploy key %q was created in project %d but its metadata could not be read: "+
				"%s. The key has been saved to state; the remaining attributes are populated on the next "+
				"refresh.", args.Name, projectID, err),
		)
		return
	}

	resp.Diagnostics.Append(applyPreviewDeployKey(ctx, &plan, key)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *previewDeployKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state previewDeployKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	projectID := state.ProjectID.ValueInt64()
	name := state.Name.ValueString()

	key, err := r.getPreviewDeployKey(ctx, projectID, name)

	if err != nil {
		var apiErr *convex.APIError

		if errors.Is(err, errDeployKeyNotFound) || (errors.As(err, &apiErr) && apiErr.IsNotFound()) {
			tflog.Debug(ctx, "Convex preview deploy key no longer exists, removing from state",
				map[string]any{"name": name, "project_id": projectID})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error reading Convex preview deploy key",
			fmt.Sprintf("Could not read preview deploy key %q in project %d: %s", name, projectID, err),
		)
		return
	}

	// The secret in state is left untouched; the list endpoint never returns it.
	resp.Diagnostics.Append(applyPreviewDeployKey(ctx, &state, key)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only runs for changes the plan did not mark as replacements, and every
// configurable attribute here forces replacement.
func (r *previewDeployKeyResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Preview deploy keys cannot be updated",
		"Convex offers no endpoint for changing a preview deploy key, so every attribute should force "+
			"replacement. Reaching this point is a bug in the provider.",
	)
}

func (r *previewDeployKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state previewDeployKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	projectID := state.ProjectID.ValueInt64()
	name := state.Name.ValueString()

	deleted, err := r.client.DeletePreviewDeployKeyWithResponse(ctx, projectID,
		convex.PlatformDeletePreviewDeployKeyArgs{Id: name})

	if err != nil {
		resp.Diagnostics.AddError("Error deleting Convex preview deploy key", err.Error())
		return
	}

	if err := convex.CheckResponse(deleted); err != nil {
		var apiErr *convex.APIError

		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			return
		}

		resp.Diagnostics.AddError(
			"Error deleting Convex preview deploy key",
			fmt.Sprintf("Could not delete preview deploy key %q in project %d: %s", name, projectID, err),
		)
	}
}

func (r *previewDeployKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	rawProjectID, name, ok := strings.Cut(req.ID, "/")

	if !ok || rawProjectID == "" || name == "" {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected \"<project_id>/<key_name>\", got %q. Import with: "+
				"terraform import convex_preview_deploy_key.ci 12345/ci", req.ID),
		)
		return
	}

	projectID, err := strconv.ParseInt(rawProjectID, 10, 64)

	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected a numeric project ID before the \"/\", got %q.", rawProjectID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), types.StringNull())...)

	resp.Diagnostics.AddWarning(
		"Imported preview deploy key has no secret",
		fmt.Sprintf("Convex returns a preview deploy key's secret only when it is created, so the `key` "+
			"attribute of %q is null. To obtain a usable secret, replace the key with "+
			"`terraform apply -replace=...` once nothing depends on the old value.", name),
	)
}

// getPreviewDeployKey finds a preview key by name. There is no get-by-id
// endpoint, so this lists and filters.
func (r *previewDeployKeyResource) getPreviewDeployKey(ctx context.Context, projectID int64, name string) (*convex.PlatformDeployKeyResponse, error) {
	// Managed keys belong to hosting integrations rather than to Terraform, but
	// they still occupy a name, so include them to avoid a confusing
	// "not found" when the name is actually taken.
	includeManaged := true
	listed, err := r.client.ListPreviewDeployKeysWithResponse(ctx, projectID,
		&convex.ListPreviewDeployKeysParams{IncludeManaged: &includeManaged})

	if err != nil {
		return nil, err
	}

	if err := convex.CheckResponse(listed); err != nil {
		return nil, err
	}

	if listed.JSON200 == nil {
		return nil, errors.New("the API returned a successful status with no deploy key list")
	}

	return findDeployKeyByName(listed.JSON200.Items, name)
}

// applyPreviewDeployKey copies list metadata onto the model, leaving the secret
// alone.
func applyPreviewDeployKey(ctx context.Context, model *previewDeployKeyResourceModel, key *convex.PlatformDeployKeyResponse) diag.Diagnostics {
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
