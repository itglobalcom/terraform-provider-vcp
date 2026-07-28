package ssh_key

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                = &sshKeyResource{}
	_ resource.ResourceWithConfigure   = &sshKeyResource{}
	_ resource.ResourceWithImportState = &sshKeyResource{}
)

func NewSSHKeyResource() resource.Resource {
	return &sshKeyResource{}
}

type sshKeyResource struct {
	client *sdk.CloudClient
}

type sshKeyModel struct {
	ID        types.Int64    `tfsdk:"id"`
	Name      types.String   `tfsdk:"name"`
	PublicKey PublicKeyValue `tfsdk:"public_key"`
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:         "Manages an SSH key.",
		MarkdownDescription: "Manages an SSH key for authentication. Changing any attribute recreates the key (delete + create); add a `lifecycle { create_before_destroy = true }` block to minimize disruption to dependent resources.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description:         "The unique identifier of the SSH key.",
				MarkdownDescription: "The unique identifier of the SSH key. Changes when the key is recreated.",
				Computed:            true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description:         "The name of the SSH key.",
				MarkdownDescription: "The name of the SSH key. Changing this will recreate the key.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"public_key": schema.StringAttribute{
				Description:         "The public key material (OpenSSH format).",
				MarkdownDescription: "The public key material in OpenSSH format. Changing this will recreate the key. Surrounding whitespace (e.g. the trailing newline from `file(...)`) is insignificant.",
				Required:            true,
				// Semantic equality: the backend trims surrounding whitespace,
				// so the trailing newline from file(...) must not cause a diff.
				CustomType: PublicKeyType{},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sshKeyModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := &entities.CreateSSHKeyRequest{
		Name:      plan.Name.ValueString(),
		PublicKey: plan.PublicKey.ValueString(),
	}

	sshKey, err := r.client.CreateSSHKey(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating SSH key",
			fmt.Sprintf("Could not create SSH key: %s", err),
		)
		return
	}

	plan.ID = types.Int64Value(int64(sshKey.ID))
	plan.Name = types.StringValue(sshKey.Name)
	// Safe to store the API echo: PublicKeyValue's semantic equality keeps the
	// user's literal (e.g. with the trailing newline from file(...)) whenever
	// the echoed key is trim-equal to the planned one.
	plan.PublicKey = NewPublicKeyValue(sshKey.PublicKey)

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sshKeyModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	sshKey, err := r.client.GetSSHKey(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error reading SSH key",
			fmt.Sprintf("Could not read SSH key ID %d: %s", state.ID.ValueInt64(), err),
		)
		return
	}

	state.ID = types.Int64Value(int64(sshKey.ID))
	state.Name = types.StringValue(sshKey.Name)
	state.PublicKey = NewPublicKeyValue(sshKey.PublicKey)

	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}

func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Unexpected Update",
		"Update should not be called because all configurable attributes require replacement.",
	)
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sshKeyModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteSSHKey(ctx, int(state.ID.ValueInt64()))
	if err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "SSH key already deleted, treating as success",
				map[string]any{"id": state.ID.ValueInt64()})
			return
		}
		resp.Diagnostics.AddError(
			"Error deleting SSH key",
			fmt.Sprintf("Could not delete SSH key ID %d: %s", state.ID.ValueInt64(), err),
		)
		return
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected numeric SSH key ID, got: %s", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
