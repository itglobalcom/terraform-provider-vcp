package llm

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                   = &accessKeyResource{}
	_ resource.ResourceWithConfigure      = &accessKeyResource{}
	_ resource.ResourceWithImportState    = &accessKeyResource{}
	_ resource.ResourceWithValidateConfig = &accessKeyResource{}
)

// NewAccessKeyResource returns the vcp_llm_access_key resource.
func NewAccessKeyResource() resource.Resource {
	return &accessKeyResource{}
}

type accessKeyResource struct {
	client *sdk.CloudClient
}

type accessKeyModel struct {
	ID         types.Int64  `tfsdk:"id"`
	GroupID    types.Int64  `tfsdk:"group_id"`
	Name       types.String `tfsdk:"name"`
	Tags       types.Set    `tfsdk:"tags"`
	Locked     types.Bool   `tfsdk:"locked"`
	LockReason types.String `tfsdk:"lock_reason"`
	Secret     types.String `tfsdk:"secret"`
	State      types.String `tfsdk:"state"`
}

func (r *accessKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_llm_access_key"
}

func (r *accessKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an access key of an LLM group. The key calls the models of its group " +
			"at the inference address of each model (`vcp_llm_models`).\n\n" +
			"~> The API hands the secret out once. The provider takes it right after issuing the key and keeps " +
			"it in the state only: protect the state. An imported key, or one whose secret could not be taken " +
			"after the issue, has no `secret`, and the only way to get one is to replace the key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "Access key ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"group_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the group (`vcp_llm_group`) the key belongs to. Changing it deletes " +
					"the key and issues a new one, with a new secret.",
				Required:      true,
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Key name. Changing it deletes the key and issues a new one, with a new secret.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, entities.LLMNameMaxLength)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"tags": schema.SetAttribute{
				MarkdownDescription: "Tags of the key. The set is applied whole; omitting it or an empty set " +
					"removes every tag.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
			},
			"locked": schema.BoolAttribute{
				MarkdownDescription: "Whether the key is locked by the project. A locked key is refused by the " +
					"models until it is unlocked. Locks by quota, by the administrator, by the balance and by " +
					"the platform are not managed here.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"lock_reason": schema.StringAttribute{
				MarkdownDescription: "Reason of the lock, for the people reading the panel. Only with " +
					"`locked = true`. Changing it on a locked key unlocks the key and locks it again with the " +
					"new reason.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"secret": schema.StringAttribute{
				MarkdownDescription: "Secret of the key, for the `Authorization` header of the inference calls. " +
					"Set when the key is issued; empty for an imported key.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Key state: `active`, `revoking`, `revoked` or `removing`. A key of a " +
					"disabled group is revoked. New states may be added over time.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *accessKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if client := clientFrom(req.ProviderData, &resp.Diagnostics); client != nil {
		r.client = client
	}
}

func (r *accessKeyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config accessKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.LockReason.IsNull() || config.LockReason.IsUnknown() || config.Locked.IsUnknown() {
		return
	}
	if !config.Locked.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("lock_reason"), "Lock Reason Without A Lock",
			"lock_reason is the reason of a lock, and the key is not locked. Remove lock_reason, or set locked = true.")
	}
}

func (r *accessKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan accessKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tags := setStrings(ctx, plan.Tags, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := entities.LLMGroupID(plan.GroupID.ValueInt64())
	key, err := r.client.IssueLLMAccessKeyAndWait(ctx, groupID, &entities.IssueLLMAccessKeyRequest{Name: plan.Name.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Error Issuing LLM Access Key",
			fmt.Sprintf("Could not issue access key %q in LLM group %d: %s", plan.Name.ValueString(), groupID, err))
		return
	}
	if key.ID == nil {
		resp.Diagnostics.AddError("Error Issuing LLM Access Key",
			fmt.Sprintf("The API reported access key %q without its ID.", plan.Name.ValueString()))
		return
	}
	id := *key.ID

	// The secret is taken first: it is handed out once, and nothing after it
	// may stand between the issue and the only chance to read it.
	secret := types.StringNull()
	if value, err := r.client.RevealLLMAccessKeySecret(ctx, groupID, id); err != nil {
		resp.Diagnostics.AddWarning("LLM Access Key Issued Without Secret",
			fmt.Sprintf("Access key %d was issued, but its secret could not be taken: %s\n\n"+
				"The key is kept in the state without a secret. To get a key with a secret, replace it: "+
				"terraform apply -replace=<address of this resource>.", id, err))
	} else {
		secret = types.StringValue(value)
	}

	if len(tags) > 0 {
		if err := r.client.SetLLMAccessKeyTags(ctx, groupID, id, &entities.SetLLMTagsRequest{Tags: tags}); err != nil {
			resp.Diagnostics.AddError("Error Setting LLM Access Key Tags",
				fmt.Sprintf("Access key %d was issued, but its tags could not be set: %s", id, err))
		}
	}
	if plan.Locked.ValueBool() && !resp.Diagnostics.HasError() {
		if _, err := r.client.LockLLMAccessKeyAndWait(ctx, groupID, id, lockRequest(plan.LockReason)); err != nil {
			resp.Diagnostics.AddError("Error Locking LLM Access Key",
				fmt.Sprintf("Access key %d was issued, but it could not be locked: %s", id, err))
		}
	}

	r.refresh(ctx, groupID, id, secret, &resp.State, &resp.Diagnostics)
}

func (r *accessKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state accessKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := entities.LLMGroupID(state.GroupID.ValueInt64())
	id := entities.LLMAccessKeyID(state.ID.ValueInt64())
	key, err := r.client.GetLLMAccessKey(ctx, groupID, id)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading LLM Access Key",
			fmt.Sprintf("Could not read access key %d of LLM group %d: %s", id, groupID, err))
		return
	}
	if key.State != nil && *key.State == entities.LLMAccessKeyStateRemoved {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, accessKeyStateFrom(groupID, id, key, state.Secret))...)
}

func (r *accessKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state accessKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := entities.LLMGroupID(state.GroupID.ValueInt64())
	id := entities.LLMAccessKeyID(state.ID.ValueInt64())

	if !plan.Tags.Equal(state.Tags) {
		tags := setStrings(ctx, plan.Tags, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		if err := r.client.SetLLMAccessKeyTags(ctx, groupID, id, &entities.SetLLMTagsRequest{Tags: tags}); err != nil {
			resp.Diagnostics.AddError("Error Setting LLM Access Key Tags",
				fmt.Sprintf("Could not set the tags of access key %d: %s", id, err))
		}
	}

	if !resp.Diagnostics.HasError() {
		r.applyLock(ctx, groupID, id, state, plan, &resp.Diagnostics)
	}

	r.refresh(ctx, groupID, id, state.Secret, &resp.State, &resp.Diagnostics)
}

// applyLock moves the client lock from state to plan. The API refuses to lock
// a locked key, so a new reason on a locked key is an unlock and a lock.
func (r *accessKeyResource) applyLock(ctx context.Context, groupID entities.LLMGroupID, id entities.LLMAccessKeyID, state, plan accessKeyModel, diags *diag.Diagnostics) {
	wasLocked, locked := state.Locked.ValueBool(), plan.Locked.ValueBool()
	reasonChanged := !plan.LockReason.Equal(state.LockReason)

	if wasLocked && (!locked || reasonChanged) {
		if _, err := r.client.UnlockLLMAccessKeyAndWait(ctx, groupID, id); err != nil {
			diags.AddError("Error Unlocking LLM Access Key", fmt.Sprintf("Could not unlock access key %d: %s", id, err))
			return
		}
	}
	if locked && (!wasLocked || reasonChanged) {
		if _, err := r.client.LockLLMAccessKeyAndWait(ctx, groupID, id, lockRequest(plan.LockReason)); err != nil {
			diags.AddError("Error Locking LLM Access Key", fmt.Sprintf("Could not lock access key %d: %s", id, err))
		}
	}
}

func (r *accessKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state accessKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := entities.LLMGroupID(state.GroupID.ValueInt64())
	id := entities.LLMAccessKeyID(state.ID.ValueInt64())
	if err := r.client.DeleteLLMAccessKeyAndWait(ctx, groupID, id); err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Deleting LLM Access Key",
			fmt.Sprintf("Could not delete access key %d of LLM group %d: %s", id, groupID, err))
	}
}

// ImportState takes "<group_id>/<access_key_id>". The secret is not imported:
// the API does not hand it out again.
func (r *accessKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	groupPart, keyPart, ok := strings.Cut(req.ID, "/")
	groupID, groupErr := strconv.ParseInt(groupPart, 10, 64)
	keyID, keyErr := strconv.ParseInt(keyPart, 10, 64)
	if !ok || groupErr != nil || keyErr != nil || groupID <= 0 || keyID <= 0 {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected <group_id>/<access_key_id>, both numeric, got: %s", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), groupID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), keyID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("secret"), types.StringNull())...)
}

// refresh writes the key as the API reports it, with the secret the state
// holds. When the read fails the key is still recorded by its IDs and secret,
// with the error added.
func (r *accessKeyResource) refresh(ctx context.Context, groupID entities.LLMGroupID, id entities.LLMAccessKeyID, secret types.String, target stateSetter, diags *diag.Diagnostics) {
	key, err := r.client.GetLLMAccessKey(ctx, groupID, id)
	if err != nil {
		diags.AddError("Error Reading LLM Access Key",
			fmt.Sprintf("Could not read access key %d of LLM group %d: %s", id, groupID, err))
		diags.Append(target.SetAttribute(ctx, path.Root("group_id"), int64(groupID))...)
		diags.Append(target.SetAttribute(ctx, path.Root("id"), int64(id))...)
		diags.Append(target.SetAttribute(ctx, path.Root("secret"), secret)...)
		return
	}
	diags.Append(target.Set(ctx, accessKeyStateFrom(groupID, id, key, secret))...)
}

func lockRequest(reason types.String) *entities.LockLLMAccessKeyRequest {
	return &entities.LockLLMAccessKeyRequest{Reason: reason.ValueString()}
}

func accessKeyStateFrom(groupID entities.LLMGroupID, id entities.LLMAccessKeyID, key *entities.LLMAccessKey, secret types.String) *accessKeyModel {
	locked, reason := false, types.StringNull()
	if key.Locks != nil && key.Locks.ByClient {
		locked = true
		reason = optString(key.Locks.ClientReason)
	}
	state := types.StringNull()
	if key.State != nil {
		state = types.StringValue(string(*key.State))
	}
	return &accessKeyModel{
		ID:         types.Int64Value(int64(id)),
		GroupID:    types.Int64Value(int64(groupID)),
		Name:       types.StringValue(key.Name),
		Tags:       stringSet(key.Tags),
		Locked:     types.BoolValue(locked),
		LockReason: reason,
		Secret:     secret,
		State:      state,
	}
}
