package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/srs/terraform-provider-convex/internal/deployapi"
)

// Ensure the implementation satisfies the framework interfaces.
var (
	_ resource.Resource                = (*environmentVariableResource)(nil)
	_ resource.ResourceWithConfigure   = (*environmentVariableResource)(nil)
	_ resource.ResourceWithImportState = (*environmentVariableResource)(nil)
)

type environmentVariableResource struct {
	// token rather than a client: the Deployment Platform API is addressed at
	// each deployment's own URL, so the client is built per resource instance.
	token string
}

// NewEnvironmentVariableResource returns the convex_environment_variable
// resource.
func NewEnvironmentVariableResource() resource.Resource {
	return &environmentVariableResource{}
}

// environmentVariableResourceModel maps the schema.
type environmentVariableResourceModel struct {
	DeploymentURL types.String `tfsdk:"deployment_url"`
	Name          types.String `tfsdk:"name"`
	Value         types.String `tfsdk:"value"`
}

func (r *environmentVariableResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment_variable"
}

func (r *environmentVariableResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an environment variable on a running Convex deployment, through the " +
			"[Deployment Platform API](https://docs.convex.dev/deployment-platform-api).\n\n" +
			"This is distinct from `convex_default_environment_variable`, which seeds variables into " +
			"deployments *created later*. This resource changes a deployment that already exists, which is " +
			"usually what you want.\n\n" +
			"Variables the configuration does not declare are left alone.",
		Attributes: map[string]schema.Attribute{
			"deployment_url": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Backend URL of the deployment, as reported by " +
					"`convex_deployment.deployment_url`. Use that attribute rather than building the URL from " +
					"the deployment name: regional deployments look like " +
					"`https://calm-cow-456.eu-west-1.convex.cloud`. Changing this forces a new resource.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						deploymentURLPattern,
						"must be an absolute http or https URL, for example https://playful-otter-123.convex.cloud",
					),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Name of the environment variable, for example `STRIPE_KEY`. Changing " +
					"this forces a new resource.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"value": schema.StringAttribute{
				Required:  true,
				Sensitive: true,
				MarkdownDescription: "Value of the environment variable. Marked sensitive, since these " +
					"commonly hold credentials; note that the value is stored in Terraform state either way. " +
					"Can be changed in place.",
			},
		},
	}
}

func (r *environmentVariableResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data, ok := providerDataFrom(req.ProviderData, &resp.Diagnostics)

	if !ok {
		return
	}

	r.token = data.Token
}

func (r *environmentVariableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan environmentVariableResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.clientFor(plan.DeploymentURL)

	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment URL", err.Error())
		return
	}

	name := plan.Name.ValueString()

	// The API only upserts, so it cannot tell a create from an update. Check
	// first, to avoid silently overwriting -- and later destroying -- a
	// variable somebody else set.
	existing, err := deployapi.GetEnvironmentVariable(ctx, client, name)

	switch {
	case err == nil:
		resp.Diagnostics.AddError(
			"Environment variable already exists",
			fmt.Sprintf("%q is already set on %s, with a value of length %d. Convex upserts by name, so "+
				"creating it here would overwrite that value and later destroy it. Import it instead:"+
				"\n\n    terraform import <address> '%s/%s'",
				name, plan.DeploymentURL.ValueString(), len(existing),
				strings.TrimRight(plan.DeploymentURL.ValueString(), "/"), name),
		)

		return

	case !errors.Is(err, deployapi.ErrEnvVarNotFound):
		resp.Diagnostics.AddError(
			"Error checking for an existing environment variable",
			fmt.Sprintf("Could not check whether %q is set on %s: %s",
				name, plan.DeploymentURL.ValueString(), err),
		)

		return
	}

	if err := deployapi.SetEnvironmentVariable(ctx, client, name, plan.Value.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Error setting Convex environment variable",
			fmt.Sprintf("Could not set %q on %s: %s", name, plan.DeploymentURL.ValueString(), err),
		)

		return
	}

	tflog.Debug(ctx, "set Convex environment variable", map[string]any{
		"name": name, "deployment_url": plan.DeploymentURL.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *environmentVariableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state environmentVariableResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.clientFor(state.DeploymentURL)

	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment URL", err.Error())
		return
	}

	name := state.Name.ValueString()
	value, err := deployapi.GetEnvironmentVariable(ctx, client, name)

	if err != nil {
		var apiErr *deployapi.APIError

		if errors.Is(err, deployapi.ErrEnvVarNotFound) || (errors.As(err, &apiErr) && apiErr.IsNotFound()) {
			tflog.Debug(ctx, "Convex environment variable no longer exists, removing from state",
				map[string]any{"name": name})
			resp.State.RemoveResource(ctx)

			return
		}

		resp.Diagnostics.AddError(
			"Error reading Convex environment variable",
			fmt.Sprintf("Could not read %q on %s: %s", name, state.DeploymentURL.ValueString(), err),
		)

		return
	}

	state.Value = types.StringValue(value)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *environmentVariableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan environmentVariableResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.clientFor(plan.DeploymentURL)

	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment URL", err.Error())
		return
	}

	// Only value reaches this point; the other two force replacement.
	name := plan.Name.ValueString()

	if err := deployapi.SetEnvironmentVariable(ctx, client, name, plan.Value.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Error updating Convex environment variable",
			fmt.Sprintf("Could not update %q on %s: %s", name, plan.DeploymentURL.ValueString(), err),
		)

		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *environmentVariableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state environmentVariableResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.clientFor(state.DeploymentURL)

	if err != nil {
		resp.Diagnostics.AddError("Invalid deployment URL", err.Error())
		return
	}

	name := state.Name.ValueString()

	if err := deployapi.UnsetEnvironmentVariable(ctx, client, name); err != nil {
		var apiErr *deployapi.APIError

		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			// Already gone, which is the desired end state.
			return
		}

		resp.Diagnostics.AddError(
			"Error deleting Convex environment variable",
			fmt.Sprintf("Could not delete %q on %s: %s", name, state.DeploymentURL.ValueString(), err),
		)
	}
}

func (r *environmentVariableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The deployment URL contains slashes, so split on the last one: the
	// variable name is the final segment.
	deploymentURL, name, ok := cutLast(req.ID, "/")

	if !ok || deploymentURL == "" || name == "" {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected \"<deployment_url>/<name>\", got %q. Import with: "+
				"terraform import convex_environment_variable.example "+
				"'https://playful-otter-123.convex.cloud/STRIPE_KEY'", req.ID),
		)

		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deployment_url"), deploymentURL)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}

// clientFor builds a client bound to one deployment. The Deployment Platform
// API has no single host, so this cannot be done once at Configure time.
func (r *environmentVariableResource) clientFor(deploymentURL types.String) (*deployapi.ClientWithResponses, error) {
	return deployapi.NewAuthenticated(deploymentURL.ValueString(), r.token)
}

// cutLast is strings.Cut around the final occurrence of sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)

	if i < 0 {
		return s, "", false
	}

	return s[:i], s[i+len(sep):], true
}
