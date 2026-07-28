package metadata

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var (
	_ datasource.DataSource              = &locationsDataSource{}
	_ datasource.DataSourceWithConfigure = &locationsDataSource{}
)

func NewLocationsDataSource() datasource.DataSource {
	return &locationsDataSource{}
}

type locationsDataSource struct {
	client *sdk.CloudClient
}

type locationModel struct {
	ID                     types.String `tfsdk:"id"`
	SystemVolumeMin        types.Int64  `tfsdk:"system_volume_min"`
	AdditionalVolumeMin    types.Int64  `tfsdk:"additional_volume_min"`
	VolumeMax              types.Int64  `tfsdk:"volume_max"`
	WindowsSystemVolumeMin types.Int64  `tfsdk:"windows_system_volume_min"`
	BandwidthMin           types.Int64  `tfsdk:"bandwidth_min"`
	BandwidthMax           types.Int64  `tfsdk:"bandwidth_max"`
	CPUQuantityOptions     types.List   `tfsdk:"cpu_quantity_options"`
	RAMSizeOptions         types.List   `tfsdk:"ram_size_options"`
}

type locationsListModel struct {
	Locations []locationModel `tfsdk:"locations"`
}

func (d *locationsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_locations"
}

func (d *locationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List all available datacenter locations and their limits.",
		Attributes: map[string]schema.Attribute{
			"locations": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                        schema.StringAttribute{Computed: true},
						"system_volume_min":         schema.Int64Attribute{Computed: true},
						"additional_volume_min":     schema.Int64Attribute{Computed: true},
						"volume_max":                schema.Int64Attribute{Computed: true},
						"windows_system_volume_min": schema.Int64Attribute{Computed: true},
						"bandwidth_min":             schema.Int64Attribute{Computed: true},
						"bandwidth_max":             schema.Int64Attribute{Computed: true},
						"cpu_quantity_options":      schema.ListAttribute{ElementType: types.Int64Type, Computed: true},
						"ram_size_options":          schema.ListAttribute{ElementType: types.Int64Type, Computed: true},
					},
				},
			},
		},
	}
}

func (d *locationsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Configure Type", fmt.Sprintf("Expected *sdk.CloudClient, got %T", req.ProviderData))
		return
	}
	d.client = client
}

func (d *locationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	locs, err := d.client.GetLocations(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read locations", err.Error())
		return
	}

	var state locationsListModel
	// Empty (not null) list, so length()/for_each work on an empty account.
	state.Locations = make([]locationModel, 0, len(locs))
	for _, l := range locs {
		lm := locationModel{
			ID:                     types.StringValue(l.ID),
			SystemVolumeMin:        types.Int64Value(int64(l.SystemVolumeMin)),
			AdditionalVolumeMin:    types.Int64Value(int64(l.AdditionalVolumeMin)),
			VolumeMax:              types.Int64Value(int64(l.VolumeMax)),
			WindowsSystemVolumeMin: types.Int64Value(int64(l.WindowsSystemVolumeMin)),
			BandwidthMin:           types.Int64Value(int64(l.BandwidthMin)),
			BandwidthMax:           types.Int64Value(int64(l.BandwidthMax)),
			CPUQuantityOptions:     types.ListValueMust(types.Int64Type, toInt64AttrVals(l.CPUQuantityOptions)),
			RAMSizeOptions:         types.ListValueMust(types.Int64Type, toInt64AttrVals(l.RAMSizeOptions)),
		}
		state.Locations = append(state.Locations, lm)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// helper
func toInt64AttrVals(src []int) []attr.Value {
	out := make([]attr.Value, len(src))
	for i, v := range src {
		out[i] = types.Int64Value(int64(v))
	}
	return out
}
