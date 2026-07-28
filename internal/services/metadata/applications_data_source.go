package metadata

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &ApplicationsDataSource{}

func NewApplicationsDataSource() datasource.DataSource {
	return &ApplicationsDataSource{}
}

// ApplicationsDataSource defines the data source implementation.
type ApplicationsDataSource struct {
	client *sdk.CloudClient
}

// ApplicationsDataSourceModel describes the data source data model.
type ApplicationsDataSourceModel struct {
	LocationID   types.String       `tfsdk:"location_id"`
	Applications []ApplicationModel `tfsdk:"applications"`
}

// ApplicationModel describes a single application.
type ApplicationModel struct {
	ID         types.String   `tfsdk:"id"`
	LocationID types.String   `tfsdk:"location_id"`
	Images     []types.String `tfsdk:"images"`
}

func (d *ApplicationsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_applications"
}

func (d *ApplicationsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a list of available applications. Can be optionally filtered by location.",

		Attributes: map[string]schema.Attribute{
			"location_id": schema.StringAttribute{
				MarkdownDescription: "Optional location ID to filter applications",
				Optional:            true,
			},
			"applications": schema.ListNestedAttribute{
				MarkdownDescription: "List of available applications",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Application ID",
							Computed:            true,
						},
						"location_id": schema.StringAttribute{
							MarkdownDescription: "Location ID where application is available",
							Computed:            true,
						},
						"images": schema.ListAttribute{
							MarkdownDescription: "List of available images for this application",
							Computed:            true,
							ElementType:         types.StringType,
						},
					},
				},
			},
		},
	}
}

func (d *ApplicationsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ApplicationsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ApplicationsDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get location filter if provided
	locationID := ""
	if !data.LocationID.IsNull() {
		locationID = data.LocationID.ValueString()
	}

	// Call API
	applications, err := d.client.GetApplications(ctx, locationID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Applications",
			fmt.Sprintf("Could not read applications: %s", err.Error()),
		)
		return
	}

	// Map response to model
	data.Applications = make([]ApplicationModel, len(applications))
	for i, app := range applications {
		images := make([]types.String, len(app.Images))
		for j, img := range app.Images {
			images[j] = types.StringValue(img)
		}

		data.Applications[i] = ApplicationModel{
			ID:         types.StringValue(app.ID),
			LocationID: types.StringValue(app.LocationID),
			Images:     images,
		}
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
