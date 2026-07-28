package vstack_server

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var _ datasource.DataSource = &serversDataSource{}

func NewServersDataSource() datasource.DataSource {
	return &serversDataSource{}
}

type serversDataSource struct {
	client *sdk.CloudClient
}

type serversDataSourceModel struct {
	Servers []serverDataModel `tfsdk:"servers"`
}

func (d *serversDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_servers"
}

func (d *serversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches the list of all servers in the project.",
		Attributes: map[string]schema.Attribute{
			"servers": schema.ListNestedAttribute{
				MarkdownDescription: "List of servers.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: serverDataAttributes(false),
				},
			},
		},
	}
}

func (d *serversDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *serversDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	servers, err := d.client.GetServerList(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to List Servers", err.Error())
		return
	}

	var state serversDataSourceModel
	for _, server := range servers {
		state.Servers = append(state.Servers, mapServerToDataModel(server))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
