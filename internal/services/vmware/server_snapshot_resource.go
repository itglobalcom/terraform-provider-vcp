package vmware

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

// The snapshot of a VMware Cloud server.
//
// A VMware server holds at most ONE snapshot, and the API addresses it without
// an id of its own: /vmware/servers/{id}/snapshot. So this is a singleton
// resource keyed by its server, like vcp_vmware_server_firewall — not a
// collection. The name stays singular for the same reason: a plural one would
// promise a set the platform cannot hold.

var (
	_ resource.Resource                = &serverSnapshotResource{}
	_ resource.ResourceWithConfigure   = &serverSnapshotResource{}
	_ resource.ResourceWithImportState = &serverSnapshotResource{}
)

func NewServerSnapshotResource() resource.Resource { return &serverSnapshotResource{} }

type serverSnapshotResource struct {
	client *sdk.CloudClient
}

// serverSnapshotModel — model of vcp_vmware_server_snapshot.
type serverSnapshotModel struct {
	ID       types.Int64  `tfsdk:"id"`
	ServerID types.Int64  `tfsdk:"server_id"`
	Name     types.String `tfsdk:"name"`
	Created  types.String `tfsdk:"created"`
}

func (r *serverSnapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_server_snapshot"
}

func (r *serverSnapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the snapshot of a VMware Cloud server.",
		MarkdownDescription: "Manages the snapshot of a VMware Cloud server — the state of the machine at the " +
			"moment it was taken.\n\n" +
			"A VMware server holds **one** snapshot at a time, addressed by the server rather than by an id of " +
			"its own. Declare at most one `vcp_vmware_server_snapshot` per server; a second one is refused by the " +
			"platform, not queued behind the first.\n\n" +
			"~> The snapshot cannot be renamed: the API offers no update, so changing `name` **replaces** it — the " +
			"existing snapshot is deleted and a new one is taken of the machine as it is now, not as it was.\n\n" +
			"~> Restoring the machine from its snapshot is not part of this resource. It is an action rather than " +
			"a description of what exists, and rolling a machine back would silently undo everything the rest of " +
			"the configuration applied to it — do it in the panel when it is needed.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "Same as `server_id` — one snapshot per server, with no id of its own in the API.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"server_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the server the snapshot is taken of. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the snapshot. Changing this forces a new resource — the API cannot " +
					"rename a snapshot.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"created": schema.StringAttribute{
				MarkdownDescription: "When the snapshot was taken.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *serverSnapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *serverSnapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverSnapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(plan.ServerID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	// The platform refuses a second snapshot (OnlyOneSnapshotIsAllowed) with a
	// code that names neither the existing snapshot nor a way out. Probing first
	// turns that into the two things the reader can actually do.
	if existing, err := r.client.GetVmwareSnapshot(ctx, serverID); err == nil {
		resp.Diagnostics.AddError("VMware Server Already Has A Snapshot",
			fmt.Sprintf("Server %d already holds a snapshot named %q (taken %s), and a VMware server can hold "+
				"only one. Either adopt it with\n\n    terraform import vcp_vmware_server_snapshot.<name> %d\n\n"+
				"or delete it in the panel and apply again — which discards the state it holds.",
				serverID, existing.Name, existing.Created, serverID))
		return
	} else if !sdk.IsNotFound(err) {
		tflog.Warn(ctx, "Could not check whether the server already has a snapshot", map[string]any{
			"server_id": serverID, "error": err.Error(),
		})
	}

	tflog.Info(ctx, "Taking a VMware server snapshot",
		map[string]any{"server_id": serverID, "name": plan.Name.ValueString()})

	snapshot, err := r.client.CreateVmwareSnapshotAndWait(ctx, serverID, &entities.VmwareCreateSnapshotRequest{
		Name: plan.Name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error Creating VMware Server Snapshot",
			fmt.Sprintf("Could not take a snapshot named %q of server %d: %s\n\n"+
				"A snapshot can only be taken of a server the platform reports as active — an operation still "+
				"running on the machine has to finish first.", plan.Name.ValueString(), serverID, err.Error()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapVmwareSnapshot(serverID, snapshot))...)
}

func (r *serverSnapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverSnapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(state.ServerID.ValueInt64())
	snapshot, err := r.client.GetVmwareSnapshot(ctx, serverID)
	if err != nil {
		// A server with no snapshot answers 200 with an empty body rather than
		// 404: the Public API drops null fields (NullValueHandling.Ignore), so the
		// "snapshot" key is absent instead of null. The SDK reports the missing
		// snapshot as ErrNotFound, which is also what a missing server gives —
		// and either way there is nothing left for this resource to describe.
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading VMware Server Snapshot",
			fmt.Sprintf("Could not read the snapshot of server %d: %s", serverID, err.Error()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapVmwareSnapshot(serverID, snapshot))...)
}

// Update is a no-op: both settable attributes are RequiresReplace, so a change
// never reaches Update (Terraform destroys and recreates instead).
func (r *serverSnapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serverSnapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serverSnapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverSnapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(state.ServerID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	if err := r.client.DeleteVmwareSnapshotAndWait(ctx, serverID); err != nil {
		// The delete answers 404 both when the snapshot is already gone and when
		// the server is; neither leaves anything to delete.
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "VMware server snapshot already gone, treating as success",
				map[string]any{"server_id": serverID})
			return
		}
		resp.Diagnostics.AddError("Error Deleting VMware Server Snapshot",
			fmt.Sprintf("Could not delete the snapshot of server %d: %s", serverID, err.Error()))
	}
}

// ImportState takes the server id — the snapshot has none of its own; Read fills
// in the name and the timestamp.
func (r *serverSnapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	serverID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || serverID <= 0 {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected the numeric id of the server whose snapshot is imported, got: %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), serverID)...)
}

// mapVmwareSnapshot fills the model from an API snapshot. The API reports only
// the name and the timestamp, so the resource is keyed by its server.
func mapVmwareSnapshot(serverID int, snapshot *entities.VmwareSnapshot) serverSnapshotModel {
	return serverSnapshotModel{
		ID:       types.Int64Value(int64(serverID)),
		ServerID: types.Int64Value(int64(serverID)),
		Name:     types.StringValue(snapshot.Name),
		Created:  types.StringValue(snapshot.Created),
	}
}
