package llm

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                = &groupResource{}
	_ resource.ResourceWithConfigure   = &groupResource{}
	_ resource.ResourceWithImportState = &groupResource{}
)

// NewGroupResource returns the vcp_llm_group resource.
func NewGroupResource() resource.Resource {
	return &groupResource{}
}

type groupResource struct {
	client *sdk.CloudClient
}

type groupModel struct {
	ID       types.Int64  `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	ModelIDs types.Set    `tfsdk:"model_ids"`
	Tags     types.Set    `tfsdk:"tags"`
	State    types.String `tfsdk:"state"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_llm_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an LLM group: the set of catalog models its access keys may call. " +
			"The project has to be connected to the LLM service (`vcp_llm_connection`).\n\n" +
			"~> Destroying the group disables it together with all its access keys; it cannot be enabled " +
			"again. A disabled group stays in the project with `state = \"disabled\"`, and its name cannot " +
			"be used for a new group: creating one with it fails with `GroupAlreadyExists`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "Group ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Group name, unique within the project, including its disabled groups. " +
					"Changing it disables the group and creates a new one, with new access keys.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.LengthBetween(1, entities.LLMNameMaxLength)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"model_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of the catalog models (`vcp_llm_models`) the group may call. " +
					"The set is applied whole; omitting it or an empty set revokes every model.",
				ElementType: types.Int64Type,
				Optional:    true,
				Computed:    true,
				Default:     setdefault.StaticValue(types.SetValueMust(types.Int64Type, []attr.Value{})),
				Validators:  []validator.Set{setvalidator.ValueInt64sAre(int64validator.AtLeast(1))},
			},
			"tags": schema.SetAttribute{
				MarkdownDescription: "Tags of the group. The set is applied whole; omitting it or an empty set " +
					"removes every tag.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Group state: `active` or `disabled`. New states may be added over time.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if client := clientFrom(req.ProviderData, &resp.Diagnostics); client != nil {
		r.client = client
	}
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	modelIDs := setModelIDs(ctx, plan.ModelIDs, &resp.Diagnostics)
	tags := setStrings(ctx, plan.Tags, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.client.CreateLLMGroupAndWait(ctx, &entities.CreateLLMGroupRequest{Name: plan.Name.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Error Creating LLM Group",
			fmt.Sprintf("Could not create LLM group %q: %s", plan.Name.ValueString(), err))
		return
	}
	if group.ID == nil {
		resp.Diagnostics.AddError("Error Creating LLM Group",
			fmt.Sprintf("The API reported LLM group %q without its ID.", plan.Name.ValueString()))
		return
	}
	id := *group.ID

	// From here on the group exists: a failure below still records it, so the
	// next apply replaces it instead of creating a second one.
	if len(modelIDs) > 0 {
		if _, err := r.client.SetLLMGroupModelsAndWait(ctx, id, &entities.SetLLMGroupModelsRequest{ModelIDs: modelIDs}); err != nil {
			resp.Diagnostics.AddError("Error Setting LLM Group Models",
				fmt.Sprintf("LLM group %d was created, but its models could not be set: %s", id, err))
		}
	}
	if len(tags) > 0 && !resp.Diagnostics.HasError() {
		if err := r.client.SetLLMGroupTags(ctx, id, &entities.SetLLMTagsRequest{Tags: tags}); err != nil {
			resp.Diagnostics.AddError("Error Setting LLM Group Tags",
				fmt.Sprintf("LLM group %d was created, but its tags could not be set: %s", id, err))
		}
	}

	r.refresh(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := entities.LLMGroupID(state.ID.ValueInt64())
	group, err := r.client.GetLLMGroup(ctx, id)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading LLM Group", fmt.Sprintf("Could not read LLM group %d: %s", id, err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, groupStateFrom(id, group))...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := entities.LLMGroupID(state.ID.ValueInt64())

	if !plan.ModelIDs.Equal(state.ModelIDs) {
		modelIDs := setModelIDs(ctx, plan.ModelIDs, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		if _, err := r.client.SetLLMGroupModelsAndWait(ctx, id, &entities.SetLLMGroupModelsRequest{ModelIDs: modelIDs}); err != nil {
			resp.Diagnostics.AddError("Error Setting LLM Group Models",
				fmt.Sprintf("Could not set the models of LLM group %d: %s", id, err))
		}
	}

	if !plan.Tags.Equal(state.Tags) && !resp.Diagnostics.HasError() {
		tags := setStrings(ctx, plan.Tags, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		if err := r.client.SetLLMGroupTags(ctx, id, &entities.SetLLMTagsRequest{Tags: tags}); err != nil {
			resp.Diagnostics.AddError("Error Setting LLM Group Tags",
				fmt.Sprintf("Could not set the tags of LLM group %d: %s", id, err))
		}
	}

	r.refresh(ctx, id, &resp.State, &resp.Diagnostics)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := entities.LLMGroupID(state.ID.ValueInt64())
	group, err := r.client.GetLLMGroup(ctx, id)
	if err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Reading LLM Group", fmt.Sprintf("Could not read LLM group %d: %s", id, err))
		return
	}
	if group.State != nil && *group.State != entities.LLMGroupStateActive {
		tflog.Info(ctx, "LLM group is already disabled, treating as destroyed", map[string]any{"id": int(id)})
		return
	}

	if _, err := r.client.DisableLLMGroupAndWait(ctx, id); err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Disabling LLM Group", fmt.Sprintf("Could not disable LLM group %d: %s", id, err))
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || id <= 0 {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("Expected a numeric LLM group ID, got: %s", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

// refresh writes the group as the API reports it. When the read fails the
// group is still recorded by its ID, with the error added.
func (r *groupResource) refresh(ctx context.Context, id entities.LLMGroupID, target stateSetter, diags *diag.Diagnostics) {
	group, err := r.client.GetLLMGroup(ctx, id)
	if err != nil {
		diags.AddError("Error Reading LLM Group", fmt.Sprintf("Could not read LLM group %d: %s", id, err))
		diags.Append(target.SetAttribute(ctx, path.Root("id"), int64(id))...)
		return
	}
	diags.Append(target.Set(ctx, groupStateFrom(id, group))...)
}

func groupStateFrom(id entities.LLMGroupID, group *entities.LLMGroupDetails) *groupModel {
	modelIDs := make([]entities.LLMModelID, len(group.Models))
	for i, m := range group.Models {
		modelIDs[i] = m.ModelID
	}
	state := types.StringNull()
	if group.State != nil {
		state = types.StringValue(string(*group.State))
	}
	return &groupModel{
		ID:       types.Int64Value(int64(id)),
		Name:     types.StringValue(group.Name),
		ModelIDs: modelIDSet(modelIDs),
		Tags:     stringSet(group.Tags),
		State:    state,
	}
}
