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
	_ datasource.DataSource              = &sshKeysDataSource{}
	_ datasource.DataSourceWithConfigure = &sshKeysDataSource{}
)

func NewSSHKeysDataSource() datasource.DataSource {
	return &sshKeysDataSource{}
}

type sshKeysDataSource struct {
	client *sdk.CloudClient
}

type sshKeysDataSourceModel struct {
	SSHKeys []sshKeyModel `tfsdk:"ssh_keys"`
}

func (d *sshKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_keys"
}

func (d *sshKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Fetches a list of all SSH keys.",
		MarkdownDescription: "Fetches a list of all SSH keys in your account.",
		Attributes: map[string]schema.Attribute{
			"ssh_keys": schema.ListNestedAttribute{
				Description:         "List of SSH keys.",
				MarkdownDescription: "List of all SSH keys in your account.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{
							Description:         "The unique identifier of the SSH key.",
							MarkdownDescription: "The unique identifier of the SSH key.",
							Computed:            true,
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
				},
			},
		},
	}
}

func (d *sshKeysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *sshKeysDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state sshKeysDataSourceModel

	sshKeys, err := d.client.GetSSHKeyList(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read SSH Keys",
			fmt.Sprintf("Could not read SSH keys: %s", err),
		)
		return
	}

	// Empty (not null) list, so length()/for_each work on an empty account.
	state.SSHKeys = make([]sshKeyModel, 0, len(sshKeys))
	for _, key := range sshKeys {
		keyModel := sshKeyModel{
			ID:        types.Int64Value(int64(key.ID)),
			Name:      types.StringValue(key.Name),
			PublicKey: NewPublicKeyValue(key.PublicKey),
		}
		state.SSHKeys = append(state.SSHKeys, keyModel)
	}
	diags := resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}
