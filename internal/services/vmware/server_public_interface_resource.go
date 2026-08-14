package vmware

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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

var (
	_ resource.Resource                = &serverPublicInterfaceResource{}
	_ resource.ResourceWithConfigure   = &serverPublicInterfaceResource{}
	_ resource.ResourceWithImportState = &serverPublicInterfaceResource{}
)

func NewServerPublicInterfaceResource() resource.Resource { return &serverPublicInterfaceResource{} }

type serverPublicInterfaceResource struct {
	client *sdk.CloudClient
}

// serverPublicInterfaceModel — model of vcp_vmware_server_public_interface.
type serverPublicInterfaceModel struct {
	ID            types.Int64  `tfsdk:"id"`
	ServerID      types.Int64  `tfsdk:"server_id"`
	BandwidthMbps types.Int64  `tfsdk:"bandwidth_mbps"`
	IsIPv6        types.Bool   `tfsdk:"is_ipv6"`
	NetworkID     types.Int64  `tfsdk:"network_id"`
	IP            types.String `tfsdk:"ip"`
	MAC           types.String `tfsdk:"mac"`
	Number        types.Int64  `tfsdk:"number"`
}

func (r *serverPublicInterfaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_server_public_interface"
}

func (r *serverPublicInterfaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adds a public address to a VMware Cloud server by attaching an interface on a shared " +
			"public network.\n\n" +
			"~> A server already comes with a public interface of its own — that one belongs to `vcp_vmware_server` " +
			"(`public_network_id` / `network_bandwidth_mbps`) and must not be managed here. This resource is for the " +
			"**additional** addresses, each of which is charged separately.\n\n" +
			"~> `bandwidth_mbps` is changed in place; the address family is not, so switching `is_ipv6` replaces the " +
			"interface and the server gets a different address. Removing an interface from a running server needs an " +
			"image that supports NIC hot-remove.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "ID of the network interface.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"server_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the server. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"bandwidth_mbps": schema.Int64Attribute{
				MarkdownDescription: "Bandwidth of the interface in Mbps. Changed in place, and read back from the " +
					"interface — a change made in the panel shows up as a difference on the next plan.",
				Required:   true,
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"is_ipv6": schema.BoolAttribute{
				MarkdownDescription: "Ask for an IPv6 address instead of IPv4. Changing this forces a new resource.",
				Optional:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"network_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the shared public network the platform picked.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"ip": schema.StringAttribute{
				MarkdownDescription: "Public address assigned to the interface.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"mac": schema.StringAttribute{
				MarkdownDescription: "MAC address of the interface.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"number": schema.Int64Attribute{
				MarkdownDescription: "Position of the interface on the server.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *serverPublicInterfaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *serverPublicInterfaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverPublicInterfaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(plan.ServerID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	tflog.Info(ctx, "Adding a public interface to a VMware server",
		map[string]any{"server_id": serverID, "bandwidth_mbps": plan.BandwidthMbps.ValueInt64()})

	// Snapshot the interfaces first: the platform picks the network, so a new
	// public interface is only recognisable as the one that was not there before.
	before := snapshotVmwareNICs(ctx, r.client, serverID)

	after, err := r.client.ConnectVmwareSharedNetworkAndWait(ctx, serverID, &entities.VmwareConnectSharedNetworkRequest{
		BandwidthMbps: int(plan.BandwidthMbps.ValueInt64()),
		IsIPv6:        optionalBool(plan.IsIPv6),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error Adding Public Interface To VMware Server",
			fmt.Sprintf("Could not add a public interface to server %d: %s", serverID, err.Error()))
		return
	}

	// The primary interface is the server's own and is never this resource's.
	nic := findNewVmwareNIC(before, after, func(n *entities.VmwareNIC) bool { return !n.IsPrimary })
	if nic == nil {
		resp.Diagnostics.AddError("Error Adding Public Interface To VMware Server",
			fmt.Sprintf("The API reported the new public interface of server %d as done, but no new interface "+
				"appeared among the server's %d interface(s). Check the server in the panel: an interface may exist "+
				"without being recorded here.", serverID, len(after)))
		return
	}

	state := plan
	mapPublicInterfaceNIC(&state, serverID, nic)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverPublicInterfaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverPublicInterfaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(state.ServerID.ValueInt64())
	nics, err := r.client.GetVmwareServerNICs(ctx, serverID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading VMware Server Public Interface",
			fmt.Sprintf("Could not read the interfaces of server %d: %s", serverID, err.Error()))
		return
	}

	nic := findVmwareNIC(nics, int(state.ID.ValueInt64()))
	if nic == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	mapPublicInterfaceNIC(&state, serverID, nic)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the bandwidth in place. It is the only editable attribute, and
// the API takes it on a shared-network interface only — which is exactly what
// this resource manages.
func (r *serverPublicInterfaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverPublicInterfaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(state.ServerID.ValueInt64())
	nicID := int(state.ID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	bandwidth := int(plan.BandwidthMbps.ValueInt64())
	tflog.Info(ctx, "Changing the bandwidth of a VMware server public interface",
		map[string]any{"server_id": serverID, "nic_id": nicID, "bandwidth_mbps": bandwidth})

	// network_id is mandatory on this endpoint — the update replaces the
	// interface's placement, so the current network keeps it where it is.
	nics, err := r.client.UpdateVmwareNICAndWait(ctx, serverID, nicID, &entities.VmwareUpdateNICRequest{
		NetworkID:     int(state.NetworkID.ValueInt64()),
		BandwidthMbps: &bandwidth,
	})
	if err != nil {
		resp.Diagnostics.AddError("Error Changing VMware Server Public Interface Bandwidth",
			fmt.Sprintf("Could not change the bandwidth of interface %d on server %d: %s",
				nicID, serverID, err.Error()))
		return
	}

	out := plan
	if nic := findVmwareNIC(nics, nicID); nic != nil {
		mapPublicInterfaceNIC(&out, serverID, nic)
	} else {
		out.ID = state.ID
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}

func (r *serverPublicInterfaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverPublicInterfaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(state.ServerID.ValueInt64())
	nicID := int(state.ID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	if err := r.client.DeleteVmwareNICAndWait(ctx, serverID, nicID); err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "VMware server public interface already gone, treating as success",
				map[string]any{"server_id": serverID, "nic_id": nicID})
			return
		}
		resp.Diagnostics.AddError("Error Removing VMware Server Public Interface",
			fmt.Sprintf("Could not delete interface %d of server %d: %s\n\n%s",
				nicID, serverID, err.Error(), nicRemovalHint(serverID)))
	}
}

// ImportState parses "server_id:nic_id" and lets Read populate the rest,
// bandwidth included (SRV-5).
func (r *serverPublicInterfaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	serverID, nicID, ok := parseServerNICImportID(req.ID)
	if !ok {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID of the form 'server_id:nic_id' with two positive integers, got: %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), serverID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), nicID)...)
}

// mapPublicInterfaceNIC fills the model from an API interface.
func mapPublicInterfaceNIC(m *serverPublicInterfaceModel, serverID int, nic *entities.VmwareNIC) {
	m.ID = types.Int64Value(int64(nic.ID))
	m.ServerID = types.Int64Value(int64(serverID))
	m.NetworkID = types.Int64Value(int64(nic.NetworkID))
	m.MAC = types.StringValue(nic.Mac)
	m.Number = types.Int64Value(int64(nic.Number))
	m.IP = types.StringNull()
	if nic.IP != nil {
		m.IP = types.StringValue(*nic.IP)
	}
	// SRV-5: the interface reports its bandwidth, so a change made in the panel
	// shows up as drift instead of being masked by the configured value. A zero
	// means the field was not populated — keep what the caller had rather than
	// record a bandwidth no interface can have.
	if nic.BandwidthMbps > 0 {
		m.BandwidthMbps = types.Int64Value(int64(nic.BandwidthMbps))
	}
}
