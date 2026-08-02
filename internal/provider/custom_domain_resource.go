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
	_ resource.Resource                = (*customDomainResource)(nil)
	_ resource.ResourceWithConfigure   = (*customDomainResource)(nil)
	_ resource.ResourceWithImportState = (*customDomainResource)(nil)
)

// errCustomDomainNotFound reports that no matching domain is configured. The
// API has no get-by-id endpoint, so lookups go through the list endpoint and
// this stands in for a 404.
var errCustomDomainNotFound = errors.New("custom domain not found")

type customDomainResource struct {
	client *convex.ClientWithResponses
}

// NewCustomDomainResource returns the convex_custom_domain resource.
func NewCustomDomainResource() resource.Resource {
	return &customDomainResource{}
}

// customDomainResourceModel maps the convex_custom_domain schema.
type customDomainResourceModel struct {
	DeploymentName     types.String `tfsdk:"deployment_name"`
	Domain             types.String `tfsdk:"domain"`
	RequestDestination types.String `tfsdk:"request_destination"`

	CreationTime     types.Int64 `tfsdk:"creation_time"`
	VerificationTime types.Int64 `tfsdk:"verification_time"`
	Verified         types.Bool  `tfsdk:"verified"`
}

func (r *customDomainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_domain"
}

func (r *customDomainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Points a custom domain at a Convex deployment.\n\n" +
			"Convex verifies the domain asynchronously once its DNS records resolve. Creating this resource " +
			"registers the domain and returns immediately, so a fresh domain is normally unverified: " +
			"`verified` is `false` until the DNS is in place and Convex has checked it. Terraform does not " +
			"wait for verification, and a later refresh is what picks it up.\n\n" +
			"Convex offers no endpoint for changing a custom domain, so every attribute forces replacement.",
		Attributes: map[string]schema.Attribute{
			"deployment_name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Name of the deployment the domain points at, such as " +
					"`playful-otter-123`. Changing this forces a new resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"domain": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The custom domain itself, for example `api.example.com`. Changing this " +
					"forces a new resource.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"request_destination": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Which endpoint the domain serves. `convexCloud` fronts the deployment's " +
					"`.convex.cloud` API URL; `convexSite` fronts its `.convex.site` HTTP actions URL. A " +
					"deployment can have one domain registered for each. Changing this forces a new resource.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(convex.ConvexCloud),
						string(convex.ConvexSite),
					),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"creation_time": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp, in milliseconds since the Unix epoch.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"verification_time": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Timestamp in milliseconds at which Convex verified the domain, or null " +
					"while it is still unverified. This is set outside Terraform, once DNS resolves.",
			},
			"verified": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether Convex has verified the domain. A convenience for " +
					"`verification_time != null`; false on a freshly created domain.",
			},
		},
	}
}

func (r *customDomainResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *customDomainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan customDomainResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deploymentName := plan.DeploymentName.ValueString()
	domain := plan.Domain.ValueString()
	destination := convex.RequestDestination(plan.RequestDestination.ValueString())

	// The create and delete endpoints share a request schema in the spec, hence
	// the delete-flavoured type name on a create call.
	created, err := r.client.CreateCustomDomainWithResponse(ctx, deploymentName,
		convex.PlatformDeleteCustomDomainArgs{Domain: domain, RequestDestination: destination})
	if err != nil {
		resp.Diagnostics.AddError("Error creating Convex custom domain", err.Error())
		return
	}
	if err := convex.CheckResponse(created); err != nil {
		resp.Diagnostics.AddError(
			"Error creating Convex custom domain",
			fmt.Sprintf("Could not add %q (%s) to deployment %q: %s", domain, destination, deploymentName, err),
		)
		return
	}
	tflog.Debug(ctx, "created Convex custom domain", map[string]any{
		"domain": domain, "destination": string(destination), "deployment": deploymentName,
	})

	// The create endpoint returns no body, so the timestamps come from a
	// follow-up list. The domain exists from here on, so a failure still has to
	// write state or Terraform will lose track of it.
	found, err := r.getCustomDomain(ctx, deploymentName, domain, destination)
	if err != nil {
		plan.CreationTime = types.Int64Null()
		plan.VerificationTime = types.Int64Null()
		plan.Verified = types.BoolValue(false)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError(
			"Error reading back created custom domain",
			fmt.Sprintf("%q was added to deployment %q but could not be read back: %s. "+
				"It has been saved to state; the remaining attributes are populated on the next refresh.",
				domain, deploymentName, err),
		)
		return
	}

	applyCustomDomain(&plan, found)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *customDomainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state customDomainResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deploymentName := state.DeploymentName.ValueString()
	domain := state.Domain.ValueString()
	destination := convex.RequestDestination(state.RequestDestination.ValueString())

	found, err := r.getCustomDomain(ctx, deploymentName, domain, destination)
	if err != nil {
		var apiErr *convex.APIError
		if errors.Is(err, errCustomDomainNotFound) || (errors.As(err, &apiErr) && apiErr.IsNotFound()) {
			tflog.Debug(ctx, "Convex custom domain no longer exists, removing from state",
				map[string]any{"domain": domain, "deployment": deploymentName})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading Convex custom domain",
			fmt.Sprintf("Could not read %q on deployment %q: %s", domain, deploymentName, err),
		)
		return
	}

	applyCustomDomain(&state, found)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only runs for changes the plan did not mark as replacements, and every
// configurable attribute here forces replacement.
func (r *customDomainResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Custom domains cannot be updated",
		"Convex offers no endpoint for changing a custom domain, so every attribute should force "+
			"replacement. Reaching this point is a bug in the provider.",
	)
}

func (r *customDomainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state customDomainResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deploymentName := state.DeploymentName.ValueString()
	domain := state.Domain.ValueString()
	destination := convex.RequestDestination(state.RequestDestination.ValueString())

	deleted, err := r.client.DeleteCustomDomainWithResponse(ctx, deploymentName,
		convex.PlatformDeleteCustomDomainArgs{Domain: domain, RequestDestination: destination})
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Convex custom domain", err.Error())
		return
	}
	if err := convex.CheckResponse(deleted); err != nil {
		var apiErr *convex.APIError
		if errors.As(err, &apiErr) && apiErr.IsNotFound() {
			// Already gone, which is the desired end state.
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting Convex custom domain",
			fmt.Sprintf("Could not remove %q from deployment %q: %s", domain, deploymentName, err),
		)
	}
}

func (r *customDomainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Expected \"<deployment_name>/<request_destination>/<domain>\", got %q. Import with: "+
				"terraform import convex_custom_domain.api playful-otter-123/convexCloud/api.example.com", req.ID),
		)
		return
	}
	deploymentName, destination, domain := parts[0], parts[1], parts[2]

	if destination != string(convex.ConvexCloud) && destination != string(convex.ConvexSite) {
		resp.Diagnostics.AddError(
			"Invalid import identifier",
			fmt.Sprintf("Request destination must be %q or %q, got %q.",
				convex.ConvexCloud, convex.ConvexSite, destination),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deployment_name"), deploymentName)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("request_destination"), destination)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), domain)...)
}

// getCustomDomain finds a domain by name and destination. There is no
// get-by-id endpoint, so this lists and filters.
func (r *customDomainResource) getCustomDomain(ctx context.Context, deploymentName, domain string, destination convex.RequestDestination) (*convex.PlatformCustomDomainResponse, error) {
	listed, err := r.client.ListCustomDomainsWithResponse(ctx, deploymentName)
	if err != nil {
		return nil, err
	}
	if err := convex.CheckResponse(listed); err != nil {
		return nil, err
	}
	if listed.JSON200 == nil {
		return nil, errors.New("the API returned a successful status with no custom domain list")
	}
	return findCustomDomain(listed.JSON200.Domains, domain, destination)
}

// findCustomDomain picks a domain out of a list response. A deployment can
// serve the same domain on both destinations, so the destination is part of
// the identity rather than a detail.
func findCustomDomain(domains []convex.PlatformCustomDomainResponse, domain string, destination convex.RequestDestination) (*convex.PlatformCustomDomainResponse, error) {
	for i := range domains {
		if domains[i].Domain == domain && domains[i].RequestDestination == destination {
			return &domains[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %q is not configured for %s", errCustomDomainNotFound, domain, destination)
}

// applyCustomDomain copies an API response onto the Terraform model.
func applyCustomDomain(model *customDomainResourceModel, domain *convex.PlatformCustomDomainResponse) {
	model.DeploymentName = types.StringValue(domain.DeploymentName)
	model.Domain = types.StringValue(domain.Domain)
	model.RequestDestination = types.StringValue(string(domain.RequestDestination))
	model.CreationTime = types.Int64Value(domain.CreationTime)
	model.VerificationTime = types.Int64PointerValue(domain.VerificationTime)
	model.Verified = types.BoolValue(domain.VerificationTime != nil)
}
