package isolated_network

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &networkResource{}
	_ resource.ResourceWithConfigure   = &networkResource{}
	_ resource.ResourceWithImportState = &networkResource{}
)

// NewNetworkResource is a helper function to simplify the provider implementation.
func NewNetworkResource() resource.Resource {
	return &networkResource{}
}

// networkResource is the resource implementation.
type networkResource struct {
	client *sdk.CloudClient
}

// Metadata returns the resource type name.
func (r *networkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network"
}

// Schema defines the schema for the resource.
func (r *networkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manages an isolated network.",
		MarkdownDescription: "Manages an isolated network. Networks provide isolated networking environments for your infrastructure.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:         "The unique identifier of the network.",
				MarkdownDescription: "The unique identifier of the network. Used for referencing the network in other resources.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description:         "The name of the network.",
				MarkdownDescription: "The name of the network.",
				Required:            true,
			},
			"location_id": schema.StringAttribute{
				Description:         "The location ID where the network will be created.",
				MarkdownDescription: "The location ID where the network will be created. Changing this forces resource recreation.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Description:         "A description of the network.",
				MarkdownDescription: "A description of the network.",
				Optional:            true,
				Computed:            true,
			},
			"network_prefix": schema.StringAttribute{
				Description:         "The network prefix (e.g., 10.100.0.0). If not specified, will be assigned automatically.",
				MarkdownDescription: "The network prefix (e.g., `10.100.0.0`). If not specified, will be assigned automatically. **Changing this forces resource recreation.**",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"mask": schema.Int64Attribute{
				Description:         "The network mask (e.g., 24 for /24). If not specified, will be assigned automatically.",
				MarkdownDescription: "The network mask (e.g., `24` for `/24`). If not specified, will be assigned automatically. **Changing this forces resource recreation.**",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
					int64planmodifier.UseStateForUnknown(),
				},
			},
			// No UseStateForUnknown on server_ids/gateway_ids: attachments change
			// outside this resource by design (vcp_*_network_attachment), so pinning
			// the prior list into a plan would promise stale values and fail the
			// apply with "inconsistent result" whenever attachments changed.
			"server_ids": schema.ListAttribute{
				Description:         "List of server IDs attached to this network.",
				MarkdownDescription: "List of server IDs attached to this network. This is a read-only attribute managed by the API.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"gateway_ids": schema.ListAttribute{
				Description:         "List of gateway IDs attached to this network.",
				MarkdownDescription: "List of gateway IDs attached to this network. This is a read-only attribute managed by the API.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"created": schema.StringAttribute{
				Description:         "The creation timestamp of the network.",
				MarkdownDescription: "The creation timestamp of the network in RFC3339 format.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"tags": schema.SetAttribute{
				Description:         "Set of tags associated with the network.",
				MarkdownDescription: "If attribute is omitted, all existing tags will be removed.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default: setdefault.StaticValue(
					types.SetValueMust(types.StringType, []attr.Value{}),
				),
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

// Configure adds the provider configured client to the resource.
func (r *networkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = client
}

// Create creates the resource and sets the initial Terraform state.
func (r *networkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan networkModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build the create request
	createReq := &entities.CreateNetworkRequest{
		Name:       plan.Name.ValueString(),
		LocationID: plan.LocationID.ValueString(),
	}

	if !plan.Description.IsNull() {
		createReq.Description = plan.Description.ValueString()
	}

	if !plan.NetworkPrefix.IsNull() {
		createReq.NetworkPrefix = plan.NetworkPrefix.ValueString()
	}

	if !plan.Mask.IsNull() {
		createReq.Mask = int(plan.Mask.ValueInt64())
	}

	tflog.Info(ctx, "Creating network", map[string]any{
		"name":        createReq.Name,
		"location_id": createReq.LocationID,
	})

	// Create network using SDK (with wait)
	network, err := r.client.CreateNetworkAndWait(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Network",
			fmt.Sprintf("Could not create network '%s': %s", plan.Name.ValueString(), err.Error()),
		)
		return
	}

	tflog.Info(ctx, "Network created successfully", map[string]any{
		"id": network.ID,
	})

	// Handle tags if specified. From this point on the network exists, so no
	// path may return before State.Set below — otherwise a transient error
	// would orphan a real network from state and the next apply would create
	// a duplicate.
	tagsRefreshed := true // whether `network` reflects the tags added below
	if !plan.Tags.IsNull() && len(plan.Tags.Elements()) > 0 {
		var tags []string
		tagDiags := plan.Tags.ElementsAs(ctx, &tags, false)
		resp.Diagnostics.Append(tagDiags...)
		if !tagDiags.HasError() {
			for _, tag := range tags {
				tagReq := &sdk.AddNetworkTagRequest{Tag: tag}
				err := r.client.AddNetworkTag(ctx, network.ID, tagReq)
				if err != nil {
					resp.Diagnostics.AddError(
						"Error Adding Network Tag",
						fmt.Sprintf("Could not add tag %q to network %s: %s", tag, network.ID, err.Error()),
					)
				}
			}

			// Refresh network to get updated tags
			refreshed, rerr := r.client.GetNetwork(ctx, network.ID)
			if rerr != nil {
				tagsRefreshed = false
				resp.Diagnostics.AddWarning(
					"Network Created But Refresh Failed",
					fmt.Sprintf("Network %s was created, but reading it back failed: %s. "+
						"State was saved from the create response and will be refreshed on the next plan.",
						network.ID, rerr.Error()),
				)
			} else {
				network = refreshed
			}
		}
	}

	// Map response to state
	state := mapNetworkToModel(network)
	if !tagsRefreshed {
		// The tag calls above succeeded (any failure added an error diagnostic,
		// which fails the apply anyway), so the plan's tags are authoritative.
		state.Tags = plan.Tags
	}

	// Set state
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

// Read refreshes the Terraform state with the latest data.
func (r *networkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state networkModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get network from API
	network, err := r.client.GetNetwork(ctx, state.ID.ValueString())
	if err != nil {

		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error Reading Network",
			fmt.Sprintf("Could not read network %s: %s", state.ID.ValueString(), err.Error()),
		)
		return
	}

	// Map response to state
	state = mapNetworkToModel(network)

	// Set refreshed state
	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *networkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state networkModel

	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)

	diags = req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Build the update request
	updateReq := &entities.UpdateNetworkRequest{
		Name: plan.Name.ValueString(),
	}

	if !plan.Description.IsNull() {
		updateReq.Description = plan.Description.ValueString()
	}

	tflog.Info(ctx, "Updating network", map[string]any{
		"id":   state.ID.ValueString(),
		"name": updateReq.Name,
	})

	// Update network
	network, err := r.client.UpdateNetwork(ctx, state.ID.ValueString(), updateReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Updating Network",
			fmt.Sprintf("Could not update network %s: %s", state.ID.ValueString(), err.Error()),
		)
		return
	}

	// Handle tags updates
	if !plan.Tags.Equal(state.Tags) {
		var planTags, stateTags []string

		if plan.Tags.IsNull() {
			tflog.Debug(ctx, "Plan tags is NULL, will use empty list")
			planTags = []string{}
		} else {
			diags = plan.Tags.ElementsAs(ctx, &planTags, false)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			tflog.Debug(ctx, "Extracted plan tags", map[string]any{
				"tags":  planTags,
				"count": len(planTags),
			})
		}

		// Get tags from state
		if state.Tags.IsNull() {
			tflog.Debug(ctx, "State tags is NULL, will use empty list")
			stateTags = []string{}
		} else {
			diags = state.Tags.ElementsAs(ctx, &stateTags, false)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			tflog.Debug(ctx, "Extracted state tags", map[string]any{
				"tags":  stateTags,
				"count": len(stateTags),
			})
		}

		if resp.Diagnostics.HasError() {
			return
		}
		// Add new tags
		for _, tag := range planTags {
			if !slices.Contains(stateTags, tag) {
				tagReq := &sdk.AddNetworkTagRequest{Tag: tag}
				err := r.client.AddNetworkTag(ctx, network.ID, tagReq)
				if err != nil {
					resp.Diagnostics.AddError(
						"Error Adding Tag",
						fmt.Sprintf("Could not add tag %s to network %s: %s", tag, network.ID, err.Error()),
					)
					return
				}
			}
		}

		// Remove old tags
		for _, tag := range stateTags {
			if !slices.Contains(planTags, tag) {
				err := r.client.DeleteNetworkTag(ctx, network.ID, tag)
				if err != nil {
					resp.Diagnostics.AddError(
						"Error Removing Tag",
						fmt.Sprintf("Could not remove tag %s from network %s: %s", tag, network.ID, err.Error()),
					)
					return
				}
			}
		}

		// Refresh network to get updated tags
		network, err = r.client.GetNetwork(ctx, network.ID)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error Refreshing Network After Updating Tags",
				fmt.Sprintf("Could not read network %s: %s", network.ID, err.Error()),
			)
			return
		}
	}

	// Map response to state
	newState := mapNetworkToModel(network)

	// Set state
	diags = resp.State.Set(ctx, newState)
	resp.Diagnostics.Append(diags...)
}

func (r *networkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state networkModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Deleting network", map[string]any{
		"id": state.ID.ValueString(),
	})

	// The backend answers DELETE for a nonexistent network with HTTP 500 (not
	// 404), which the SDK retries at length before failing. Probe with GET
	// first: a missing network means there is nothing to delete.
	if _, err := r.client.GetNetwork(ctx, state.ID.ValueString()); sdk.IsNotFound(err) {
		tflog.Info(ctx, "Network already deleted, treating as success", map[string]any{
			"id": state.ID.ValueString(),
		})
		return
	}

	// Delete network
	err := r.client.DeleteNetwork(ctx, state.ID.ValueString())
	if err != nil {
		// Ignore 404 error - resource is already deleted (idempotency)
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Network already deleted (404), treating as success", map[string]any{
				"id": state.ID.ValueString(),
			})
			return
		}

		if sdk.HasAPICode(err, sdk.APICodeNetworkInUse) {
			resp.Diagnostics.AddError(
				"Network Still In Use",
				fmt.Sprintf("Network %s is still attached to a gateway or server, so the API refuses to delete it.\n\n"+
					"If the connections are managed by Terraform (vcp_gateway_network_attachment / "+
					"vcp_server_network_attachment), remove those resources in the same or a prior apply — "+
					"Terraform will detach before deleting.\n"+
					"Connections made outside Terraform must be detached manually first.\n\n"+
					"API error: %s", state.ID.ValueString(), err.Error()),
			)
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting Network",
			fmt.Sprintf("Could not delete network %s: %s", state.ID.ValueString(), err.Error()),
		)
		return
	}

	tflog.Info(ctx, "Network deleted successfully", map[string]any{
		"id": state.ID.ValueString(),
	})
}

// ImportState imports the resource into Terraform state.
func (r *networkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import by ID
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
