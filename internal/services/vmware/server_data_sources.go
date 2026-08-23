package vmware

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// dsServerModel is the read-only view of a server.
type dsServerModel struct {
	ID               types.Int64  `tfsdk:"id"`
	LocationID       types.Int64  `tfsdk:"location_id"`
	Name             types.String `tfsdk:"name"`
	ComputerName     types.String `tfsdk:"computer_name"`
	ImageID          types.Int64  `tfsdk:"image_id"`
	CPU              types.Int64  `tfsdk:"cpu"`
	RamMB            types.Int64  `tfsdk:"ram_mb"`
	SystemDiskMB     types.Int64  `tfsdk:"system_disk_mb"`
	SystemDiskType   types.String `tfsdk:"system_disk_type"`
	State            types.String `tfsdk:"state"`
	IsPowerOn        types.Bool   `tfsdk:"is_power_on"`
	VmToolsInstalled types.Bool   `tfsdk:"vm_tools_installed"`
	NestedHypervisor types.Bool   `tfsdk:"nested_hypervisor"`
	Gpu              types.Object `tfsdk:"gpu"`
	Nics             types.List   `tfsdk:"nics"`
	Created          types.String `tfsdk:"created"`
}

func mapServerToDSModel(s *entities.VmwareServer) dsServerModel {
	var m serverModel
	mapServerComputed(&m, s)
	return dsServerModel{
		ID:               m.ID,
		LocationID:       m.LocationID,
		Name:             m.Name,
		ComputerName:     m.ComputerName,
		ImageID:          m.ImageID,
		CPU:              m.CPU,
		RamMB:            m.RamMB,
		SystemDiskMB:     m.SystemDiskMB,
		SystemDiskType:   m.SystemDiskType,
		State:            m.State,
		IsPowerOn:        m.IsPowerOn,
		VmToolsInstalled: m.VmToolsInstalled,
		NestedHypervisor: m.NestedHypervisor,
		Gpu:              m.Gpu,
		Nics:             m.Nics,
		Created:          m.Created,
	}
}

func dsServerAttributes(computedID bool) map[string]schema.Attribute {
	idAttr := schema.Int64Attribute{Computed: true, Description: "Server ID."}
	nameAttr := schema.StringAttribute{Computed: true, Description: "Display name of the server."}
	if !computedID {
		// The single-server source is reached either way round: by id, which is what
		// state and the panel URL carry, or by the display name, which is what a
		// person actually knows. Exactly one of the two is given.
		idAttr = schema.Int64Attribute{Optional: true, Computed: true,
			Description: "Server ID. Give this or name."}
		nameAttr = schema.StringAttribute{Optional: true, Computed: true,
			Description: "Display name of the server. Give this or id; the name must be unique."}
	}
	gpu := schema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]schema.Attribute{
			"model_id":   schema.Int64Attribute{Computed: true},
			"vram_mb":    schema.Int64Attribute{Computed: true},
			"card_count": schema.Int64Attribute{Computed: true},
		},
	}
	nics := schema.ListNestedAttribute{
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
	}
	return map[string]schema.Attribute{
		"id":                 idAttr,
		"location_id":        schema.Int64Attribute{Computed: true},
		"name":               nameAttr,
		"computer_name":      schema.StringAttribute{Computed: true},
		"image_id":           schema.Int64Attribute{Computed: true},
		"cpu":                schema.Int64Attribute{Computed: true},
		"ram_mb":             schema.Int64Attribute{Computed: true},
		"system_disk_mb":     schema.Int64Attribute{Computed: true},
		"system_disk_type":   schema.StringAttribute{Computed: true},
		"state":              schema.StringAttribute{Computed: true},
		"is_power_on":        schema.BoolAttribute{Computed: true},
		"vm_tools_installed": schema.BoolAttribute{Computed: true},
		"nested_hypervisor": schema.BoolAttribute{Computed: true,
			Description: "Whether the guest OS may run its own hypervisor (nested virtualization)."},
		"gpu":     gpu,
		"nics":    nics,
		"created": schema.StringAttribute{Computed: true},
	}
}

// ===================== Single server =====================

var (
	_ datasource.DataSource              = &serverDataSource{}
	_ datasource.DataSourceWithConfigure = &serverDataSource{}
)

func NewServerDataSource() datasource.DataSource { return &serverDataSource{} }

type serverDataSource struct{ client *sdk.CloudClient }

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetch a single VMware Cloud server by ID.",
		Attributes:  dsServerAttributes(false),
	}
}

func (d *serverDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg dsServerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	byID := !cfg.ID.IsNull() && !cfg.ID.IsUnknown()
	byName := !cfg.Name.IsNull() && !cfg.Name.IsUnknown()
	switch {
	case byID && byName:
		resp.Diagnostics.AddError("Conflicting Attributes",
			"Give either id or name, not both — they would have to agree, and there is nothing to do if they do not.")
		return
	case !byID && !byName:
		resp.Diagnostics.AddError("Missing Attribute",
			"Give id or name to say which server to read.")
		return
	}

	var (
		server *entities.VmwareServer
		err    error
	)
	if byID {
		server, err = d.client.GetVmwareServer(ctx, int(cfg.ID.ValueInt64()))
	} else {
		server, err = d.findServerByName(ctx, cfg.Name.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VMware server", err.Error())
		return
	}

	state := mapServerToDSModel(server)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// findServerByName locates a server by its display name. The API has no lookup by
// name, so this is a scan of the inventory — and a name that matches more than
// one server is reported rather than resolved arbitrarily.
func (d *serverDataSource) findServerByName(ctx context.Context, name string) (*entities.VmwareServer, error) {
	servers, err := d.client.GetVmwareServerList(ctx, nil)
	if err != nil {
		return nil, err
	}

	var found []*entities.VmwareServer
	for _, server := range servers {
		if server != nil && server.Name == name {
			found = append(found, server)
		}
	}

	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no VMware server is named %q", name)
	case 1:
		// The list omits the fields only a by-id read carries (SRV-4:
		// vm_tools_installed), so fetch the server itself now that the id is known.
		return d.client.GetVmwareServer(ctx, found[0].ID)
	default:
		ids := make([]string, 0, len(found))
		for _, server := range found {
			ids = append(ids, strconv.Itoa(server.ID))
		}
		return nil, fmt.Errorf("%d VMware servers are named %q (ids %s) — read one of them by id instead",
			len(found), name, strings.Join(ids, ", "))
	}
}

// ===================== Server list =====================

var (
	_ datasource.DataSource              = &serversDataSource{}
	_ datasource.DataSourceWithConfigure = &serversDataSource{}
)

func NewServersDataSource() datasource.DataSource { return &serversDataSource{} }

type serversDataSource struct{ client *sdk.CloudClient }

type serversListModel struct {
	LocationID types.Int64     `tfsdk:"location_id"`
	Servers    []dsServerModel `tfsdk:"servers"`
}

func (d *serversDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_servers"
}

func (d *serversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List VMware Cloud servers, optionally filtered by location.",
		Attributes: map[string]schema.Attribute{
			"location_id": schema.Int64Attribute{Optional: true, Description: "Filter servers by location ID."},
			"servers": schema.ListNestedAttribute{
				Computed:     true,
				NestedObject: schema.NestedAttributeObject{Attributes: dsServerAttributes(true)},
			},
		},
	}
}

func (d *serversDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *serversDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serversListModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	items, err := d.client.GetVmwareServerList(ctx, optionalInt(cfg.LocationID))
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VMware servers", err.Error())
		return
	}
	cfg.Servers = make([]dsServerModel, 0, len(items))
	for _, s := range items {
		cfg.Servers = append(cfg.Servers, mapServerToDSModel(s))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
