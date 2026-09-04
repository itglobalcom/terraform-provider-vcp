// Package server_snapshot implements the vcp_server_snapshot resource and the
// vcp_server_snapshots data source: the point-in-time snapshots of a vStack
// server.
//
// A snapshot has no edit operation in the API, so everything the configuration
// says about one is fixed when it is taken: changing the name means taking
// another snapshot. Rolling a server back onto a snapshot is an operational
// action rather than a description of what exists, and has no place in a
// resource — do it in the panel or with the SDK.
package server_snapshot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

var (
	_ resource.Resource                = &snapshotResource{}
	_ resource.ResourceWithConfigure   = &snapshotResource{}
	_ resource.ResourceWithImportState = &snapshotResource{}
)

// snapshotNameMaxLength is the limit the API enforces on a snapshot name
// (SnapshotNameTooLong). Checking it at plan time turns a refusal that arrives
// after the apply has started into a message that names the attribute.
const snapshotNameMaxLength = 25

func NewResource() resource.Resource { return &snapshotResource{} }

type snapshotResource struct {
	client *sdk.CloudClient
}

// snapshotModel is shared by the resource and the data source: the API reports
// the same snapshot either way.
type snapshotModel struct {
	ID       types.Int64  `tfsdk:"id"`
	ServerID types.String `tfsdk:"server_id"`
	Name     types.String `tfsdk:"name"`
	SizeMB   types.Int64  `tfsdk:"size_mb"`
	Created  types.String `tfsdk:"created"`
}

func (r *snapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_snapshot"
}

func (r *snapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a snapshot of a server.",
		MarkdownDescription: "Manages a snapshot of a server — the state of its disks at the moment it was " +
			"taken.\n\n" +
			"~> A snapshot cannot be edited: the API offers no update, so changing `name` **replaces** the " +
			"snapshot — the old one is deleted and a new one is taken of the server as it is now, not as it was.\n\n" +
			"~> Taking and deleting a snapshot makes the server busy for the duration, and a snapshot keeps " +
			"growing as the server writes to disk (`size_mb` shows how much it holds). Rolling the server back " +
			"onto a snapshot is not part of this resource: restoring a machine is an action, not a description " +
			"of what exists, and Terraform has no way to express it without destroying the state it manages.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "ID of the snapshot.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server the snapshot is taken of. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("Name of the snapshot, up to %d characters. Changing this forces "+
					"a new resource — the API cannot rename a snapshot.", snapshotNameMaxLength),
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, snapshotNameMaxLength),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"size_mb": schema.Int64Attribute{
				MarkdownDescription: "Size of the snapshot in megabytes, as the platform reports it. It grows " +
					"while the server writes to its disks, so a change here is not a difference to settle.",
				Computed: true,
			},
			"created": schema.StringAttribute{
				MarkdownDescription: "When the snapshot was taken.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *snapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *snapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan snapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := plan.ServerID.ValueString()
	defer locks.Server(serverID)()

	tflog.Info(ctx, "Taking a server snapshot",
		map[string]any{"server_id": serverID, "name": plan.Name.ValueString()})

	snapshot, err := r.client.CreateServerSnapshotAndWait(ctx, serverID, &entities.CreateSnapshotRequest{
		Name: plan.Name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error Creating Server Snapshot",
			fmt.Sprintf("Could not take a snapshot named %q of server %s: %s",
				plan.Name.ValueString(), serverID, err.Error()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapSnapshot(serverID, snapshot))...)
}

func (r *snapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state snapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := state.ServerID.ValueString()
	snapshot, err := r.client.GetServerSnapshot(ctx, serverID, int(state.ID.ValueInt64()))
	if err != nil {
		// A deleted snapshot and a deleted server both answer 404, and both mean
		// the same thing here: there is nothing left to describe.
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Server Snapshot",
			fmt.Sprintf("Could not read snapshot %d of server %s: %s",
				state.ID.ValueInt64(), serverID, err.Error()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapSnapshot(serverID, snapshot))...)
}

// Update is a no-op: both settable attributes are RequiresReplace, so a change
// never reaches Update (Terraform destroys and recreates instead).
func (r *snapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan snapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *snapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state snapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := state.ServerID.ValueString()
	snapshotID := int(state.ID.ValueInt64())
	defer locks.Server(serverID)()

	if err := r.client.DeleteServerSnapshotAndWait(ctx, serverID, snapshotID); err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Server snapshot already gone, treating as success",
				map[string]any{"server_id": serverID, "snapshot_id": snapshotID})
			return
		}
		resp.Diagnostics.AddError("Error Deleting Server Snapshot",
			fmt.Sprintf("Could not delete snapshot %d of server %s: %s", snapshotID, serverID, err.Error()))
	}
}

// ImportState parses "server_id:snapshot_id" and lets Read populate the rest.
func (r *snapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	serverID, snapshotID, ok := parseImportID(req.ID)
	if !ok {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID of the form 'server_id:snapshot_id', where the snapshot id is a "+
				"positive integer, got: %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), serverID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), snapshotID)...)
}

// parseImportID splits "server_id:snapshot_id".
func parseImportID(id string) (string, int64, bool) {
	serverID, rest, found := strings.Cut(id, ":")
	if !found || serverID == "" {
		return "", 0, false
	}
	snapshotID, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || snapshotID <= 0 {
		return "", 0, false
	}
	return serverID, snapshotID, true
}

// mapSnapshot transfers an API snapshot into the model. The server id comes from
// the caller: the list response carries it, a single read need not.
func mapSnapshot(serverID string, snapshot *entities.Snapshot) snapshotModel {
	return snapshotModel{
		ID:       types.Int64Value(int64(snapshot.ID)),
		ServerID: types.StringValue(serverID),
		Name:     types.StringValue(snapshot.Name),
		SizeMB:   types.Int64Value(int64(snapshot.SizeMB)),
		Created:  types.StringValue(snapshot.Created),
	}
}
