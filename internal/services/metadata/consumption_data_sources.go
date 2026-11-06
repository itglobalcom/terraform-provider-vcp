package metadata

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ datasource.DataSourceWithConfigure = &projectConsumptionDataSource{}
	_ datasource.DataSourceWithConfigure = &projectConsumptionResourcesDataSource{}
	_ datasource.DataSourceWithConfigure = &projectConsumptionDailyDataSource{}
)

const consumptionServiceValues = "`vstack`, `vmware`, `kubernetes`, `vpn`, `cloud_director`, " +
	"`ssl_certificates`, `unoc_v1`, `unoc_v2`, `monitoring`, `object_storage`, `cdn`, `llm`, " +
	"`managed_servers`, `vdi`, `streaming`"

const consumptionTypeValues = "`vstack_server`, `vstack_edge`, `vmware_server`, " +
	"`vmware_server_template`, `vmware_network`, `kubernetes_cluster`, `vpn_server`, " +
	"`cloud_director_vdc`, `ssl_certificate`, `unoc_v1_company`, `unoc_v2_rdp_user`, " +
	"`unoc_v2_web_user`, `unoc_v2_terminal_server`, `unoc_v2_additional_infobases`, " +
	"`unoc_v2_additional_infobase_size`, `unoc_v2_infobase_backup`, `monitoring_host`, " +
	"`object_storage`, `cdn_site`, `cdn_s3_resource`, `cdn_vod`, `stream`, `llm_access_key`, " +
	"`llm_credits`, `llm_package`, " +
	"`managed_servers_subscription`, `manual_billing`"

const consumptionPeriodNote = "\n\nThe period is counted in the partner's time zone (`time_zone`) and both ends are " +
	"inclusive. It may be at most 366 days long.\n\n" +
	"~> While `is_final` is `false` the period reaches today and its amounts still grow as the day is " +
	"billed: a plan run later can read a different `total`."

// consumptionEnvelope is the part every project consumption read shares.
type consumptionEnvelope struct {
	From     types.String  `tfsdk:"from"`
	To       types.String  `tfsdk:"to"`
	TimeZone types.String  `tfsdk:"time_zone"`
	Currency types.String  `tfsdk:"currency"`
	IsFinal  types.Bool    `tfsdk:"is_final"`
	Total    types.Float64 `tfsdk:"total"`
}

func (e *consumptionEnvelope) period() *entities.ConsumptionPeriodRequest {
	return &entities.ConsumptionPeriodRequest{
		From: e.From.ValueString(),
		To:   e.To.ValueString(),
	}
}

func (e *consumptionEnvelope) fill(totals entities.ConsumptionTotals) {
	e.From = types.StringValue(totals.From)
	e.To = types.StringValue(totals.To)
	e.TimeZone = types.StringValue(totals.TimeZone)
	e.Currency = types.StringValue(totals.Currency)
	e.IsFinal = types.BoolValue(totals.IsFinal)
	e.Total = types.Float64Value(totals.Total)
}

// consumptionEnvelopeAttributes returns the envelope attributes; totalDescription
// says what the envelope total adds up.
func consumptionEnvelopeAttributes(totalDescription string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"from": schema.StringAttribute{
			MarkdownDescription: "First day of the period, `YYYY-MM-DD`, inclusive. " +
				"Defaults to the first day of the current month.",
			Optional: true,
			Computed: true,
		},
		"to": schema.StringAttribute{
			MarkdownDescription: "Last day of the period, `YYYY-MM-DD`, inclusive. Defaults to today. " +
				"Must not be earlier than `from`.",
			Optional: true,
			Computed: true,
		},
		"time_zone": schema.StringAttribute{
			MarkdownDescription: "IANA time zone of the partner, in which the period dates are counted.",
			Computed:            true,
		},
		"currency": schema.StringAttribute{
			MarkdownDescription: "Currency of the amounts — the project currency, as in `vcp_project`.",
			Computed:            true,
		},
		"is_final": schema.BoolAttribute{
			MarkdownDescription: "`false` while `to` is not earlier than today: the last day is still being billed.",
			Computed:            true,
		},
		"total": schema.Float64Attribute{
			MarkdownDescription: totalDescription,
			Computed:            true,
		},
	}
}

func configureConsumptionClient(req datasource.ConfigureRequest, diags *diag.Diagnostics) *sdk.CloudClient {
	if req.ProviderData == nil {
		return nil
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		diags.AddError("Unexpected Configure Type", fmt.Sprintf("Expected *sdk.CloudClient, got %T", req.ProviderData))
		return nil
	}
	return client
}

// ----------------------------------------------------------------------
// vcp_project_consumption
// ----------------------------------------------------------------------

func NewProjectConsumptionDataSource() datasource.DataSource {
	return &projectConsumptionDataSource{}
}

type projectConsumptionDataSource struct {
	client *sdk.CloudClient
}

type projectConsumptionModel struct {
	consumptionEnvelope
	Services []serviceConsumptionModel `tfsdk:"services"`
}

type serviceConsumptionModel struct {
	Service types.String  `tfsdk:"service"`
	Total   types.Float64 `tfsdk:"total"`
}

func (d *projectConsumptionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_consumption"
}

func (d *projectConsumptionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := consumptionEnvelopeAttributes("Amount charged to the project over the period; " +
		"the sum of `services` and of the days of `vcp_project_consumption_daily` for the same period.")
	attrs["services"] = schema.ListNestedAttribute{
		MarkdownDescription: "Amount per service, largest first. Only services charged in the period are listed.",
		Computed:            true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"service": schema.StringAttribute{
					MarkdownDescription: "Billed service: " + consumptionServiceValues + ". " +
						"New services are added to the set over time.",
					Computed: true,
				},
				"total": schema.Float64Attribute{
					MarkdownDescription: "Amount charged for the service over the period.",
					Computed:            true,
				},
			},
		},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads what the project was charged over a period, broken down by service." +
			consumptionPeriodNote,
		Attributes: attrs,
	}
}

func (d *projectConsumptionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configureConsumptionClient(req, &resp.Diagnostics); client != nil {
		d.client = client
	}
}

func (d *projectConsumptionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data projectConsumptionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	consumption, err := d.client.GetProjectConsumption(ctx, data.period())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project consumption", err.Error())
		return
	}

	data.fill(consumption.ConsumptionTotals)
	data.Services = make([]serviceConsumptionModel, len(consumption.Services))
	for i, s := range consumption.Services {
		data.Services[i] = serviceConsumptionModel{
			Service: types.StringValue(string(s.Service)),
			Total:   types.Float64Value(s.Total),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// ----------------------------------------------------------------------
// vcp_project_consumption_resources
// ----------------------------------------------------------------------

func NewProjectConsumptionResourcesDataSource() datasource.DataSource {
	return &projectConsumptionResourcesDataSource{}
}

type projectConsumptionResourcesDataSource struct {
	client *sdk.CloudClient
}

type projectConsumptionResourcesModel struct {
	consumptionEnvelope
	Service   types.String               `tfsdk:"service"`
	Resources []resourceConsumptionModel `tfsdk:"resources"`
}

type resourceConsumptionModel struct {
	Service types.String  `tfsdk:"service"`
	Type    types.String  `tfsdk:"type"`
	ID      types.String  `tfsdk:"id"`
	Name    types.String  `tfsdk:"name"`
	Total   types.Float64 `tfsdk:"total"`
}

func (d *projectConsumptionResourcesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_consumption_resources"
}

func (d *projectConsumptionResourcesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := consumptionEnvelopeAttributes("Amount charged to the project over the period; " +
		"the sum of the listed `resources`, so with `service` set it covers that service only.")
	attrs["service"] = schema.StringAttribute{
		MarkdownDescription: "Lists the resources of this service only: " + consumptionServiceValues + ". " +
			"Unset lists every service.",
		Optional: true,
	}
	attrs["resources"] = schema.ListNestedAttribute{
		MarkdownDescription: "Amount per root resource, largest first. Only resources charged in the period are listed.",
		Computed:            true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"service": schema.StringAttribute{
					MarkdownDescription: "Billed service the resource belongs to, from the same set as `service`.",
					Computed:            true,
				},
				"type": schema.StringAttribute{
					MarkdownDescription: "Kind of the resource: " + consumptionTypeValues + ". " +
						"New kinds are added to the set over time.",
					Computed: true,
				},
				"id": schema.StringAttribute{
					MarkdownDescription: "Identifier of the resource in the API, always a string: the `id` of a `vcp_server`, " +
						"or the `id` of a `vcp_vmware_server` or `vcp_vmware_network` written in decimal. " +
						"Null when the resource cannot be addressed through the API: a vStack gateway, most kinds " +
						"other than servers, networks and clusters, or a resource that no longer exists.",
					Computed: true,
				},
				"name": schema.StringAttribute{
					MarkdownDescription: "Name of the resource as the panel shows it in the charge details, " +
						"on the last day of the period it was charged. Null when the resource has no name of its own.",
					Computed: true,
				},
				"total": schema.Float64Attribute{
					MarkdownDescription: "Amount charged for the resource over the period.",
					Computed:            true,
				},
			},
		},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads what the project was charged over a period, broken down by root resource " +
			"(a server, a network, a cluster, …), optionally for one service." + consumptionPeriodNote,
		Attributes: attrs,
	}
}

func (d *projectConsumptionResourcesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configureConsumptionClient(req, &resp.Diagnostics); client != nil {
		d.client = client
	}
}

func (d *projectConsumptionResourcesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data projectConsumptionResourcesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	service := entities.ConsumptionService(data.Service.ValueString())
	consumption, err := d.client.GetProjectConsumptionResources(ctx, data.period(), service)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project consumption by resource", err.Error())
		return
	}

	data.fill(consumption.ConsumptionTotals)
	data.Resources = make([]resourceConsumptionModel, len(consumption.Resources))
	for i, r := range consumption.Resources {
		data.Resources[i] = resourceConsumptionModel{
			Service: types.StringValue(string(r.Service)),
			Type:    types.StringValue(string(r.Type)),
			ID:      types.StringPointerValue(r.ID),
			Name:    types.StringPointerValue(r.Name),
			Total:   types.Float64Value(r.Total),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// ----------------------------------------------------------------------
// vcp_project_consumption_daily
// ----------------------------------------------------------------------

func NewProjectConsumptionDailyDataSource() datasource.DataSource {
	return &projectConsumptionDailyDataSource{}
}

type projectConsumptionDailyDataSource struct {
	client *sdk.CloudClient
}

type projectConsumptionDailyModel struct {
	consumptionEnvelope
	Days []dailyConsumptionModel `tfsdk:"days"`
}

type dailyConsumptionModel struct {
	Date  types.String  `tfsdk:"date"`
	Total types.Float64 `tfsdk:"total"`
}

func (d *projectConsumptionDailyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_consumption_daily"
}

func (d *projectConsumptionDailyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := consumptionEnvelopeAttributes("Amount charged to the project over the period; the sum of `days`.")
	attrs["days"] = schema.ListNestedAttribute{
		MarkdownDescription: "Amount per day, in date order. Every day of the period is listed; " +
			"a day without charges has a `total` of `0`.",
		Computed: true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"date": schema.StringAttribute{
					MarkdownDescription: "Day, `YYYY-MM-DD`, in the partner's time zone.",
					Computed:            true,
				},
				"total": schema.Float64Attribute{
					MarkdownDescription: "Amount charged on the day.",
					Computed:            true,
				},
			},
		},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads what the project was charged on each day of a period." + consumptionPeriodNote,
		Attributes:          attrs,
	}
}

func (d *projectConsumptionDailyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configureConsumptionClient(req, &resp.Diagnostics); client != nil {
		d.client = client
	}
}

func (d *projectConsumptionDailyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data projectConsumptionDailyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	consumption, err := d.client.GetProjectConsumptionDaily(ctx, data.period())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read daily project consumption", err.Error())
		return
	}

	data.fill(consumption.ConsumptionTotals)
	data.Days = make([]dailyConsumptionModel, len(consumption.Days))
	for i, day := range consumption.Days {
		data.Days[i] = dailyConsumptionModel{
			Date:  types.StringValue(day.Date),
			Total: types.Float64Value(day.Total),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
