package dns

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                = &domainResource{}
	_ resource.ResourceWithConfigure   = &domainResource{}
	_ resource.ResourceWithImportState = &domainResource{}
)

// NewDomainResource is a helper function to simplify the provider implementation.
func NewDomainResource() resource.Resource {
	return &domainResource{}
}

type domainResource struct {
	client *sdk.CloudClient
}

func (r *domainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_domain"
}

func (r *domainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a DNS domain (zone). The zone is addressed by its name; there is no update " +
			"operation, so changing `name` forces recreation. Names are canonicalized to a trailing-dot FQDN.\n\n" +
			"**Note:** creating a zone automatically provisions system NS records; those are not managed by " +
			"Terraform and appear in the `vcp_dns_domain` data source.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Domain identifier (equal to the canonical name).",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Domain (zone) name, e.g. `example.com.`. Case and the trailing dot are not " +
					"significant. Changing this forces a new resource.",
				CustomType: FQDNType{},
				Required:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"is_delegated": schema.BoolAttribute{
				MarkdownDescription: "Whether the zone is delegated to the provider's name servers.",
				Computed:            true,
			},
		},
	}
}

func (r *domainResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := normalizeName(plan.Name.ValueString())
	tflog.Info(ctx, "Creating DNS domain", map[string]any{"name": name})

	domain, err := r.client.CreateDomainAndWait(ctx, &entities.CreateDomainRequest{Name: name})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating DNS Domain",
			fmt.Sprintf("Could not create domain '%s': %s", name, err.Error()),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, domainStateFrom(domain))...)
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	domain, err := r.client.GetDomain(ctx, state.Name.ValueString())
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error Reading DNS Domain",
			fmt.Sprintf("Could not read domain %s: %s", state.Name.ValueString(), err.Error()),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, domainStateFrom(domain))...)
}

func (r *domainResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Unexpected Update",
		"DNS domains have no update operation; all configurable attributes require replacement.",
	)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state domainResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	// Deletion is asynchronous — the SDK waits until the zone is actually gone.
	if err := r.client.DeleteDomainAndWait(ctx, name); err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError(
			"Error Deleting DNS Domain",
			fmt.Sprintf("Could not delete domain %s: %s", name, err.Error()),
		)
	}
}

func (r *domainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	name := normalizeName(req.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}

func domainStateFrom(d *entities.Domain) domainResourceModel {
	name := normalizeName(d.Name)
	return domainResourceModel{
		ID:          types.StringValue(name),
		Name:        NewFQDNValue(name),
		IsDelegated: types.BoolValue(d.IsDelegated),
	}
}
