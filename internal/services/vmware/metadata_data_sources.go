package vmware

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// configureClient extracts the SDK client from provider data (shared by all
// VMware data sources).
func configureClient(providerData any, diags interface {
	AddError(string, string)
}) *sdk.CloudClient {
	if providerData == nil {
		return nil
	}
	client, ok := providerData.(*sdk.CloudClient)
	if !ok {
		diags.AddError("Unexpected Configure Type", fmt.Sprintf("Expected *sdk.CloudClient, got %T", providerData))
		return nil
	}
	return client
}

func int64AttrVals(src []int) []attr.Value {
	out := make([]attr.Value, len(src))
	for i, v := range src {
		out[i] = types.Int64Value(int64(v))
	}
	return out
}

func optionalInt(v types.Int64) *int {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	x := int(v.ValueInt64())
	return &x
}

func optionalBool(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	x := v.ValueBool()
	return &x
}

func optionalString(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	x := v.ValueString()
	return &x
}

// ===================== Locations =====================

var (
	_ datasource.DataSource              = &locationsDataSource{}
	_ datasource.DataSourceWithConfigure = &locationsDataSource{}
)

func NewLocationsDataSource() datasource.DataSource { return &locationsDataSource{} }

type locationsDataSource struct{ client *sdk.CloudClient }

type vmwareLocationModel struct {
	ID           types.Int64  `tfsdk:"id"`
	TechTitle    types.String `tfsdk:"tech_title"`
	GpuSupported types.Bool   `tfsdk:"gpu_supported"`
	// NestedHypervisorSupported is derived from the VDCs this project may be
	// provisioned in, so the same location can report differently for two
	// projects — it is the capability to consult before ordering a server with
	// nested_hypervisor, not a property of the datacenter.
	NestedHypervisorSupported types.Bool                    `tfsdk:"nested_hypervisor_supported"`
	DiskTypes                 []vmwareLocationDiskTypeModel `tfsdk:"disk_types"`
}

// vmwareLocationDiskTypeModel is a disk type offered inside a location (API-11
// redesign). Limits are in MB, keyed by title.
type vmwareLocationDiskTypeModel struct {
	Title                  types.String `tfsdk:"title"`
	IsDefault              types.Bool   `tfsdk:"is_default"`
	IsSSD                  types.Bool   `tfsdk:"is_ssd"`
	IsAllowedForSystemDisk types.Bool   `tfsdk:"is_allowed_for_system_disk"`
	MinMB                  types.Int64  `tfsdk:"min_mb"`
	MaxMB                  types.Int64  `tfsdk:"max_mb"`
	StepMB                 types.Int64  `tfsdk:"step_mb"`
	DefaultSizeMB          types.Int64  `tfsdk:"default_size_mb"`
}

type locationsListModel struct {
	Locations []vmwareLocationModel `tfsdk:"locations"`
}

func (d *locationsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_locations"
}

func (d *locationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List available VMware Cloud datacenter locations.",
		Attributes: map[string]schema.Attribute{
			"locations": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":            schema.Int64Attribute{Computed: true},
						"tech_title":    schema.StringAttribute{Computed: true},
						"gpu_supported": schema.BoolAttribute{Computed: true},
						"nested_hypervisor_supported": schema.BoolAttribute{Computed: true,
							Description: "Whether a VDC available to this project in the location supports " +
								"nested virtualization, which is what vcp_vmware_server.nested_hypervisor needs."},
						"disk_types": schema.ListNestedAttribute{
							Computed: true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"title":                      schema.StringAttribute{Computed: true},
									"is_default":                 schema.BoolAttribute{Computed: true},
									"is_ssd":                     schema.BoolAttribute{Computed: true},
									"is_allowed_for_system_disk": schema.BoolAttribute{Computed: true},
									"min_mb":                     schema.Int64Attribute{Computed: true},
									"max_mb":                     schema.Int64Attribute{Computed: true},
									"step_mb":                    schema.Int64Attribute{Computed: true},
									"default_size_mb":            schema.Int64Attribute{Computed: true},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *locationsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *locationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	items, err := d.client.GetVmwareLocationList(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VMware locations", err.Error())
		return
	}
	state := locationsListModel{Locations: make([]vmwareLocationModel, 0, len(items))}
	for _, l := range items {
		diskTypes := make([]vmwareLocationDiskTypeModel, 0, len(l.DiskTypes))
		for _, dt := range l.DiskTypes {
			diskTypes = append(diskTypes, vmwareLocationDiskTypeModel{
				Title:                  types.StringValue(dt.Title),
				IsDefault:              types.BoolValue(dt.IsDefault),
				IsSSD:                  types.BoolValue(dt.IsSSD),
				IsAllowedForSystemDisk: types.BoolValue(dt.IsAllowedForSystemDisk),
				MinMB:                  types.Int64Value(int64(dt.MinMB)),
				MaxMB:                  types.Int64Value(int64(dt.MaxMB)),
				StepMB:                 types.Int64Value(int64(dt.StepMB)),
				DefaultSizeMB:          types.Int64Value(int64(dt.DefaultSizeMB)),
			})
		}
		state.Locations = append(state.Locations, vmwareLocationModel{
			ID:                        types.Int64Value(int64(l.ID)),
			TechTitle:                 types.StringValue(l.TechTitle),
			GpuSupported:              types.BoolValue(l.GPUSupported),
			NestedHypervisorSupported: types.BoolValue(l.NestedHypervisorSupported),
			DiskTypes:                 diskTypes,
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// ===================== Images =====================

var (
	_ datasource.DataSource              = &imagesDataSource{}
	_ datasource.DataSourceWithConfigure = &imagesDataSource{}
)

func NewImagesDataSource() datasource.DataSource { return &imagesDataSource{} }

type imagesDataSource struct{ client *sdk.CloudClient }

type vmwareImageModel struct {
	ID                   types.Int64  `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	OSFamily             types.String `tfsdk:"os_family"`
	OSType               types.String `tfsdk:"os_type"`
	MinRamMB             types.Int64  `tfsdk:"min_ram_mb"`
	HddGB                types.Int64  `tfsdk:"hdd_gb"`
	SSHKeySupported      types.Bool   `tfsdk:"ssh_key_supported"`
	CPUHotAdd            types.Bool   `tfsdk:"cpu_hot_add"`
	MemoryHotAdd         types.Bool   `tfsdk:"memory_hot_add"`
	NicHotRemove         types.Bool   `tfsdk:"nic_hot_remove"`
	IsGpuOnly            types.Bool   `tfsdk:"is_gpu_only"`
	SupportedGpuModelIDs types.List   `tfsdk:"supported_gpu_model_ids"`
}

type imagesListModel struct {
	LocationID types.Int64        `tfsdk:"location_id"`
	Gpu        types.String       `tfsdk:"gpu"`
	Images     []vmwareImageModel `tfsdk:"images"`
}

func (d *imagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_images"
}

func (d *imagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List available VMware Cloud OS images/templates.",
		Attributes: map[string]schema.Attribute{
			"location_id": schema.Int64Attribute{Optional: true, Description: "Filter images by location ID."},
			"gpu":         schema.StringAttribute{Optional: true, Description: "GPU filter: \"required\" (GPU-only images) or \"unsupported\" (non-GPU images); omit for all."},
			"images": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                      schema.Int64Attribute{Computed: true},
						"name":                    schema.StringAttribute{Computed: true},
						"os_family":               schema.StringAttribute{Computed: true},
						"os_type":                 schema.StringAttribute{Computed: true},
						"min_ram_mb":              schema.Int64Attribute{Computed: true},
						"hdd_gb":                  schema.Int64Attribute{Computed: true},
						"ssh_key_supported":       schema.BoolAttribute{Computed: true},
						"cpu_hot_add":             schema.BoolAttribute{Computed: true},
						"memory_hot_add":          schema.BoolAttribute{Computed: true},
						"nic_hot_remove":          schema.BoolAttribute{Computed: true},
						"is_gpu_only":             schema.BoolAttribute{Computed: true},
						"supported_gpu_model_ids": schema.ListAttribute{ElementType: types.Int64Type, Computed: true},
					},
				},
			},
		},
	}
}

func (d *imagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *imagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg imagesListModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	items, err := d.client.GetVmwareImageList(ctx, optionalInt(cfg.LocationID), optionalString(cfg.Gpu))
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VMware images", err.Error())
		return
	}
	cfg.Images = make([]vmwareImageModel, 0, len(items))
	for _, img := range items {
		cfg.Images = append(cfg.Images, vmwareImageModel{
			ID:                   types.Int64Value(int64(img.ID)),
			Name:                 types.StringValue(img.Name),
			OSFamily:             types.StringValue(img.OsFamily),
			OSType:               types.StringValue(img.OsType),
			MinRamMB:             types.Int64Value(int64(img.MinRamMB)),
			HddGB:                types.Int64Value(int64(img.HddGB)),
			SSHKeySupported:      types.BoolValue(img.SSHKeySupported),
			CPUHotAdd:            types.BoolValue(img.CPUHotAdd),
			MemoryHotAdd:         types.BoolValue(img.MemoryHotAdd),
			NicHotRemove:         types.BoolValue(img.NICHotRemove),
			IsGpuOnly:            types.BoolValue(img.IsGPUOnly),
			SupportedGpuModelIDs: types.ListValueMust(types.Int64Type, int64AttrVals(img.SupportedGPUModelIDs)),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ===================== GPU models =====================

var (
	_ datasource.DataSource              = &gpuModelsDataSource{}
	_ datasource.DataSourceWithConfigure = &gpuModelsDataSource{}
)

func NewGpuModelsDataSource() datasource.DataSource { return &gpuModelsDataSource{} }

type gpuModelsDataSource struct{ client *sdk.CloudClient }

type vmwareGpuModelModel struct {
	ID                    types.Int64  `tfsdk:"id"`
	TechTitle             types.String `tfsdk:"tech_title"`
	Name                  types.String `tfsdk:"name"`
	CapacityVramMB        types.Int64  `tfsdk:"capacity_vram_mb"`
	GpuCardCount          types.Int64  `tfsdk:"gpu_card_count"`
	ServerAllocationLimit types.Int64  `tfsdk:"server_allocation_limit"`
	MaxServerRamMB        types.Int64  `tfsdk:"max_server_ram_mb"`
	IsAvailable           types.Bool   `tfsdk:"is_available"`
}

type gpuModelsListModel struct {
	LocationID types.Int64           `tfsdk:"location_id"`
	GpuModels  []vmwareGpuModelModel `tfsdk:"gpu_models"`
}

func (d *gpuModelsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_gpu_models"
}

func (d *gpuModelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List available VMware Cloud GPU models.",
		Attributes: map[string]schema.Attribute{
			"location_id": schema.Int64Attribute{Optional: true, Description: "Filter GPU models by location ID."},
			"gpu_models": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                      schema.Int64Attribute{Computed: true},
						"tech_title":              schema.StringAttribute{Computed: true},
						"name":                    schema.StringAttribute{Computed: true},
						"capacity_vram_mb":        schema.Int64Attribute{Computed: true},
						"gpu_card_count":          schema.Int64Attribute{Computed: true},
						"server_allocation_limit": schema.Int64Attribute{Computed: true},
						"max_server_ram_mb":       schema.Int64Attribute{Computed: true},
						"is_available":            schema.BoolAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *gpuModelsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *gpuModelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg gpuModelsListModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	items, err := d.client.GetVmwareGPUModelList(ctx, optionalInt(cfg.LocationID))
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VMware GPU models", err.Error())
		return
	}
	cfg.GpuModels = make([]vmwareGpuModelModel, 0, len(items))
	for _, gm := range items {
		m := vmwareGpuModelModel{
			ID:                    types.Int64Value(int64(gm.ID)),
			TechTitle:             types.StringValue(gm.TechTitle),
			Name:                  types.StringValue(gm.Name),
			CapacityVramMB:        types.Int64Value(int64(gm.CapacityVramMB)),
			GpuCardCount:          types.Int64Value(int64(gm.GPUCardCount)),
			ServerAllocationLimit: types.Int64Value(int64(gm.ServerAllocationLimit)),
			MaxServerRamMB:        types.Int64Null(),
			IsAvailable:           types.BoolNull(),
		}
		if gm.MaxServerRamMB != nil {
			m.MaxServerRamMB = types.Int64Value(int64(*gm.MaxServerRamMB))
		}
		if gm.IsAvailable != nil {
			m.IsAvailable = types.BoolValue(*gm.IsAvailable)
		}
		cfg.GpuModels = append(cfg.GpuModels, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
