// Package server_backup implements the backup service of a vStack server: the
// vcp_server_backup_storages data source, which lists the storages a schedule
// can put copies into, and the vcp_server_backup resource, which is the
// schedule itself.
package server_backup

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ datasource.DataSource              = &storagesDataSource{}
	_ datasource.DataSourceWithConfigure = &storagesDataSource{}
)

func NewStoragesDataSource() datasource.DataSource { return &storagesDataSource{} }

type storagesDataSource struct {
	client *sdk.CloudClient
}

type storagesDataSourceModel struct {
	ServerID types.String   `tfsdk:"server_id"`
	Storages []storageModel `tfsdk:"storages"`
	Limits   *limitsModel   `tfsdk:"limits"`
}

type storageModel struct {
	ID                 types.Int64   `tfsdk:"id"`
	Name               types.String  `tfsdk:"name"`
	Description        types.String  `tfsdk:"description"`
	SortOrder          types.Int64   `tfsdk:"sort_order"`
	PricePerGB         types.Float64 `tfsdk:"price_per_gb"`
	LocationID         types.String  `tfsdk:"location_id"`
	RestoreLocationIDs []string      `tfsdk:"restore_location_ids"`
}

type limitsModel struct {
	ScheduleWindowFromHour types.Int64      `tfsdk:"schedule_window_from_hour"`
	ScheduleWindowToHour   types.Int64      `tfsdk:"schedule_window_to_hour"`
	Daily                  *ruleLimitsModel `tfsdk:"daily"`
	Weekly                 *ruleLimitsModel `tfsdk:"weekly"`
	Monthly                *ruleLimitsModel `tfsdk:"monthly"`
}

type ruleLimitsModel struct {
	MaxKeep     types.Int64 `tfsdk:"max_keep"`
	DefaultKeep types.Int64 `tfsdk:"default_keep"`
}

func (d *storagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_backup_storages"
}

func ruleLimitsAttribute(rule string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: fmt.Sprintf("Limits of the `%s` rule of `vcp_server_backup`. Null when the partner "+
			"sets none.", rule),
		Computed: true,
		Attributes: map[string]schema.Attribute{
			"max_keep": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The largest `keep` the rule accepts.",
			},
			"default_keep": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The `keep` the panel offers for the rule by default.",
			},
		},
	}
}

func (d *storagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches the backup storages available to a server and the limits of its backup schedule.",
		MarkdownDescription: "Fetches the backup storages a server's copies can be kept in, with their price, " +
			"and the partner's limits a `vcp_server_backup` schedule has to fit into. The list is the one the " +
			"panel offers in the server's backup settings: it depends on the server's location and on the " +
			"project's tariff, so it is read per server.",
		Attributes: map[string]schema.Attribute{
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server whose backup storages are listed.",
				Required:            true,
			},
			"storages": schema.ListNestedAttribute{
				MarkdownDescription: "The storages available to the server, in the order the API lists them. " +
					"`id` is what a rule of `vcp_server_backup` takes as `backup_storage_id`.",
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{Computed: true, MarkdownDescription: "ID of the storage."},
						"name": schema.StringAttribute{Computed: true,
							MarkdownDescription: "Name of the storage."},
						"description": schema.StringAttribute{Computed: true,
							MarkdownDescription: "Description of the storage. Null when it has none."},
						"sort_order": schema.Int64Attribute{Computed: true,
							MarkdownDescription: "Position of the storage in the panel's list."},
						"price_per_gb": schema.Float64Attribute{Computed: true,
							MarkdownDescription: "Price of keeping one gigabyte of copies, in the project's tariff."},
						"location_id": schema.StringAttribute{Computed: true,
							MarkdownDescription: "ID of the location the storage is in."},
						"restore_location_ids": schema.ListAttribute{Computed: true, ElementType: types.StringType,
							MarkdownDescription: "IDs of the locations a copy from this storage can be restored " +
								"into as a new server."},
					},
				},
			},
			"limits": schema.SingleNestedAttribute{
				MarkdownDescription: "The partner's limits of the backup schedule. Null when the API returns none.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"schedule_window_from_hour": schema.Int64Attribute{Computed: true,
						MarkdownDescription: "The earliest `hour` a schedule may start copies at."},
					"schedule_window_to_hour": schema.Int64Attribute{Computed: true,
						MarkdownDescription: "The latest `hour` a schedule may start copies at."},
					"daily":   ruleLimitsAttribute("daily"),
					"weekly":  ruleLimitsAttribute("weekly"),
					"monthly": ruleLimitsAttribute("monthly"),
				},
			},
		},
	}
}

func (d *storagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *storagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config storagesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := config.ServerID.ValueString()
	catalog, err := d.client.GetServerBackupStorages(ctx, serverID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to List Server Backup Storages",
			fmt.Sprintf("Could not list the backup storages of server %s: %s", serverID, err.Error()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapStorageCatalog(serverID, catalog))...)
}

// mapStorageCatalog transfers the API catalog into the data source model.
// Lists come out empty rather than null, so `length(...)` works on a server
// with no storages; objects the API leaves out come out null.
func mapStorageCatalog(serverID string, catalog *entities.BackupStorageCatalog) storagesDataSourceModel {
	m := storagesDataSourceModel{
		ServerID: types.StringValue(serverID),
		Storages: make([]storageModel, 0, len(catalog.Storages)),
	}
	for _, s := range catalog.Storages {
		restore := make([]string, 0, len(s.RestoreLocationIDs))
		restore = append(restore, s.RestoreLocationIDs...)
		description := types.StringNull()
		if s.Description != "" {
			description = types.StringValue(s.Description)
		}
		m.Storages = append(m.Storages, storageModel{
			ID:                 types.Int64Value(int64(s.ID)),
			Name:               types.StringValue(s.Name),
			Description:        description,
			SortOrder:          types.Int64Value(int64(s.SortOrder)),
			PricePerGB:         types.Float64Value(s.PricePerGB),
			LocationID:         types.StringValue(s.LocationID),
			RestoreLocationIDs: restore,
		})
	}
	if l := catalog.Limits; l != nil {
		m.Limits = &limitsModel{
			ScheduleWindowFromHour: types.Int64Value(int64(l.ScheduleWindowFromHour)),
			ScheduleWindowToHour:   types.Int64Value(int64(l.ScheduleWindowToHour)),
			Daily:                  mapRuleLimits(l.Daily),
			Weekly:                 mapRuleLimits(l.Weekly),
			Monthly:                mapRuleLimits(l.Monthly),
		}
	}
	return m
}

func mapRuleLimits(l *entities.BackupRuleLimits) *ruleLimitsModel {
	if l == nil {
		return nil
	}
	return &ruleLimitsModel{
		MaxKeep:     types.Int64Value(int64(l.MaxKeep)),
		DefaultKeep: types.Int64Value(int64(l.DefaultKeep)),
	}
}
