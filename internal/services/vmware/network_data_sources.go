package vmware

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// dsNetworkModel is the read-only view of a network (no write-only inputs).
type dsNetworkModel struct {
	ID            types.Int64  `tfsdk:"id"`
	LocationID    types.Int64  `tfsdk:"location_id"`
	Type          types.String `tfsdk:"type"`
	Name          types.String `tfsdk:"name"`
	Address       types.String `tfsdk:"address"`
	Mask          types.Int64  `tfsdk:"mask"`
	Gateway       types.String `tfsdk:"gateway"`
	BandwidthMbps types.Int64  `tfsdk:"bandwidth_mbps"`
	IsDhcp        types.Bool   `tfsdk:"is_dhcp"`
	Shared        types.Bool   `tfsdk:"shared"`
	State         types.String `tfsdk:"state"`
	NicsCount     types.Int64  `tfsdk:"nics_count"`
}

func mapNetworkToDSModel(n *entities.VmwareNetwork) dsNetworkModel {
	m := mapNetworkToModel(n)
	return dsNetworkModel{
		ID:            m.ID,
		LocationID:    m.LocationID,
		Type:          m.Type,
		Name:          m.Name,
		Address:       m.Address,
		Mask:          m.Mask,
		Gateway:       m.Gateway,
		BandwidthMbps: m.BandwidthMbps,
		IsDhcp:        m.IsDhcp,
		Shared:        m.Shared,
		State:         m.State,
		NicsCount:     m.NicsCount,
	}
}

func dsNetworkAttributes(computedID bool) map[string]schema.Attribute {
	idAttr := schema.Int64Attribute{Computed: true, Description: "Network ID."}
	if !computedID {
		idAttr = schema.Int64Attribute{Required: true, Description: "Network ID."}
	}
	return map[string]schema.Attribute{
		"id":             idAttr,
		"location_id":    schema.Int64Attribute{Computed: true},
		"type":           schema.StringAttribute{Computed: true},
		"name":           schema.StringAttribute{Computed: true},
		"address":        schema.StringAttribute{Computed: true},
		"mask":           schema.Int64Attribute{Computed: true},
		"gateway":        schema.StringAttribute{Computed: true},
		"bandwidth_mbps": schema.Int64Attribute{Computed: true},
		"is_dhcp":        schema.BoolAttribute{Computed: true},
		"shared":         schema.BoolAttribute{Computed: true},
		"state":          schema.StringAttribute{Computed: true},
		"nics_count":     schema.Int64Attribute{Computed: true},
	}
}

// ===================== Single network =====================

var (
	_ datasource.DataSource              = &networkDataSource{}
	_ datasource.DataSourceWithConfigure = &networkDataSource{}
)

func NewNetworkDataSource() datasource.DataSource { return &networkDataSource{} }

type networkDataSource struct{ client *sdk.CloudClient }

func (d *networkDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_network"
}

func (d *networkDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetch a single VMware Cloud network by ID.",
		Attributes:  dsNetworkAttributes(false),
	}
}

func (d *networkDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *networkDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg dsNetworkModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	network, err := d.client.GetVmwareNetwork(ctx, int(cfg.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VMware network", err.Error())
		return
	}
	state := mapNetworkToDSModel(network)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// ===================== Network list =====================

var (
	_ datasource.DataSource              = &networksDataSource{}
	_ datasource.DataSourceWithConfigure = &networksDataSource{}
)

func NewNetworksDataSource() datasource.DataSource { return &networksDataSource{} }

type networksDataSource struct{ client *sdk.CloudClient }

type networksListModel struct {
	LocationID types.Int64      `tfsdk:"location_id"`
	Networks   []dsNetworkModel `tfsdk:"networks"`
}

func (d *networksDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_networks"
}

func (d *networksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List VMware Cloud networks, optionally filtered by location.",
		Attributes: map[string]schema.Attribute{
			"location_id": schema.Int64Attribute{Optional: true, Description: "Filter networks by location ID."},
			"networks": schema.ListNestedAttribute{
				Computed:     true,
				NestedObject: schema.NestedAttributeObject{Attributes: dsNetworkAttributes(true)},
			},
		},
	}
}

func (d *networksDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *networksDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg networksListModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	items, err := d.client.GetVmwareNetworkList(ctx, optionalInt(cfg.LocationID))
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VMware networks", err.Error())
		return
	}
	cfg.Networks = make([]dsNetworkModel, 0, len(items))
	for _, n := range items {
		cfg.Networks = append(cfg.Networks, mapNetworkToDSModel(n))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
