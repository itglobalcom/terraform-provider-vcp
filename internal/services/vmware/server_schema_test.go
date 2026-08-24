package vmware

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The declaration of `nested_hypervisor`, asserted attribute by attribute: an
// attribute that is Computed but not Optional cannot be asked for, one without
// UseStateForUnknown plans as "known after apply" on every run, and one that
// requires replacement destroys the machine to change an in-place setting.

// dataSourceSchema returns a data source's schema, validated the way the
// framework validates it.
func dataSourceSchema(t *testing.T, name string, ctor func() datasource.DataSource) dsschema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	ctor().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("%s schema is invalid: %v", name, diags)
	}
	return resp.Schema
}

// dsNestedAttrs returns the attributes of one element of a computed list.
func dsNestedAttrs(t *testing.T, s dsschema.Schema, owner, name string) map[string]dsschema.Attribute {
	t.Helper()
	list, ok := s.Attributes[name].(dsschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("%s: %q must be a list of nested objects, got %T", owner, name, s.Attributes[name])
	}
	return list.NestedObject.Attributes
}

// requireComputedBool asserts a data source declares the flag as a Computed bool.
func requireComputedBool(t *testing.T, attrs map[string]dsschema.Attribute, owner, name string) {
	t.Helper()
	attr, ok := attrs[name]
	if !ok {
		t.Errorf("%s does not declare %q", owner, name)
		return
	}
	if _, ok := attr.(dsschema.BoolAttribute); !ok {
		t.Errorf("%s: %q must be a bool, got %T", owner, name, attr)
	}
	if !attr.IsComputed() {
		t.Errorf("%s: %q must be Computed — a data source only reports", owner, name)
	}
}

func TestServerNestedHypervisorAttribute(t *testing.T) {
	s := resourceSchema(t, &serverResource{})

	attr, ok := s.Attributes["nested_hypervisor"].(schema.BoolAttribute)
	if !ok {
		t.Fatalf(`vcp_vmware_server: "nested_hypervisor" must be a schema.BoolAttribute, got %T`,
			s.Attributes["nested_hypervisor"])
	}

	if !attr.IsOptional() {
		t.Error(`"nested_hypervisor" must be Optional — it is a setting a user asks for`)
	}
	if !attr.IsComputed() {
		t.Error(`"nested_hypervisor" must be Computed — the platform reports the value it settled on`)
	}
	if attr.MarkdownDescription == "" {
		t.Error(`"nested_hypervisor" must carry a MarkdownDescription — docs/ is generated from it`)
	}

	t.Run("a known state is kept instead of planning unknown", func(t *testing.T) {
		// The machine has the setting on and the configuration says nothing about
		// it, so the framework hands the modifier an unknown planned value;
		// UseStateForUnknown must keep the known state instead.
		unset := nestedHypervisorModel(true)
		unset.NestedHypervisor = types.BoolNull()
		unknown := nestedHypervisorModel(true)
		unknown.NestedHypervisor = types.BoolUnknown()

		req := planmodifier.BoolRequest{
			Path:        path.Root("nested_hypervisor"),
			Config:      configOf(t, s, unset),
			ConfigValue: types.BoolNull(),
			State:       stateOf(t, s, nestedHypervisorModel(true)),
			StateValue:  types.BoolValue(true),
			Plan:        planOf(t, s, unknown),
			PlanValue:   types.BoolUnknown(),
		}
		resp := &planmodifier.BoolResponse{PlanValue: req.PlanValue}
		for _, modifier := range attr.PlanModifiers {
			modifier.PlanModifyBool(context.Background(), req, resp)
		}

		if resp.PlanValue.IsUnknown() {
			t.Fatal("an unset attribute planned as unknown beside a known state — " +
				"boolplanmodifier.UseStateForUnknown() is missing, and every plan will show a diff")
		}
		if !resp.PlanValue.ValueBool() {
			t.Errorf("the planned value is %v, want the value already in state", resp.PlanValue)
		}
	})

	t.Run("switching it is an edit, not a replacement", func(t *testing.T) {
		// RequiresReplace here would destroy the server to change an in-place setting.
		off, on := nestedHypervisorModel(false), nestedHypervisorModel(true)
		req := planmodifier.BoolRequest{
			Path:        path.Root("nested_hypervisor"),
			Config:      configOf(t, s, on),
			ConfigValue: types.BoolValue(true),
			Plan:        planOf(t, s, on),
			PlanValue:   types.BoolValue(true),
			State:       stateOf(t, s, off),
			StateValue:  types.BoolValue(false),
		}
		resp := &planmodifier.BoolResponse{PlanValue: req.PlanValue}
		for _, modifier := range attr.PlanModifiers {
			modifier.PlanModifyBool(context.Background(), req, resp)
		}

		if resp.RequiresReplace {
			t.Error("switching nested_hypervisor asked for a replacement; the platform edits it in place")
		}
	})
}

// nestedHypervisorModel is a server whose only interesting attribute is the one
// under test. The nulls carry their element types: the zero value of a
// Set/Object/List has none, and the framework refuses it.
func nestedHypervisorModel(enabled bool) serverModel {
	return serverModel{
		ID:               types.Int64Value(5678),
		LocationID:       types.Int64Value(5),
		Name:             types.StringValue("web-01"),
		ImageID:          types.Int64Value(42),
		CPU:              types.Int64Value(1),
		RamMB:            types.Int64Value(1024),
		SystemDiskMB:     types.Int64Value(51200),
		NestedHypervisor: types.BoolValue(enabled),
		SSHKeyIDs:        types.SetNull(types.Int64Type),
		Gpu:              types.ObjectNull(gpuAttrTypes),
		Nics:             types.ListNull(types.ObjectType{AttrTypes: nicAttrTypes}),
	}
}

// Both server data sources report the setting — the by-id one and the list,
// two schemas built from one attribute map.
func TestServerDataSourcesReportNestedHypervisor(t *testing.T) {
	byID := dataSourceSchema(t, "vcp_vmware_server", NewServerDataSource)
	requireComputedBool(t, byID.Attributes, "data.vcp_vmware_server", "nested_hypervisor")

	list := dataSourceSchema(t, "vcp_vmware_servers", NewServersDataSource)
	requireComputedBool(t, dsNestedAttrs(t, list, "data.vcp_vmware_servers", "servers"),
		"data.vcp_vmware_servers.servers[*]", "nested_hypervisor")
}

// The capability has to be discoverable before a machine is ordered; the
// catalog is the only place to find it out without provoking the refusal.
func TestLocationsReportNestedHypervisorSupport(t *testing.T) {
	locations := dataSourceSchema(t, "vcp_vmware_locations", NewLocationsDataSource)
	attrs := dsNestedAttrs(t, locations, "data.vcp_vmware_locations", "locations")

	requireComputedBool(t, attrs, "data.vcp_vmware_locations.locations[*]", "nested_hypervisor_supported")
	// gpu_supported is the flag it sits beside, read the same way.
	requireComputedBool(t, attrs, "data.vcp_vmware_locations.locations[*]", "gpu_supported")
}
