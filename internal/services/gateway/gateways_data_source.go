package gateway

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var _ datasource.DataSource = &gatewaysDataSource{}

func NewGatewaysDataSource() datasource.DataSource {
	return &gatewaysDataSource{}
}

type gatewaysDataSource struct {
	client *sdk.CloudClient
}

type gatewaysDataSourceModel struct {
	Gateways []gatewayDataModel `tfsdk:"gateways"`
}

func (d *gatewaysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateways"
}

func (d *gatewaysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches the list of all gateways in the project.",
		Attributes: map[string]schema.Attribute{
			"gateways": schema.ListNestedAttribute{
				MarkdownDescription: "List of gateways.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway ID."},
						"location_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Location ID."},
						"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway name."},
						"tags": schema.SetAttribute{
							MarkdownDescription: "Set of tags.",
							Computed:            true,
							ElementType:         types.StringType,
						},
						"isolated_net_nics": isolatedNICsDataSchema(),
						"public_net_nics":   publicNICsDataSchema(),
						"state":             schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway state."},
						"powered_on":        schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the gateway is powered on."},
						"created":           schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
					},
				},
			},
		},
	}
}

func (d *gatewaysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *gatewaysDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	gateways, err := d.client.GetGatewayList(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to List Gateways", err.Error())
		return
	}

	var state gatewaysDataSourceModel
	// Empty (not null) list, so length()/for_each work on an empty account.
	state.Gateways = make([]gatewayDataModel, 0, len(gateways))
	for _, gw := range gateways {
		state.Gateways = append(state.Gateways, mapGatewayToDataModel(gw))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
