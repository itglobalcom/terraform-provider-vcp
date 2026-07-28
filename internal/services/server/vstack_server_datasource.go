package vstack_server

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var _ datasource.DataSource = &serverDataSource{}

func NewServerDataSource() datasource.DataSource {
	return &serverDataSource{}
}

type serverDataSource struct {
	client *sdk.CloudClient
}

// serverDataModel is a read-only reflection of a server. Volumes are listed as
// the API reports them (no config-side "number" slots); login/password and
// create-only arguments (init_script, applications_ids) are not exposed.
type serverDataModel struct {
	ID              types.String         `tfsdk:"id"`
	Name            types.String         `tfsdk:"name"`
	LocationID      types.String         `tfsdk:"location_id"`
	ImageID         types.String         `tfsdk:"image_id"`
	CPU             types.Int64          `tfsdk:"cpu"`
	RamMB           types.Int64          `tfsdk:"ram_mb"`
	State           types.String         `tfsdk:"state"`
	IsPowerOn       types.Bool           `tfsdk:"is_power_on"`
	Created         types.String         `tfsdk:"created"`
	AffinityGroupID types.String         `tfsdk:"affinity_group_id"`
	SSHKeyIDs       []types.Int64        `tfsdk:"ssh_key_ids"`
	Tags            types.Set            `tfsdk:"tags"`
	Volumes         []serverVolumeModel  `tfsdk:"volumes"`
	NICs            []serverNICDataModel `tfsdk:"nics"`
}

type serverVolumeModel struct {
	ID      types.Int64  `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	SizeMB  types.Int64  `tfsdk:"size_mb"`
	Created types.String `tfsdk:"created"`
}

type serverNICDataModel struct {
	ID        types.Int64  `tfsdk:"id"`
	NetworkID types.String `tfsdk:"network_id"`
	IPAddress types.String `tfsdk:"ip_address"`
	MAC       types.String `tfsdk:"mac"`
}

func mapServerToDataModel(server *entities.Server) serverDataModel {
	model := serverDataModel{
		ID:              types.StringValue(server.ID),
		Name:            types.StringValue(server.Name),
		LocationID:      types.StringValue(server.LocationID),
		ImageID:         types.StringValue(server.ImageID),
		CPU:             types.Int64Value(int64(server.CPU)),
		RamMB:           types.Int64Value(int64(server.RamMB)),
		State:           types.StringValue(server.State),
		IsPowerOn:       types.BoolValue(server.IsPowerOn),
		Created:         types.StringValue(server.Created),
		AffinityGroupID: types.StringValue(server.AffinityGroupID),
		Tags:            stringsToTagSet(server.Tags),
	}

	for _, keyID := range server.SSHKeyIDs {
		model.SSHKeyIDs = append(model.SSHKeyIDs, types.Int64Value(int64(keyID)))
	}

	for _, vol := range server.Volumes {
		model.Volumes = append(model.Volumes, serverVolumeModel{
			ID:      types.Int64Value(int64(vol.ID)),
			Name:    types.StringValue(vol.Name),
			SizeMB:  types.Int64Value(int64(vol.SizeMB)),
			Created: types.StringValue(vol.Created),
		})
	}

	for _, nic := range server.NICs {
		model.NICs = append(model.NICs, serverNICDataModel{
			ID:        types.Int64Value(int64(nic.ID)),
			NetworkID: types.StringValue(nic.NetworkID),
			IPAddress: types.StringValue(nic.IPAddress),
			MAC:       types.StringValue(nic.MAC),
		})
	}

	return model
}

// stringsToTagSet converts a string slice to a set value (empty set for nil —
// the same null-vs-empty convention the resources use).
func stringsToTagSet(values []string) types.Set {
	elements := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elements = append(elements, types.StringValue(v))
	}
	return types.SetValueMust(types.StringType, elements)
}

// serverDataAttributes is the shared attribute set of the singular and plural
// data sources; only the "id" requirement differs.
func serverDataAttributes(idRequired bool) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			MarkdownDescription: "Server ID.",
			Required:            idRequired,
			Computed:            !idRequired,
		},
		"name":              schema.StringAttribute{Computed: true, MarkdownDescription: "Server name."},
		"location_id":       schema.StringAttribute{Computed: true, MarkdownDescription: "Location ID."},
		"image_id":          schema.StringAttribute{Computed: true, MarkdownDescription: "OS image ID the server was created from."},
		"cpu":               schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of CPU cores."},
		"ram_mb":            schema.Int64Attribute{Computed: true, MarkdownDescription: "RAM size in megabytes."},
		"state":             schema.StringAttribute{Computed: true, MarkdownDescription: "Server state (e.g. Active, Busy)."},
		"is_power_on":       schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the server is powered on."},
		"created":           schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
		"affinity_group_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Affinity group ID (empty if none)."},
		"ssh_key_ids": schema.ListAttribute{
			MarkdownDescription: "SSH key IDs installed on the server.",
			Computed:            true,
			ElementType:         types.Int64Type,
		},
		"tags": schema.SetAttribute{
			MarkdownDescription: "Set of tags.",
			Computed:            true,
			ElementType:         types.StringType,
		},
		"volumes": schema.ListNestedAttribute{
			MarkdownDescription: "Volumes attached to the server.",
			Computed:            true,
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"id":      schema.Int64Attribute{Computed: true, MarkdownDescription: "Volume ID."},
					"name":    schema.StringAttribute{Computed: true, MarkdownDescription: "Volume name."},
					"size_mb": schema.Int64Attribute{Computed: true, MarkdownDescription: "Volume size in megabytes."},
					"created": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
				},
			},
		},
		"nics": schema.ListNestedAttribute{
			MarkdownDescription: "Network interfaces of the server.",
			Computed:            true,
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"id":         schema.Int64Attribute{Computed: true, MarkdownDescription: "NIC ID."},
					"network_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Connected network ID."},
					"ip_address": schema.StringAttribute{Computed: true, MarkdownDescription: "IP address."},
					"mac":        schema.StringAttribute{Computed: true, MarkdownDescription: "MAC address."},
				},
			},
		},
	}
}

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches information about a specific server by ID.",
		Attributes:          serverDataAttributes(true),
	}
}

func (d *serverDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data serverDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, err := d.client.GetServer(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Server",
			fmt.Sprintf("Could not read server %s: %s", data.ID.ValueString(), err.Error()),
		)
		return
	}

	data = mapServerToDataModel(server)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
