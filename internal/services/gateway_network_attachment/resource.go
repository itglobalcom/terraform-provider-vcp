// Package gateway_network_attachment implements the vcp_gateway_network_attachment
// resource: a connection between a gateway and an isolated network. Modelled as a
// standalone resource so Terraform tears the attachment down before either the
// gateway or the network, allowing a shared network to be deleted in one apply.
package gateway_network_attachment

import (
	"context"
	"fmt"
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
	_ resource.Resource                = &attachmentResource{}
	_ resource.ResourceWithConfigure   = &attachmentResource{}
	_ resource.ResourceWithImportState = &attachmentResource{}
)

func NewResource() resource.Resource {
	return &attachmentResource{}
}

type attachmentResource struct {
	client *sdk.CloudClient
}

// attachmentModel — model for the vcp_gateway_network_attachment resource.
type attachmentModel struct {
	ID        types.Int64  `tfsdk:"id"`
	GatewayID types.String `tfsdk:"gateway_id"`
	NetworkID types.String `tfsdk:"network_id"`
	IPAddress types.String `tfsdk:"ip_address"`
}

func (r *attachmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_network_attachment"
}

func (r *attachmentResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Connects a gateway to an isolated network. Both endpoints force a new resource.\n\n" +
			"~> Declare at most ONE attachment per gateway+network pair. A gateway has a single NIC per " +
			"network, so a duplicate attachment resource would manage the same NIC and corrupt state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "NIC ID on the gateway.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"gateway_id": schema.StringAttribute{
				MarkdownDescription: "ID of the gateway. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"network_id": schema.StringAttribute{
				MarkdownDescription: "ID of the isolated network. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ip_address": schema.StringAttribute{
				MarkdownDescription: "IP address assigned to the gateway on the network (computed).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *attachmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *attachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan attachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := plan.GatewayID.ValueString()
	netID := plan.NetworkID.ValueString()
	defer locks.Gateway(gwID)()
	tflog.Info(ctx, "Connecting network to gateway", map[string]any{"gateway_id": gwID, "network_id": netID})

	connErr := r.client.ConnectNetworkAndWait(ctx, gwID, &entities.ConnectNetworkRequest{NetworkID: netID})
	if connErr != nil {
		resp.Diagnostics.AddError("Error Connecting Network To Gateway",
			fmt.Sprintf("Could not connect network %s to gateway %s: %s", netID, gwID, connErr.Error()))
		// The NIC may still have been created (e.g. the wait timed out while the
		// backend was Busy) — fall through and record it if it already exists, so
		// a retry does not try to connect the same network a second time.
	}

	// ConnectNetworkAndWait returns no NIC id — read the gateway back to recover it.
	gw, err := r.client.GetGateway(ctx, gwID)
	if err != nil {
		if connErr == nil {
			// Connected but unreadable: record what is known so the NIC is not
			// orphaned. The error taints the resource; the retry re-reads it.
			state := plan
			state.ID = types.Int64Null()
			state.IPAddress = types.StringNull()
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			resp.Diagnostics.AddError("Error Reading Gateway After Connect",
				fmt.Sprintf("Network %s was connected to gateway %s but the gateway could not be read back: %s\n\n"+
					"The attachment was recorded in state and marked tainted — run terraform apply again.",
					netID, gwID, err.Error()))
		}
		return
	}
	nic, ok := findNICByNetwork(gw, netID)
	if !ok {
		if connErr == nil {
			resp.Diagnostics.AddError("Error Locating Gateway NIC",
				fmt.Sprintf("Network %s connected to gateway %s but no matching NIC was found", netID, gwID))
		}
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(gwID, nic))...)
}

func (r *attachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state attachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := state.GatewayID.ValueString()
	gw, err := r.client.GetGateway(ctx, gwID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Gateway Network Attachment",
			fmt.Sprintf("Could not read gateway %s: %s", gwID, err.Error()))
		return
	}

	// network_id is the stable key (one NIC per network on a gateway).
	nic, ok := findNICByNetwork(gw, state.NetworkID.ValueString())
	if !ok {
		// Network was disconnected out of band.
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(gwID, nic))...)
}

// Update is a no-op: both endpoints are RequiresReplace, so a change never
// reaches Update.
func (r *attachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan attachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *attachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state attachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := state.GatewayID.ValueString()
	nicID := int(state.ID.ValueInt64())
	defer locks.Gateway(gwID)()

	// The backend answers HTTP 500 (not 404) when disconnecting a NIC that is
	// already gone, so always probe first: a missing gateway or NIC means
	// there is nothing to disconnect.
	gw, err := r.client.GetGateway(ctx, gwID)
	if err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Reading Gateway For Detach", err.Error())
		return
	}
	if nicID <= 0 {
		// Fall back to resolving the NIC id from the network if state lost it.
		nic, ok := findNICByNetwork(gw, state.NetworkID.ValueString())
		if !ok {
			return // already disconnected
		}
		nicID = nic.ID
	} else {
		found := false
		for _, nic := range gw.NICs {
			if nic.ID == nicID {
				found = true
				break
			}
		}
		if !found {
			tflog.Info(ctx, "Gateway network attachment already gone, treating as success",
				map[string]any{"gateway_id": gwID, "nic_id": nicID})
			return
		}
	}

	if err := r.client.DisconnectNetworkAndWait(ctx, gwID, nicID); err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Gateway network attachment already gone, treating as success",
				map[string]any{"gateway_id": gwID, "nic_id": nicID})
			return
		}
		resp.Diagnostics.AddError("Error Disconnecting Network From Gateway",
			fmt.Sprintf("Could not disconnect NIC %d from gateway %s: %s", nicID, gwID, err.Error()))
	}
}

// ImportState parses "gateway_id:network_id" and lets Read populate id/ip_address.
func (r *attachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected import ID in the form 'gateway_id:network_id', got: %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("gateway_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("network_id"), parts[1])...)
}

// findNICByNetwork looks up the gateway NIC by network_id.
func findNICByNetwork(gw *entities.Gateway, networkID string) (*entities.GatewayNIC, bool) {
	for i := range gw.NICs {
		if gw.NICs[i].NetworkID == networkID {
			return &gw.NICs[i], true
		}
	}
	return nil, false
}

// mapNIC copies gateway NIC fields into the resource model.
func mapNIC(gatewayID string, nic *entities.GatewayNIC) attachmentModel {
	return attachmentModel{
		ID:        types.Int64Value(int64(nic.ID)),
		GatewayID: types.StringValue(gatewayID),
		NetworkID: types.StringValue(nic.NetworkID),
		IPAddress: types.StringValue(nic.IPAddress),
	}
}
