package server_backup

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

func storagesSchema(t *testing.T) datasource.SchemaResponse {
	t.Helper()
	var resp datasource.SchemaResponse
	NewStoragesDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema validation: %v", diags)
	}
	return resp
}

func TestStoragesDataSourceSchema(t *testing.T) {
	attrs := storagesSchema(t).Schema.Attributes

	// The catalog depends on the server's location and tariff; without
	// server_id the data source would have nothing to ask.
	if !attrs["server_id"].IsRequired() {
		t.Error(`"server_id" must be Required`)
	}
	for _, name := range []string{"storages", "limits"} {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("data source schema is missing %q", name)
			continue
		}
		if !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("%q must be Computed only", name)
		}
	}
}

// TestMapStorageCatalog_full checks every field of a full catalog lands in the
// model and that the model is accepted by the schema: a model and a schema that
// disagree fail only at State.Set, which is here and not on a live apply.
func TestMapStorageCatalog_full(t *testing.T) {
	catalog := &entities.BackupStorageCatalog{
		Storages: []entities.BackupStorage{{
			ID:                 7,
			Name:               "S3 Moscow",
			Description:        "Object storage",
			SortOrder:          2,
			PricePerGB:         1.25,
			LocationID:         "msk1",
			RestoreLocationIDs: []string{"msk1", "spb1"},
		}},
		Limits: &entities.BackupLimits{
			ScheduleWindowFromHour: 1,
			ScheduleWindowToHour:   6,
			Daily:                  &entities.BackupRuleLimits{MaxKeep: 14, DefaultKeep: 7},
			Weekly:                 &entities.BackupRuleLimits{MaxKeep: 8, DefaultKeep: 4},
			Monthly:                &entities.BackupRuleLimits{MaxKeep: 12, DefaultKeep: 3},
		},
	}

	m := mapStorageCatalog("l1s2", catalog)

	if got := m.ServerID.ValueString(); got != "l1s2" {
		t.Errorf("server_id = %q, want l1s2", got)
	}
	if len(m.Storages) != 1 {
		t.Fatalf("storages has %d item(s), want 1", len(m.Storages))
	}
	s := m.Storages[0]
	if s.ID.ValueInt64() != 7 || s.Name.ValueString() != "S3 Moscow" ||
		s.Description.ValueString() != "Object storage" || s.SortOrder.ValueInt64() != 2 ||
		s.PricePerGB.ValueFloat64() != 1.25 || s.LocationID.ValueString() != "msk1" {
		t.Errorf("storage mapped wrong: %+v", s)
	}
	if len(s.RestoreLocationIDs) != 2 || s.RestoreLocationIDs[0] != "msk1" || s.RestoreLocationIDs[1] != "spb1" {
		t.Errorf("restore_location_ids = %v, want [msk1 spb1]", s.RestoreLocationIDs)
	}
	if m.Limits == nil {
		t.Fatal("limits is nil")
	}
	if m.Limits.ScheduleWindowFromHour.ValueInt64() != 1 || m.Limits.ScheduleWindowToHour.ValueInt64() != 6 {
		t.Errorf("schedule window = %v..%v, want 1..6",
			m.Limits.ScheduleWindowFromHour, m.Limits.ScheduleWindowToHour)
	}
	if m.Limits.Daily == nil || m.Limits.Daily.MaxKeep.ValueInt64() != 14 || m.Limits.Daily.DefaultKeep.ValueInt64() != 7 {
		t.Errorf("daily limits = %+v, want 14/7", m.Limits.Daily)
	}
	if m.Limits.Weekly == nil || m.Limits.Weekly.MaxKeep.ValueInt64() != 8 {
		t.Errorf("weekly limits = %+v, want max_keep 8", m.Limits.Weekly)
	}
	if m.Limits.Monthly == nil || m.Limits.Monthly.MaxKeep.ValueInt64() != 12 {
		t.Errorf("monthly limits = %+v, want max_keep 12", m.Limits.Monthly)
	}

	setStoragesState(t, m)
}

// TestMapStorageCatalog_sparse is the wire shape of a catalog with nothing
// optional in it: the Public API drops null fields rather than sending them,
// so the limits, the rule limits, the description and the restore locations
// can all be missing.
func TestMapStorageCatalog_sparse(t *testing.T) {
	catalog := &entities.BackupStorageCatalog{
		Storages: []entities.BackupStorage{{ID: 1, Name: "Local", LocationID: "msk1"}},
	}

	m := mapStorageCatalog("l1s2", catalog)

	if m.Limits != nil {
		t.Errorf("limits = %+v, want nil (null in state)", m.Limits)
	}
	s := m.Storages[0]
	if !s.Description.IsNull() {
		t.Errorf("description = %v, want null", s.Description)
	}
	if s.RestoreLocationIDs == nil || len(s.RestoreLocationIDs) != 0 {
		t.Errorf("restore_location_ids = %#v, want an empty non-nil list", s.RestoreLocationIDs)
	}
	setStoragesState(t, m)

	// Limits present, rule limits missing.
	m = mapStorageCatalog("l1s2", &entities.BackupStorageCatalog{
		Limits: &entities.BackupLimits{ScheduleWindowToHour: 23},
	})
	if m.Limits == nil || m.Limits.Daily != nil || m.Limits.Weekly != nil || m.Limits.Monthly != nil {
		t.Errorf("limits = %+v, want the object with null rule limits", m.Limits)
	}
	setStoragesState(t, m)
}

// TestMapStorageCatalog_empty covers a server with no storages at all: the
// list must be empty, not null, so `length(...) == 0` works on it.
func TestMapStorageCatalog_empty(t *testing.T) {
	m := mapStorageCatalog("l1s2", &entities.BackupStorageCatalog{})
	if m.Storages == nil || len(m.Storages) != 0 {
		t.Errorf("storages = %#v, want an empty non-nil list", m.Storages)
	}
	setStoragesState(t, m)
}

// setStoragesState writes the model into a state built from the data source
// schema, failing the test on any disagreement between the two.
func setStoragesState(t *testing.T, m storagesDataSourceModel) {
	t.Helper()
	ctx := context.Background()
	s := storagesSchema(t).Schema
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := state.Set(ctx, &m); diags.HasError() {
		t.Fatalf("State.Set: %v", diags)
	}
}

// ============================================================================
// vcp_server_backup
// ============================================================================

func backupSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema validation: %v", diags)
	}
	return resp.Schema
}

// TestBackupResourceSchema_replaceOnlyOnServer guards the in-place update:
// every change of the schedule is one PUT, so only server_id may carry
// RequiresReplace. A rule that replaced the resource would disable the service
// — and delete the server's copies — on an edit of the schedule.
func TestBackupResourceSchema_replaceOnlyOnServer(t *testing.T) {
	attrs := backupSchema(t).Attributes

	if !hasRequiresReplace(attrs["server_id"]) {
		t.Error(`"server_id" must carry RequiresReplace`)
	}
	for _, name := range []string{"hour", "minute"} {
		if hasRequiresReplace(attrs[name]) {
			t.Errorf("%q must NOT carry RequiresReplace: the schedule is updated in place", name)
		}
	}
	for _, name := range ruleNames {
		rule, ok := attrs[name].(schema.SingleNestedAttribute)
		if !ok {
			t.Errorf("%q is %T, expected schema.SingleNestedAttribute", name, attrs[name])
			continue
		}
		if !rule.IsOptional() {
			t.Errorf("%q must be Optional: a rule that is left out is off", name)
		}
		if len(rule.PlanModifiers) != 0 {
			t.Errorf("%q carries plan modifiers; the rule is updated in place", name)
		}
		for inner, attr := range rule.Attributes {
			if hasRequiresReplace(attr) {
				t.Errorf("%s.%s must NOT carry RequiresReplace: the schedule is updated in place", name, inner)
			}
		}
	}
	if _, ok := attrs["enabled"]; ok {
		t.Error(`"enabled" must not exist: the resource existing is the service being enabled`)
	}
}

// TestExpandSchedule checks the plan read through the schema lands in the
// request unchanged, and that a rule left out of the configuration is left out
// of the request rather than sent as a zero rule the API would refuse.
func TestExpandSchedule(t *testing.T) {
	ctx := context.Background()
	s := backupSchema(t)
	plan := tfsdk.Plan{Schema: s, Raw: backupValue(t, s, map[string]tftypes.Value{
		"daily":   ruleValue(t, s, "daily", 7, 3, nil),
		"monthly": ruleValue(t, s, "monthly", 12, 4, map[string]tftypes.Value{"day_of_month": tftypes.NewValue(tftypes.String, "last")}),
	})}
	var m backupModel
	if diags := plan.Get(ctx, &m); diags.HasError() {
		t.Fatalf("Plan.Get: %v", diags)
	}

	req := expandSchedule(m)

	if req.Hour != 2 || req.Minute != 30 {
		t.Errorf("time = %02d:%02d, want 02:30", req.Hour, req.Minute)
	}
	if req.Daily == nil || req.Daily.Keep != 7 || req.Daily.BackupStorageID != 3 {
		t.Errorf("daily = %+v, want keep 7 storage 3", req.Daily)
	}
	if req.Weekly != nil {
		t.Errorf("weekly = %+v, want nil: the rule is not in the configuration", req.Weekly)
	}
	if req.Monthly == nil || req.Monthly.Keep != 12 || req.Monthly.BackupStorageID != 4 || req.Monthly.DayOfMonth != "last" {
		t.Errorf("monthly = %+v, want keep 12 storage 4 day last", req.Monthly)
	}
	if err := req.Validate(); err != nil {
		t.Errorf("the request fails the SDK's own validation: %v", err)
	}

	weekly := expandSchedule(backupModel{
		Weekly: &weeklyRuleModel{Keep: types.Int64Value(4), BackupStorageID: types.Int64Value(5), Weekday: types.StringValue("sunday")},
	}).Weekly
	if weekly == nil || weekly.Keep != 4 || weekly.BackupStorageID != 5 || weekly.Weekday != entities.BackupWeekdaySunday {
		t.Errorf("weekly = %+v, want keep 4 storage 5 weekday sunday", weekly)
	}
}

// TestMapServerBackup_roundTrip is what keeps the plan empty after an apply:
// the state built from the response to a request has to equal the plan the
// request was built from.
func TestMapServerBackup_roundTrip(t *testing.T) {
	plan := backupModel{
		ID:       types.StringValue("l1s2"),
		ServerID: types.StringValue("l1s2"),
		Hour:     types.Int64Value(23),
		Minute:   types.Int64Value(5),
		Daily:    &dailyRuleModel{Keep: types.Int64Value(7), BackupStorageID: types.Int64Value(3)},
		Weekly:   &weeklyRuleModel{Keep: types.Int64Value(4), BackupStorageID: types.Int64Value(3), Weekday: types.StringValue("monday")},
		Monthly:  &monthlyRuleModel{Keep: types.Int64Value(2), BackupStorageID: types.Int64Value(9), DayOfMonth: types.StringValue("15")},
	}

	var diags diag.Diagnostics
	got, ok := mapServerBackup("l1s2", &entities.ServerBackup{Enabled: true, Schedule: expandSchedule(plan)}, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("mapServerBackup failed: %v", diags)
	}
	if got.ID != plan.ID || got.ServerID != plan.ServerID || got.Hour != plan.Hour || got.Minute != plan.Minute ||
		*got.Daily != *plan.Daily || *got.Weekly != *plan.Weekly || *got.Monthly != *plan.Monthly {
		t.Errorf("state after the round trip = %+v, want the plan %+v", got, plan)
	}
	setBackupState(t, got)
}

// TestMapServerBackup_rulesMissing is the wire shape of a schedule with one
// rule: the Public API drops the other two rather than sending null, and they
// have to arrive in state as null objects, which is what a configuration that
// leaves them out plans.
func TestMapServerBackup_rulesMissing(t *testing.T) {
	var diags diag.Diagnostics
	got, ok := mapServerBackup("l1s2", &entities.ServerBackup{Enabled: true, Schedule: &entities.BackupSchedule{
		Hour:   1,
		Weekly: &entities.BackupWeeklyRule{BackupRule: entities.BackupRule{Keep: 4, BackupStorageID: 3}, Weekday: entities.BackupWeekdaySunday},
	}}, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("mapServerBackup failed: %v", diags)
	}
	if got.Daily != nil || got.Monthly != nil {
		t.Errorf("daily = %+v, monthly = %+v, want both nil (null in state)", got.Daily, got.Monthly)
	}
	if got.Weekly == nil || got.Weekly.Weekday.ValueString() != "sunday" {
		t.Errorf("weekly = %+v, want weekday sunday", got.Weekly)
	}
	if got.Minute.ValueInt64() != 0 {
		t.Errorf("minute = %v, want 0", got.Minute)
	}
	setBackupState(t, got)
}

// TestMapServerBackup_legacyModel covers a server whose backups are on the
// legacy model: the service is enabled but has no schedule. Recording it as a
// schedule of zero rules would plan an update the API cannot apply; removing it
// from state would plan an enable that fails on a service already enabled. The
// user gets the reason instead.
func TestMapServerBackup_legacyModel(t *testing.T) {
	var diags diag.Diagnostics
	if _, ok := mapServerBackup("l1s2", &entities.ServerBackup{Enabled: true}, &diags); ok {
		t.Fatal("a legacy-model service must not map into a model")
	}
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), "legacy backup model") {
		t.Errorf("diagnostics = %v, want an error naming the legacy backup model", diags)
	}
}

// TestMapServerBackup_disabled covers an apply that finished with the service
// still off: recording it would put a schedule in state that the server does
// not have.
func TestMapServerBackup_disabled(t *testing.T) {
	var diags diag.Diagnostics
	if _, ok := mapServerBackup("l1s2", &entities.ServerBackup{}, &diags); ok || !diags.HasError() {
		t.Errorf("a disabled service must fail the mapping with an error, got ok=%v diags=%v", ok, diags)
	}
}

// TestImportState_setsServerID checks the import id is taken as the server id;
// Read, which reads the service by server_id, has nothing else to go on.
func TestImportState_setsServerID(t *testing.T) {
	ctx := context.Background()
	s := backupSchema(t)
	resp := resource.ImportStateResponse{
		State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)},
	}

	NewResource().(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: "l1s2"}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", resp.Diagnostics)
	}
	var serverID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("server_id"), &serverID)...)
	if serverID.ValueString() != "l1s2" {
		t.Errorf("server_id after import = %v, want l1s2", serverID)
	}
}

// TestValidateConfig_requiresARule guards the plan-time refusal of a schedule
// without any rule, which the API would otherwise refuse mid-apply; a rule that
// is not known yet must not trip it.
func TestValidateConfig_requiresARule(t *testing.T) {
	ctx := context.Background()
	s := backupSchema(t)
	validate := func(rules map[string]tftypes.Value) diag.Diagnostics {
		var resp resource.ValidateConfigResponse
		NewResource().(resource.ResourceWithValidateConfig).ValidateConfig(ctx,
			resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: backupValue(t, s, rules)}}, &resp)
		return resp.Diagnostics
	}

	diags := validate(nil)
	if !diags.HasError() {
		t.Fatal("a schedule without a rule must be refused")
	}
	if d, ok := diags.Errors()[0].(diag.DiagnosticWithPath); !ok || !d.Path().Equal(path.Root("daily")) {
		t.Errorf("the error must be on daily, got %v", diags.Errors()[0])
	}

	if diags := validate(map[string]tftypes.Value{"weekly": ruleValue(t, s, "weekly", 4, 3,
		map[string]tftypes.Value{"weekday": tftypes.NewValue(tftypes.String, "sunday")})}); diags.HasError() {
		t.Errorf("a schedule with a weekly rule must pass, got %v", diags)
	}

	unknown := tftypes.NewValue(ruleType(t, s, "monthly"), tftypes.UnknownValue)
	if diags := validate(map[string]tftypes.Value{"monthly": unknown}); diags.HasError() {
		t.Errorf("a rule that is not known yet must pass, got %v", diags)
	}
}

// backupValue builds a vcp_server_backup value for server l1s2 at 02:30 with
// the given rules; the rules not given are null.
func backupValue(t *testing.T, s schema.Schema, rules map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	objectType, ok := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatal("the resource schema is not an object")
	}
	values := map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, nil),
		"server_id": tftypes.NewValue(tftypes.String, "l1s2"),
		"hour":      tftypes.NewValue(tftypes.Number, 2),
		"minute":    tftypes.NewValue(tftypes.Number, 30),
	}
	for _, name := range ruleNames {
		values[name] = tftypes.NewValue(objectType.AttributeTypes[name], nil)
		if v, ok := rules[name]; ok {
			values[name] = v
		}
	}
	return tftypes.NewValue(objectType, values)
}

func ruleType(t *testing.T, s schema.Schema, rule string) tftypes.Object {
	t.Helper()
	objectType, ok := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatal("the resource schema is not an object")
	}
	ruleType, ok := objectType.AttributeTypes[rule].(tftypes.Object)
	if !ok {
		t.Fatalf("%q is not an object", rule)
	}
	return ruleType
}

// ruleValue builds a rule with keep and backup_storage_id plus the rule's own
// attributes in extra.
func ruleValue(t *testing.T, s schema.Schema, rule string, keep, storageID int, extra map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	values := map[string]tftypes.Value{
		"keep":              tftypes.NewValue(tftypes.Number, keep),
		"backup_storage_id": tftypes.NewValue(tftypes.Number, storageID),
	}
	for k, v := range extra {
		values[k] = v
	}
	return tftypes.NewValue(ruleType(t, s, rule), values)
}

// setBackupState writes the model into a state built from the resource schema,
// failing the test on any disagreement between the two.
func setBackupState(t *testing.T, m backupModel) {
	t.Helper()
	ctx := context.Background()
	s := backupSchema(t)
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := state.Set(ctx, &m); diags.HasError() {
		t.Fatalf("State.Set: %v", diags)
	}
}

// hasRequiresReplace reports whether an attribute carries a RequiresReplace plan
// modifier, identified by its public description text.
func hasRequiresReplace(attr any) bool {
	const marker = "destroy and recreate the resource"
	var descriptions []string
	switch a := attr.(type) {
	case schema.StringAttribute:
		for _, pm := range a.PlanModifiers {
			descriptions = append(descriptions, pm.Description(context.Background()))
		}
	case schema.Int64Attribute:
		for _, pm := range a.PlanModifiers {
			descriptions = append(descriptions, pm.Description(context.Background()))
		}
	}
	for _, d := range descriptions {
		if strings.Contains(d, marker) {
			return true
		}
	}
	return false
}
