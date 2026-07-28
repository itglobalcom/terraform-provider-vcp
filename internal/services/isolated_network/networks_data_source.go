package isolated_network

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ datasource.DataSource              = &networksDataSource{}
	_ datasource.DataSourceWithConfigure = &networksDataSource{}
)

// NewNetworksDataSource is a helper function to simplify the provider implementation.
func NewNetworksDataSource() datasource.DataSource {
	return &networksDataSource{}
}

// networksDataSource is the data source implementation.
type networksDataSource struct {
	client *sdk.CloudClient
}

// networksDataSourceModel maps the data source schema data.
type networksDataSourceModel struct {
	Networks []networkModel `tfsdk:"networks"`
}

// Metadata returns the data source type name.
func (d *networksDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_networks"
}

// Schema defines the schema for the data source.
func (d *networksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Fetches a list of all isolated networks.",
		MarkdownDescription: "Fetches a list of all isolated networks in your account.",
		Attributes: map[string]schema.Attribute{
			"networks": schema.ListNestedAttribute{
				Description:         "List of networks.",
				MarkdownDescription: "List of all networks in your account.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Description:         "The unique identifier of the network.",
							MarkdownDescription: "The unique identifier of the network.",
							Computed:            true,
						},
						"name": schema.StringAttribute{
							Description:         "The name of the network.",
							MarkdownDescription: "The name of the network.",
							Computed:            true,
						},
						"location_id": schema.StringAttribute{
							Description:         "The location ID of the network.",
							MarkdownDescription: "The location ID where the network is deployed.",
							Computed:            true,
						},
						"description": schema.StringAttribute{
							Description:         "The description of the network.",
							MarkdownDescription: "The description of the network.",
							Computed:            true,
						},
						"network_prefix": schema.StringAttribute{
							Description:         "The network prefix.",
							MarkdownDescription: "The network prefix (e.g., `10.100.0.0`).",
							Computed:            true,
						},
						"mask": schema.Int64Attribute{
							Description:         "The network mask.",
							MarkdownDescription: "The network mask (e.g., `24`).",
							Computed:            true,
						},
						"server_ids": schema.ListAttribute{
							Description:         "List of server IDs.",
							MarkdownDescription: "List of server IDs attached to this network.",
							Computed:            true,
							ElementType:         types.StringType,
						},
						"gateway_ids": schema.ListAttribute{
							Description:         "List of gateway IDs.",
							MarkdownDescription: "List of gateway IDs attached to this network.",
							Computed:            true,
							ElementType:         types.StringType,
						},
						"created": schema.StringAttribute{
							Description:         "The creation timestamp.",
							MarkdownDescription: "The creation timestamp in RFC3339 format.",
							Computed:            true,
						},
						"tags": schema.SetAttribute{
							Description:         "List of tags.",
							MarkdownDescription: "List of tags associated with the network.",
							Computed:            true,
							ElementType:         types.StringType,
						},
					},
				},
			},
		},
	}
}

// Configure adds the provider configured client to the data source.
func (d *networksDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// Read refreshes the Terraform state with the latest data.
func (d *networksDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state networksDataSourceModel

	// Get networks from API
	networks, err := d.client.GetNetworkList(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Networks",
			fmt.Sprintf("Could not read networks: %s", err.Error()),
		)
		return
	}

	// Map response to state. Empty (not null) list, so length()/for_each work
	// on an empty account.
	state.Networks = make([]networkModel, 0, len(networks))
	for _, network := range networks {
		networkModel := mapNetworkToModel(network)
		state.Networks = append(state.Networks, networkModel)
	}

	// Set state
	diags := resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}
