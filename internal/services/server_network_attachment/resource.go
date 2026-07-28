// Package server_network_attachment implements the vcp_server_network_attachment
// resource: a connection between a server and an isolated network. Modelled as a
// standalone resource so Terraform tears the attachment down before either the
// server or the network, allowing a shared network to be deleted in one apply.
package server_network_attachment

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

// attachmentModel — model of the vcp_server_network_attachment resource.
type attachmentModel struct {
	ID        types.Int64  `tfsdk:"id"`
	ServerID  types.String `tfsdk:"server_id"`
	NetworkID types.String `tfsdk:"network_id"`
	IPAddress types.String `tfsdk:"ip_address"`
	MAC       types.String `tfsdk:"mac"`
	Mask      types.Int64  `tfsdk:"mask"`
}

func (r *attachmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_network_attachment"
}

func (r *attachmentResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Attaches a server to an isolated network. All fields force a new resource " +
			"(the API recreates the NIC when the network or IP changes).",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "NIC ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"network_id": schema.StringAttribute{
				MarkdownDescription: "ID of the isolated network. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ip_address": schema.StringAttribute{
				MarkdownDescription: "IP address on the network. Auto-assigned if not specified. Changing this forces a new resource.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
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

	serverID := plan.ServerID.ValueString()
	defer locks.Server(serverID)()
	createReq := &entities.CreateNICRequest{NetworkID: plan.NetworkID.ValueString()}
	if !plan.IPAddress.IsNull() && !plan.IPAddress.IsUnknown() && plan.IPAddress.ValueString() != "" {
		createReq.IPAddress = plan.IPAddress.ValueString()
	}

	tflog.Info(ctx, "Attaching server to network", map[string]any{
		"server_id": serverID, "network_id": plan.NetworkID.ValueString(),
	})

	// Snapshot NIC ids first: if the create succeeds but the wait fails (e.g.
	// the server wedges Busy past the timeout), the new NIC can still be
	// located and recorded instead of being leaked.
	before, snapErr := r.client.GetServerNICs(ctx, serverID)

	nic, err := r.client.CreateServerNICAndWait(ctx, serverID, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Error Attaching Server To Network",
			fmt.Sprintf("Could not attach server %s to network %s: %s", serverID, plan.NetworkID.ValueString(), err.Error()))
		if snapErr == nil {
			if orphan := findNewNIC(ctx, r.client, serverID, before, createReq); orphan != nil {
				resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(serverID, orphan))...)
				resp.Diagnostics.AddWarning("Attachment Recorded Despite Error",
					fmt.Sprintf("NIC %d exists on server %s and was recorded in state; the resource is tainted and the next apply replaces it.", orphan.ID, serverID))
			}
		}
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(serverID, nic))...)
}

// findNewNIC returns the NIC that appeared on the server since the `before`
// snapshot and matches the create request (same criteria the SDK uses to
// locate a just-created NIC), or nil.
func findNewNIC(ctx context.Context, client *sdk.CloudClient, serverID string, before []entities.NIC, req *entities.CreateNICRequest) *entities.NIC {
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
		if known[nic.ID] || nic.NetworkID != req.NetworkID {
			continue
		}
		if req.IPAddress != "" && nic.IPAddress != req.IPAddress {
			continue
		}
		return nic
	}
	return nil
}

func (r *attachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state attachmentModel
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
		resp.Diagnostics.AddError("Error Reading Server Network Attachment",
			fmt.Sprintf("Could not read NIC %d on server %s: %s", state.ID.ValueInt64(), serverID, err.Error()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapNIC(serverID, nic))...)
}

// Update is a no-op: every settable field is RequiresReplace, so a change never
// reaches Update (Terraform destroys+recreates instead).
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

	serverID := state.ServerID.ValueString()
	defer locks.Server(serverID)()

	// The backend can answer HTTP 500 (not 404) when deleting a NIC that is
	// already gone, so probe first: a missing NIC means there is nothing to do.
	if _, err := r.client.GetServerNIC(ctx, serverID, int(state.ID.ValueInt64())); sdk.IsNotFound(err) {
		tflog.Info(ctx, "Server network attachment already gone, treating as success",
			map[string]any{"server_id": serverID, "nic_id": state.ID.ValueInt64()})
		return
	}

	if err := r.client.DeleteServerNICAndWait(ctx, serverID, int(state.ID.ValueInt64())); err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Server network attachment already gone, treating as success",
				map[string]any{"server_id": serverID, "nic_id": state.ID.ValueInt64()})
			return
		}
		resp.Diagnostics.AddError("Error Detaching Server From Network",
			fmt.Sprintf("Could not delete NIC %d on server %s: %s", state.ID.ValueInt64(), serverID, err.Error()))
	}
}

// ImportState parses "server_id:nic_id" and lets Read populate the rest.
func (r *attachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
func mapNIC(serverID string, nic *entities.NIC) attachmentModel {
	return attachmentModel{
		ID:        types.Int64Value(int64(nic.ID)),
		ServerID:  types.StringValue(serverID),
		NetworkID: types.StringValue(nic.NetworkID),
		IPAddress: types.StringValue(nic.IPAddress),
		MAC:       types.StringValue(nic.MAC),
		Mask:      types.Int64Value(int64(nic.Mask)),
	}
}
