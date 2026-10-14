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

	UniqueName     types.String `tfsdk:"unique_name"`
	ID             types.Int64  `tfsdk:"id"`
	CreationTime   types.Int64  `tfsdk:"creation_time"`
	LastUsedTime   types.Int64  `tfsdk:"last_used_time"`
	Creator        types.Int64  `tfsdk:"creator"`
	AllowedActions types.Set    `tfsdk:"allowed_actions"`
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
			"Convex makes deploy key names unique by appending a suffix: a key created as `previews` is " +
			"listed as `previews (<uuid>)`. The provider keeps `name` as configured and reports the full " +
			"listed name in `unique_name`. Convex has no get-by-id endpoint, so reads list the project's keys " +
			"and match on the stable `id`. A key with no `id` in state (an import, or " +
			"state written by v0.2.0) is adopted by an exact match on `unique_name`, else by name. If that " +
			"finds no single key, a key whose secret is in state is kept with a warning (destroy deletes it " +
			"by the secret); otherwise a miss drops it from state and an ambiguous name fails, listing the " +
			"candidates' ids. Names match when equal or when the listed one is the configured one plus a " +
			"` (<uuid>)` suffix.",
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
				MarkdownDescription: "Name the preview deploy key is created with. Convex appends a unique " +
					"suffix when storing it, which is reported in `unique_name`; this attribute keeps the " +
					"configured value. Changing this forces a new key.",
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
			"unique_name": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "The key's full name as Convex lists it, such as " +
					"`previews (cbd71ace-cdac-417a-b967-78b318339e2e)`. Convex appends the suffix to make names " +
					"unique; keys created before it did so have no suffix.",
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

	secret, key, created, err := createDeployKey(ctx, r.scope(projectID), args.Name,
		func(ctx context.Context) (string, error) {
			created, err := r.client.CreatePreviewDeployKeyWithResponse(ctx, projectID, args)

			if err != nil {
				return "", err
			}

			if err := convex.CheckResponse(created); err != nil {
				return "", err
			}

			if created.JSON200 == nil {
				return "", errors.New("the API returned a successful status with no deploy key body")
			}

			return created.JSON200.PreviewDeployKey, nil
		})

	if !created {
		resp.Diagnostics.AddError(
			"Error creating Convex preview deploy key",
			fmt.Sprintf("Could not create preview deploy key %q in project %d: %s", args.Name, projectID, err),
		)
		return
	}

	// This is the only moment the secret is available.
	plan.Key = types.StringValue(secret)
	tflog.Debug(ctx, "created Convex preview deploy key", map[string]any{
		"name": args.Name, "project_id": projectID,
	})

	// The create response carries the secret and nothing else. The key exists
	// from here on, so a failure must still write state -- losing the secret
	// would strand a key that cannot be recovered.
	if err != nil {
		plan.UniqueName = types.StringNull()
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
			fmt.Sprintf("Preview deploy key %q was created in project %d but could not be identified in the "+
				"project's key list: %s. The key and its secret have been saved to state; later refreshes keep "+
				"it, and destroying the resource deletes it by its secret.", args.Name, projectID, err),
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

	key, remove, diags := readDeployKey(ctx, r.scope(projectID), state.ref())
	resp.Diagnostics.Append(diags...)

	if remove {
		tflog.Debug(ctx, "Convex preview deploy key no longer exists, removing from state",
			map[string]any{"name": name, "project_id": projectID})
		resp.State.RemoveResource(ctx)
		return
	}

	if key == nil {
		// Kept as it is; the diagnostics say why.
		return
	}

	state.Name = adoptedDeployKeyName(state.ID, name, key)

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

	target, gone, err := deployKeyDeleteTarget(ctx, r.scope(projectID).list, state.ref())

	if gone {
		// Already gone, which is the desired end state.
		return
	}

	if err != nil {
		resp.Diagnostics.AddError(
			"Error deleting Convex preview deploy key",
			fmt.Sprintf("Could not find preview deploy key %q in project %d to delete it: %s", name, projectID, err),
		)
		return
	}

	deleted, err := r.client.DeletePreviewDeployKeyWithResponse(ctx, projectID,
		convex.PlatformDeletePreviewDeployKeyArgs{Id: target})

	if err != nil {
		resp.Diagnostics.AddError("Error deleting Convex preview deploy key", err.Error())
		return
	}

	if err := convex.CheckResponse(deleted); err != nil {
		if isNotFound(err) {
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
			fmt.Sprintf("Expected \"<project_id>/<key_name>\", got %q, where <key_name> is either the name the "+
				"key was created with or its full listed name. Import with: "+
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
	// The id stays null, so the read that follows the import looks the key up:
	// by an exact match on unique_name first, then by name match.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("unique_name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), types.StringNull())...)

	resp.Diagnostics.AddWarning(
		"Imported preview deploy key has no secret",
		fmt.Sprintf("Convex returns a preview deploy key's secret only when it is created, so the `key` "+
			"attribute of %q is null. To obtain a usable secret, replace the key with "+
			"`terraform apply -replace=...` once nothing depends on the old value.", name),
	)
}

// listPreviewDeployKeys returns every preview key of a project. There is no
// get-by-id endpoint, so every lookup goes through this.
func (r *previewDeployKeyResource) listPreviewDeployKeys(ctx context.Context, projectID int64) ([]convex.PlatformDeployKeyResponse, error) {
	// Managed keys belong to hosting integrations rather than to Terraform, but
	// listing them keeps a name match from silently skipping a key that
	// answers to the same name -- better an ambiguity error than a wrong
	// adoption.
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

	return listed.JSON200.Items, nil
}

// scope describes one project's preview key list to the shared lifecycle
// helpers.
func (r *previewDeployKeyResource) scope(projectID int64) deployKeyScope {
	return deployKeyScope{
		lockKey: fmt.Sprintf("project/%d", projectID),
		list: func(ctx context.Context) ([]convex.PlatformDeployKeyResponse, error) {
			return r.listPreviewDeployKeys(ctx, projectID)
		},
		kind:  "preview deploy key",
		where: fmt.Sprintf("in project %d", projectID),
		importCommand: fmt.Sprintf("terraform import convex_preview_deploy_key.<name> '%d/<full listed name>'",
			projectID),
	}
}

// ref extracts what the shared lookups need from state.
func (m previewDeployKeyResourceModel) ref() deployKeyRef {
	return deployKeyRef{ID: m.ID, UniqueName: m.UniqueName, Name: m.Name.ValueString(), Secret: m.Key}
}

// applyPreviewDeployKey copies list metadata onto the model, leaving the secret
// alone.
func applyPreviewDeployKey(ctx context.Context, model *previewDeployKeyResourceModel, key *convex.PlatformDeployKeyResponse) diag.Diagnostics {
	metadata, diags := readDeployKeyMetadata(ctx, key)

	if diags.HasError() {
		return diags
	}

	// Name stays as configured; the suffixed form Convex stores goes in
	// unique_name.
	model.UniqueName = types.StringValue(key.Name)
	model.ID = metadata.ID
	model.CreationTime = metadata.CreationTime
	model.LastUsedTime = metadata.LastUsedTime
	model.ExpiresAt = metadata.ExpiresAt
	model.Creator = metadata.Creator
	model.AllowedActions = metadata.AllowedActions
	return diags
}
