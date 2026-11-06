package vstack_server

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var applicationAttrTypes = map[string]attr.Type{
	"id":            types.StringType,
	"parameters":    types.MapType{ElemType: types.StringType},
	"issue_llm_key": types.BoolType,
}

var applicationInstallationAttrTypes = map[string]attr.Type{
	"id":             types.StringType,
	"state":          types.StringType,
	"outcome_reason": types.StringType,
	"llm_key_id":     types.StringType,
}

// ApplicationModel is one entry of the applications block of a server order.
type ApplicationModel struct {
	ID          types.String `tfsdk:"id"`
	Parameters  types.Map    `tfsdk:"parameters"`
	IssueLLMKey types.Bool   `tfsdk:"issue_llm_key"`
}

// ApplicationInstallationModel is the outcome of installing one ordered application.
type ApplicationInstallationModel struct {
	ID            types.String `tfsdk:"id"`
	State         types.String `tfsdk:"state"`
	OutcomeReason types.String `tfsdk:"outcome_reason"`
	LLMKeyID      types.String `tfsdk:"llm_key_id"`
}

func applicationsSchemaAttribute() schema.Attribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: "Applications from the `vcp_applications` catalog to install on the server, each with " +
			"the values of its parameters. Only applied during creation; reordering the entries changes nothing.\n\n" +
			"~> Changing this forces a new resource. Conflicts with `applications_ids`: an order with " +
			"`applications` does not apply `applications_ids`, so set one or the other.",
		Optional: true,
		PlanModifiers: []planmodifier.List{
			listplanmodifier.RequiresReplaceIf(
				applicationsChangedBeyondOrder,
				"Changing applications forces a new resource; reordering them does not.",
				"Changing `applications` forces a new resource; reordering them does not.",
			),
		},
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{
					MarkdownDescription: "Application ID from the `vcp_applications` data source. An application is " +
						"listed once per server.",
					Required: true,
				},
				"parameters": schema.MapAttribute{
					MarkdownDescription: "Parameter values by parameter name, as the catalog entry names them in " +
						"`parameters`. Every `required` parameter without a `default` must be set; a name the " +
						"application does not declare is refused.",
					Optional:    true,
					Sensitive:   true,
					ElementType: types.StringType,
				},
				"issue_llm_key": schema.BoolAttribute{
					MarkdownDescription: "Issue a platform language model key for the application and pass it, " +
						"with the inference address, into the parameters the catalog marks with `llm_kind`. " +
						"Accepted only for an application whose catalog entry has `llm_key_enabled`. Defaults to `false`.",
					Optional: true,
					Computed: true,
					Default:  booldefault.StaticBool(false),
				},
			},
		},
	}
}

func applicationInstallationsSchemaAttribute() schema.Attribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: "Outcome of installing every application ordered with the server. A failed " +
			"installation is an outcome of a created server, not a failure of the apply. The application " +
			"sign-in is not kept here.",
		Computed: true,
		PlanModifiers: []planmodifier.List{
			listplanmodifier.UseStateForUnknown(),
		},
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{
					MarkdownDescription: "Application ID.",
					Computed:            true,
				},
				"state": schema.StringAttribute{
					MarkdownDescription: "Installation state: `Installing`, `Installed` or `Failed`.",
					Computed:            true,
				},
				"outcome_reason": schema.StringAttribute{
					MarkdownDescription: "Why the installation did not succeed, for example `DeployTimeout`, " +
						"`HostUnreachable` or `LlmKeyValueMissing`; empty for a successful one.",
					Computed: true,
				},
				"llm_key_id": schema.StringAttribute{
					MarkdownDescription: "ID of the language model key issued for the application; empty when " +
						"none was issued.",
					Computed: true,
				},
			},
		},
	}
}

// applicationsChangedBeyondOrder asks for replacement unless the planned
// entries are the prior ones in another order: installation happens once, at
// creation, so the order of the entries means nothing afterwards.
func applicationsChangedBeyondOrder(ctx context.Context, req planmodifier.ListRequest, resp *listplanmodifier.RequiresReplaceIfFuncResponse) {
	resp.RequiresReplace = !isPermutation(req.StateValue, req.PlanValue)
}

func isPermutation(a, b types.List) bool {
	if a.IsNull() || b.IsNull() || a.IsUnknown() || b.IsUnknown() {
		return false
	}
	left, right := a.Elements(), b.Elements()
	if len(left) != len(right) {
		return false
	}
	used := make([]bool, len(right))
	for _, l := range left {
		found := false
		for j, r := range right {
			if !used[j] && l.Equal(r) {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// validateApplications refuses what the order alone decides: applications
// together with applications_ids, and an application listed twice.
func validateApplications(ctx context.Context, data ServerResourceModel, diags *diag.Diagnostics) {
	if data.Applications.IsNull() || data.Applications.IsUnknown() {
		return
	}

	if len(data.Applications.Elements()) > 0 && len(data.ApplicationsIDs) > 0 {
		diags.AddAttributeError(path.Root("applications_ids"), "Conflicting Attributes",
			"applications and applications_ids both order applications for the server. An order with "+
				"applications does not apply applications_ids, so the IDs here would be ignored. "+
				"Move them into applications and drop applications_ids.")
	}

	var apps []ApplicationModel
	if d := data.Applications.ElementsAs(ctx, &apps, true); d.HasError() {
		return
	}
	seen := make(map[string]bool, len(apps))
	for i, app := range apps {
		if app.ID.IsNull() || app.ID.IsUnknown() {
			continue
		}
		id := app.ID.ValueString()
		if seen[id] {
			diags.AddAttributeError(path.Root("applications").AtListIndex(i).AtName("id"), "Duplicate Application",
				fmt.Sprintf("Application %q is listed more than once. A server installs an application once; "+
					"keep a single entry with all its parameters.", id))
		}
		seen[id] = true
	}
}

// buildApplicationSpecs converts the applications block into the order entries.
func buildApplicationSpecs(ctx context.Context, apps types.List) ([]entities.ApplicationSpec, diag.Diagnostics) {
	if apps.IsNull() || apps.IsUnknown() || len(apps.Elements()) == 0 {
		return nil, nil
	}

	var models []ApplicationModel
	diags := apps.ElementsAs(ctx, &models, false)
	if diags.HasError() {
		return nil, diags
	}

	specs := make([]entities.ApplicationSpec, 0, len(models))
	for _, m := range models {
		spec := entities.ApplicationSpec{
			ID:          m.ID.ValueString(),
			IssueLLMKey: m.IssueLLMKey.ValueBool(),
		}
		if !m.Parameters.IsNull() && !m.Parameters.IsUnknown() && len(m.Parameters.Elements()) > 0 {
			params := make(map[string]string, len(m.Parameters.Elements()))
			diags.Append(m.Parameters.ElementsAs(ctx, &params, false)...)
			if diags.HasError() {
				return nil, diags
			}
			spec.Parameters = params
		}
		specs = append(specs, spec)
	}
	return specs, diags
}

// mapApplicationInstallations sets application_installations from the server.
// A response without the field keeps the known value the model already holds;
// an unknown never survives into state, and a null is given its element type.
func mapApplicationInstallations(installations []entities.ApplicationInstallation, model *ServerResourceModel) {
	objType := types.ObjectType{AttrTypes: applicationInstallationAttrTypes}

	if installations == nil {
		if model.ApplicationInstallations.IsUnknown() || model.ApplicationInstallations.IsNull() {
			model.ApplicationInstallations = types.ListNull(objType)
		}
		return
	}

	values := make([]attr.Value, len(installations))
	for i, inst := range installations {
		values[i] = types.ObjectValueMust(applicationInstallationAttrTypes, map[string]attr.Value{
			"id":             types.StringValue(inst.ID),
			"state":          types.StringValue(string(inst.State)),
			"outcome_reason": optionalString(string(inst.OutcomeReason)),
			"llm_key_id":     optionalString(inst.LLMKeyID),
		})
	}
	model.ApplicationInstallations = types.ListValueMust(objType, values)
}

func optionalString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func applicationsNull() types.List {
	return types.ListNull(types.ObjectType{AttrTypes: applicationAttrTypes})
}
