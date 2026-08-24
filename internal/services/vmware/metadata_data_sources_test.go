package vmware

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Reading the location capability back out of the catalog.
//
// nested_hypervisor_supported has to be named in three places — the model, the
// schema and the Read that fills the model in — and the schema is the only one
// TestLocationsReportNestedHypervisorSupport covers. A Read that never writes the
// field leaves it null, which reads as "no VDC here supports it" and sends a user
// hunting for a location that already was the right one.
func TestLocationsDataSourceReadReportsNestedHypervisorSupport(t *testing.T) {
	api := newFakeAPI(t)
	// Two locations, each capability set the opposite way round on them: a Read
	// that never wrote the field, or wrote the wrong one, fails on one of the two.
	api.addLocation(5, "spb", false, true)
	api.addLocation(6, "minsk", true, false)

	state := readLocations(t, api)

	if len(state.Locations) != 2 {
		t.Fatalf("read %d locations, want 2: %+v", len(state.Locations), state.Locations)
	}
	for i, want := range []bool{true, false} {
		got := state.Locations[i].NestedHypervisorSupported
		if got.IsNull() || got.IsUnknown() {
			t.Fatalf("locations[%d].nested_hypervisor_supported = %v, want %v — the Read never wrote it",
				i, got, want)
		}
		if got.ValueBool() != want {
			t.Errorf("locations[%d].nested_hypervisor_supported = %v, want %v", i, got.ValueBool(), want)
		}
	}
	// The capability it sits beside, so a Read filling every bool from one source
	// would be noticed rather than passing.
	if state.Locations[0].GpuSupported.ValueBool() || !state.Locations[1].GpuSupported.ValueBool() {
		t.Errorf("gpu_supported did not come through: %+v", state.Locations)
	}
	if state.Locations[0].ID.ValueInt64() != 5 || state.Locations[0].TechTitle.ValueString() != "spb" {
		t.Errorf("the rest of the location did not come through: %+v", state.Locations[0])
	}
}

// readLocations drives the locations data source against the fake and returns the
// state it left behind.
func readLocations(t *testing.T, api *fakeAPI) locationsListModel {
	t.Helper()
	ctx := context.Background()

	ds, ok := NewLocationsDataSource().(datasource.DataSourceWithConfigure)
	if !ok {
		t.Fatal("the locations data source cannot be configured")
	}
	configureResp := datasource.ConfigureResponse{}
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: api.client(t)}, &configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("configuring the data source: %v", configureResp.Diagnostics)
	}

	schemaResp := datasource.SchemaResponse{}
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("reading the schema: %v", schemaResp.Diagnostics)
	}
	s := schemaResp.Schema

	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)},
	}
	ds.Read(ctx, datasource.ReadRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("reading the locations: %v", resp.Diagnostics)
	}

	var out locationsListModel
	if diags := resp.State.Get(ctx, &out); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	return out
}
