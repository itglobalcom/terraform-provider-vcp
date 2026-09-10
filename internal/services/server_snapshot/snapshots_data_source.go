package server_snapshot

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var (
	_ datasource.DataSource              = &snapshotsDataSource{}
	_ datasource.DataSourceWithConfigure = &snapshotsDataSource{}
)

func NewSnapshotsDataSource() datasource.DataSource { return &snapshotsDataSource{} }

type snapshotsDataSource struct {
	client *sdk.CloudClient
}

type snapshotsDataSourceModel struct {
	ServerID  types.String    `tfsdk:"server_id"`
	Snapshots []snapshotModel `tfsdk:"snapshots"`
}

func (d *snapshotsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_snapshots"
}

func (d *snapshotsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches the snapshots of a server.",
		MarkdownDescription: "Fetches the snapshots of a server, whether they were taken by Terraform or in the " +
			"panel. Snapshots are per-server: there is no way to list every snapshot in the project at once.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server whose snapshots are listed.",
				Required:            true,
			},
			"snapshots": schema.ListNestedAttribute{
				MarkdownDescription: "The snapshots the server holds, in the order the API lists them.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":        schema.Int64Attribute{Computed: true, MarkdownDescription: "ID of the snapshot."},
						"server_id": schema.StringAttribute{Computed: true, MarkdownDescription: "ID of the server the snapshot belongs to."},
						"name":      schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the snapshot."},
						"size_mb":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Size of the snapshot in megabytes."},
						"created":   schema.StringAttribute{Computed: true, MarkdownDescription: "When the snapshot was taken."},
					},
				},
			},
		},
	}
}

func (d *snapshotsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData))
		return
	}
	d.client = client
}

func (d *snapshotsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config snapshotsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := config.ServerID.ValueString()
	snapshots, err := d.client.GetServerSnapshots(ctx, serverID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to List Server Snapshots",
			fmt.Sprintf("Could not list the snapshots of server %s: %s", serverID, err.Error()))
		return
	}

	// An empty list, not null: a server with no snapshots is a normal answer, and
	// `length(...) == 0` has to work on it.
	config.Snapshots = make([]snapshotModel, 0, len(snapshots))
	for i := range snapshots {
		config.Snapshots = append(config.Snapshots, mapSnapshot(serverID, &snapshots[i]))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
