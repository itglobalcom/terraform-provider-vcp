package metadata

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var (
	_ datasource.DataSource              = &projectDataSource{}
	_ datasource.DataSourceWithConfigure = &projectDataSource{}
)

func NewProjectDataSource() datasource.DataSource {
	return &projectDataSource{}
}

type projectDataSource struct {
	client *sdk.CloudClient
}

type projectModel struct {
	ID       types.Int64   `tfsdk:"id"`
	Balance  types.Float64 `tfsdk:"balance"`
	Currency types.String  `tfsdk:"currency"`
	State    types.String  `tfsdk:"state"`
	Created  types.String  `tfsdk:"created"`
}

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetch current project information (balance, currency, etc.).",
		Attributes: map[string]schema.Attribute{
			"id":       schema.Int64Attribute{Computed: true},
			"balance":  schema.Float64Attribute{Computed: true},
			"currency": schema.StringAttribute{Computed: true},
			"state":    schema.StringAttribute{Computed: true},
			"created":  schema.StringAttribute{Computed: true},
		},
	}
}

func (d *projectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *projectDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	proj, err := d.client.GetProject(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	var state projectModel
	state.ID = types.Int64Value(int64(proj.ID))
	state.Balance = types.Float64Value(proj.Balance)
	state.Currency = types.StringValue(proj.Currency)
	state.State = types.StringValue(proj.State)
	state.Created = types.StringValue(proj.Created)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
