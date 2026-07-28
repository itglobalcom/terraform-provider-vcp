package ssh_key

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var (
	_ datasource.DataSource              = &sshKeyDataSource{}
	_ datasource.DataSourceWithConfigure = &sshKeyDataSource{}
)

func NewSSHKeyDataSource() datasource.DataSource {
	return &sshKeyDataSource{}
}

type sshKeyDataSource struct {
	client *sdk.CloudClient
}

func (d *sshKeyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (d *sshKeyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Fetches information about an existing SSH key.",
		MarkdownDescription: "Fetches information about an existing SSH key by ID.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description:         "The unique identifier of the SSH key.",
				MarkdownDescription: "The unique identifier of the SSH key to fetch.",
				Required:            true,
			},
			"name": schema.StringAttribute{
				Description:         "The name of the SSH key.",
				MarkdownDescription: "The name of the SSH key.",
				Computed:            true,
			},
			"public_key": schema.StringAttribute{
				Description:         "The public key material.",
				MarkdownDescription: "The public key material in OpenSSH format.",
				Computed:            true,
				CustomType:          PublicKeyType{}, // shared sshKeyModel uses PublicKeyValue
			},
		},
	}
}

func (d *sshKeyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *sshKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config sshKeyModel
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	sshKey, err := d.client.GetSSHKey(ctx, int(config.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read SSH Key",
			fmt.Sprintf("Could not read SSH key %d: %s", config.ID.ValueInt64(), err),
		)
		return
	}

	state := sshKeyModel{
		ID:        types.Int64Value(int64(sshKey.ID)),
		Name:      types.StringValue(sshKey.Name),
		PublicKey: NewPublicKeyValue(sshKey.PublicKey),
	}

	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}
