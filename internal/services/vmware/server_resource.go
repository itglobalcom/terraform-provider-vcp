package vmware

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                = &serverResource{}
	_ resource.ResourceWithConfigure   = &serverResource{}
	_ resource.ResourceWithImportState = &serverResource{}
)

func NewServerResource() resource.Resource { return &serverResource{} }

type serverResource struct{ client *sdk.CloudClient }

func (r *serverResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_server"
}

func (r *serverResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiresReplaceInt := []planmodifier.Int64{int64planmodifier.RequiresReplace()}
	requiresReplaceStr := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description:         "Manages a VMware Cloud server (virtual machine).",
		MarkdownDescription: "Manages a VMware Cloud server. `cpu`, `ram_mb`, `system_disk_mb`, `name` and `computer_name` are editable in place; other inputs force recreation.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Computed:      true,
				Description:   "The unique identifier of the server.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"location_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Location ID where the server is created. Changing this forces recreation.",
				PlanModifiers: requiresReplaceInt,
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Display name of the server.",
			},
			"computer_name": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Guest OS hostname. If omitted, the platform derives one. " +
					"The backend normalises the value to UPPERCASE (SRV-3); a case-only difference " +
					"is suppressed so it does not produce a perpetual diff.",
				MarkdownDescription: "Guest OS hostname. If omitted, the platform derives one.\n\n" +
					"The backend normalises this value to **UPPERCASE** (review SRV-3); the provider " +
					"treats `computer_name` case-insensitively, so writing it in any case does not " +
					"cause a perpetual diff.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					caseInsensitiveStringModifier{}, // SRV-3
				},
			},
			"image_id": schema.Int64Attribute{
				Required:      true,
				Description:   "OS image/template ID. Changing this forces recreation.",
				PlanModifiers: requiresReplaceInt,
			},
			"cpu":            schema.Int64Attribute{Required: true, Description: "Number of vCPUs."},
			"ram_mb":         schema.Int64Attribute{Required: true, Description: "RAM in MB."},
			"system_disk_mb": schema.Int64Attribute{Required: true, Description: "System disk size in MB."},
			"system_disk_type": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "System disk type. Changing this forces recreation.",
				PlanModifiers: append([]planmodifier.String{stringplanmodifier.UseStateForUnknown()}, requiresReplaceStr...),
			},
			"public_network_id": schema.Int64Attribute{
				Optional:      true,
				Description:   "Public network to connect at creation. Changing this forces recreation.",
				PlanModifiers: requiresReplaceInt,
			},
			"network_bandwidth_mbps": schema.Int64Attribute{
				Optional:      true,
				Description:   "Bandwidth (Mbps) for the public interface at creation. Changing this forces recreation.",
				PlanModifiers: requiresReplaceInt,
			},
			// backup_enabled/ssh_keys/need_sysprep are write-only create inputs that
			// Update never applies; without RequiresReplace a post-create change would
			// be silently swallowed (state would diverge from the backend).
			"backup_enabled": schema.BoolAttribute{
				Optional:      true,
				Description:   "Enable backups at creation. Changing this forces recreation.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"backup_period": schema.Int64Attribute{
				Optional:      true,
				Description:   "Backup period at creation. Changing this forces recreation.",
				PlanModifiers: requiresReplaceInt,
			},
			"ssh_keys": schema.ListAttribute{
				Optional:      true,
				ElementType:   types.Int64Type,
				Description:   "SSH key IDs to inject at creation. Changing this forces recreation.",
				PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()},
			},
			"need_sysprep": schema.BoolAttribute{
				Optional:      true,
				Description:   "Run sysprep at creation. Changing this forces recreation.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"gpu": schema.SingleNestedAttribute{
				Optional:      true,
				Description:   "GPU profile. Changing this forces recreation.",
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				// When a gpu block is present the backend selects the slicing policy by
				// the exact (model_id, vram_mb, card_count) triple, so all three are
				// required — the platform does not derive vram_mb/card_count (SDK S4).
				Attributes: map[string]schema.Attribute{
					"model_id":   schema.Int64Attribute{Required: true, Description: "GPU model ID."},
					"vram_mb":    schema.Int64Attribute{Required: true, Description: "VRAM in MB."},
					"card_count": schema.Int64Attribute{Required: true, Description: "Number of GPU cards."},
				},
			},
			"state":              schema.StringAttribute{Computed: true, Description: "Server lifecycle state."},
			"is_power_on":        schema.BoolAttribute{Computed: true, Description: "Whether the server is powered on."},
			"vm_tools_installed": schema.BoolAttribute{Computed: true, Description: "Whether VMware Tools is installed (live, single-server read only)."},
			"created":            schema.StringAttribute{Computed: true, Description: "Creation timestamp."},
			"nics": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         schema.Int64Attribute{Computed: true},
						"number":     schema.Int64Attribute{Computed: true},
						"is_primary": schema.BoolAttribute{Computed: true},
						"network_id": schema.Int64Attribute{Computed: true},
						"ip":         schema.StringAttribute{Computed: true},
						"mac":        schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (r *serverResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := &entities.VmwareCreateServerRequest{
		LocationID:       int(plan.LocationID.ValueInt64()),
		Name:             plan.Name.ValueString(),
		ImageID:          int(plan.ImageID.ValueInt64()),
		CPUCount:         int(plan.CPU.ValueInt64()),
		RamMB:            int(plan.RamMB.ValueInt64()),
		SystemDiskSizeMB: int(plan.SystemDiskMB.ValueInt64()),
	}
	// computer_name is Optional+Computed: send only when the user set it, otherwise
	// let the platform derive it (avoid posting an empty hostname). It is read back
	// (possibly normalised) into state afterwards.
	if !plan.ComputerName.IsNull() && !plan.ComputerName.IsUnknown() {
		createReq.ComputerName = plan.ComputerName.ValueString()
	}
	if !plan.SystemDiskType.IsNull() && !plan.SystemDiskType.IsUnknown() {
		createReq.SystemDiskType = plan.SystemDiskType.ValueString()
	}
	createReq.PublicNetworkID = optionalInt(plan.PublicNetworkID)
	createReq.NetworkBandwidthMbps = optionalInt(plan.NetworkBandwidthMbps)
	createReq.BackupEnabled = optionalBool(plan.BackupEnabled)
	createReq.BackupPeriod = optionalInt(plan.BackupPeriod)
	createReq.NeedSysprep = optionalBool(plan.NeedSysprep)

	sshKeys, diags := int64ListToInts(ctx, plan.SSHKeys)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	createReq.SSHKeys = sshKeys

	gpu, diags := buildGpuRequest(ctx, plan.Gpu)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	createReq.GPU = gpu

	tflog.Info(ctx, "Creating VMware server", map[string]any{"name": createReq.Name})
	order, err := r.client.CreateVmwareServer(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Error Creating VMware Server", err.Error())
		return
	}
	if order.TaskID != "" {
		if _, err := r.client.WaitVmwareTask(ctx, order.TaskID); err != nil {
			resp.Diagnostics.AddError("Error Awaiting VMware Server Creation", err.Error())
			return
		}
	}

	server, err := r.client.GetVmwareServer(ctx, order.ServerID)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Created VMware Server", err.Error())
		return
	}

	state := plan // preserve write-only create inputs
	mapServerComputed(&state, server)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, err := r.client.GetVmwareServer(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading VMware Server", err.Error())
		return
	}

	mapServerComputed(&state, server) // write-only inputs already in state
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverID := int(state.ID.ValueInt64())

	// Resize (cpu / ram / disk).
	if !plan.CPU.Equal(state.CPU) || !plan.RamMB.Equal(state.RamMB) || !plan.SystemDiskMB.Equal(state.SystemDiskMB) {
		task, err := r.client.ChangeVmwareServerConfiguration(ctx, serverID, &entities.VmwareChangeConfigurationRequest{
			CPU:              int(plan.CPU.ValueInt64()),
			RamMB:            int(plan.RamMB.ValueInt64()),
			SystemDiskSizeMB: int(plan.SystemDiskMB.ValueInt64()),
		})
		if err != nil {
			resp.Diagnostics.AddError("Error Resizing VMware Server", err.Error())
			return
		}
		if err := r.waitTask(ctx, task); err != nil {
			resp.Diagnostics.AddError("Error Awaiting VMware Server Resize", err.Error())
			return
		}
	}

	// Rename (display name).
	if !plan.Name.Equal(state.Name) {
		// C-11: RenameVmwareServer now takes *entities.VmwareRenameServerRequest.
		if err := r.client.RenameVmwareServer(ctx, serverID, &entities.VmwareRenameServerRequest{Name: plan.Name.ValueString()}); err != nil {
			resp.Diagnostics.AddError("Error Renaming VMware Server", err.Error())
			return
		}
	}

	// Change guest hostname.
	if !plan.ComputerName.Equal(state.ComputerName) && !plan.ComputerName.IsNull() && !plan.ComputerName.IsUnknown() {
		task, err := r.client.ChangeVmwareServerComputerName(ctx, serverID, &entities.VmwareComputerNameRequest{
			ComputerName: plan.ComputerName.ValueString(),
		})
		if err != nil {
			resp.Diagnostics.AddError("Error Changing VMware Server Computer Name", err.Error())
			return
		}
		if err := r.waitTask(ctx, task); err != nil {
			resp.Diagnostics.AddError("Error Awaiting VMware Server Computer Name Change", err.Error())
			return
		}
	}

	server, err := r.client.GetVmwareServer(ctx, serverID)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Updated VMware Server", err.Error())
		return
	}
	newState := plan // preserve write-only create inputs
	mapServerComputed(&newState, server)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *serverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	task, err := r.client.DeleteVmwareServer(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Deleting VMware Server", err.Error())
		return
	}
	if err := r.waitTask(ctx, task); err != nil {
		resp.Diagnostics.AddError("Error Awaiting VMware Server Deletion", err.Error())
		return
	}
}

func (r *serverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("Expected a numeric server id, got %q: %s", req.ID, err.Error()))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func (r *serverResource) waitTask(ctx context.Context, task *sdk.TaskID) error {
	if task == nil || task.ID == "" {
		return nil
	}
	_, err := r.client.WaitVmwareTask(ctx, task.ID)
	return err
}

// ===================== helpers =====================

func int64ListToInts(ctx context.Context, list types.List) ([]int, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var vals []int64
	diags := list.ElementsAs(ctx, &vals, false)
	if diags.HasError() {
		return nil, diags
	}
	out := make([]int, len(vals))
	for i, v := range vals {
		out[i] = int(v)
	}
	return out, diags
}

func buildGpuRequest(ctx context.Context, obj types.Object) (*entities.VmwareGPURequest, diag.Diagnostics) {
	if obj.IsNull() || obj.IsUnknown() {
		return nil, nil
	}
	var gm struct {
		ModelID   types.Int64 `tfsdk:"model_id"`
		VramMB    types.Int64 `tfsdk:"vram_mb"`
		CardCount types.Int64 `tfsdk:"card_count"`
	}
	diags := obj.As(ctx, &gm, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}
	req := &entities.VmwareGPURequest{GPUModelID: int(gm.ModelID.ValueInt64())}
	if !gm.VramMB.IsNull() && !gm.VramMB.IsUnknown() {
		v := int(gm.VramMB.ValueInt64())
		req.VramMB = &v
	}
	if !gm.CardCount.IsNull() && !gm.CardCount.IsUnknown() {
		v := int(gm.CardCount.ValueInt64())
		req.CardCount = &v
	}
	return req, diags
}

// caseInsensitiveStringModifier suppresses a plan diff when the planned value
// differs from the prior state only by letter case. SRV-3: the backend upper-cases
// computer_name, so without this a lowercase config value would diff forever
// against the stored uppercase value.
type caseInsensitiveStringModifier struct{}

func (m caseInsensitiveStringModifier) Description(_ context.Context) string {
	return "Suppresses case-only differences (the backend upper-cases the value, SRV-3)."
}

func (m caseInsensitiveStringModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m caseInsensitiveStringModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Only act on a known planned value against an existing state value.
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if strings.EqualFold(req.StateValue.ValueString(), req.PlanValue.ValueString()) {
		resp.PlanValue = req.StateValue
	}
}
