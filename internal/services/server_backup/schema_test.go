package server_backup

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
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
