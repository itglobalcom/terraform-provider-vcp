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
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

const (
	networkTypeIsolated = "isolated"
	networkTypeRouted   = "routed"
	networkTypePublic   = "public"
)

// coarseNetworkType maps the granular network type returned by the API
// (entities.VmwareNetworkType*) back to the coarse selector this resource
// accepts on input (isolated/routed/public). Without this the read value
// ("routed_client", "public_shared", …) would never equal the configured
// `type` and every apply would fail with "inconsistent result after apply";
// it also lets ImportState recover `type` from a read. The mapping is
// many-to-one and deterministic in this direction.
func coarseNetworkType(apiType string) string {
	switch apiType {
	case entities.VmwareNetworkTypePrivateClient:
		return networkTypeIsolated
	case entities.VmwareNetworkTypeRoutedClient:
		return networkTypeRouted
	case entities.VmwareNetworkTypePublicClient,
		entities.VmwareNetworkTypePublicShared,
		entities.VmwareNetworkTypePublicSharedIPv6:
		return networkTypePublic
	default:
		return apiType // unknown future type: surface it as-is
	}
}

var (
	_ resource.Resource                   = &networkResource{}
	_ resource.ResourceWithConfigure      = &networkResource{}
	_ resource.ResourceWithImportState    = &networkResource{}
	_ resource.ResourceWithValidateConfig = &networkResource{}
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

// ValidateConfig refuses the attributes that belong to another flavour of
// network, and asks for the ones this flavour cannot do without.
//
// Every one of these is refused by the API too, but with a message that names
// neither the field nor the reason — an isolated network given a bandwidth
// answers a flat "Network bandwidth outside allowable limits" (NET-3), which
// reads as "pick another number" rather than "this network has no bandwidth".
// Deciding it here also means the answer arrives at plan time, before anything
// is created.
func (r *networkResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config networkModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A type taken from another resource is not known yet; the per-attribute
	// validators still apply, and the API remains the backstop.
	if !isSet(config.Type) {
		return
	}
	netType := config.Type.ValueString()

	notApplicable := func(attr string, set bool, why string) {
		if set {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Attribute Not Applicable To This Network",
				fmt.Sprintf("%s does not apply to a %s network: %s.", attr, netType, why))
		}
	}
	required := func(attr string, set bool, why string) {
		if !set {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Missing Attribute",
				fmt.Sprintf("%s is required for a %s network: %s.", attr, netType, why))
		}
	}

	addressSet := isSet(config.Address)
	maskSet := !config.Mask.IsNull() && !config.Mask.IsUnknown()
	dhcpSet := !config.EnableDhcp.IsNull() && !config.EnableDhcp.IsUnknown()
	capacitySet := isSet(config.Capacity)
	bandwidthSet := !config.BandwidthMbps.IsNull() && !config.BandwidthMbps.IsUnknown()

	switch netType {
	case networkTypeIsolated:
		required("address", addressSet, "an isolated network is defined by the range it hands out")
		notApplicable("capacity", capacitySet, "capacity orders public addresses, and an isolated network has none")
		notApplicable("bandwidth_mbps", bandwidthSet,
			"an isolated network carries no traffic beyond itself and has no bandwidth to shape")
	case networkTypeRouted:
		required("address", addressSet, "a routed network is defined by the range it hands out")
		notApplicable("capacity", capacitySet, "capacity orders public addresses, and a routed network has none")
	case networkTypePublic:
		required("capacity", capacitySet, "a public network is ordered by how many addresses it carries")
		notApplicable("address", addressSet, "the platform assigns the range of a public network")
		notApplicable("mask", maskSet, "the platform assigns the range of a public network")
		notApplicable("enable_dhcp", dhcpSet, "a public network has no DHCP of its own to switch")
	}
}

func (r *networkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan networkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	locationID := int(plan.LocationID.ValueInt64())
	netType := plan.Type.ValueString()

	var task *sdk.VmwareTaskID
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
	// Map the granular API type back to the coarse input selector so it equals
	// the configured `type` (isolated/routed/public).
	state.Type = types.StringValue(coarseNetworkType(network.Type))
	// Preserve write-only create inputs (not returned by read).
	state.EnableDhcp = plan.EnableDhcp
	state.Capacity = plan.Capacity
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// resolveCreatedNetworkID waits for the create task and returns the resulting
// network ID from the task payload.
func (r *networkResource) resolveCreatedNetworkID(ctx context.Context, task *sdk.VmwareTaskID) (int, error) {
	if task.IsZero() {
		return 0, fmt.Errorf("API did not return a task id for the create operation")
	}
	done, err := r.client.WaitVmwareTask(ctx, task.ID)
	if err != nil {
		return 0, err
	}
	// Unified Task model: the created network id is reported via resources[]
	// (type "network"), not a dedicated network_id field.
	networkID, ok := done.NetworkID()
	if !ok {
		return 0, fmt.Errorf("create task %s completed without a network id", task.ID)
	}
	return networkID, nil
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
	refreshed.Type = types.StringValue(coarseNetworkType(network.Type))
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
	defer locks.VmwareNetwork(networkID)()

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
	// EditVmwareNetwork answers a no-op change synchronously, without a task.
	if !task.IsZero() {
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
	newState.Type = types.StringValue(coarseNetworkType(network.Type))
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
	defer locks.VmwareNetwork(networkID)()

	task, err := r.client.DeleteVmwareNetwork(ctx, networkID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		// The API refuses to delete a network anything is still attached to, and its
		// message does not say what to detach.
		if sdk.IsNetworkInUse(err) {
			resp.Diagnostics.AddError("Error Deleting VMware Network",
				fmt.Sprintf("Network %d still has servers connected to it: %s\n\n"+
					"Destroy the vcp_vmware_server_network_attachment resources that use it first. An interface "+
					"created outside Terraform holds the network too — remove it from the panel.", networkID, err.Error()))
			return
		}
		resp.Diagnostics.AddError("Error Deleting VMware Network", err.Error())
		return
	}
	if !task.IsZero() {
		if _, err := r.client.WaitVmwareTask(ctx, task.ID); err != nil {
			resp.Diagnostics.AddError("Error Awaiting VMware Network Deletion", err.Error())
			return
		}
	}
	// The delete task finishes before the network disappears, so wait for the
	// object to be gone — otherwise a dependent resource (or a re-create of the
	// same address range) races the backend.
	if err := r.client.WaitVmwareNetworkGone(ctx, networkID); err != nil {
		resp.Diagnostics.AddError("Error Awaiting VMware Network Deletion", err.Error())
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
