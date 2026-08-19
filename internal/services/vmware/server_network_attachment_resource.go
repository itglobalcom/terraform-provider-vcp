package vmware

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                = &serverNetworkAttachmentResource{}
	_ resource.ResourceWithConfigure   = &serverNetworkAttachmentResource{}
	_ resource.ResourceWithImportState = &serverNetworkAttachmentResource{}
)

func NewServerNetworkAttachmentResource() resource.Resource {
	return &serverNetworkAttachmentResource{}
}

type serverNetworkAttachmentResource struct {
	client *sdk.CloudClient
}

// serverNetworkAttachmentModel — model of vcp_vmware_server_network_attachment.
type serverNetworkAttachmentModel struct {
	ID        types.Int64  `tfsdk:"id"`
	ServerID  types.Int64  `tfsdk:"server_id"`
	NetworkID types.Int64  `tfsdk:"network_id"`
	IP        types.String `tfsdk:"ip"`
	MAC       types.String `tfsdk:"mac"`
	Number    types.Int64  `tfsdk:"number"`
	IsPrimary types.Bool   `tfsdk:"is_primary"`
}

func (r *serverNetworkAttachmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_server_network_attachment"
}

func (r *serverNetworkAttachmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Attaches a VMware Cloud server to a client network — an `isolated` or a `routed` one.\n\n" +
			"A server always has a public interface of its own (see `vcp_vmware_server`), so this resource adds " +
			"private connectivity next to it. Use `vcp_vmware_server_public_interface` for an additional public address.\n\n" +
			"~> Every attribute forces a new resource: the API recreates the interface when the network or the " +
			"address changes. Detaching an interface from a running server needs an image that supports NIC " +
			"hot-remove — otherwise power the server off before applying a change that replaces it.",
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
			"network_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the client network to attach. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"ip": schema.StringAttribute{
				MarkdownDescription: "Static address on the network. Only a network with DHCP switched off accepts " +
					"one; leave it unset otherwise and the platform assigns the address. Changing this forces a new resource.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
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
			"is_primary": schema.BoolAttribute{
				MarkdownDescription: "Whether this is the server's primary interface. Always `false` here — the " +
					"primary interface belongs to `vcp_vmware_server`.",
				Computed: true,
			},
		},
	}
}

func (r *serverNetworkAttachmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serverNetworkAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverNetworkAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(plan.ServerID.ValueInt64())
	networkID := int(plan.NetworkID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	attachReq := &entities.VmwareConnectClientNetworkRequest{NetworkID: networkID}
	if isSet(plan.IP) {
		attachReq.IP = plan.IP.ValueString()
	}

	tflog.Info(ctx, "Attaching VMware server to client network",
		map[string]any{"server_id": serverID, "network_id": networkID})

	// Snapshot the interfaces first: a second attachment to the same network is
	// otherwise indistinguishable from the first one.
	before := snapshotVmwareNICs(ctx, r.client, serverID)

	after, err := r.client.ConnectVmwareClientNetworkAndWait(ctx, serverID, attachReq)
	if err != nil {
		resp.Diagnostics.AddError("Error Attaching VMware Server To Network",
			fmt.Sprintf("Could not attach server %d to network %d: %s", serverID, networkID, err.Error()))
		return
	}

	nic := findNewVmwareNIC(before, after, func(n *entities.VmwareNIC) bool {
		if n.NetworkID != networkID {
			return false
		}
		return attachReq.IP == "" || nicIP(n) == attachReq.IP
	})
	if nic == nil {
		resp.Diagnostics.AddError("Error Attaching VMware Server To Network",
			fmt.Sprintf("The API reported the attachment of server %d to network %d as done, but no new interface "+
				"on that network appeared among the server's %d interface(s). Check the server in the panel: an "+
				"interface may exist without being recorded here.", serverID, networkID, len(after)))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapAttachmentNIC(serverID, nic))...)
}

func (r *serverNetworkAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverNetworkAttachmentModel
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
		resp.Diagnostics.AddError("Error Reading VMware Server Network Attachment",
			fmt.Sprintf("Could not read the interfaces of server %d: %s", serverID, err.Error()))
		return
	}

	nic := findVmwareNIC(nics, int(state.ID.ValueInt64()))
	if nic == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, mapAttachmentNIC(serverID, nic))...)
}

// Update is a no-op: every settable attribute is RequiresReplace, so a change
// never reaches Update (Terraform destroys and recreates instead).
func (r *serverNetworkAttachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serverNetworkAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serverNetworkAttachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverNetworkAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(state.ServerID.ValueInt64())
	nicID := int(state.ID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	if err := r.client.DeleteVmwareNICAndWait(ctx, serverID, nicID); err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "VMware server network attachment already gone, treating as success",
				map[string]any{"server_id": serverID, "nic_id": nicID})
			return
		}
		resp.Diagnostics.AddError("Error Detaching VMware Server From Network",
			fmt.Sprintf("Could not delete interface %d of server %d: %s\n\n%s",
				nicID, serverID, err.Error(), nicRemovalHint(serverID)))
	}
}

// ImportState parses "server_id:nic_id" and lets Read populate the rest.
func (r *serverNetworkAttachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	serverID, nicID, ok := parseServerNICImportID(req.ID)
	if !ok {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID of the form 'server_id:nic_id' with two positive integers, got: %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), serverID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), nicID)...)
}

// parseServerNICImportID splits the "server_id:nic_id" import form.
func parseServerNICImportID(id string) (serverID, nicID int64, ok bool) {
	left, right, found := strings.Cut(id, ":")
	if !found {
		return 0, 0, false
	}
	serverID, serverErr := strconv.ParseInt(left, 10, 64)
	nicID, nicErr := strconv.ParseInt(right, 10, 64)
	if serverErr != nil || nicErr != nil || serverID <= 0 || nicID <= 0 {
		return 0, 0, false
	}
	return serverID, nicID, true
}

// mapAttachmentNIC transfers the API interface fields into the resource model.
func mapAttachmentNIC(serverID int, nic *entities.VmwareNIC) serverNetworkAttachmentModel {
	m := serverNetworkAttachmentModel{
		ID:        types.Int64Value(int64(nic.ID)),
		ServerID:  types.Int64Value(int64(serverID)),
		NetworkID: types.Int64Value(int64(nic.NetworkID)),
		IP:        types.StringNull(),
		MAC:       types.StringValue(nic.Mac),
		Number:    types.Int64Value(int64(nic.Number)),
		IsPrimary: types.BoolValue(nic.IsPrimary),
	}
	if nic.IP != nil {
		m.IP = types.StringValue(*nic.IP)
	}
	return m
}
