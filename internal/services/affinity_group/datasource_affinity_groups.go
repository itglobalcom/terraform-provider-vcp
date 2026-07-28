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

var _ datasource.DataSource = &AffinityGroupsDataSource{}

func NewAffinityGroupsDataSource() datasource.DataSource {
	return &AffinityGroupsDataSource{}
}

type AffinityGroupsDataSource struct {
	client *sdk.CloudClient
}

type AffinityGroupsDataSourceModel struct {
	AffinityGroups []AffinityGroupModel `tfsdk:"affinity_groups"`
}

type AffinityGroupModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	LocationID types.String `tfsdk:"location_id"`
	Policy     types.String `tfsdk:"policy"`
	ServerIDs  types.Set    `tfsdk:"server_ids"`
}

func (d *AffinityGroupsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_affinity_groups"
}

func (d *AffinityGroupsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches list of all affinity groups.",

		Attributes: map[string]schema.Attribute{
			"affinity_groups": schema.ListNestedAttribute{
				MarkdownDescription: "List of affinity groups",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Affinity group ID",
							Computed:            true,
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
							MarkdownDescription: "Placement policy",
							Computed:            true,
						},
						"server_ids": schema.SetAttribute{
							MarkdownDescription: "Server IDs",
							Computed:            true,
							ElementType:         types.StringType,
						},
					},
				},
			},
		},
	}
}

func (d *AffinityGroupsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *AffinityGroupsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AffinityGroupsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groups, err := d.client.GetAffinityGroupList(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Affinity Groups",
			fmt.Sprintf("Could not read affinity groups: %s", err.Error()),
		)
		return
	}

	// Map response to model
	data.AffinityGroups = make([]AffinityGroupModel, len(groups))
	for i, group := range groups {
		serverIDsElements := make([]attr.Value, len(group.ServerIDs))
		for j, id := range group.ServerIDs {
			serverIDsElements[j] = types.StringValue(id)
		}
		serverIDsSet, diags := types.SetValue(types.StringType, serverIDsElements)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		policy := "anti-affinity"
		if group.Affinity {
			policy = "affinity"
		}

		data.AffinityGroups[i] = AffinityGroupModel{
			ID:         types.StringValue(group.ID),
			Name:       types.StringValue(group.Name),
			LocationID: types.StringValue(group.LocationID),
			Policy:     types.StringValue(policy),
			ServerIDs:  serverIDsSet,
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
