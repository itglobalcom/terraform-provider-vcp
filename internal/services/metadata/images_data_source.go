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
	_ datasource.DataSource              = &imagesDataSource{}
	_ datasource.DataSourceWithConfigure = &imagesDataSource{}
)

func NewImagesDataSource() datasource.DataSource {
	return &imagesDataSource{}
}

type imagesDataSource struct {
	client *sdk.CloudClient
}

type imageModel struct {
	ID           types.String `tfsdk:"id"`
	LocationID   types.String `tfsdk:"location_id"`
	Type         types.String `tfsdk:"type"`
	OSVersion    types.String `tfsdk:"os_version"`
	Architecture types.String `tfsdk:"architecture"`
	AllowSSHKeys types.Bool   `tfsdk:"allow_ssh_keys"`
}

type imagesListModel struct {
	Images []imageModel `tfsdk:"images"`
}

func (d *imagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_images"
}

func (d *imagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List available OS images.",
		Attributes: map[string]schema.Attribute{
			"images": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":             schema.StringAttribute{Computed: true},
						"location_id":    schema.StringAttribute{Computed: true},
						"type":           schema.StringAttribute{Computed: true},
						"os_version":     schema.StringAttribute{Computed: true},
						"architecture":   schema.StringAttribute{Computed: true},
						"allow_ssh_keys": schema.BoolAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *imagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *imagesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	imgs, err := d.client.GetImages(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read images", err.Error())
		return
	}

	var state imagesListModel
	// Empty (not null) list, so length()/for_each work on an empty account.
	state.Images = make([]imageModel, 0, len(imgs))
	for _, img := range imgs {
		im := imageModel{
			ID:           types.StringValue(img.ID),
			LocationID:   types.StringValue(img.LocationID),
			Type:         types.StringValue(img.Type),
			OSVersion:    types.StringValue(img.OSVersion),
			Architecture: types.StringValue(img.Architecture),
			AllowSSHKeys: types.BoolValue(img.AllowSSHKeys),
		}
		state.Images = append(state.Images, im)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
