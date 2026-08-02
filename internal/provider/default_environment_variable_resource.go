package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.Resource                = (*defaultEnvironmentVariableResource)(nil)
	_ resource.ResourceWithConfigure   = (*defaultEnvironmentVariableResource)(nil)
	_ resource.ResourceWithImportState = (*defaultEnvironmentVariableResource)(nil)
)

// errEnvVarNotFound reports that no matching default environment variable is
// configured. The API has no get-by-id endpoint, so lookups go through the
// filtered list endpoint and this stands in for a 404.
var errEnvVarNotFound = errors.New("default environment variable not found")

type defaultEnvironmentVariableResource struct {
	client *convex.ClientWithResponses
}

// NewDefaultEnvironmentVariableResource returns the
// convex_default_environment_variable resource.
func NewDefaultEnvironmentVariableResource() resource.Resource {
	return &defaultEnvironmentVariableResource{}
}

// defaultEnvironmentVariableResourceModel maps the schema.
type defaultEnvironmentVariableResourceModel struct {
	ProjectID      types.Int64  `tfsdk:"project_id"`
	Name           types.String `tfsdk:"name"`
	DeploymentType types.String `tfsdk:"deployment_type"`
	Value          types.String `tfsdk:"value"`
}

func (r *defaultEnvironmentVariableResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_default_environment_variable"
}

func (r *defaultEnvironmentVariableResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one default environment variable for a Convex project.\n\n" +
			"~> **Defaults apply only to deployments created afterwards.** Convex uses these values when a new " +
			"deployment is created and does not push them to deployments that already exist, nor keep them in " +
			"sync. Changing one here has no effect on a running deployment.\n\n" +
			"Each resource manages a single `(name, deployment_type)` pair, which is exactly how the API keys " +
			"these variables. Variables that Terraform does not declare are left alone.\n\n" +
			"The API only upserts, so it cannot tell a create from an update. Creating a variable that " +
			"already exists therefore fails rather than silently overwriting a value set elsewhere; import " +
			"it instead -- the error reports the import command.\n\n" +
			"Writes are retried on transient 5xx failures, which Convex has been seen returning when several " +
			"variables of one project are written at once. If it persists, `terraform apply -parallelism=1` " +
			"serialises them.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "Numeric identifier of the project. Changing this forces a new resource.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Name of the environment variable, for example `SENTRY_DSN`. Changing this " +
					"forces a new resource.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"deployment_type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Deployment type the default applies to: `dev`, `prod`, `preview` or " +
					"`custom`. The same variable name can carry a different value per type, so this is part of " +
					"the resource's identity. Changing it forces a new resource.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(convex.DeploymentTypeDev),
						string(convex.DeploymentTypeProd),
						string(convex.DeploymentTypePreview),
						string(convex.DeploymentTypeCustom),
					),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"value": schema.StringAttribute{
				Required:  true,
				Sensitive: true,
				MarkdownDescription: "Value of the environment variable. Marked sensitive, since these commonly " +
					"hold credentials; note that the value is stored in Terraform state either way. Can be " +
					"changed in place.",
			},
		},
	}
}

func (r *defaultEnvironmentVariableResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data, ok := providerDataFrom(req.ProviderData, &resp.Diagnostics)

	if !ok {
		return
	}

	r.client = data.Management
}

func (r *defaultEnvironmentVariableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan defaultEnvironmentVariableResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// The API upserts, so it cannot tell us whether the variable already
	// existed. Check first, to avoid silently adopting -- and then destroying
	// on the next terraform destroy -- a variable somebody else set.
	projectID := plan.ProjectID.ValueInt64()
	name := plan.Name.ValueString()
	deploymentType := convex.DeploymentType(plan.DeploymentType.ValueString())

	existing, err := r.getEnvVar(ctx, projectID, name, deploymentType)

	switch {
	case err == nil:
		resp.Diagnostics.AddError(
			"Default environment variable already exists",
			fmt.Sprintf("%q is already set for %s deployments in project %d, with a value of length %d. "+
				"Convex upserts by name, so creating it here would overwrite that value and later destroy it. "+
				"Import it instead:\n\n    terraform import <address> %d/%s/%s",
				name, deploymentType, projectID, len(existing.Value), projectID, deploymentType, name),
		)
		return
	case !errors.Is(err, errEnvVarNotFound):
		resp.Diagnostics.AddError(
			"Error checking for an existing default environment variable",
			fmt.Sprintf("Could not check whether %q is already set in project %d: %s", name, projectID, err),
		)
		return
	}

	if err := r.setEnvVar(ctx, projectID, name, deploymentType, plan.Value.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Error setting Convex default environment variable",
			fmt.Sprintf("Could not set %q for %s deployments in project %d: %s",
				name, deploymentType, projectID, err),
		)
		return
	}

	tflog.Debug(ctx, "set Convex default environment variable", map[string]any{
		"name": name, "deployment_type": string(deploymentType), "project_id": projectID,
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *defaultEnvironmentVariableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state defaultEnvironmentVariableResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	projectID := state.ProjectID.ValueInt64()
	name := state.Name.ValueString()
	deploymentType := convex.DeploymentType(state.DeploymentType.ValueString())

	found, err := r.getEnvVar(ctx, projectID, name, deploymentType)

	if err != nil {
		var apiErr *convex.APIError

		if errors.Is(err, errEnvVarNotFound) || (errors.As(err, &apiErr) && apiErr.IsNotFound()) {
			tflog.Debug(ctx, "Convex default environment variable no longer exists, removing from state",
				map[string]any{"name": name, "project_id": projectID})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error reading Convex default environment variable",
			fmt.Sprintf("Could not read %q for %s deployments in project %d: %s",
				name, deploymentType, projectID, err),
		)
		return
	}

	state.Value = types.StringValue(found.Value)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *defaultEnvironmentVariableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan defaultEnvironmentVariableResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Only value can reach this point; the identity attributes all force
	// replacement.
	projectID := plan.ProjectID.ValueInt64()
	name := plan.Name.ValueString()
	deploymentType := convex.DeploymentType(plan.DeploymentType.ValueString())

	if err := r.setEnvVar(ctx, projectID, name, deploymentType, plan.Value.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Error updating Convex default environment variable",
			fmt.Sprintf("Could not update %q for %s deployments in project %d: %s",
				name, deploymentType, projectID, err),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *defaultEnvironmentVariableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state defaultEnvironmentVariableResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	projectID := state.ProjectID.ValueInt64()
	name := state.Name.ValueString()
	deploymentType := convex.DeploymentType(state.DeploymentType.ValueString())

	if err := convex.DeleteDefaultEnvironmentVariable(ctx, r.client, projectID, name, deploymentType); err != nil {
		var apiErr *convex.APIError

		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			// Already gone, which is the desired end state.
			return
		}

		detail := fmt.Sprintf("Could not delete %q for %s deployments in project %d: %s",
			name, deploymentType, projectID, err)

		// A server-side fault that outlives the retries leaves the resource in
		// state, which blocks `terraform destroy` on everything that depends on
		// it. Say how to get unstuck rather than leaving that to be worked out.
		if convex.IsTransient(err) {
			detail += "\n\nThe retries were exhausted, so this is a fault on the Convex side rather than " +
				"something wrong with the configuration. Try again in a few minutes. If it keeps failing, " +
				"delete the variable in the Convex dashboard and drop it from state to unblock the destroy:" +
				"\n\n    terraform state rm 'convex_default_environment_variable.<name>'"
		}

		resp.Diagnostics.AddError("Error deleting Convex default environment variable", detail)
	}
}

func (r *defaultEnvironmentVariableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Split on the first two separators only, so that a variable name
	// containing "/" survives the round trip.
	parts := strings.SplitN(req.ID, "/", 3)

	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected \"<project_id>/<deployment_type>/<name>\", got %q. Import with: "+
				"terraform import convex_default_environment_variable.sentry 12345/prod/SENTRY_DSN", req.ID),
		)
		return
	}

	rawProjectID, deploymentType, name := parts[0], parts[1], parts[2]

	projectID, err := strconv.ParseInt(rawProjectID, 10, 64)

	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected a numeric project ID before the first \"/\", got %q.", rawProjectID),
		)
		return
	}

	switch convex.DeploymentType(deploymentType) {
	case convex.DeploymentTypeDev, convex.DeploymentTypeProd,
		convex.DeploymentTypePreview, convex.DeploymentTypeCustom:
	default:
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Deployment type must be dev, prod, preview or custom, got %q.", deploymentType),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deployment_type"), deploymentType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}

// setEnvVar upserts a variable. The endpoint returns no body, so there is
// nothing to decode. Transient failures are retried inside the client helper.
func (r *defaultEnvironmentVariableResource) setEnvVar(ctx context.Context, projectID int64, name string, deploymentType convex.DeploymentType, value string) error {
	return convex.SetDefaultEnvironmentVariable(ctx, r.client, projectID, name, deploymentType, value)
}

// getEnvVar finds one variable. The list endpoint filters server-side on both
// name and deployment type, so this expects at most one match and never has to
// page.
func (r *defaultEnvironmentVariableResource) getEnvVar(ctx context.Context, projectID int64, name string, deploymentType convex.DeploymentType) (*convex.DefaultEnvironmentVariableResponse, error) {
	listed, err := r.client.ListDefaultEnvironmentVariablesWithResponse(ctx, projectID,
		&convex.ListDefaultEnvironmentVariablesParams{
			Name:           &name,
			DeploymentType: &deploymentType,
		})

	if err != nil {
		return nil, err
	}

	if err := convex.CheckResponse(listed); err != nil {
		return nil, err
	}

	if listed.JSON200 == nil {
		return nil, errors.New("the API returned a successful status with no environment variable list")
	}

	return findEnvVar(listed.JSON200.Items, name, deploymentType)
}

// findEnvVar picks a variable out of a list response.
//
// The response groups by name and value, listing every deployment type that
// shares them, so the requested type has to be confirmed rather than assumed --
// the server-side filter is not re-checked here in case a caller passes an
// unfiltered list.
func findEnvVar(items []convex.DefaultEnvironmentVariableResponse, name string, deploymentType convex.DeploymentType) (*convex.DefaultEnvironmentVariableResponse, error) {
	for i := range items {
		if items[i].Name != name {
			continue
		}

		for _, candidate := range items[i].DeploymentTypes {
			if candidate == deploymentType {
				return &items[i], nil
			}
		}
	}

	return nil, fmt.Errorf("%w: %q is not set for %s deployments", errEnvVarNotFound, name, deploymentType)
}
