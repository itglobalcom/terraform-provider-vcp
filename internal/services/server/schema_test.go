package vstack_server

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestServerResourceSchema_TagsIsSet guards against a regression: `tags` must
// be a Set (unordered). It used to be a List, and since the API returns tags
// in a different order, apply failed with "Provider produced inconsistent
// result after apply".
func TestServerResourceSchema_TagsIsSet(t *testing.T) {
	var resp resource.SchemaResponse
	NewServerResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	attr, ok := resp.Schema.Attributes["tags"]
	if !ok {
		t.Fatal(`server resource schema has no "tags" attribute`)
	}
	if _, ok := attr.(schema.SetAttribute); !ok {
		t.Fatalf(`server "tags" must be schema.SetAttribute (order-insensitive), got %T`, attr)
	}
}

// TestServerResourceSchema_UnorderedCollectionsAreSets guards ssh_key_ids and
// applications_ids: both are order-insensitive to the API and carry
// RequiresReplace, so as Lists a pure reorder in config would plan a
// destructive server replacement.
func TestServerResourceSchema_UnorderedCollectionsAreSets(t *testing.T) {
	var resp resource.SchemaResponse
	NewServerResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	for _, name := range []string{"ssh_key_ids", "applications_ids"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Fatalf("server resource schema has no %q attribute", name)
		}
		if _, ok := attr.(schema.SetAttribute); !ok {
			t.Errorf("server %q must be schema.SetAttribute (order-insensitive), got %T", name, attr)
		}
	}
}

// TestServerResourceSchema_Applications pins the applications block and its
// outcome: the attr.Type maps must match the nested schema (a mismatch fails
// only at apply), parameter values are secrets, and the outcome carries no
// application sign-in.
func TestServerResourceSchema_Applications(t *testing.T) {
	ctx := context.Background()
	s := serverSchema(t)

	apps, ok := s.Attributes["applications"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf(`"applications" must be schema.ListNestedAttribute, got %T`, s.Attributes["applications"])
	}
	if !apps.Optional || apps.Computed {
		t.Errorf("applications: Optional=%v Computed=%v, want an optional, non-computed block", apps.Optional, apps.Computed)
	}
	if got := apps.NestedObject.Type().(types.ObjectType).AttrTypes; !reflect.DeepEqual(got, applicationAttrTypes) {
		t.Errorf("applications schema types %v differ from applicationAttrTypes %v", got, applicationAttrTypes)
	}
	params := apps.NestedObject.Attributes["parameters"].(schema.MapAttribute)
	if !params.Sensitive {
		t.Error("applications[].parameters must be Sensitive: it carries application secrets")
	}
	issue := apps.NestedObject.Attributes["issue_llm_key"].(schema.BoolAttribute)
	if issue.Default == nil {
		t.Fatal("applications[].issue_llm_key has no default")
	}
	var defResp defaults.BoolResponse
	issue.Default.DefaultBool(ctx, defaults.BoolRequest{}, &defResp)
	if !defResp.PlanValue.Equal(types.BoolValue(false)) {
		t.Errorf("applications[].issue_llm_key defaults to %s, want false", defResp.PlanValue)
	}
	if !strings.Contains(apps.MarkdownDescription, "applications_ids") {
		t.Error("applications description must name the conflict with applications_ids")
	}
	if ids := s.Attributes["applications_ids"].(schema.SetAttribute); !strings.Contains(ids.MarkdownDescription, "`applications`") {
		t.Error("applications_ids description must name the conflict with applications")
	}

	inst, ok := s.Attributes["application_installations"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf(`"application_installations" must be schema.ListNestedAttribute, got %T`, s.Attributes["application_installations"])
	}
	if !inst.Computed || inst.Optional || inst.Required {
		t.Errorf("application_installations must be computed only")
	}
	if got := inst.NestedObject.Type().(types.ObjectType).AttrTypes; !reflect.DeepEqual(got, applicationInstallationAttrTypes) {
		t.Errorf("application_installations schema types %v differ from applicationInstallationAttrTypes %v", got, applicationInstallationAttrTypes)
	}
	for name := range inst.NestedObject.Attributes {
		if name != "id" && name != "state" && name != "outcome_reason" && name != "llm_key_id" {
			t.Errorf("application_installations carries %q: the application sign-in is not kept in state", name)
		}
	}
}

// TestServerResourceSchema_OrderArgumentsOptionalComputed pins the order
// arguments as Optional + Computed (a server that is not ordered from an image
// gets them from the platform), while name stays Required. Each must keep its
// state value when the configuration leaves it out: a plan left unknown would
// show a perpetual diff, and on location_id and image_id RequiresReplace would
// then replace the server on every plan. A changed location or image still
// replaces it.
func TestServerResourceSchema_OrderArgumentsOptionalComputed(t *testing.T) {
	ctx := context.Background()
	s := serverSchema(t)

	if name := s.Attributes["name"]; !name.IsRequired() {
		t.Error("name must stay Required")
	}
	for _, arg := range []string{"location_id", "image_id", "cpu", "ram_mb", "volumes"} {
		a := s.Attributes[arg]
		if !a.IsOptional() || !a.IsComputed() || a.IsRequired() {
			t.Errorf("%s: Optional=%v Computed=%v Required=%v, want Optional + Computed",
				arg, a.IsOptional(), a.IsComputed(), a.IsRequired())
		}
	}

	model := orderModel()
	whole := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := whole.Set(ctx, &model); diags.HasError() {
		t.Fatalf("building the state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: s, Raw: whole.Raw}

	runString := func(arg string, config, planned, state types.String) (types.String, bool) {
		mods := s.Attributes[arg].(schema.StringAttribute).PlanModifiers
		value, replace := planned, false
		for _, m := range mods {
			resp := planmodifier.StringResponse{PlanValue: value}
			m.PlanModifyString(ctx, planmodifier.StringRequest{
				Path: path.Root(arg), Config: tfsdk.Config{Schema: s, Raw: whole.Raw}, Plan: plan, State: whole,
				ConfigValue: config, PlanValue: value, StateValue: state,
			}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("%s: %v", arg, resp.Diagnostics)
			}
			value, replace = resp.PlanValue, replace || resp.RequiresReplace
		}
		return value, replace
	}
	for _, arg := range []string{"location_id", "image_id"} {
		got, replace := runString(arg, types.StringNull(), types.StringUnknown(), types.StringValue("kept"))
		if !got.Equal(types.StringValue("kept")) || replace {
			t.Errorf("%s left out of the configuration: plan %s, replace %v; want the state value, no replacement", arg, got, replace)
		}
		_, replace = runString(arg, types.StringValue("new"), types.StringValue("new"), types.StringValue("old"))
		if !replace {
			t.Errorf("%s changed in the configuration must replace the server", arg)
		}
	}

	for _, arg := range []string{"cpu", "ram_mb"} {
		value := types.Int64Unknown()
		for _, m := range s.Attributes[arg].(schema.Int64Attribute).PlanModifiers {
			resp := planmodifier.Int64Response{PlanValue: value}
			m.PlanModifyInt64(ctx, planmodifier.Int64Request{
				Path: path.Root(arg), Config: tfsdk.Config{Schema: s, Raw: whole.Raw}, Plan: plan, State: whole,
				ConfigValue: types.Int64Null(), PlanValue: value, StateValue: types.Int64Value(4),
			}, &resp)
			value = resp.PlanValue
		}
		if !value.Equal(types.Int64Value(4)) {
			t.Errorf("%s left out of the configuration: plan %s, want the state value 4", arg, value)
		}
	}

	var stateVolumes types.List
	if diags := whole.GetAttribute(ctx, path.Root("volumes"), &stateVolumes); diags.HasError() {
		t.Fatalf("reading volumes: %v", diags)
	}
	volumes := s.Attributes["volumes"].(schema.ListNestedAttribute)
	value := types.ListUnknown(stateVolumes.ElementType(ctx))
	for _, m := range volumes.PlanModifiers {
		resp := planmodifier.ListResponse{PlanValue: value}
		m.PlanModifyList(ctx, planmodifier.ListRequest{
			Path: path.Root("volumes"), Config: tfsdk.Config{Schema: s, Raw: whole.Raw}, Plan: plan, State: whole,
			ConfigValue: types.ListNull(stateVolumes.ElementType(ctx)), PlanValue: value, StateValue: stateVolumes,
		}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("volumes: %v", resp.Diagnostics)
		}
		value = resp.PlanValue
	}
	if !value.Equal(stateVolumes) {
		t.Errorf("volumes left out of the configuration: plan %s, want the state value %s", value, stateVolumes)
	}
}

// TestServerResourceSchema_CloneFromServerID pins clone_from_server_id as a
// create-only argument: Optional and not Computed (the API never reports the
// source, so a Computed value would be made up), and changing or removing it
// replaces the server — the clone cannot be re-made from another source in
// place. Keeping the value plans no replacement.
func TestServerResourceSchema_CloneFromServerID(t *testing.T) {
	ctx := context.Background()
	s := serverSchema(t)

	a, ok := s.Attributes["clone_from_server_id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf(`"clone_from_server_id" must be schema.StringAttribute, got %T`, s.Attributes["clone_from_server_id"])
	}
	if !a.Optional || a.Computed || a.Required {
		t.Errorf("clone_from_server_id: Optional=%v Computed=%v Required=%v, want Optional only",
			a.Optional, a.Computed, a.Required)
	}

	model := orderModel()
	model.CloneFromServerID = types.StringValue("src-1")
	whole := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := whole.Set(ctx, &model); diags.HasError() {
		t.Fatalf("building the state: %v", diags)
	}

	replaces := func(config, state types.String) bool {
		replace := false
		for _, m := range a.PlanModifiers {
			resp := planmodifier.StringResponse{PlanValue: config}
			m.PlanModifyString(ctx, planmodifier.StringRequest{
				Path: path.Root("clone_from_server_id"), Config: tfsdk.Config{Schema: s, Raw: whole.Raw},
				Plan: tfsdk.Plan{Schema: s, Raw: whole.Raw}, State: whole,
				ConfigValue: config, PlanValue: config, StateValue: state,
			}, &resp)
			replace = replace || resp.RequiresReplace
		}
		return replace
	}
	if !replaces(types.StringValue("src-2"), types.StringValue("src-1")) {
		t.Error("a changed clone_from_server_id must replace the server")
	}
	if !replaces(types.StringNull(), types.StringValue("src-1")) {
		t.Error("a removed clone_from_server_id must replace the server")
	}
	if replaces(types.StringValue("src-1"), types.StringValue("src-1")) {
		t.Error("an unchanged clone_from_server_id must not replace the server")
	}
}
