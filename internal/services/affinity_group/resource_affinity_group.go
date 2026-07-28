package affinity

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &AffinityGroupResource{}
var _ resource.ResourceWithImportState = &AffinityGroupResource{}

func NewAffinityGroupResource() resource.Resource {
	return &AffinityGroupResource{}
}

// AffinityGroupResource defines the resource implementation.
type AffinityGroupResource struct {
	client *sdk.CloudClient
}

// AffinityGroupResourceModel describes the resource data model.
type AffinityGroupResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	LocationID types.String `tfsdk:"location_id"`
	Policy     types.String `tfsdk:"policy"`
	ServerIDs  types.Set    `tfsdk:"server_ids"`
}

func (r *AffinityGroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_affinity_group"
}

func (r *AffinityGroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an affinity or anti-affinity group for server placement policies.\n\n" +
			"**Note:** Any changes to this resource will result in replacement of the affinity group " +
			"and recreation of all servers that are members of the group.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Affinity group ID",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Affinity group name (max 25 characters). Changing this forces a new resource to be created.",
				Required:            true,
				Validators: []validator.String{
					// The backend rejects longer names; catch it at plan time.
					stringvalidator.LengthBetween(1, 25),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"location_id": schema.StringAttribute{
				MarkdownDescription: "Location ID where the affinity group is created. Changing this forces a new resource to be created.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"policy": schema.StringAttribute{
				MarkdownDescription: "Placement policy: 'affinity' (servers on same host) or 'anti-affinity' (servers on different hosts). " +
					"Changing this forces a new resource to be created.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.OneOf("affinity", "anti-affinity"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			// Set, not List: membership is unordered and the API returns it in
			// nondeterministic order — a List rewrites state on every refresh.
			"server_ids": schema.SetAttribute{
				MarkdownDescription: "Set of server IDs that are members of this affinity group. This is computed and managed by associating servers with the group.",
				Computed:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (r *AffinityGroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AffinityGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AffinityGroupResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Convert policy string to affinity boolean
	affinity := data.Policy.ValueString() == "affinity"

	createReq := &entities.CreateAffinityGroupRequest{
		Name:       data.Name.ValueString(),
		LocationID: data.LocationID.ValueString(),
		Affinity:   affinity,
	}

	group, err := r.client.CreateAffinityGroup(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Affinity Group",
			fmt.Sprintf("Could not create affinity group: %s", err.Error()),
		)
		return
	}

	// Map response to model
	data.ID = types.StringValue(group.ID)
	data.Name = types.StringValue(group.Name)
	data.LocationID = types.StringValue(group.LocationID)

	// Convert boolean back to policy string
	if group.Affinity {
		data.Policy = types.StringValue("affinity")
	} else {
		data.Policy = types.StringValue("anti-affinity")
	}

	serverIDsElements := make([]attr.Value, len(group.ServerIDs))
	for i, id := range group.ServerIDs {
		serverIDsElements[i] = types.StringValue(id)
	}
	serverIDsSet, diags := types.SetValue(types.StringType, serverIDsElements)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ServerIDs = serverIDsSet

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AffinityGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AffinityGroupResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.client.GetAffinityGroup(ctx, data.ID.ValueString())
	if err != nil {
		// Deleted out-of-band — drop from state so Terraform plans a recreate.
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Affinity group not found, removing from state", map[string]any{
				"id": data.ID.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error Reading Affinity Group",
			fmt.Sprintf("Could not read affinity group %s: %s", data.ID.ValueString(), err.Error()),
		)
		return
	}

	// Update model from API response
	data.Name = types.StringValue(group.Name)
	data.LocationID = types.StringValue(group.LocationID)

	// Convert boolean to policy string
	if group.Affinity {
		data.Policy = types.StringValue("affinity")
	} else {
		data.Policy = types.StringValue("anti-affinity")
	}

	serverIDsElements := make([]attr.Value, len(group.ServerIDs))
	for i, id := range group.ServerIDs {
		serverIDsElements[i] = types.StringValue(id)
	}
	serverIDsSet, diags := types.SetValue(types.StringType, serverIDsElements)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ServerIDs = serverIDsSet

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AffinityGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// This should never be called as all attributes have RequiresReplace modifier
	// If we reach here, something went wrong

	resp.Diagnostics.AddError(
		"Unexpected Update Call",
		"All affinity group attributes require replacement. This update call should not have been made. "+
			"This is likely a bug in the provider. Please report this issue.",
	)
}

func (r *AffinityGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data AffinityGroupResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := data.ID.ValueString()

	// Retry deletion - servers might be deleting in parallel
	maxRetries := 30
	retryInterval := 3 * time.Second

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		err := r.client.DeleteAffinityGroup(ctx, groupID)
		if err == nil {
			return // Success
		}

		// Check if already deleted (404)
		if sdk.IsNotFound(err) {
			return
		}

		// Check if error is "servers still in group" - retry
		if sdk.HasAPICode(err, sdk.APICodeAffinityGroupNotEmpty) {
			lastErr = err
			tflog.Info(ctx, "Affinity group still has servers, waiting for them to be deleted...", map[string]any{
				"group_id": groupID,
				"attempt":  i + 1,
			})
			select {
			case <-ctx.Done():
				resp.Diagnostics.AddError(
					"Error Deleting Affinity Group",
					fmt.Sprintf("Cancelled while waiting to delete affinity group %s: %s (last API error: %s)",
						groupID, ctx.Err(), lastErr.Error()),
				)
				return
			case <-time.After(retryInterval):
			}
			continue
		}

		// Other error - fail immediately
		resp.Diagnostics.AddError(
			"Error Deleting Affinity Group",
			fmt.Sprintf("Could not delete affinity group %s: %s", groupID, err.Error()),
		)
		return
	}

	// Exhausted retries
	resp.Diagnostics.AddError(
		"Error Deleting Affinity Group",
		fmt.Sprintf("Could not delete affinity group %s after %d retries (waited %v): %s. "+
			"Servers may still be attached to the group.",
			groupID, maxRetries, time.Duration(maxRetries)*retryInterval, lastErr.Error()),
	)
}

func (r *AffinityGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
