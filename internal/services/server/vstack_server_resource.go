package vstack_server

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ServerResource{}
var _ resource.ResourceWithImportState = &ServerResource{}
var _ resource.ResourceWithValidateConfig = &ServerResource{}

func NewServerResource() resource.Resource {
	return &ServerResource{}
}

// ServerResource defines the resource implementation.
type ServerResource struct {
	client *sdk.CloudClient
}

func (r *ServerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (r *ServerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a virtual server (VM) instance.\n\n" +
			"**Note:** At least one volume with `name = \"boot\"` is required. " +
			"Changing `location_id` or `image_id` will force recreation of the server.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Server ID",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Server name",
				Required:            true,
			},
			"location_id": schema.StringAttribute{
				MarkdownDescription: "Location ID where the server will be created. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"image_id": schema.StringAttribute{
				MarkdownDescription: "OS image ID to use for the server. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			// Set, not List: the collection is order-insensitive to the API, and a
			// List + RequiresReplace would plan a destructive server replacement on
			// a pure reorder in config.
			"applications_ids": schema.SetAttribute{
				MarkdownDescription: "Set of application IDs to install on the server. Only applied during creation. **Changing this forces a new resource.**",
				Optional:            true,
				ElementType:         types.StringType,
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.RequiresReplace(),
				},
			},
			"init_script": schema.StringAttribute{
				MarkdownDescription: "Bash init script. It will run once on the first start. Only applied during creation. **Changing this forces a new resource.**",
				Optional:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"cpu": schema.Int64Attribute{
				MarkdownDescription: "Number of CPU cores",
				Required:            true,
			},
			"ram_mb": schema.Int64Attribute{
				MarkdownDescription: "RAM size in megabytes",
				Required:            true,
			},
			// SSH Keys. Set, not List: order-insensitive to the API, and a List +
			// RequiresReplace would plan a destructive server replacement on a
			// pure reorder in config.
			"ssh_key_ids": schema.SetAttribute{
				MarkdownDescription: "Set of SSH key IDs to add to the server. " +
					"SSH keys are only applied during server creation. " +
					"**Changing this forces a new resource.**",
				Optional:    true,
				ElementType: types.Int64Type,
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.RequiresReplace(),
				},
			},
			// Affinity Group
			"affinity_group_id": schema.StringAttribute{
				MarkdownDescription: "Affinity group ID to assign the server to. " +
					"This controls server placement policy (affinity or anti-affinity with other servers in the group). " +
					"**Changing this forces a new resource.**",
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tags": schema.SetAttribute{
				Description:         "Set of tags associated with the server.",
				MarkdownDescription: "Set of tags associated with the server. If attribute is omitted, all existing tags will be removed.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default: setdefault.StaticValue(
					types.SetValueMust(types.StringType, []attr.Value{}),
				),
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
			},
			// Volumes
			"volumes": schema.ListNestedAttribute{
				MarkdownDescription: "List of volumes. At least one volume with `name = \"boot\"` is required. " +
					"Volume size must be a multiple of 10240 MB (10 GB).\n\n" +
					"The `number` field is a unique identifier for each volume. " +
					"It must be unique across all volumes and is used to track volume changes.",
				Required: true,
				PlanModifiers: []planmodifier.List{
					VolumesNumberChangeModifier(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"number": schema.Int64Attribute{
							MarkdownDescription: "Unique volume number (0-9). Used to identify the volume in Terraform configuration.",
							Required:            true,
							Validators: []validator.Int64{
								int64validator.Between(0, 9),
							},
						},
						"id": schema.Int64Attribute{
							MarkdownDescription: "Volume ID from API (computed)",
							Computed:            true,
						},
						"name": schema.StringAttribute{
							MarkdownDescription: "Volume name. Use \"boot\" for boot volume.",
							Required:            true,
						},
						"size_mb": schema.Int64Attribute{
							MarkdownDescription: "Volume size in megabytes. Must be a multiple of 10240 MB (10 GB). Can only be increased.",
							Required:            true,
							Validators: []validator.Int64{
								int64validator.AtLeast(10240),
							},
						},
						"created": schema.StringAttribute{
							MarkdownDescription: "Volume creation timestamp",
							Computed:            true,
						},
					},
				},
			},
			// Network interfaces are managed as standalone resources:
			// vcp_server_public_interface (external) and vcp_server_network_attachment (isolated).

			// Computed attributes
			"state": schema.StringAttribute{
				MarkdownDescription: "Server state (New, Active, Busy, Blocked)",
				Computed:            true,
			},
			"is_power_on": schema.BoolAttribute{
				MarkdownDescription: "Whether the server is powered on",
				Computed:            true,
			},
			"created": schema.StringAttribute{
				MarkdownDescription: "Server creation timestamp",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"login": schema.StringAttribute{
				MarkdownDescription: "Server login username",
				Computed:            true,
				Sensitive:           true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Server password",
				Computed:            true,
				Sensitive:           true,
			},
		},
	}
}

func (r *ServerResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *ServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data ServerResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Validate Volumes
	if err := r.validateUniqueVolumeNumbers(data.Volumes); err != nil {
		resp.Diagnostics.AddError("Invalid Volume Configuration", err.Error())
	}

	if r.findBootVolume(data.Volumes) == nil {
		resp.Diagnostics.AddError("Missing Boot Volume",
			"At least one volume with name=\"boot\" is required")
	}

	for _, vol := range data.Volumes {
		if err := r.validateVolumeSize(vol); err != nil {
			resp.Diagnostics.AddError("Invalid Volume Size", err.Error())
		}
	}
}

func (r *ServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save plan data for later restoration
	planVolumes := data.Volumes
	planVolumeOrder := r.getVolumeOrder(data.Volumes)
	planTags := data.Tags

	// Build sorted volume specs for API
	volumeSpecs := r.buildSortedVolumeSpecs(data.Volumes)

	// Build SSH key IDs for API
	sshKeyIDs := r.buildSSHKeyIDs(data.SSHKeyIDs)

	// Build application IDs for API
	applicationIDs := r.buildApplicationIDs(data.ApplicationsIDs)

	// The server is created without NICs; interfaces are attached afterwards via
	// the vcp_server_public_interface / vcp_server_network_attachment resources.
	//
	// `Networks` MUST be an explicit empty slice, never left nil: the field has
	// no `omitempty`, so a nil slice marshals to `"networks": null`, which the
	// API reads as "assign the default public interface" — verified live: null
	// yields a NIC with a public IP, `[]` yields a server with zero NICs.
	createReq := &entities.CreateServerRequest{
		Name:           data.Name.ValueString(),
		LocationID:     data.LocationID.ValueString(),
		ImageID:        data.ImageID.ValueString(),
		CPU:            int(data.CPU.ValueInt64()),
		RamMB:          int(data.RamMB.ValueInt64()),
		Volumes:        volumeSpecs,
		Networks:       []entities.NetworkSpec{},
		SSHKeyIDs:      sshKeyIDs,
		ApplicationIDs: applicationIDs,
	}

	// Add init script if specified
	if !data.InitScript.IsNull() && data.InitScript.ValueString() != "" {
		createReq.ServerInitScript = data.InitScript.ValueString()
	}

	// Add affinity group if specified
	if !data.AffinityGroupID.IsNull() && data.AffinityGroupID.ValueString() != "" {
		createReq.AffinityGroupID = data.AffinityGroupID.ValueString()
	}

	tflog.Info(ctx, "Creating server", map[string]any{
		"name":        createReq.Name,
		"location_id": createReq.LocationID,
		"image_id":    createReq.ImageID,
	})

	server, err := r.client.CreateServerAndWait(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Error Creating Server", fmt.Sprintf("Could not create server: %s", err.Error()))
		return
	}

	tflog.Info(ctx, "Server created successfully", map[string]any{
		"id": server.ID,
	})

	// Handle tags if specified. From this point on the server exists and is
	// billed, so no path may return before State.Set below — otherwise a
	// transient error would orphan a real server from state and the next
	// apply would create a duplicate.
	tagsRefreshed := true // whether `server` reflects the tags added below
	if !planTags.IsNull() && len(planTags.Elements()) > 0 {
		var tags []string
		tagDiags := planTags.ElementsAs(ctx, &tags, false)
		resp.Diagnostics.Append(tagDiags...)
		if !tagDiags.HasError() {
			for _, tag := range tags {
				tagReq := &entities.CreateServerTagRequest{Tag: tag}
				err := r.client.CreateServerTag(ctx, server.ID, tagReq)
				if err != nil {
					resp.Diagnostics.AddError(
						"Error Adding Server Tag",
						fmt.Sprintf("Could not add tag %q to server %s: %s", tag, server.ID, err.Error()),
					)
				}
			}

			// Refresh server to get updated tags
			refreshed, rerr := r.client.GetServer(ctx, server.ID)
			if rerr != nil {
				tagsRefreshed = false
				resp.Diagnostics.AddWarning(
					"Server Created But Refresh Failed",
					fmt.Sprintf("Server %s was created and tagged, but reading it back failed: %s. "+
						"State was saved from the create response and will be refreshed on the next plan.",
						server.ID, rerr.Error()),
				)
			} else {
				server = refreshed
			}
		}
	}

	r.mapServerToModel(ctx, server, &data)
	if !tagsRefreshed {
		// The tag calls above succeeded (any failure added an error diagnostic,
		// which fails the apply anyway), so the plan's tags are authoritative.
		data.Tags = planTags
	}

	// Preserve init_script and applications_ids from plan (API doesn't return them)
	// They are already in data from the plan

	// Validate volume count. Error without return: the server must still be
	// recorded in state below (tainted) — returning here would orphan it.
	if len(data.Volumes) != len(planVolumes) {
		resp.Diagnostics.AddError(
			"Volume Count Mismatch",
			fmt.Sprintf("Expected %d volumes, but API returned %d", len(planVolumes), len(data.Volumes)),
		)
	}

	// Assign volume numbers after create
	r.assignVolumeNumbersAfterCreate(&data.Volumes, planVolumes)
	// In the mismatch case the matcher can leave volumes without a number;
	// give them free ones so the recorded (tainted) state stays well-formed.
	used := make(map[int64]bool, len(data.Volumes))
	for _, vol := range data.Volumes {
		if !vol.Number.IsNull() && !vol.Number.IsUnknown() {
			used[vol.Number.ValueInt64()] = true
		}
	}
	r.restoreAndAssignVolumeNumbers(&data.Volumes, nil, used)
	data.Volumes = r.sortVolumesByPlanOrder(data.Volumes, planVolumeOrder)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := data.ID.ValueString()

	// Save state data for restoration
	stateVolumeOrder := r.getVolumeOrder(data.Volumes)
	stateVolumeNumbers := r.extractVolumeNumbers(data.Volumes)
	stateSSHKeyIDs := data.SSHKeyIDs
	stateAffinityGroupID := data.AffinityGroupID
	stateInitScript := data.InitScript
	stateApplicationsIDs := data.ApplicationsIDs

	usedNumbers := make(map[int64]bool)
	for _, num := range stateVolumeNumbers {
		usedNumbers[num] = true
	}

	server, err := r.client.GetServer(ctx, serverID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Server",
			fmt.Sprintf("Could not read server %s: %s", serverID, err.Error()))
		return
	}

	r.mapServerToModel(ctx, server, &data)

	if len(stateSSHKeyIDs) > 0 {
		data.SSHKeyIDs = stateSSHKeyIDs
	}

	// Restore affinity group ID from state if API doesn't return it.
	// Deliberate, NOT drift masking: a server cannot leave its affinity group
	// without being recreated (backend rule), so an empty value in a GET can
	// only be an API omission — never a real membership change.
	if !stateAffinityGroupID.IsNull() && data.AffinityGroupID.IsNull() {
		data.AffinityGroupID = stateAffinityGroupID
	}

	// Restore init_script from state (API doesn't return it)
	if !stateInitScript.IsNull() {
		data.InitScript = stateInitScript
	}

	// Restore applications_ids from state (API doesn't return them)
	if len(stateApplicationsIDs) > 0 {
		data.ApplicationsIDs = stateApplicationsIDs
	}

	// Restore volume numbers and order
	r.restoreAndAssignVolumeNumbers(&data.Volumes, stateVolumeNumbers, usedNumbers)
	data.Volumes = r.sortVolumesByPlanOrder(data.Volumes, stateVolumeOrder)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save plan order
	planVolumeOrder := r.getVolumeOrder(plan.Volumes)

	serverID := state.ID.ValueString()
	// Serialize with NIC resources of this same server (patch CPU/RAM and NIC
	// operations conflict in the same apply otherwise: -19605/-19803).
	defer locks.Server(serverID)()

	// 1. Update CPU/RAM
	needsResourceUpdate := false
	patchReq := &entities.PatchServerRequest{}

	if !plan.CPU.Equal(state.CPU) {
		cpu := int(plan.CPU.ValueInt64())
		patchReq.CPU = &cpu
		needsResourceUpdate = true
	}

	if !plan.RamMB.Equal(state.RamMB) {
		ramMB := int(plan.RamMB.ValueInt64())
		patchReq.RamMB = &ramMB
		needsResourceUpdate = true
	}

	if needsResourceUpdate {
		_, err := r.client.PatchServerAndWait(ctx, serverID, patchReq)
		if err != nil {
			resp.Diagnostics.AddError("Error Updating Server Resources",
				fmt.Sprintf("Could not update server %s: %s", serverID, err.Error()))
			return
		}
	}

	// 2. Update name
	if !plan.Name.Equal(state.Name) {
		renameReq := &entities.RenameServerRequest{Name: plan.Name.ValueString()}
		_, err := r.client.RenameServerAndWait(ctx, serverID, renameReq)
		if err != nil {
			resp.Diagnostics.AddError("Error Renaming Server",
				fmt.Sprintf("Could not rename server %s: %s", serverID, err.Error()))
			return
		}
	}

	// 3. Update volumes
	if err := r.updateVolumes(ctx, serverID, state.Volumes, &plan.Volumes); err != nil {
		resp.Diagnostics.AddError("Error Updating Volumes",
			fmt.Sprintf("Could not update volumes for server %s: %s", serverID, err.Error()))
		// updateVolumes may have created/resized/deleted volumes before failing;
		// persist what actually exists so the ID of a just-created volume is not
		// lost (an unrecorded volume would be re-adopted under an arbitrary
		// number — or deleted — on the next apply).
		r.persistServerState(ctx, serverID, plan, state, resp)
		return
	}

	// 4. Update Tags
	if !plan.Tags.Equal(state.Tags) {
		var planTags, stateTags []string

		if plan.Tags.IsNull() {
			tflog.Debug(ctx, "Plan tags is NULL, will use empty list")
			planTags = []string{}
		} else {
			diags := plan.Tags.ElementsAs(ctx, &planTags, false)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
		}
		tflog.Debug(ctx, "Extracted plan tags", map[string]any{
			"tags":  planTags,
			"count": len(planTags),
		})

		if state.Tags.IsNull() {
			tflog.Debug(ctx, "State tags is NULL, will use empty list")
			stateTags = []string{}
		} else {
			diags := state.Tags.ElementsAs(ctx, &stateTags, false)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
		}
		tflog.Debug(ctx, "Extracted state tags", map[string]any{
			"tags":  stateTags,
			"count": len(stateTags),
		})

		// Add new tags
		for _, tag := range planTags {
			if !slices.Contains(stateTags, tag) {
				tagReq := &entities.CreateServerTagRequest{Tag: tag}
				err := r.client.CreateServerTag(ctx, serverID, tagReq)
				if err != nil {
					resp.Diagnostics.AddError("Error Adding Tag",
						fmt.Sprintf("Could not add tag '%s' to server %s: %s", tag, serverID, err.Error()))
					return
				}
			}
		}

		// Remove old tags
		for _, tag := range stateTags {
			if !slices.Contains(planTags, tag) {
				err := r.client.DeleteServerTag(ctx, serverID, tag)
				if err != nil {
					resp.Diagnostics.AddError("Error Removing Tag",
						fmt.Sprintf("Could not remove tag '%s' from server %s: %s", tag, serverID, err.Error()))
					return
				}
			}
		}
	}

	// 5. Read final state
	server, err := r.client.GetServer(ctx, serverID)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Server After Update",
			fmt.Sprintf("Could not read server %s: %s", serverID, err.Error()))
		return
	}

	// Seed from prior state: mapServerToModel leaves login/password (and
	// ssh_key_ids) untouched when the API returns them empty, and a zero
	// model would then hold null where the plan promised the prior known
	// value — "inconsistent result after apply".
	finalState := state
	r.mapServerToModel(ctx, server, &finalState)

	// Restore volume numbers from plan
	r.restoreVolumeNumbersFromPlan(&finalState.Volumes, plan.Volumes)

	// Restore volume order
	finalState.Volumes = r.sortVolumesByPlanOrder(finalState.Volumes, planVolumeOrder)

	// Preserve init_script and applications_ids from plan (API doesn't return them)
	finalState.InitScript = plan.InitScript
	finalState.ApplicationsIDs = plan.ApplicationsIDs

	// Preserve SSH key IDs from plan
	finalState.SSHKeyIDs = plan.SSHKeyIDs

	resp.Diagnostics.Append(resp.State.Set(ctx, &finalState)...)
}

// persistServerState best-effort refreshes the server and records it in state
// after a partially-applied Update, so IDs of volumes that were already
// created are not lost. Volume numbers are restored from both the prior state
// and the (partially updated) plan, whose entries carry the fresh IDs that
// updateVolumes assigned before failing. If the refresh itself fails, the
// prior state is kept — the framework preserves it automatically.
func (r *ServerResource) persistServerState(ctx context.Context, serverID string, plan, state ServerResourceModel, resp *resource.UpdateResponse) {
	server, err := r.client.GetServer(ctx, serverID)
	if err != nil {
		tflog.Warn(ctx, "Could not refresh server after failed update; keeping prior state",
			map[string]any{"id": serverID, "error": err.Error()})
		return
	}

	data := state // start from prior state: every field keeps a known value
	r.mapServerToModel(ctx, server, &data)

	numbersByID := r.extractVolumeNumbers(state.Volumes)
	for id, num := range r.extractVolumeNumbers(plan.Volumes) {
		numbersByID[id] = num
	}
	used := make(map[int64]bool, len(numbersByID))
	for _, num := range numbersByID {
		used[num] = true
	}
	r.restoreAndAssignVolumeNumbers(&data.Volumes, numbersByID, used)
	data.Volumes = r.sortVolumesByPlanOrder(data.Volumes, r.getVolumeOrder(plan.Volumes))

	// Not returned by the API; keep the prior values.
	data.InitScript = state.InitScript
	data.ApplicationsIDs = state.ApplicationsIDs
	if len(state.SSHKeyIDs) > 0 {
		data.SSHKeyIDs = state.SSHKeyIDs
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := data.ID.ValueString()

	tflog.Info(ctx, "Deleting server", map[string]any{
		"id": serverID,
	})

	err := r.client.DeleteServer(ctx, serverID)
	if err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Server already deleted (404), treating as success", map[string]any{
				"id": serverID,
			})
			return
		}
		resp.Diagnostics.AddError(
			"Error Deleting Server",
			fmt.Sprintf("Could not delete server %s: %s", serverID, err.Error()),
		)
		return
	}

	tflog.Info(ctx, "Server deleted successfully", map[string]any{
		"id": serverID,
	})

	// Deletion is asynchronous; wait until the server is gone so dependent resources
	// (isolated networks, SSH keys) aren't torn down while the server still references them.
	if err := r.waitServerGone(ctx, serverID); err != nil {
		tflog.Warn(ctx, "Timed out waiting for server to disappear after delete", map[string]any{
			"id":    serverID,
			"error": err.Error(),
		})
	}
}

// waitServerGone polls until the server no longer exists (deletion is async).
// The cap matches the 5-minute backend task ceiling (see provider.go).
func (r *ServerResource) waitServerGone(ctx context.Context, serverID string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := r.client.GetServer(ctx, serverID); sdk.IsNotFound(err) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *ServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	serverID := req.ID

	server, err := r.client.GetServer(ctx, serverID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Importing Server",
			fmt.Sprintf("Could not read server %s: %s", serverID, err.Error()),
		)
		return
	}

	var data ServerResourceModel
	r.mapServerToModel(ctx, server, &data)

	// Auto-generate volume numbers based on index
	for i := range data.Volumes {
		data.Volumes[i].Number = types.Int64Value(int64(i))
	}

	// Set init_script and applications_ids to null (not available from API)
	data.InitScript = types.StringNull()
	data.ApplicationsIDs = nil

	sshKeyInfo := "no SSH keys"
	if len(data.SSHKeyIDs) > 0 {
		sshKeyInfo = fmt.Sprintf("%d SSH key(s)", len(data.SSHKeyIDs))
	}

	tagInfo := "no tags"
	if !data.Tags.IsNull() && len(data.Tags.Elements()) > 0 {
		tagInfo = fmt.Sprintf("%d tag(s)", len(data.Tags.Elements()))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

	resp.Diagnostics.AddWarning(
		"Import Complete",
		fmt.Sprintf(
			"Imported server '%s' with %d volume(s), %s, and %s. "+
				"Volume numbers have been auto-generated (0, 1, 2, ...). "+
				"Network interfaces are separate resources (vcp_server_public_interface, "+
				"vcp_server_network_attachment) and must be imported separately. "+
				"SSH keys are only applied at creation and cannot be modified. "+
				"init_script and applications_ids are not available from API and must be set manually if needed. "+
				"Review and update your configuration accordingly.",
			serverID,
			len(data.Volumes),
			sshKeyInfo,
			tagInfo,
		),
	)
}

// ============================================================================
// MAPPING HELPERS
// ============================================================================

func (r *ServerResource) mapServerToModel(ctx context.Context, server *entities.Server, model *ServerResourceModel) {
	model.ID = types.StringValue(server.ID)
	model.Name = types.StringValue(server.Name)
	model.LocationID = types.StringValue(server.LocationID)
	model.ImageID = types.StringValue(server.ImageID)
	model.CPU = types.Int64Value(int64(server.CPU))
	model.RamMB = types.Int64Value(int64(server.RamMB))
	model.State = types.StringValue(server.State)
	model.IsPowerOn = types.BoolValue(server.IsPowerOn)
	model.Created = types.StringValue(server.Created)

	// login/password: the API currently echoes both on every GET. If it ever
	// stops (e.g. password hardening), keep the known value the model already
	// carries (plan on Create, prior state on Read/Update) — but an unknown
	// must never survive into state, so it degrades to null.
	if server.Login != "" {
		model.Login = types.StringValue(server.Login)
	} else if model.Login.IsUnknown() {
		model.Login = types.StringNull()
	}
	if server.Password != "" {
		model.Password = types.StringValue(server.Password)
	} else if model.Password.IsUnknown() {
		model.Password = types.StringNull()
	}

	if len(server.SSHKeyIDs) > 0 {
		model.SSHKeyIDs = make([]types.Int64, len(server.SSHKeyIDs))
		for i, id := range server.SSHKeyIDs {
			model.SSHKeyIDs[i] = types.Int64Value(int64(id))
		}
	}

	// Map affinity group ID from API
	if server.AffinityGroupID != "" {
		model.AffinityGroupID = types.StringValue(server.AffinityGroupID)
	} else {
		model.AffinityGroupID = types.StringNull()
	}

	// Map tags from API
	if len(server.Tags) > 0 {
		tagValues := make([]attr.Value, len(server.Tags))
		for i, tag := range server.Tags {
			tagValues[i] = types.StringValue(tag)
		}
		model.Tags = types.SetValueMust(types.StringType, tagValues)
	} else {
		model.Tags = types.SetValueMust(types.StringType, []attr.Value{})
	}

	// Map volumes
	model.Volumes = r.mapVolumesFromServer(server.Volumes)
}

func (r *ServerResource) restoreVolumeNumbersFromPlan(apiVolumes *[]VolumeAttrModel, planVolumes []VolumeAttrModel) {
	planVolumesByID := make(map[int64]int64)
	for _, vol := range planVolumes {
		if !vol.ID.IsNull() && vol.ID.ValueInt64() != 0 {
			planVolumesByID[vol.ID.ValueInt64()] = vol.Number.ValueInt64()
		}
	}

	planVolumesByNumber := make(map[int64]VolumeAttrModel)
	for _, vol := range planVolumes {
		planVolumesByNumber[vol.Number.ValueInt64()] = vol
	}

	for i := range *apiVolumes {
		vol := &(*apiVolumes)[i]
		apiID := vol.ID.ValueInt64()

		if number, exists := planVolumesByID[apiID]; exists {
			vol.Number = types.Int64Value(number)
			continue
		}

		for num, planVol := range planVolumesByNumber {
			if planVol.ID.IsNull() || planVol.ID.ValueInt64() == 0 {
				if vol.Name.ValueString() == planVol.Name.ValueString() &&
					vol.SizeMB.ValueInt64() == planVol.SizeMB.ValueInt64() {
					vol.Number = types.Int64Value(num)
					delete(planVolumesByNumber, num)
					break
				}
			}
		}
	}
}

// buildSSHKeyIDs converts terraform list to API format
func (r *ServerResource) buildSSHKeyIDs(sshKeyIDs []types.Int64) []int {
	if len(sshKeyIDs) == 0 {
		return nil
	}

	result := make([]int, 0, len(sshKeyIDs))
	for _, id := range sshKeyIDs {
		if !id.IsNull() && !id.IsUnknown() {
			result = append(result, int(id.ValueInt64()))
		}
	}
	return result
}

// buildApplicationIDs converts terraform list to API format
func (r *ServerResource) buildApplicationIDs(appIDs []types.String) []string {
	if len(appIDs) == 0 {
		return nil
	}

	result := make([]string, 0, len(appIDs))
	for _, id := range appIDs {
		if !id.IsNull() && !id.IsUnknown() {
			result = append(result, id.ValueString())
		}
	}
	return result
}
