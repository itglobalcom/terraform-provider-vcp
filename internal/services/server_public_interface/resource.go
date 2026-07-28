// Package server_public_interface implements the vcp_server_public_interface
// resource: a server's external (public) network interface, modelled as a
// standalone resource so all server NICs live outside vcp_server.
package server_public_interface

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
	_ resource.Resource                = &publicInterfaceResource{}
	_ resource.ResourceWithConfigure   = &publicInterfaceResource{}
	_ resource.ResourceWithImportState = &publicInterfaceResource{}
)

func NewResource() resource.Resource {
	return &publicInterfaceResource{}
}

type publicInterfaceResource struct {
	client *sdk.CloudClient
}

// publicInterfaceModel — model of the vcp_server_public_interface resource.
type publicInterfaceModel struct {
	ID            types.Int64  `tfsdk:"id"`
	ServerID      types.String `tfsdk:"server_id"`
	BandwidthMbps types.Int64  `tfsdk:"bandwidth_mbps"`
	IPAddress     types.String `tfsdk:"ip_address"`
	MAC           types.String `tfsdk:"mac"`
	Mask          types.Int64  `tfsdk:"mask"`
	Gateway       types.String `tfsdk:"gateway"`
}

func (r *publicInterfaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_public_interface"
}

func (r *publicInterfaceResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a server's external (public) network interface. " +
			"Changing `server_id` forces a new resource; `bandwidth_mbps` is updated in place.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "NIC ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server this interface belongs to. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"bandwidth_mbps": schema.Int64Attribute{
				MarkdownDescription: "Uplink bandwidth in Mbps (must be a multiple of 10).",
				Required:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(10), multipleOf(10)},
			},
			"ip_address": schema.StringAttribute{
				MarkdownDescription: "Public IP address (computed).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"mac": schema.StringAttribute{
				MarkdownDescription: "MAC address (computed).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"mask": schema.Int64Attribute{
				MarkdownDescription: "Network mask (computed).",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"gateway": schema.StringAttribute{
				MarkdownDescription: "Gateway address (computed).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *publicInterfaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *publicInterfaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan publicInterfaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := plan.ServerID.ValueString()
	defer locks.Server(serverID)()
	tflog.Info(ctx, "Creating server public interface", map[string]any{"server_id": serverID})

	// Snapshot NIC ids first: if the create succeeds but the wait fails (e.g.
	// the server wedges Busy past the timeout), the new NIC can still be
	// located and recorded instead of being leaked.
	before, snapErr := r.client.GetServerNICs(ctx, serverID)

	bandwidth := int(plan.BandwidthMbps.ValueInt64())
	nic, err := r.client.CreateServerNICAndWait(ctx, serverID, &entities.CreateNICRequest{
		BandwidthMbps: bandwidth,
	})
	if err != nil {
		resp.Diagnostics.AddError("Error Creating Server Public Interface",
			fmt.Sprintf("Could not create public interface on server %s: %s", serverID, err.Error()))
		if snapErr == nil {
			if orphan := findNewPublicNIC(ctx, r.client, serverID, before, bandwidth); orphan != nil {
				resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(serverID, orphan))...)
				resp.Diagnostics.AddWarning("Interface Recorded Despite Error",
					fmt.Sprintf("NIC %d exists on server %s and was recorded in state; the resource is tainted and the next apply replaces it.", orphan.ID, serverID))
			}
		}
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(serverID, nic))...)
}

// findNewPublicNIC returns the NIC that appeared on the server since the
// `before` snapshot and matches the requested bandwidth (the same criteria the
// SDK uses to locate a just-created public NIC), or nil.
func findNewPublicNIC(ctx context.Context, client *sdk.CloudClient, serverID string, before []entities.NIC, bandwidth int) *entities.NIC {
	after, err := client.GetServerNICs(ctx, serverID)
	if err != nil {
		return nil
	}
	known := make(map[int]bool, len(before))
	for _, nic := range before {
		known[nic.ID] = true
	}
	for i := range after {
		nic := &after[i]
		if !known[nic.ID] && nic.BandwidthMbps == bandwidth {
			return nic
		}
	}
	return nil
}

func (r *publicInterfaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state publicInterfaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := state.ServerID.ValueString()
	nic, err := r.client.GetServerNIC(ctx, serverID, int(state.ID.ValueInt64()))
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Server Public Interface",
			fmt.Sprintf("Could not read NIC %d on server %s: %s", state.ID.ValueInt64(), serverID, err.Error()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(serverID, nic))...)
}

func (r *publicInterfaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state publicInterfaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := state.ServerID.ValueString()
	nicID := int(state.ID.ValueInt64())

	if !plan.BandwidthMbps.Equal(state.BandwidthMbps) {
		defer locks.Server(serverID)()
		nic, err := r.client.UpdateServerNICAndWait(ctx, serverID, nicID, &entities.UpdateNICRequest{
			BandwidthMbps: int(plan.BandwidthMbps.ValueInt64()),
		})
		if err != nil {
			resp.Diagnostics.AddError("Error Updating Server Public Interface",
				fmt.Sprintf("Could not update bandwidth of NIC %d on server %s: %s", nicID, serverID, err.Error()))
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(serverID, nic))...)
		return
	}

	// Nothing changed — keep prior state.
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *publicInterfaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state publicInterfaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := state.ServerID.ValueString()
	defer locks.Server(serverID)()

	// The backend can answer HTTP 500 (not 404) when deleting a NIC that is
	// already gone, so probe first (same workaround as
	// vcp_server_network_attachment): a missing NIC means there is nothing to do.
	if _, err := r.client.GetServerNIC(ctx, serverID, int(state.ID.ValueInt64())); sdk.IsNotFound(err) {
		tflog.Info(ctx, "Server public interface already gone, treating as success",
			map[string]any{"server_id": serverID, "nic_id": state.ID.ValueInt64()})
		return
	}

	if err := r.client.DeleteServerNICAndWait(ctx, serverID, int(state.ID.ValueInt64())); err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Server public interface already gone, treating as success",
				map[string]any{"server_id": serverID, "nic_id": state.ID.ValueInt64()})
			return
		}
		resp.Diagnostics.AddError("Error Deleting Server Public Interface",
			fmt.Sprintf("Could not delete NIC %d on server %s: %s", state.ID.ValueInt64(), serverID, err.Error()))
	}
}

// ImportState parses "server_id:nic_id" and lets Read populate the rest.
func (r *publicInterfaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected import ID in the form 'server_id:nic_id', got: %q", req.ID))
		return
	}
	nicID, err := strconv.Atoi(parts[1])
	if err != nil || nicID <= 0 {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("NIC ID must be a positive integer, got: %q", parts[1]))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), int64(nicID))...)
}

// mapNIC transfers the API NIC fields into the resource model.
func mapNIC(serverID string, nic *entities.NIC) publicInterfaceModel {
	return publicInterfaceModel{
		ID:            types.Int64Value(int64(nic.ID)),
		ServerID:      types.StringValue(serverID),
		BandwidthMbps: types.Int64Value(int64(nic.BandwidthMbps)),
		IPAddress:     types.StringValue(nic.IPAddress),
		MAC:           types.StringValue(nic.MAC),
		Mask:          types.Int64Value(int64(nic.Mask)),
		Gateway:       types.StringValue(nic.Gateway),
	}
}
