package metadata

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
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
	// LLMKeyEnabled reports that issue_llm_key is accepted for the application.
	LLMKeyEnabled types.Bool                  `tfsdk:"llm_key_enabled"`
	Parameters    []ApplicationParameterModel `tfsdk:"parameters"`
}

// ApplicationParameterModel describes a value the application asks for when a server is ordered.
type ApplicationParameterModel struct {
	Name     types.String `tfsdk:"name"`
	Required types.Bool   `tfsdk:"required"`
	Secret   types.Bool   `tfsdk:"secret"`
	Default  types.String `tfsdk:"default"`
	LLMKind  types.String `tfsdk:"llm_kind"`
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
						"llm_key_enabled": schema.BoolAttribute{
							MarkdownDescription: "Whether a platform language model key can be issued for the application: " +
								"`issue_llm_key` in the `applications` block of `vcp_server` is accepted only when this is `true`.",
							Computed: true,
						},
						"parameters": schema.ListNestedAttribute{
							MarkdownDescription: "Parameters the application asks for when a server is ordered; their values go " +
								"into `parameters` of the `applications` block of `vcp_server`, by `name`.",
							Computed: true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										MarkdownDescription: "Parameter name, the key in `parameters` of the server order.",
										Computed:            true,
									},
									"required": schema.BoolAttribute{
										MarkdownDescription: "Whether the order must carry a value; a `default` stands in for a missing one.",
										Computed:            true,
									},
									"secret": schema.BoolAttribute{
										MarkdownDescription: "Whether the value is a secret that the platform does not log or echo back.",
										Computed:            true,
									},
									"default": schema.StringAttribute{
										MarkdownDescription: "Value used when the order carries none; empty when there is no default.",
										Computed:            true,
									},
									"llm_kind": schema.StringAttribute{
										MarkdownDescription: "Role of the parameter in language model access: `Key` receives the issued key, " +
											"`Endpoint` the inference address, when the server is ordered with `issue_llm_key`. " +
											"Empty for an ordinary parameter.",
										Computed: true,
									},
								},
							},
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
			ID:            types.StringValue(app.ID),
			LocationID:    types.StringValue(app.LocationID),
			Images:        images,
			LLMKeyEnabled: types.BoolValue(app.LLMKeyEnabled),
			Parameters:    mapApplicationParameters(app.Parameters),
		}
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func mapApplicationParameters(params []entities.ApplicationParameter) []ApplicationParameterModel {
	result := make([]ApplicationParameterModel, len(params))
	for i, p := range params {
		result[i] = ApplicationParameterModel{
			Name:     types.StringValue(p.Name),
			Required: types.BoolValue(p.Required),
			Secret:   types.BoolValue(p.Secret),
			Default:  optionalString(p.Default),
			LLMKind:  optionalString(string(p.LLMKind)),
		}
	}
	return result
}

func optionalString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
