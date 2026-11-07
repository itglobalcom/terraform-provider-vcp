package llm

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                = &connectionResource{}
	_ resource.ResourceWithConfigure   = &connectionResource{}
	_ resource.ResourceWithImportState = &connectionResource{}
	_ resource.ResourceWithModifyPlan  = &connectionResource{}
)

// NewConnectionResource returns the vcp_llm_connection resource.
func NewConnectionResource() resource.Resource {
	return &connectionResource{}
}

type connectionResource struct {
	client *sdk.CloudClient
}

type connectionModel struct {
	ID           types.String `tfsdk:"id"`
	GroupName    types.String `tfsdk:"group_name"`
	FirstKeyName types.String `tfsdk:"first_key_name"`
	State        types.String `tfsdk:"state"`
}

func (r *connectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_llm_connection"
}

func (r *connectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	nameValidators := []validator.String{stringvalidator.LengthBetween(1, entities.LLMNameMaxLength)}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Connects the LLM service to the project. A project has one connection: connecting " +
			"creates the first group (`group_name`) with its first access key (`first_key_name`), checks the " +
			"funds and issues the service license. On a project that is already connected the resource takes " +
			"the existing connection into the state without calling the API, and `group_name` and " +
			"`first_key_name` are not applied.\n\n" +
			"~> The service cannot be disconnected: `terraform destroy` only removes the connection from the " +
			"state, the project stays connected. The first group and key are not managed by this resource — " +
			"import them as `vcp_llm_group` and `vcp_llm_access_key` to manage them.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "ID of the project the connection belongs to.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"group_name": schema.StringAttribute{
				MarkdownDescription: "Name of the first group, created by the connection. Applied only when " +
					"the project is connected: changing it afterwards is refused at plan time.",
				Required:   true,
				Validators: nameValidators,
			},
			"first_key_name": schema.StringAttribute{
				MarkdownDescription: "Name of the first access key of the first group, issued by the connection. " +
					"Applied only when the project is connected: changing it afterwards is refused at plan time.",
				Required:   true,
				Validators: nameValidators,
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Connection state: `connecting` or `connected`. New states may be added over time.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *connectionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if client := clientFrom(req.ProviderData, &resp.Diagnostics); client != nil {
		r.client = client
	}
}

// ModifyPlan refuses a change of the names once the project is connected: the
// API applies them only while connecting, and a replacement would need a
// disconnection the contract does not have. A name the state does not hold
// (an imported connection the API reported without it) is taken from the
// configuration.
func (r *connectionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var plan, state connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for _, attr := range []struct {
		name        string
		plan, state types.String
	}{
		{"group_name", plan.GroupName, state.GroupName},
		{"first_key_name", plan.FirstKeyName, state.FirstKeyName},
	} {
		if attr.state.IsNull() || attr.plan.IsUnknown() || attr.plan.Equal(attr.state) {
			continue
		}
		resp.Diagnostics.AddAttributeError(path.Root(attr.name), "Connection Name Cannot Be Changed",
			fmt.Sprintf("%s is applied only when the LLM service is connected, and the project is connected "+
				"with %s. The service cannot be reconnected. Set %s back to %s; to rename the group or the key, "+
				"manage them as vcp_llm_group and vcp_llm_access_key.",
				attr.name, attr.state, attr.name, attr.state))
	}
}

func (r *connectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	project, err := r.client.GetProject(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Project", fmt.Sprintf("Could not read the project: %s", err))
		return
	}

	connection, err := r.client.GetLLMConnection(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading LLM Connection",
			fmt.Sprintf("Could not read the LLM connection of the project: %s", err))
		return
	}

	switch connection.State {
	case entities.LLMConnectionStateConnecting:
		task := ""
		if connection.TaskID != nil {
			task = *connection.TaskID
		}
		resp.Diagnostics.AddError("LLM Connection In Progress",
			fmt.Sprintf("The project is being connected to the LLM service by another request (task %q). "+
				"Run the apply again once it completes: the connection is then taken into the state.", task))
		return
	case entities.LLMConnectionStateConnected:
		resp.Diagnostics.AddWarning("Existing LLM Connection Taken Into State",
			"The project is already connected to the LLM service, so no connection was made: "+
				"group_name and first_key_name were not applied.")
	default:
		connection, err = r.client.ConnectLLMAndWait(ctx, &entities.ConnectLLMRequest{
			GroupName:    plan.GroupName.ValueString(),
			FirstKeyName: plan.FirstKeyName.ValueString(),
		})
		if err != nil {
			resp.Diagnostics.AddError("Error Connecting LLM Service",
				fmt.Sprintf("Could not connect the LLM service: %s", err))
			return
		}
	}

	plan.ID = types.StringValue(strconv.Itoa(project.ID))
	plan.State = types.StringValue(string(connection.State))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state connectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	connection, err := r.client.GetLLMConnection(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading LLM Connection",
			fmt.Sprintf("Could not read the LLM connection of the project: %s", err))
		return
	}

	if connection.State == entities.LLMConnectionStateNotConnected {
		resp.State.RemoveResource(ctx)
		return
	}

	// The names are inputs of the connection, not a description of the
	// groups: once known, they stay as written.
	if state.GroupName.IsNull() {
		state.GroupName = optString(connection.GroupName)
	}
	if state.FirstKeyName.IsNull() {
		state.FirstKeyName = optString(connection.FirstKeyName)
	}
	state.State = types.StringValue(string(connection.State))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only takes the names into a state that lacked them; ModifyPlan
// refuses every other change.
func (r *connectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = state.ID
	plan.State = state.State
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning("LLM Service Stays Connected",
		"The LLM service cannot be disconnected: the connection was removed from the Terraform state only. "+
			"Its groups and access keys keep working and keep being billed.")
}

// ImportState takes the project ID — the one the API key belongs to.
func (r *connectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, err := r.client.GetProject(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Project", fmt.Sprintf("Could not read the project: %s", err))
		return
	}

	id := strconv.Itoa(project.ID)
	if req.ID != id {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected the ID of the project the provider key belongs to, %s, got: %s.", id, req.ID))
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
