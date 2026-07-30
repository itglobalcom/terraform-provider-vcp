package vmware

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

const (
	networkTypeIsolated = "isolated"
	networkTypeRouted   = "routed"
	networkTypePublic   = "public"
)

var (
	_ resource.Resource                = &networkResource{}
	_ resource.ResourceWithConfigure   = &networkResource{}
	_ resource.ResourceWithImportState = &networkResource{}
)

func NewNetworkResource() resource.Resource { return &networkResource{} }

type networkResource struct{ client *sdk.CloudClient }

func (r *networkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_network"
}

func (r *networkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manages a VMware Cloud network (isolated, routed or public).",
		MarkdownDescription: "Manages a VMware Cloud network. The `type` selects the flavour: `isolated`, `routed` or `public`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Computed:      true,
				Description:   "The unique identifier of the network.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Required:            true,
				Description:         "Network flavour: isolated, routed or public. Changing this forces recreation.",
				MarkdownDescription: "Network flavour: `isolated`, `routed` or `public`. **Changing this forces recreation.**",
				Validators: []validator.String{
					stringvalidator.OneOf(networkTypeIsolated, networkTypeRouted, networkTypePublic),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"location_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Location ID where the network is created. Changing this forces recreation.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "The name of the network.",
			},
			"address": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Network address (CIDR base, e.g. 10.100.0.0). Required for isolated/routed. Changing this forces recreation.",
				MarkdownDescription: "Network address (e.g. `10.100.0.0`). Required for `isolated`/`routed`. **Changing this forces recreation.**",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"mask": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Network mask prefix length (e.g. 24). Changing this forces recreation.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace(), int64planmodifier.UseStateForUnknown()},
			},
			"enable_dhcp": schema.BoolAttribute{
				Optional:            true,
				Description:         "Enable DHCP on the network (isolated/routed only). Changing this forces recreation.",
				MarkdownDescription: "Enable DHCP (`isolated`/`routed` only). Write-only. **Changing this forces recreation.**",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"capacity": schema.StringAttribute{
				Optional:            true,
				Description:         "Public IP pool capacity (public networks only). Changing this forces recreation.",
				MarkdownDescription: "Public IP pool capacity (`public` networks only). Write-only. **Changing this forces recreation.**",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"bandwidth_mbps": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Description:         "Bandwidth in Mbps (routed/public networks). Editable in place.",
				MarkdownDescription: "Bandwidth in Mbps (`routed`/`public` networks). Editable in place.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"gateway":    schema.StringAttribute{Computed: true, Description: "Gateway IP address."},
			"is_dhcp":    schema.BoolAttribute{Computed: true, Description: "Whether DHCP is enabled."},
			"shared":     schema.BoolAttribute{Computed: true, Description: "Whether the network is shared."},
			"state":      schema.StringAttribute{Computed: true, Description: "Network lifecycle state."},
			"nics_count": schema.Int64Attribute{Computed: true, Description: "Number of attached NICs."},
		},
	}
}

func (r *networkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *networkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan networkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	locationID := int(plan.LocationID.ValueInt64())
	netType := plan.Type.ValueString()

	var task *sdk.TaskID
	var err error

	switch netType {
	case networkTypeIsolated:
		if plan.Address.IsNull() || plan.Address.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("address"), "Missing address", "address is required for isolated networks.")
			return
		}
		task, err = r.client.CreateVmwareIsolatedNetwork(ctx, &entities.VmwareCreateIsolatedNetworkRequest{
			LocationID: locationID,
			Name:       plan.Name.ValueString(),
			Address:    plan.Address.ValueString(),
			Mask:       optionalInt(plan.Mask),
			EnableDhcp: optionalBool(plan.EnableDhcp),
		})
	case networkTypeRouted:
		if plan.Address.IsNull() || plan.Address.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("address"), "Missing address", "address is required for routed networks.")
			return
		}
		task, err = r.client.CreateVmwareRoutedNetwork(ctx, &entities.VmwareCreateRoutedNetworkRequest{
			LocationID:    locationID,
			Name:          plan.Name.ValueString(),
			Address:       plan.Address.ValueString(),
			Mask:          optionalInt(plan.Mask),
			EnableDhcp:    optionalBool(plan.EnableDhcp),
			BandwidthMbps: optionalInt(plan.BandwidthMbps),
		})
	case networkTypePublic:
		if plan.Capacity.IsNull() || plan.Capacity.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("capacity"), "Missing capacity", "capacity is required for public networks.")
			return
		}
		task, err = r.client.CreateVmwarePublicNetwork(ctx, &entities.VmwareCreatePublicNetworkRequest{
			LocationID:    locationID,
			Name:          plan.Name.ValueString(),
			Capacity:      plan.Capacity.ValueString(),
			BandwidthMbps: optionalInt(plan.BandwidthMbps),
		})
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Creating VMware Network", err.Error())
		return
	}

	networkID, err := r.resolveCreatedNetworkID(ctx, task)
	if err != nil {
		resp.Diagnostics.AddError("Error Awaiting VMware Network Creation", err.Error())
		return
	}

	network, err := r.client.GetVmwareNetwork(ctx, networkID)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Created VMware Network", err.Error())
		return
	}

	state := mapNetworkToModel(network)
	// Preserve write-only create inputs (not returned by read).
	state.EnableDhcp = plan.EnableDhcp
	state.Capacity = plan.Capacity
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// resolveCreatedNetworkID waits for the create task and returns the resulting
// network ID from the task payload.
func (r *networkResource) resolveCreatedNetworkID(ctx context.Context, task *sdk.TaskID) (int, error) {
	if task == nil || task.ID == "" {
		return 0, fmt.Errorf("API did not return a task id for the create operation")
	}
	done, err := r.client.WaitVmwareTask(ctx, task.ID)
	if err != nil {
		return 0, err
	}
	if done.NetworkID == nil {
		return 0, fmt.Errorf("create task %s completed without a network id", task.ID)
	}
	return *done.NetworkID, nil
}

func (r *networkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state networkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	network, err := r.client.GetVmwareNetwork(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading VMware Network", err.Error())
		return
	}

	refreshed := mapNetworkToModel(network)
	// Preserve write-only create inputs that read does not return.
	refreshed.EnableDhcp = state.EnableDhcp
	refreshed.Capacity = state.Capacity
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *networkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state networkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(state.ID.ValueInt64())
	editReq := &entities.VmwareEditNetworkRequest{Name: plan.Name.ValueString()}
	// bandwidth is only editable for routed/public; isolated rejects it (400).
	if plan.Type.ValueString() != networkTypeIsolated && !plan.BandwidthMbps.Equal(state.BandwidthMbps) {
		editReq.BandwidthMbps = optionalInt(plan.BandwidthMbps)
	}

	tflog.Info(ctx, "Updating VMware network", map[string]any{"id": networkID})
	task, err := r.client.EditVmwareNetwork(ctx, networkID, editReq)
	if err != nil {
		resp.Diagnostics.AddError("Error Updating VMware Network", err.Error())
		return
	}
	if task != nil && task.ID != "" {
		if _, err := r.client.WaitVmwareTask(ctx, task.ID); err != nil {
			resp.Diagnostics.AddError("Error Awaiting VMware Network Update", err.Error())
			return
		}
	}

	network, err := r.client.GetVmwareNetwork(ctx, networkID)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Updated VMware Network", err.Error())
		return
	}
	newState := mapNetworkToModel(network)
	newState.EnableDhcp = plan.EnableDhcp
	newState.Capacity = plan.Capacity
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *networkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state networkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(state.ID.ValueInt64())
	task, err := r.client.DeleteVmwareNetwork(ctx, networkID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Deleting VMware Network", err.Error())
		return
	}
	if task != nil && task.ID != "" {
		if _, err := r.client.WaitVmwareTask(ctx, task.ID); err != nil {
			resp.Diagnostics.AddError("Error Awaiting VMware Network Deletion", err.Error())
			return
		}
	}
}

func (r *networkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("Expected a numeric network id, got %q: %s", req.ID, err.Error()))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
