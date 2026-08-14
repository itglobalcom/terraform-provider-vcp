package gateway

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var _ datasource.DataSource = &gatewayDataSource{}

func NewGatewayDataSource() datasource.DataSource {
	return &gatewayDataSource{}
}

type gatewayDataSource struct {
	client *sdk.CloudClient
}

// isolatedNICsDataSchema / publicNICsDataSchema — nested NIC schemas for the
// data sources (the same shape as the resource's, but all computed).
func isolatedNICsDataSchema() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: "Isolated networks connected to the gateway.",
		Computed:            true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"network_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Connected isolated network ID."},
				"id":         schema.Int64Attribute{Computed: true, MarkdownDescription: "NIC ID."},
				"ip_address": schema.StringAttribute{Computed: true, MarkdownDescription: "IP address assigned on the network."},
			},
		},
	}
}

func publicNICsDataSchema() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: "External (WAN) interfaces of the gateway.",
		Computed:            true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"bandwidth_mbps": schema.Int64Attribute{Computed: true, MarkdownDescription: "Uplink bandwidth in Mbps."},
				"id":             schema.Int64Attribute{Computed: true, MarkdownDescription: "NIC ID."},
				"ip_address":     schema.StringAttribute{Computed: true, MarkdownDescription: "Public IP address."},
			},
		},
	}
}

func (d *gatewayDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway"
}

func (d *gatewayDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches information about a specific gateway by ID.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Gateway ID.",
				Required:            true,
			},
			"location_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Location ID."},
			"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway name."},
			"tags": schema.SetAttribute{
				MarkdownDescription: "Set of tags.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"isolated_net_nics": isolatedNICsDataSchema(),
			"public_net_nics":   publicNICsDataSchema(),
			"nat_rules":         natRulesDataSchema(),
			"firewall_rules":    firewallRulesDataSchema(),
			"state":             schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway state."},
			"powered_on":        schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the gateway is powered on."},
			"created":           schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
		},
	}
}

func (d *gatewayDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *gatewayDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data gatewayDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gw, err := d.client.GetGateway(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Gateway",
			fmt.Sprintf("Could not read gateway %s: %s", data.ID.ValueString(), err.Error()),
		)
		return
	}

	data = mapGatewayToDataModel(gw)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
