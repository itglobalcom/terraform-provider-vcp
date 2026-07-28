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
	_ datasource.DataSource              = &networkDataSource{}
	_ datasource.DataSourceWithConfigure = &networkDataSource{}
)

// NewNetworkDataSource is a helper function to simplify the provider implementation.
func NewNetworkDataSource() datasource.DataSource {
	return &networkDataSource{}
}

// networkDataSource is the data source implementation.
type networkDataSource struct {
	client *sdk.CloudClient
}

// Metadata returns the data source type name.
func (d *networkDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network"
}

// Schema defines the schema for the data source.
func (d *networkDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Fetches information about an existing isolated network.",
		MarkdownDescription: "Fetches information about an existing isolated network by ID.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:         "The unique identifier of the network.",
				MarkdownDescription: "The unique identifier of the network to fetch.",
				Required:            true,
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
				Description:         "The network prefix (e.g., 10.100.0.0).",
				MarkdownDescription: "The network prefix (e.g., `10.100.0.0`).",
				Computed:            true,
			},
			"mask": schema.Int64Attribute{
				Description:         "The network mask (e.g., 24).",
				MarkdownDescription: "The network mask (e.g., `24` for `/24`).",
				Computed:            true,
			},
			"server_ids": schema.ListAttribute{
				Description:         "List of server IDs attached to this network.",
				MarkdownDescription: "List of server IDs currently attached to this network.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"gateway_ids": schema.ListAttribute{
				Description:         "List of gateway IDs attached to this network.",
				MarkdownDescription: "List of gateway IDs currently attached to this network.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"created": schema.StringAttribute{
				Description:         "The creation timestamp of the network.",
				MarkdownDescription: "The creation timestamp of the network in RFC3339 format.",
				Computed:            true,
			},
			"tags": schema.SetAttribute{
				Description:         "List of tags associated with the network.",
				MarkdownDescription: "List of tags associated with the network.",
				Computed:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

// Configure adds the provider configured client to the data source.
func (d *networkDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	d.client = client
}

// Read refreshes the Terraform state with the latest data.
func (d *networkDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config networkModel

	// Read configuration
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get network from API
	network, err := d.client.GetNetwork(ctx, config.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Network",
			fmt.Sprintf("Could not read network %s: %s", config.ID.ValueString(), err.Error()),
		)
		return
	}

	// Map response to state
	state := mapNetworkToModel(network)

	// Set state
	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}
