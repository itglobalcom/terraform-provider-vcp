package dns

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var (
	_ datasource.DataSource              = &domainDataSource{}
	_ datasource.DataSourceWithConfigure = &domainDataSource{}
)

// NewDomainDataSource is a helper function to simplify the provider implementation.
func NewDomainDataSource() datasource.DataSource {
	return &domainDataSource{}
}

type domainDataSource struct {
	client *sdk.CloudClient
}

func (d *domainDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_domain"
}

func (d *domainDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a DNS domain (zone) by name, including all of its record sets " +
			"(grouped by name+type). System NS record sets provisioned with the zone are included.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Domain (zone) name to fetch, e.g. `example.com.`.",
				Required:            true,
			},
			"is_delegated": schema.BoolAttribute{
				MarkdownDescription: "Whether the zone is delegated to the provider's name servers.",
				Computed:            true,
			},
			"record_sets": schema.ListNestedAttribute{
				MarkdownDescription: "Record sets in the zone, grouped by name and type.",
				Computed:            true,
				NestedObject:        recordSetDataNestedObject(),
			},
		},
	}
}

// recordSetDataNestedObject describes one RRset in the data source.
func recordSetDataNestedObject() schema.NestedAttributeObject {
	return schema.NestedAttributeObject{
		Attributes: map[string]schema.Attribute{
			"name":     schema.StringAttribute{Computed: true, MarkdownDescription: "Record set name (FQDN)."},
			"type":     schema.StringAttribute{Computed: true, MarkdownDescription: "Record type (A, AAAA, MX, CNAME, NS, TXT, SRV)."},
			"ttl":      schema.StringAttribute{Computed: true, MarkdownDescription: "TTL shared by the whole record set."},
			"service":  schema.StringAttribute{Computed: true, MarkdownDescription: "SRV service (null for other types)."},
			"protocol": schema.StringAttribute{Computed: true, MarkdownDescription: "SRV protocol (null for other types)."},
			"values": schema.SetNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Values (rdata) of the record set.",
				NestedObject:        valueDataNestedObject(),
			},
		},
	}
}

// valueDataNestedObject describes one rdata value in the data source.
func valueDataNestedObject() schema.NestedAttributeObject {
	return schema.NestedAttributeObject{
		Attributes: map[string]schema.Attribute{
			"ip":               schema.StringAttribute{Computed: true, MarkdownDescription: "IPv4/IPv6 address (A/AAAA)."},
			"mail_host":        schema.StringAttribute{Computed: true, MarkdownDescription: "Mail host (MX)."},
			"priority":         schema.Int64Attribute{Computed: true, MarkdownDescription: "Priority (MX/SRV)."},
			"canonical_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "Canonical name (CNAME)."},
			"name_server_host": schema.StringAttribute{Computed: true, MarkdownDescription: "Name server host (NS)."},
			"text":             schema.StringAttribute{Computed: true, MarkdownDescription: "Text (TXT)."},
			"weight":           schema.Int64Attribute{Computed: true, MarkdownDescription: "Weight (SRV)."},
			"port":             schema.Int64Attribute{Computed: true, MarkdownDescription: "Port (SRV)."},
			"target":           schema.StringAttribute{Computed: true, MarkdownDescription: "Target (SRV)."},
		},
	}
}

func (d *domainDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *domainDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config domainDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	domain, err := d.client.GetDomain(ctx, config.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read DNS Domain",
			fmt.Sprintf("Could not read domain %s: %s", config.Name.ValueString(), err.Error()),
		)
		return
	}

	state := mapDomainToDataModel(domain)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
