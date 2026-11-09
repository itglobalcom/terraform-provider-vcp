package vstack_server

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var applicationObjectType = types.ObjectType{AttrTypes: applicationAttrTypes}

// application builds one entry of the applications block; a nil params leaves
// parameters null.
func application(t *testing.T, id string, params map[string]string, issueKey bool) attr.Value {
	t.Helper()
	parameters := types.MapNull(types.StringType)
	if params != nil {
		elems := make(map[string]attr.Value, len(params))
		for k, v := range params {
			elems[k] = types.StringValue(v)
		}
		parameters = types.MapValueMust(types.StringType, elems)
	}
	return types.ObjectValueMust(applicationAttrTypes, map[string]attr.Value{
		"id":            types.StringValue(id),
		"parameters":    parameters,
		"issue_llm_key": types.BoolValue(issueKey),
	})
}

func applicationsList(entries ...attr.Value) types.List {
	return types.ListValueMust(applicationObjectType, entries)
}

func serverSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewServerResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("reading the schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// validateServerConfig runs ValidateConfig on a configuration with a boot volume
// and the given application order, so that only the application checks can fail.
func validateServerConfig(t *testing.T, apps types.List, appIDs []string) resource.ValidateConfigResponse {
	t.Helper()
	ctx := context.Background()
	s := serverSchema(t)

	model := ServerResourceModel{
		Tags:                     types.SetNull(types.StringType),
		Applications:             apps,
		ApplicationInstallations: types.ListNull(types.ObjectType{AttrTypes: applicationInstallationAttrTypes}),
		Volumes: []VolumeAttrModel{{
			Number: types.Int64Value(0),
			Name:   types.StringValue("boot"),
			SizeMB: types.Int64Value(20480),
		}},
	}
	for _, id := range appIDs {
		model.ApplicationsIDs = append(model.ApplicationsIDs, types.StringValue(id))
	}

	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("building the configuration: %v", diags)
	}

	var resp resource.ValidateConfigResponse
	NewServerResource().(resource.ResourceWithValidateConfig).ValidateConfig(ctx,
		resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: state.Raw}}, &resp)
	return resp
}

// TestServerValidateConfigApplications refuses at plan time what the order
// alone decides: without it, applications_ids next to applications would be
// silently dropped by the API, and a repeated id would fail mid-apply.
func TestServerValidateConfigApplications(t *testing.T) {
	cases := map[string]struct {
		apps      types.List
		appIDs    []string
		wantPaths []path.Path
	}{
		"applications alone": {
			apps: applicationsList(application(t, "n8n", map[string]string{"N8N_HOST": "h"}, true), application(t, "redis", nil, false)),
		},
		"applications_ids alone": {
			apps:   applicationsNull(),
			appIDs: []string{"n8n"},
		},
		"empty applications next to applications_ids": {
			apps:   applicationsList(),
			appIDs: []string{"n8n"},
		},
		"both set": {
			apps:      applicationsList(application(t, "n8n", nil, false)),
			appIDs:    []string{"redis"},
			wantPaths: []path.Path{path.Root("applications_ids")},
		},
		"repeated id": {
			apps:      applicationsList(application(t, "n8n", nil, false), application(t, "redis", nil, false), application(t, "n8n", nil, true)),
			wantPaths: []path.Path{path.Root("applications").AtListIndex(2).AtName("id")},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resp := validateServerConfig(t, tc.apps, tc.appIDs)

			var got []path.Path
			for _, d := range resp.Diagnostics.Errors() {
				withPath, ok := d.(interface{ Path() path.Path })
				if !ok {
					t.Fatalf("error without an attribute path: %s: %s", d.Summary(), d.Detail())
				}
				got = append(got, withPath.Path())
			}
			if len(got) != len(tc.wantPaths) {
				t.Fatalf("errors at %v, want at %v: %v", got, tc.wantPaths, resp.Diagnostics)
			}
			for i := range got {
				if !got[i].Equal(tc.wantPaths[i]) {
					t.Errorf("error %d at %s, want at %s", i, got[i], tc.wantPaths[i])
				}
			}
		})
	}
}

// TestBuildApplicationSpecs pins the order body: every entry keeps its id, its
// parameter values and its key flag; an entry without values sends no
// parameters at all rather than an empty object.
func TestBuildApplicationSpecs(t *testing.T) {
	ctx := context.Background()

	t.Run("entries", func(t *testing.T) {
		got, diags := buildApplicationSpecs(ctx, applicationsList(
			application(t, "n8n", map[string]string{"N8N_HOST": "n8n.example.com", "N8N_ENCRYPTION_KEY": "s3cret"}, true),
			application(t, "redis", map[string]string{}, false),
			application(t, "nginx", nil, false),
		))
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		want := []entities.ApplicationSpec{
			{ID: "n8n", Parameters: map[string]string{"N8N_HOST": "n8n.example.com", "N8N_ENCRYPTION_KEY": "s3cret"}, IssueLLMKey: true},
			{ID: "redis"},
			{ID: "nginx"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("specs = %#v, want %#v", got, want)
		}
	})

	for name, apps := range map[string]types.List{
		"null":    applicationsNull(),
		"unknown": types.ListUnknown(applicationObjectType),
		"empty":   applicationsList(),
	} {
		t.Run(name, func(t *testing.T) {
			got, diags := buildApplicationSpecs(ctx, apps)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if got != nil {
				t.Errorf("specs = %#v, want nil: the order must carry no applications", got)
			}
		})
	}
}

func installationsList(t *testing.T, m ServerResourceModel) []map[string]attr.Value {
	t.Helper()
	var out []map[string]attr.Value
	for _, e := range m.ApplicationInstallations.Elements() {
		out = append(out, e.(types.Object).Attributes())
	}
	return out
}

// TestMapApplicationInstallations maps the outcome the API reports; an empty
// reason or key id must come out null, not "", or every refresh would differ
// from what the schema documents as "empty".
func TestMapApplicationInstallations(t *testing.T) {
	objType := types.ObjectType{AttrTypes: applicationInstallationAttrTypes}

	t.Run("outcomes", func(t *testing.T) {
		model := ServerResourceModel{ApplicationInstallations: types.ListUnknown(objType)}
		mapApplicationInstallations([]entities.ApplicationInstallation{
			{ID: "n8n", State: "Installed", LLMKeyID: "key-42"},
			{ID: "redis", State: "Failed", OutcomeReason: entities.ApplicationOutcomeReasonDeployTimeout},
		}, &model)

		got := installationsList(t, model)
		want := []map[string]attr.Value{
			{"id": types.StringValue("n8n"), "state": types.StringValue("Installed"), "outcome_reason": types.StringNull(), "llm_key_id": types.StringValue("key-42")},
			{"id": types.StringValue("redis"), "state": types.StringValue("Failed"), "outcome_reason": types.StringValue("DeployTimeout"), "llm_key_id": types.StringNull()},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d installations, want %d", len(got), len(want))
		}
		for i := range want {
			for k, v := range want[i] {
				if !got[i][k].Equal(v) {
					t.Errorf("installation %d %s = %s, want %s", i, k, got[i][k], v)
				}
			}
		}
	})

	t.Run("empty response is an empty list", func(t *testing.T) {
		model := ServerResourceModel{ApplicationInstallations: types.ListUnknown(objType)}
		mapApplicationInstallations([]entities.ApplicationInstallation{}, &model)
		if model.ApplicationInstallations.IsNull() || model.ApplicationInstallations.IsUnknown() || len(model.ApplicationInstallations.Elements()) != 0 {
			t.Errorf("application_installations = %s, want []", model.ApplicationInstallations)
		}
	})

	t.Run("missing field keeps the known value", func(t *testing.T) {
		known := types.ListValueMust(objType, []attr.Value{types.ObjectValueMust(applicationInstallationAttrTypes, map[string]attr.Value{
			"id": types.StringValue("n8n"), "state": types.StringValue("Installed"),
			"outcome_reason": types.StringNull(), "llm_key_id": types.StringNull(),
		})})
		model := ServerResourceModel{ApplicationInstallations: known}
		mapApplicationInstallations(nil, &model)
		if !model.ApplicationInstallations.Equal(known) {
			t.Errorf("application_installations = %s, want the prior %s", model.ApplicationInstallations, known)
		}
	})

	t.Run("missing field never leaves unknown in state", func(t *testing.T) {
		model := ServerResourceModel{ApplicationInstallations: types.ListUnknown(objType)}
		mapApplicationInstallations(nil, &model)
		if !model.ApplicationInstallations.Equal(types.ListNull(objType)) {
			t.Errorf("application_installations = %s, want a typed null", model.ApplicationInstallations)
		}
	})
}

// TestApplicationsReplacement guards the plan of the applications block:
// installation happens once, so a reorder must not replace the server, while
// any real change — and setting the block on an imported server — must.
func TestApplicationsReplacement(t *testing.T) {
	n8n := application(t, "n8n", map[string]string{"N8N_HOST": "h"}, true)
	redis := application(t, "redis", nil, false)

	cases := map[string]struct {
		state, plan types.List
		replace     bool
	}{
		"same order":        {applicationsList(n8n, redis), applicationsList(n8n, redis), false},
		"reordered":         {applicationsList(n8n, redis), applicationsList(redis, n8n), false},
		"parameter changed": {applicationsList(n8n, redis), applicationsList(application(t, "n8n", map[string]string{"N8N_HOST": "other"}, true), redis), true},
		"key flag changed":  {applicationsList(n8n), applicationsList(application(t, "n8n", map[string]string{"N8N_HOST": "h"}, false)), true},
		"entry added":       {applicationsList(n8n), applicationsList(n8n, redis), true},
		"entry removed":     {applicationsList(n8n, redis), applicationsList(redis), true},
		"same entry twice":  {applicationsList(n8n, redis), applicationsList(n8n, n8n), true},
		"set after import":  {applicationsNull(), applicationsList(n8n), true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := serverSchema(t)
			attrSchema := s.Attributes["applications"].(schema.ListNestedAttribute)
			if len(attrSchema.PlanModifiers) != 1 {
				t.Fatalf("applications has %d plan modifiers, want 1", len(attrSchema.PlanModifiers))
			}
			ctx := context.Background()
			req := planmodifier.ListRequest{
				Path:        path.Root("applications"),
				StateValue:  tc.state,
				PlanValue:   tc.plan,
				ConfigValue: tc.plan,
				// A non-null prior state and plan mark an update, not a create or
				// a destroy, which RequiresReplaceIf skips.
				State: tfsdk.State{Schema: s, Raw: nonNullObject(t, s)},
				Plan:  tfsdk.Plan{Schema: s, Raw: nonNullObject(t, s)},
			}
			resp := &planmodifier.ListResponse{PlanValue: tc.plan}
			attrSchema.PlanModifiers[0].PlanModifyList(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			if resp.RequiresReplace != tc.replace {
				t.Errorf("RequiresReplace = %v, want %v", resp.RequiresReplace, tc.replace)
			}
		})
	}
}

// nonNullObject returns a known resource object with every attribute null.
func nonNullObject(t *testing.T, s schema.Schema) tftypes.Value {
	t.Helper()
	objType := s.Type().TerraformType(context.Background()).(tftypes.Object)
	values := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}
	return tftypes.NewValue(objType, values)
}
