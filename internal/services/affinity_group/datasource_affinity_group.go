package affinity

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var _ datasource.DataSource = &AffinityGroupDataSource{}

func NewAffinityGroupDataSource() datasource.DataSource {
	return &AffinityGroupDataSource{}
}

type AffinityGroupDataSource struct {
	client *sdk.CloudClient
}

type AffinityGroupDataSourceModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	LocationID types.String `tfsdk:"location_id"`
	Policy     types.String `tfsdk:"policy"`
	ServerIDs  types.Set    `tfsdk:"server_ids"`
}

func (d *AffinityGroupDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_affinity_group"
}

func (d *AffinityGroupDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches information about a specific affinity group.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Affinity group ID",
				Required:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Affinity group name",
				Computed:            true,
			},
			"location_id": schema.StringAttribute{
				MarkdownDescription: "Location ID",
				Computed:            true,
			},
			"policy": schema.StringAttribute{
				MarkdownDescription: "Placement policy: 'affinity' or 'anti-affinity'",
				Computed:            true,
			},
			"server_ids": schema.SetAttribute{
				MarkdownDescription: "Set of server IDs in this group",
				Computed:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (d *AffinityGroupDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *AffinityGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AffinityGroupDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := d.client.GetAffinityGroup(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Affinity Group",
			fmt.Sprintf("Could not read affinity group %s: %s", data.ID.ValueString(), err.Error()),
		)
		return
	}

	// Map response to model
	data.Name = types.StringValue(group.Name)
	data.LocationID = types.StringValue(group.LocationID)

	// Convert boolean to policy string
	if group.Affinity {
		data.Policy = types.StringValue("affinity")
	} else {
		data.Policy = types.StringValue("anti-affinity")
	}

	serverIDsElements := make([]attr.Value, len(group.ServerIDs))
	for i, id := range group.ServerIDs {
		serverIDsElements[i] = types.StringValue(id)
	}
	serverIDsSet, diags := types.SetValue(types.StringType, serverIDsElements)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ServerIDs = serverIDsSet

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
