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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                   = &serverResource{}
	_ resource.ResourceWithConfigure      = &serverResource{}
	_ resource.ResourceWithImportState    = &serverResource{}
	_ resource.ResourceWithValidateConfig = &serverResource{}
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
		Description: "Manages a VMware Cloud server (virtual machine).",
		MarkdownDescription: "Manages a VMware Cloud server.\n\n" +
			"`cpu`, `ram_mb`, `system_disk_mb`, `network_bandwidth_mbps`, `name`, `computer_name`, " +
			"`nested_hypervisor` and `volumes` are changed in place. Everything else replaces the machine.\n\n" +
			"~> **Changing `image_id` destroys the server and creates another one** — with a new id, a new address " +
			"and an empty disk. There is no way to reinstall a machine in place, so a plan that shows a " +
			"replacement here is a plan that loses the data. `location_id`, `system_disk_type`, " +
			"`public_network_id`, `gpu`, `ssh_key_ids`, `backup_*` and `need_sysprep` replace it for the same " +
			"reason.\n\n" +
			"~> Resizing a running machine needs an image that supports it (`cpu_hot_add` / `memory_hot_add` in " +
			"`vcp_vmware_images`). Without them, power the server off before applying.\n\n" +
			"### Importing\n\n" +
			"`terraform import vcp_vmware_server.<name> <id>` reads everything the API reports. It cannot report " +
			"the options that only exist at order time, so restate them in the configuration afterwards and check " +
			"the first plan is empty:\n\n" +
			"* `ssh_key_ids`, `backup_enabled`, `backup_period`, `need_sysprep` — write-only, never returned;\n" +
			"* `public_network_id` — the interface is reported, the network it was ordered from is not;\n" +
			"* `computer_name` — reported in the platform's upper-case spelling (SRV-3), which the provider " +
			"compares case-insensitively;\n" +
			"* `volumes` — an imported server manages no disks until they are declared, each with a `number` of " +
			"your choosing.",
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
				Optional: true,
				Description: "Public network to connect at creation. When set, the interface takes its bandwidth " +
					"from the network and network_bandwidth_mbps must not be set. Changing this forces recreation.",
				PlanModifiers: requiresReplaceInt,
			},
			"network_bandwidth_mbps": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Description: "Bandwidth (Mbps) of the server's primary public interface. Editable in place. " +
					"Mutually exclusive with public_network_id, which brings its own bandwidth.",
				MarkdownDescription: "Bandwidth (Mbps) of the server's primary public interface — the one every " +
					"VMware server is created with. Editable in place.\n\n" +
					"Mutually exclusive with `public_network_id`: when a network is named, the interface takes the " +
					"bandwidth of that network and a value here would be ignored.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			// backup_enabled/ssh_key_ids/need_sysprep are write-only create inputs that
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
			// A set, not a list: the API does not care in which order the keys are
			// sent, so ordering them differently must not read as a change — with a
			// list it would plan a replacement of the whole machine.
			"ssh_key_ids": schema.SetAttribute{
				Optional:      true,
				ElementType:   types.Int64Type,
				Description:   "SSH key IDs to inject at creation. Changing this forces recreation.",
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
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
			// Optional+Computed like computer_name: a machine ordered without it comes
			// back with the platform's value (off), and UseStateForUnknown is what keeps
			// the plan from showing "known after apply" on every run — an Optional+Computed
			// attribute without it is the classic perpetual diff.
			"nested_hypervisor": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Whether the guest OS may run its own hypervisor (nested virtualization). " +
					"Editable in place; switching it restarts a running machine. Cannot be combined with gpu, " +
					"and the location must offer a VDC that supports it (nested_hypervisor_supported in " +
					"vcp_vmware_locations).",
				MarkdownDescription: "Whether the guest operating system may run its own hypervisor — nested " +
					"virtualization, what the panel calls exposing hardware-assisted CPU virtualization to the " +
					"guest OS. Off unless asked for, and editable in place.\n\n" +
					"~> **Switching this restarts a running machine.** The platform powers the guest off, changes " +
					"the setting and powers it back on; a machine that is already off stays off.\n\n" +
					"~> It cannot be combined with `gpu`, and the location has to offer a VDC that supports it — " +
					"`nested_hypervisor_supported` in `vcp_vmware_locations`. Both refusals come from the " +
					"platform, and the first one is reported at plan time.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"volumes":            serverVolumesAttribute(),
			"state":              schema.StringAttribute{Computed: true, Description: "Server lifecycle state."},
			"is_power_on":        schema.BoolAttribute{Computed: true, Description: "Whether the server is powered on."},
			"vm_tools_installed": schema.BoolAttribute{Computed: true, Description: "Whether VMware Tools is installed (live, single-server read only)."},
			"created":            schema.StringAttribute{Computed: true, Description: "Creation timestamp."},
			"nics": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":             schema.Int64Attribute{Computed: true},
						"number":         schema.Int64Attribute{Computed: true},
						"is_primary":     schema.BoolAttribute{Computed: true},
						"network_id":     schema.Int64Attribute{Computed: true},
						"ip":             schema.StringAttribute{Computed: true},
						"mac":            schema.StringAttribute{Computed: true},
						"bandwidth_mbps": schema.Int64Attribute{Computed: true}, // SRV-5
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

// ValidateConfig reports what the configuration can be judged on by itself:
// attributes that describe the same interface in two ways, a machine asking for
// two mutually exclusive platform features, and a set of disks that cannot be
// told apart.
func (r *serverResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config serverModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The primary interface takes its bandwidth from the network it is put on, so
	// a value alongside public_network_id would be accepted and then ignored — the
	// state would say one thing and the platform another.
	networkSet := !config.PublicNetworkID.IsNull() && !config.PublicNetworkID.IsUnknown()
	bandwidthSet := !config.NetworkBandwidthMbps.IsNull() && !config.NetworkBandwidthMbps.IsUnknown()
	if networkSet && bandwidthSet {
		resp.Diagnostics.AddAttributeError(path.Root("network_bandwidth_mbps"), "Conflicting Attributes",
			"public_network_id and network_bandwidth_mbps describe the same interface in two ways. A server put "+
				"on a named public network takes that network's bandwidth, and the value here would be ignored. "+
				"Set one or the other.")
	}

	// GPU and nested virtualization are mutually exclusive on the platform, which
	// answers the order with APICodeVmwareOperationNotSupportedForGpuServer. Saying
	// so here means the plan fails instead of promising a machine that the apply
	// then refuses to order.
	gpuSet := !config.Gpu.IsNull() && !config.Gpu.IsUnknown()
	nestedRequested := !config.NestedHypervisor.IsNull() && !config.NestedHypervisor.IsUnknown() &&
		config.NestedHypervisor.ValueBool()
	if gpuSet && nestedRequested {
		resp.Diagnostics.AddAttributeError(path.Root("nested_hypervisor"), "Conflicting Attributes",
			"gpu and nested_hypervisor cannot be used on the same server. A machine with a GPU allocation "+
				"cannot expose hardware-assisted virtualization to its guest, so the platform refuses the order. "+
				"Drop one of the two.")
	}

	validateServerVolumes(config.Volumes, &resp.Diagnostics)
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
	// Optional+Computed: unknown means "not asked for", and the platform orders the
	// machine with nested virtualization off.
	createReq.NestedHypervisor = optionalBool(plan.NestedHypervisor)

	sshKeys, diags := int64SetToInts(ctx, plan.SSHKeyIDs)
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
		resp.Diagnostics.AddError("Error Creating VMware Server", err.Error()+nestedHypervisorFailureHint(err))
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

	// The data disks come after the machine: the order takes only the boot disk.
	if len(plan.Volumes) > 0 {
		defer locks.VmwareServer(server.ID)()
		state.Volumes = syncServerVolumes(ctx, r.client, server.ID, nil, plan.Volumes, &resp.Diagnostics)
		warnUntrackedVolumes(ctx, r.client, server.ID, state.Volumes, &resp.Diagnostics)
	}

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

	volumes, err := refreshServerVolumes(ctx, r.client, int(state.ID.ValueInt64()), state.Volumes)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading VMware Server Volumes",
			fmt.Sprintf("Could not read the disks of server %d: %s", state.ID.ValueInt64(), err.Error()))
		return
	}
	state.Volumes = volumes

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
	defer locks.VmwareServer(serverID)()

	// Resize (cpu / ram / disk).
	cpuChanged := !plan.CPU.Equal(state.CPU)
	ramChanged := !plan.RamMB.Equal(state.RamMB)
	if cpuChanged || ramChanged || !plan.SystemDiskMB.Equal(state.SystemDiskMB) {
		task, err := r.client.ChangeVmwareServerConfiguration(ctx, serverID, &entities.VmwareChangeConfigurationRequest{
			CPU:              int(plan.CPU.ValueInt64()),
			RamMB:            int(plan.RamMB.ValueInt64()),
			SystemDiskSizeMB: int(plan.SystemDiskMB.ValueInt64()),
		})
		if err != nil {
			resp.Diagnostics.AddError("Error Resizing VMware Server",
				err.Error()+r.resizeFailureHint(ctx, &state, cpuChanged, ramChanged))
			return
		}
		if err := r.waitTask(ctx, task); err != nil {
			resp.Diagnostics.AddError("Error Awaiting VMware Server Resize",
				err.Error()+r.resizeFailureHint(ctx, &state, cpuChanged, ramChanged))
			return
		}
	}

	// Bandwidth of the primary public interface. The API takes it on the interface
	// rather than on the server, and only on one attached to a shared network —
	// which the primary one is.
	if !plan.NetworkBandwidthMbps.Equal(state.NetworkBandwidthMbps) &&
		!plan.NetworkBandwidthMbps.IsNull() && !plan.NetworkBandwidthMbps.IsUnknown() {
		if !r.updatePrimaryNICBandwidth(ctx, serverID, int(plan.NetworkBandwidthMbps.ValueInt64()), &resp.Diagnostics) {
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

	// Nested virtualization: two operations rather than one flag with a target
	// state, and no replacement — the platform switches the setting on the machine
	// that is already there. A null/unknown plan value means the attribute was
	// dropped from the configuration, which is not a request to switch anything.
	if !plan.NestedHypervisor.Equal(state.NestedHypervisor) &&
		!plan.NestedHypervisor.IsNull() && !plan.NestedHypervisor.IsUnknown() {
		if !r.switchNestedHypervisor(ctx, serverID, plan.NestedHypervisor.ValueBool(), &resp.Diagnostics) {
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

	// Disks last: a failure here still has to record the disks that were created,
	// or the next apply creates them a second time.
	newState.Volumes = syncServerVolumes(ctx, r.client, serverID, state.Volumes, plan.Volumes, &resp.Diagnostics)
	warnUntrackedVolumes(ctx, r.client, serverID, newState.Volumes, &resp.Diagnostics)

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

// updatePrimaryNICBandwidth changes the bandwidth of the interface the server was
// created with, keeping it on the network it is already on.
func (r *serverResource) updatePrimaryNICBandwidth(ctx context.Context, serverID, bandwidth int,
	diags *diag.Diagnostics) bool {
	nics, err := r.client.GetVmwareServerNICs(ctx, serverID)
	if err != nil {
		diags.AddError("Error Reading VMware Server Interfaces",
			fmt.Sprintf("Could not read the interfaces of server %d to change the bandwidth: %s", serverID, err.Error()))
		return false
	}

	var primary *entities.VmwareNIC
	for _, nic := range nics {
		if nic != nil && nic.IsPrimary {
			primary = nic
			break
		}
	}
	if primary == nil {
		diags.AddError("Error Changing VMware Server Bandwidth",
			fmt.Sprintf("Server %d reports no primary interface, so there is nothing to apply the bandwidth to. "+
				"Check the server in the panel.", serverID))
		return false
	}

	tflog.Info(ctx, "Changing the bandwidth of the primary interface", map[string]any{
		"server_id": serverID, "nic_id": primary.ID, "bandwidth_mbps": bandwidth,
	})

	if _, err := r.client.UpdateVmwareNICAndWait(ctx, serverID, primary.ID, &entities.VmwareUpdateNICRequest{
		NetworkID:     primary.NetworkID,
		BandwidthMbps: &bandwidth,
	}); err != nil {
		diags.AddError("Error Changing VMware Server Bandwidth",
			fmt.Sprintf("Could not set the bandwidth of interface %d on server %d to %d Mbps: %s\n\n"+
				"The API accepts a bandwidth only on an interface attached to a shared public network. If this "+
				"server was created on a named public network (public_network_id), its bandwidth comes from that "+
				"network and cannot be set here.", primary.ID, serverID, bandwidth, err.Error()))
		return false
	}
	return true
}

// switchNestedHypervisor turns nested virtualization on or off on an existing
// machine. The platform runs it as a saga that power-cycles a running guest, and
// answers a request that matches the current state with no task at all — that is
// success with nothing to await, not an error.
func (r *serverResource) switchNestedHypervisor(ctx context.Context, serverID int, enable bool,
	diags *diag.Diagnostics) bool {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	tflog.Info(ctx, "Switching nested virtualization on a VMware server", map[string]any{
		"server_id": serverID, "nested_hypervisor": enable,
	})

	var (
		task *sdk.VmwareTaskID
		err  error
	)
	if enable {
		task, err = r.client.EnableVmwareServerNestedHypervisor(ctx, serverID)
	} else {
		task, err = r.client.DisableVmwareServerNestedHypervisor(ctx, serverID)
	}
	if err != nil {
		diags.AddError("Error Switching VMware Server Nested Hypervisor",
			fmt.Sprintf("Could not %s nested_hypervisor on server %d: %s%s",
				verb, serverID, err.Error(), nestedHypervisorFailureHint(err)))
		return false
	}
	if err := r.waitTask(ctx, task); err != nil {
		diags.AddError("Error Awaiting VMware Server Nested Hypervisor Change",
			fmt.Sprintf("The task that was to %s nested_hypervisor on server %d did not finish: %s",
				verb, serverID, err.Error()))
		return false
	}
	return true
}

// nestedHypervisorFailureHint turns the platform's three refusals of nested
// virtualization into something a user can act on: the API messages name the
// condition but not what to do about it, and the GPU one does not even mention
// nested virtualization. It returns "" for anything else, so it can be appended
// to any error of the create and switch paths.
func nestedHypervisorFailureHint(err error) string {
	switch {
	case sdk.IsVmwareOperationNotSupportedForGpuServer(err):
		return "\n\nThe platform refuses this for a machine with a GPU allocation: gpu and nested_hypervisor " +
			"are mutually exclusive. Order the machine without gpu, or leave nested_hypervisor off."
	case sdk.IsVmwareNestedHypervisorNotSupportedInLocation(err):
		return "\n\nNo VDC available to this project in that location supports nested virtualization. Check " +
			"nested_hypervisor_supported in vcp_vmware_locations and pick a location that reports true."
	case sdk.IsVmwareServerSuspended(err):
		return "\n\nThe machine is suspended, and the platform cannot change this setting on a suspended " +
			"machine. Resume it in the panel and apply again."
	}
	return ""
}

// resizeFailureHint explains the refusal a user can act on: a running machine can
// only be resized if its image supports adding CPU or memory on the fly, and the
// API's own message does not mention the image at all.
func (r *serverResource) resizeFailureHint(ctx context.Context, state *serverModel, cpuChanged, ramChanged bool) string {
	if !state.IsPowerOn.ValueBool() || (!cpuChanged && !ramChanged) {
		return ""
	}

	image, err := r.findImage(ctx, int(state.LocationID.ValueInt64()), int(state.ImageID.ValueInt64()))
	if err != nil || image == nil {
		return "\n\nIf the server is running, check whether its image supports adding CPU or memory without a " +
			"reboot (cpu_hot_add / memory_hot_add in vcp_vmware_images). When it does not, power the server off " +
			"and apply again."
	}

	var missing []string
	if cpuChanged && !image.CPUHotAdd {
		missing = append(missing, "CPU (cpu_hot_add is false)")
	}
	if ramChanged && !image.MemoryHotAdd {
		missing = append(missing, "memory (memory_hot_add is false)")
	}
	if len(missing) == 0 {
		return ""
	}

	return fmt.Sprintf("\n\nThe server is running and its image (%q) cannot have %s added on the fly. "+
		"Power the server off and apply again.", image.Name, strings.Join(missing, " or "))
}

// findImage looks the server's image up in the catalog of its location.
func (r *serverResource) findImage(ctx context.Context, locationID, imageID int) (*entities.VmwareImage, error) {
	images, err := r.client.GetVmwareImageList(ctx, &locationID, nil)
	if err != nil {
		return nil, err
	}
	for _, image := range images {
		if image != nil && image.ID == imageID {
			return image, nil
		}
	}
	return nil, nil
}

func (r *serverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverID := int(state.ID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	// DeleteVmwareServerAndWait waits for the object to disappear, not just for the
	// task to finish — the task completes first, and a dependent resource (or a
	// network the server still holds a NIC on) would otherwise race the backend.
	if err := r.client.DeleteVmwareServerAndWait(ctx, serverID); err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Deleting VMware Server", err.Error())
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

// waitTask blocks until a VMware task finishes. A nil/empty reference means the
// endpoint answered synchronously (no task was started) — that is a no-op, not
// an error, so IsZero is safe on a nil receiver here.
func (r *serverResource) waitTask(ctx context.Context, task *sdk.VmwareTaskID) error {
	if task.IsZero() {
		return nil
	}
	_, err := r.client.WaitVmwareTask(ctx, task.ID)
	return err
}

// ===================== helpers =====================

func int64SetToInts(ctx context.Context, set types.Set) ([]int, diag.Diagnostics) {
	if set.IsNull() || set.IsUnknown() {
		return nil, nil
	}
	var vals []int64
	diags := set.ElementsAs(ctx, &vals, false)
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
