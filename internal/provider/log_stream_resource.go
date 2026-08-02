package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/srs/terraform-provider-convex/internal/deployapi"
)

// Ensure the implementation satisfies the framework interfaces.
var (
	_ resource.Resource                     = (*logStreamResource)(nil)
	_ resource.ResourceWithConfigure        = (*logStreamResource)(nil)
	_ resource.ResourceWithImportState      = (*logStreamResource)(nil)
	_ resource.ResourceWithConfigValidators = (*logStreamResource)(nil)
)

type logStreamResource struct {
	token string
}

// NewLogStreamResource returns the convex_log_stream resource.
func NewLogStreamResource() resource.Resource {
	return &logStreamResource{}
}

func (r *logStreamResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_log_stream"
}

func (r *logStreamResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = logStreamSchema()
}

// ConfigValidators requires exactly one sink block, which the schema alone
// cannot express.
func (r *logStreamResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	paths := make([]path.Expression, 0, len(sinkAttributeNames))

	for _, name := range sinkAttributeNames {
		paths = append(paths, path.MatchRoot(name))
	}

	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(paths...),
	}
}

func (r *logStreamResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data, ok := providerDataFrom(req.ProviderData, &resp.Diagnostics)

	if !ok {
		return
	}

	r.token = data.Token
}

func (r *logStreamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan logStreamModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := deployapi.NewAuthenticated(plan.DeploymentURL.ValueString(), r.token)

	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment URL", err.Error())
		return
	}

	payload, diags := plan.createPayload(ctx)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	created, err := deployapi.CreateLogStream(ctx, client, payload)

	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating Convex log stream",
			fmt.Sprintf("Could not create the log stream on %s: %s\n\n"+
				"Note that log streams require a Convex Pro plan.",
				plan.DeploymentURL.ValueString(), err),
		)

		return
	}

	tflog.Debug(ctx, "created Convex log stream", map[string]any{
		"id": created.ID, "type": string(created.Type),
	})

	plan.ID = types.StringValue(created.ID)

	// The webhook secret is revealed once, here.
	if created.HMACSecret != "" && plan.Webhook != nil {
		plan.Webhook.HMACSecret = types.StringValue(created.HMACSecret)
	}

	// The stream exists now, so a failure past this point must still write
	// state or Terraform loses track of it.
	stream, err := deployapi.GetLogStream(ctx, client, created.ID)

	if err != nil {
		plan.setUnknownComputedToNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError(
			"Error reading back created log stream",
			fmt.Sprintf("Log stream %s was created but could not be read back: %s. "+
				"It has been saved to state; the remaining attributes are populated on the next refresh.",
				created.ID, err),
		)

		return
	}

	resp.Diagnostics.Append(plan.apply(ctx, stream)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *logStreamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state logStreamModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := deployapi.NewAuthenticated(state.DeploymentURL.ValueString(), r.token)

	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment URL", err.Error())
		return
	}

	stream, err := deployapi.GetLogStream(ctx, client, state.ID.ValueString())

	if err != nil {
		var apiErr *deployapi.APIError

		if errors.Is(err, deployapi.ErrLogStreamNotFound) || (errors.As(err, &apiErr) && apiErr.IsNotFound()) {
			tflog.Debug(ctx, "Convex log stream no longer exists, removing from state",
				map[string]any{"id": state.ID.ValueString()})
			resp.State.RemoveResource(ctx)

			return
		}

		resp.Diagnostics.AddError(
			"Error reading Convex log stream",
			fmt.Sprintf("Could not read log stream %s: %s", state.ID.ValueString(), err),
		)

		return
	}

	// Credentials are deliberately left as configured: the API never returns
	// them, so overwriting would blank them out in state.
	resp.Diagnostics.Append(state.apply(ctx, stream)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *logStreamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state logStreamModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = state.ID

	client, err := deployapi.NewAuthenticated(plan.DeploymentURL.ValueString(), r.token)

	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment URL", err.Error())
		return
	}

	payload, diags := plan.updatePayload(ctx)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	if err := deployapi.UpdateLogStream(ctx, client, plan.ID.ValueString(), payload); err != nil {
		resp.Diagnostics.AddError(
			"Error updating Convex log stream",
			fmt.Sprintf("Could not update log stream %s: %s", plan.ID.ValueString(), err),
		)

		return
	}

	// The update endpoint returns no body, so refresh from a follow-up read.
	stream, err := deployapi.GetLogStream(ctx, client, plan.ID.ValueString())

	if err != nil {
		resp.Diagnostics.AddError(
			"Error reading back updated log stream",
			fmt.Sprintf("Log stream %s was updated but could not be read back: %s",
				plan.ID.ValueString(), err),
		)

		return
	}

	// Carry the webhook secret over: it is issued at creation and the update
	// response does not repeat it.
	if plan.Webhook != nil && state.Webhook != nil && plan.Webhook.HMACSecret.IsUnknown() {
		plan.Webhook.HMACSecret = state.Webhook.HMACSecret
	}

	resp.Diagnostics.Append(plan.apply(ctx, stream)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *logStreamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state logStreamModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := deployapi.NewAuthenticated(state.DeploymentURL.ValueString(), r.token)

	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment URL", err.Error())
		return
	}

	if err := deployapi.DeleteLogStream(ctx, client, state.ID.ValueString()); err != nil {
		var apiErr *deployapi.APIError

		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			// Already gone, which is the desired end state.
			return
		}

		resp.Diagnostics.AddError(
			"Error deleting Convex log stream",
			fmt.Sprintf("Could not delete log stream %s: %s", state.ID.ValueString(), err),
		)
	}
}

func (r *logStreamResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The deployment URL contains slashes, so the id is the final segment.
	deploymentURL, id, ok := cutLast(req.ID, "/")

	if !ok || deploymentURL == "" || id == "" {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected \"<deployment_url>/<log_stream_id>\", got %q. Import with: "+
				"terraform import convex_log_stream.example "+
				"'https://playful-otter-123.convex.cloud/abc123'", req.ID),
		)

		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deployment_url"), deploymentURL)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)

	resp.Diagnostics.AddWarning(
		"Imported log stream has no credentials",
		"Convex never returns a log stream's API key or DSN, so those attributes are empty after import. "+
			"Add them to configuration to match what the stream was created with; a mismatch is not "+
			"detectable and the next apply will overwrite the remote value.",
	)
}

// sinkNames lists the sink blocks set in a configuration, for error messages.
func sinkNames(model *logStreamModel) string {
	var set []string

	for _, name := range sinkAttributeNames {
		if model.hasSink(name) {
			set = append(set, name)
		}
	}

	return strings.Join(set, ", ")
}
