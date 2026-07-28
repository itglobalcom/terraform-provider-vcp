package dns

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var (
	_ datasource.DataSource              = &domainsDataSource{}
	_ datasource.DataSourceWithConfigure = &domainsDataSource{}
)

// NewDomainsDataSource is a helper function to simplify the provider implementation.
func NewDomainsDataSource() datasource.DataSource {
	return &domainsDataSource{}
}

type domainsDataSource struct {
	client *sdk.CloudClient
}

func (d *domainsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_domains"
}

func (d *domainsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches all DNS domains (zones) in the project, each including its record sets.",
		Attributes: map[string]schema.Attribute{
			"domains": schema.ListNestedAttribute{
				MarkdownDescription: "All zones in the project.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":         schema.StringAttribute{Computed: true, MarkdownDescription: "Zone name (FQDN)."},
						"is_delegated": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the zone is delegated."},
						"record_sets": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Record sets in the zone, grouped by name and type.",
							NestedObject:        recordSetDataNestedObject(),
						},
					},
				},
			},
		},
	}
}

func (d *domainsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *domainsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state domainsDataModel

	domains, err := d.client.GetDomains(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read DNS Domains",
			fmt.Sprintf("Could not read domains: %s", err.Error()),
		)
		return
	}

	// Empty (not null) list, so length()/for_each work on an empty account.
	state.Domains = make([]domainDataModel, 0, len(domains))
	for _, domain := range domains {
		state.Domains = append(state.Domains, mapDomainToDataModel(domain))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
