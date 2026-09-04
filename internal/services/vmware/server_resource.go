package vmware

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
	_ resource.ResourceWithModifyPlan     = &serverResource{}
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
			"`cpu`, `ram_mb`, `system_disk_mb`, `network_bandwidth_mbps`, `name`, `computer_name` and `volumes` " +
			"are changed in place. Everything else replaces the machine.\n\n" +
			"~> **Changing `image_id` destroys the server and creates another one** — with a new id, a new address " +
			"and an empty disk. There is no way to reinstall a machine in place, so a plan that shows a " +
			"replacement here is a plan that loses the data. `location_id`, `system_disk_type`, " +
			"`public_network_id`, `gpu`, `ssh_key_ids`, `backup_*` and `need_sysprep` replace it for the same " +
			"reason.\n\n" +
			"~> Resizing a running machine needs an image that supports it (`cpu_hot_add` / `memory_hot_add` in " +
			"`vcp_vmware_images`). Without them, power the server off before applying.\n\n" +
			"### Copying an existing machine\n\n" +
			"`copy_from_server_id` is the second way to bring a server into being: instead of ordering one from " +
			"an image, the platform duplicates a machine that already exists, disks and all. The copy takes its " +
			"whole specification from the source, so `copy_from_server_id` and `name` are the only arguments a " +
			"copy accepts — declare anything else and the plan says so rather than letting the platform ignore " +
			"it. Once the copy exists it is an ordinary server: add `cpu`, `ram_mb`, `volumes` and the rest to the " +
			"same resource and the next apply changes them in place.\n\n" +
			"~> Copying takes minutes, and `copy_from_server_id` records where the machine came from — the API " +
			"never reports it. Like the other create-only arguments, changing or removing it **replaces the " +
			"machine**, which is not what tidying up a configuration should do: keep it, or add " +
			"`lifecycle { ignore_changes = [copy_from_server_id] }`.\n\n" +
			"### Importing\n\n" +
			"`terraform import vcp_vmware_server.<name> <id>` reads everything the API reports. It cannot report " +
			"the options that only exist at order time, so restate them in the configuration afterwards and check " +
			"the first plan is empty:\n\n" +
			"* `ssh_key_ids`, `backup_enabled`, `backup_period`, `need_sysprep` — write-only, never returned;\n" +
			"* `public_network_id` — the interface is reported, the network it was ordered from is not;\n" +
			"* `computer_name` — reported in the platform's upper-case spelling (SRV-3), which the provider " +
			"compares case-insensitively;\n" +
			"* `volumes` — an imported server manages no disks until they are declared, each with a `number` of " +
			"your choosing;\n" +
			"* `copy_from_server_id` — a copy is indistinguishable from an ordered machine once it exists.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Computed:      true,
				Description:   "The unique identifier of the server.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"location_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Description: "Location ID where the server is created. Required when ordering a server; a copy " +
					"(copy_from_server_id) is created in the location of its source. Changing this forces recreation.",
				PlanModifiers: append([]planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, requiresReplaceInt...),
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Display name of the server.",
			},
			// The second way a server comes into being. It is write-only: no read
			// reports that a machine is a copy, so it is RequiresReplace like the
			// other create-only inputs — and its whole point is that a copy takes
			// its specification from the source, which is why the plan refuses the
			// order-time arguments beside it (checked in ModifyPlan, where the
			// create can be told from an update).
			//
			// client_network_id, the copy request's other field, is deliberately not
			// exposed: an interface on a client network is already
			// vcp_vmware_server_network_attachment, which reads back, imports and
			// shows drift, and a create-only duplicate of it would do none of those.
			"copy_from_server_id": schema.Int64Attribute{
				Optional: true,
				Description: "Create the server as a copy of an existing one instead of ordering it from an " +
					"image. The copy takes its whole specification from the source, so name is the only other " +
					"argument it accepts. Changing this forces recreation.",
				PlanModifiers: requiresReplaceInt,
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
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
				Optional: true,
				Computed: true,
				Description: "OS image/template ID. Required when ordering a server; a copy " +
					"(copy_from_server_id) carries the image of its source. Changing this forces recreation.",
				PlanModifiers: append([]planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, requiresReplaceInt...),
			},
			// cpu, ram_mb and system_disk_mb are Optional + Computed rather than
			// Required because a copy is created from its source's specification and
			// states none of them. Leaving one out when ordering a server is still
			// refused — by ValidateConfig, which knows which of the two the
			// configuration describes.
			"cpu": schema.Int64Attribute{
				Optional: true, Computed: true,
				Description:   "Number of vCPUs. Required when ordering a server; a copy inherits the source's.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"ram_mb": schema.Int64Attribute{
				Optional: true, Computed: true,
				Description:   "RAM in MB. Required when ordering a server; a copy inherits the source's.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"system_disk_mb": schema.Int64Attribute{
				Optional: true, Computed: true,
				Description:   "System disk size in MB. Required when ordering a server; a copy inherits the source's.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
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
			// Computed as well as Optional: the API reports the profile back, and a
			// copy of a GPU-equipped machine carries one the configuration never
			// asked for — without Computed that read would be an inconsistent
			// result after apply.
			"gpu": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "GPU profile. Changing this forces recreation.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
					objectplanmodifier.RequiresReplace(),
				},
				// When a gpu block is present the backend selects the slicing policy by
				// the exact (model_id, vram_mb, card_count) triple, so all three are
				// required — the platform does not derive vram_mb/card_count (SDK S4).
				Attributes: map[string]schema.Attribute{
					"model_id":   schema.Int64Attribute{Required: true, Description: "GPU model ID."},
					"vram_mb":    schema.Int64Attribute{Required: true, Description: "VRAM in MB."},
					"card_count": schema.Int64Attribute{Required: true, Description: "Number of GPU cards."},
				},
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
// attributes that describe the same interface in two ways, the order-time
// arguments a server cannot be ordered without, and a set of disks that cannot
// be told apart.
func (r *serverResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config serverModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The primary interface takes its bandwidth from the network it is put on, so
	// a value alongside public_network_id would be accepted and then ignored — the
	// state would say one thing and the platform another.
	networkSet := isSetInt(config.PublicNetworkID)
	bandwidthSet := isSetInt(config.NetworkBandwidthMbps)
	if networkSet && bandwidthSet {
		resp.Diagnostics.AddAttributeError(path.Root("network_bandwidth_mbps"), "Conflicting Attributes",
			"public_network_id and network_bandwidth_mbps describe the same interface in two ways. A server put "+
				"on a named public network takes that network's bandwidth, and the value here would be ignored. "+
				"Set one or the other.")
	}

	// location_id, image_id, cpu, ram_mb and system_disk_mb are Optional so that a
	// copy can leave them to its source; a server that is *ordered* still cannot
	// do without them. Reporting it here keeps the guarantee Required used to
	// give, without Required's blindness to the two ways a server is created.
	if !isDeclared(config.CopyFromServerID) {
		for _, arg := range []struct {
			name     string
			declared bool
		}{
			{"location_id", isDeclared(config.LocationID)},
			{"image_id", isDeclared(config.ImageID)},
			{"cpu", isDeclared(config.CPU)},
			{"ram_mb", isDeclared(config.RamMB)},
			{"system_disk_mb", isDeclared(config.SystemDiskMB)},
		} {
			if arg.declared {
				continue
			}
			resp.Diagnostics.AddAttributeError(path.Root(arg.name), "Missing Attribute",
				fmt.Sprintf("%s is needed to order a server. Set it, or set copy_from_server_id to create this "+
					"machine as a copy of an existing one, which takes its specification from the source.", arg.name))
		}
	}

	validateServerVolumes(config.Volumes, &resp.Diagnostics)
}

// ModifyPlan refuses, at plan time, an order-time argument beside
// copy_from_server_id — but only while the machine is being created.
//
// The copy request carries a name and nothing else, so anything else in the
// configuration would be accepted and then silently ignored. The check cannot
// live in ValidateConfig, which never sees prior state: copy_from_server_id
// stays in the configuration for the life of the machine (removing it would
// replace it), and a copy has to remain resizable like any other server.
func (r *serverResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Null plan = destroy; non-null state = the machine already exists.
	if req.Plan.Raw.IsNull() || !req.State.Raw.IsNull() {
		return
	}

	var config serverModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || !isDeclared(config.CopyFromServerID) {
		return
	}

	for _, arg := range []struct {
		name     string
		declared bool
	}{
		{"location_id", isDeclared(config.LocationID)},
		{"image_id", isDeclared(config.ImageID)},
		{"cpu", isDeclared(config.CPU)},
		{"ram_mb", isDeclared(config.RamMB)},
		{"system_disk_mb", isDeclared(config.SystemDiskMB)},
		{"system_disk_type", isDeclared(config.SystemDiskType)},
		{"public_network_id", isDeclared(config.PublicNetworkID)},
		{"network_bandwidth_mbps", isDeclared(config.NetworkBandwidthMbps)},
		{"computer_name", isDeclared(config.ComputerName)},
		{"backup_enabled", isDeclared(config.BackupEnabled)},
		{"backup_period", isDeclared(config.BackupPeriod)},
		{"need_sysprep", isDeclared(config.NeedSysprep)},
		{"ssh_key_ids", isDeclared(config.SSHKeyIDs)},
		{"gpu", isDeclared(config.Gpu)},
		{"volumes", len(config.Volumes) > 0},
	} {
		if !arg.declared {
			continue
		}
		resp.Diagnostics.AddAttributeError(path.Root(arg.name), "Argument Not Accepted By A Copy",
			fmt.Sprintf("A copy takes its whole specification from the server it is copied from, so the platform "+
				"has no way to apply %s while creating it — copy_from_server_id and name are the only arguments "+
				"a copy accepts.\n\nRemove %s to create the copy. Once it exists it is an ordinary server: add "+
				"%s back and the next apply changes it in place, or replaces the machine where that argument "+
				"requires it.", arg.name, arg.name, arg.name))
	}
}

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Two ways a server comes into being: ordered from an image, or copied from a
	// machine that already exists. Which one the configuration describes is
	// settled at plan time (ValidateConfig, ModifyPlan), so here it is one branch.
	var (
		server *entities.VmwareServer
		ok     bool
	)
	if isDeclared(plan.CopyFromServerID) {
		server, ok = r.copyServer(ctx, &plan, &resp.Diagnostics)
	} else {
		server, ok = r.orderServer(ctx, &plan, &resp.Diagnostics)
	}
	if !ok {
		return
	}

	state := plan // preserve write-only create inputs, copy_from_server_id included
	mapServerComputed(&state, server)

	// The data disks come after the machine: the order takes only the boot disk.
	if len(plan.Volumes) > 0 {
		defer locks.VmwareServer(server.ID)()
		state.Volumes = syncServerVolumes(ctx, r.client, server.ID, nil, plan.Volumes, &resp.Diagnostics)
		warnUntrackedVolumes(ctx, r.client, server.ID, state.Volumes, &resp.Diagnostics)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// copyServer duplicates an existing machine. The copy request carries a name and
// nothing else — the rest of the specification is the source's — and copying
// takes minutes.
//
// The source is locked, not the copy: the platform serializes changes per object,
// and the copy is read out of a source that must not be changing while it is.
func (r *serverResource) copyServer(ctx context.Context, plan *serverModel,
	diags *diag.Diagnostics) (*entities.VmwareServer, bool) {
	sourceID := int(plan.CopyFromServerID.ValueInt64())
	defer locks.VmwareServer(sourceID)()

	tflog.Info(ctx, "Copying VMware server", map[string]any{
		"source_server_id": sourceID, "name": plan.Name.ValueString(),
	})

	server, err := r.client.CopyVmwareServerAndWait(ctx, sourceID, &entities.VmwareCopyServerRequest{
		Name: plan.Name.ValueString(),
	})
	if err != nil {
		diags.AddError("Error Copying VMware Server",
			fmt.Sprintf("Could not copy server %d to %q: %s\n\n"+
				"If the message says the copy was created, the machine exists on the platform without being "+
				"recorded here — find it in the panel and either delete it or import it.",
				sourceID, plan.Name.ValueString(), err.Error()))
		return nil, false
	}
	return server, true
}

// orderServer creates a server from an image, which is what every argument of
// the resource beyond name and copy_from_server_id describes.
func (r *serverResource) orderServer(ctx context.Context, plan *serverModel,
	diags *diag.Diagnostics) (*entities.VmwareServer, bool) {
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

	sshKeys, keyDiags := int64SetToInts(ctx, plan.SSHKeyIDs)
	diags.Append(keyDiags...)
	if diags.HasError() {
		return nil, false
	}
	createReq.SSHKeys = sshKeys

	gpu, gpuDiags := buildGpuRequest(ctx, plan.Gpu)
	diags.Append(gpuDiags...)
	if diags.HasError() {
		return nil, false
	}
	createReq.GPU = gpu

	tflog.Info(ctx, "Creating VMware server", map[string]any{"name": createReq.Name})
	order, err := r.client.CreateVmwareServer(ctx, createReq)
	if err != nil {
		diags.AddError("Error Creating VMware Server", err.Error())
		return nil, false
	}
	if order.TaskID != "" {
		if _, err := r.client.WaitVmwareTask(ctx, order.TaskID); err != nil {
			diags.AddError("Error Awaiting VMware Server Creation", err.Error())
			return nil, false
		}
	}

	server, err := r.client.GetVmwareServer(ctx, order.ServerID)
	if err != nil {
		diags.AddError("Error Reading Created VMware Server", err.Error())
		return nil, false
	}
	return server, true
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
