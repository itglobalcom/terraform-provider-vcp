package vmware

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// volumeAttrTypes mirrors the schema of one `volumes` entry, so a model can be
// turned into the types.List the plan modifier works on.
var volumeAttrTypes = map[string]attr.Type{
	"number":    types.Int64Type,
	"id":        types.Int64Type,
	"name":      types.StringType,
	"size_mb":   types.Int64Type,
	"disk_type": types.StringType,
}

func volume(number, id, sizeMB int64, name, diskType string) serverVolumeModel {
	vol := serverVolumeModel{
		Number:   types.Int64Value(number),
		ID:       types.Int64Value(id),
		Name:     types.StringValue(name),
		SizeMB:   types.Int64Value(sizeMB),
		DiskType: types.StringValue(diskType),
	}
	if id == 0 {
		vol.ID = types.Int64Unknown()
	}
	return vol
}

func volumeList(t *testing.T, volumes ...serverVolumeModel) types.List {
	t.Helper()
	list, diags := types.ListValueFrom(context.Background(),
		types.ObjectType{AttrTypes: volumeAttrTypes}, volumes)
	if diags.HasError() {
		t.Fatalf("building the volume list: %v", diags)
	}
	return list
}

func planVolumes(t *testing.T, state, plan types.List) ([]serverVolumeModel, diag.Diagnostics) {
	t.Helper()
	req := planmodifier.ListRequest{Path: path.Root("volumes"), StateValue: state, PlanValue: plan}
	resp := planmodifier.ListResponse{PlanValue: plan}
	serverVolumesPlanModifier{}.PlanModifyList(context.Background(), req, &resp)

	var out []serverVolumeModel
	if !resp.PlanValue.IsNull() {
		resp.Diagnostics.Append(resp.PlanValue.ElementsAs(context.Background(), &out, false)...)
	}
	return out, resp.Diagnostics
}

// The number is what carries a disk's identity across a plan. Renaming a volume
// or growing it keeps the id, which is what makes it an edit of that disk rather
// than a new one — and the plan says nothing about destroying anything.
func TestServerVolumesPlanModifierKeepsIdentityAcrossRename(t *testing.T) {
	state := volumeList(t, volume(0, 101, 10240, "data", "ssd"))
	plan := volumeList(t, serverVolumeModel{
		Number:   types.Int64Value(0),
		ID:       types.Int64Unknown(),
		Name:     types.StringValue("db-data"), // renamed
		SizeMB:   types.Int64Value(20480),      // and grown
		DiskType: types.StringValue("ssd"),
	})

	planned, diags := planVolumes(t, state, plan)
	if diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	if len(planned) != 1 {
		t.Fatalf("planned %d volumes, want 1", len(planned))
	}
	if planned[0].ID.ValueInt64() != 101 {
		t.Errorf("id = %v, want 101 carried over from state — a rename is not a new disk", planned[0].ID)
	}
	if diags.WarningsCount() != 0 {
		t.Errorf("a rename must not warn about destroying anything, got: %v", diags)
	}
}

// A disk cannot be moved to another type in place, so this is a destroy — and it
// has to be said out loud before the apply, not discovered afterwards.
func TestServerVolumesPlanModifierWarnsOnDiskTypeChange(t *testing.T) {
	state := volumeList(t, volume(0, 101, 10240, "data", "ssd"))
	plan := volumeList(t, volume(0, 0, 10240, "data", "hdd"))

	planned, diags := planVolumes(t, state, plan)
	if diags.WarningsCount() != 1 {
		t.Fatalf("expected one warning about the disk being destroyed, got: %v", diags)
	}
	if summary := diags.Warnings()[0].Summary(); summary != "Disks Will Be Destroyed" {
		t.Errorf("unexpected warning: %q", summary)
	}
	if detail := diags.Warnings()[0].Detail(); !strings.Contains(detail, "disk_type") {
		t.Errorf("the warning should name the reason, got: %s", detail)
	}
	if !planned[0].ID.IsUnknown() {
		t.Error("a replaced disk must plan an unknown id — it is not the disk state knows")
	}
}

// A disk dropped from the configuration is a disk about to be deleted.
func TestServerVolumesPlanModifierWarnsOnRemoval(t *testing.T) {
	state := volumeList(t, volume(0, 101, 10240, "data", "ssd"), volume(1, 102, 10240, "logs", "ssd"))
	plan := volumeList(t, volume(0, 101, 10240, "data", "ssd"))

	_, diags := planVolumes(t, state, plan)
	if diags.WarningsCount() != 1 {
		t.Fatalf("expected a warning about the removed disk, got: %v", diags)
	}
	if detail := diags.Warnings()[0].Detail(); !strings.Contains(detail, "logs") {
		t.Errorf("the warning should name the disk, got: %s", detail)
	}
}

// The first apply has nothing to destroy, so it must not talk about destruction.
func TestServerVolumesPlanModifierIsQuietOnCreate(t *testing.T) {
	plan := volumeList(t, volume(0, 0, 10240, "data", "ssd"))

	planned, diags := planVolumes(t, types.ListNull(types.ObjectType{AttrTypes: volumeAttrTypes}), plan)
	if len(diags) != 0 {
		t.Fatalf("creating disks must not produce diagnostics, got: %v", diags)
	}
	if !planned[0].ID.IsUnknown() {
		t.Error("a new disk has no id until the API assigns one")
	}
}

func TestValidateServerVolumes(t *testing.T) {
	cases := map[string]struct {
		volumes   []serverVolumeModel
		wantError string
	}{
		"distinct disks are fine": {
			volumes: []serverVolumeModel{
				volume(0, 0, 10240, "data", "ssd"),
				volume(1, 0, 10240, "logs", "ssd"),
			},
		},
		"two disks cannot share a number": {
			volumes: []serverVolumeModel{
				volume(0, 0, 10240, "data", "ssd"),
				volume(0, 0, 10240, "logs", "ssd"),
			},
			wantError: "Duplicate Volume Number",
		},
		"two disks cannot share a name": {
			volumes: []serverVolumeModel{
				volume(0, 0, 10240, "data", "ssd"),
				volume(1, 0, 10240, "data", "ssd"),
			},
			wantError: "Duplicate Volume Name",
		},
		// The boot disk is ordered with the machine; describing it here would
		// create a second disk and leave the real one unmanaged.
		"the boot disk does not belong here": {
			volumes:   []serverVolumeModel{volume(0, 0, 51200, "boot", "ssd")},
			wantError: "Boot Disk Is Not A Volume",
		},
		"and not under another spelling either": {
			volumes:   []serverVolumeModel{volume(0, 0, 51200, "Boot", "ssd")},
			wantError: "Boot Disk Is Not A Volume",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateServerVolumes(tc.volumes, &diags)

			if tc.wantError == "" {
				if diags.HasError() {
					t.Fatalf("unexpected error: %v", diags)
				}
				return
			}
			if !diags.HasError() {
				t.Fatalf("expected %q, got no error", tc.wantError)
			}
			if got := diags.Errors()[0].Summary(); got != tc.wantError {
				t.Errorf("error = %q, want %q", got, tc.wantError)
			}
		})
	}
}

// The server's ValidateConfig, driven the way the framework drives it. The three
// things it decides — a bandwidth that contradicts the network it is ordered on,
// two mutually exclusive platform features asked for at once, and a disk list
// that cannot be told apart — are all settled before anything is created.
func TestServerValidateConfig(t *testing.T) {
	res := &serverResource{}
	s := resourceSchema(t, res)

	base := func() serverModel {
		return serverModel{
			LocationID:   types.Int64Value(5),
			Name:         types.StringValue("web-01"),
			ImageID:      types.Int64Value(42),
			CPU:          types.Int64Value(1),
			RamMB:        types.Int64Value(1024),
			SystemDiskMB: types.Int64Value(51200),
			// A null of the right type: the zero value of a List/Set/Object carries
			// no element type, and the framework will not accept it.
			SSHKeyIDs: types.SetNull(types.Int64Type),
			Gpu:       types.ObjectNull(gpuAttrTypes),
			Nics:      types.ListNull(types.ObjectType{AttrTypes: nicAttrTypes}),
		}
	}

	validate := func(model serverModel) diag.Diagnostics {
		resp := resource.ValidateConfigResponse{}
		res.ValidateConfig(context.Background(),
			resource.ValidateConfigRequest{Config: configOf(t, s, model)}, &resp)
		return resp.Diagnostics
	}

	t.Run("an ordinary server passes", func(t *testing.T) {
		if diags := validate(base()); diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
	})

	t.Run("a named public network brings its own bandwidth", func(t *testing.T) {
		model := base()
		model.PublicNetworkID = types.Int64Value(100)
		model.NetworkBandwidthMbps = types.Int64Value(100)

		diags := validate(model)
		if !diags.HasError() {
			t.Fatal("two ways of describing one interface must be refused")
		}
		if got := diags.Errors()[0].Summary(); got != "Conflicting Attributes" {
			t.Errorf("unexpected error: %q", got)
		}
	})

	t.Run("either attribute alone is fine", func(t *testing.T) {
		model := base()
		model.PublicNetworkID = types.Int64Value(100)
		if diags := validate(model); diags.HasError() {
			t.Errorf("public_network_id alone must pass: %v", diags)
		}

		model = base()
		model.NetworkBandwidthMbps = types.Int64Value(100)
		if diags := validate(model); diags.HasError() {
			t.Errorf("network_bandwidth_mbps alone must pass: %v", diags)
		}
	})

	// The platform refuses an order that asks for both; the validator moves the
	// failure to plan time.
	t.Run("a GPU and a nested hypervisor are not both possible", func(t *testing.T) {
		model := base()
		model.Gpu = gpuObject(t, 3, 8192, 1)
		model.NestedHypervisor = types.BoolValue(true)

		diags := validate(model)
		if !diags.HasError() {
			t.Fatal("gpu with nested_hypervisor must be refused at plan time")
		}
		if got := diags.Errors()[0].Summary(); got != "Conflicting Attributes" {
			t.Errorf("unexpected error: %q", got)
		}
	})

	t.Run("a GPU machine may still say nested_hypervisor = false", func(t *testing.T) {
		// Stating the platform's default explicitly is not a conflict and must
		// not be blocked.
		model := base()
		model.Gpu = gpuObject(t, 3, 8192, 1)
		model.NestedHypervisor = types.BoolValue(false)

		if diags := validate(model); diags.HasError() {
			t.Errorf("nested_hypervisor = false alongside gpu must pass: %v", diags)
		}
	})

	t.Run("either feature alone is fine", func(t *testing.T) {
		model := base()
		model.Gpu = gpuObject(t, 3, 8192, 1)
		if diags := validate(model); diags.HasError() {
			t.Errorf("gpu alone must pass: %v", diags)
		}

		model = base()
		model.NestedHypervisor = types.BoolValue(true)
		if diags := validate(model); diags.HasError() {
			t.Errorf("nested_hypervisor alone must pass: %v", diags)
		}
	})

	t.Run("the disk list is checked too", func(t *testing.T) {
		model := base()
		model.Volumes = []serverVolumeModel{
			volume(0, 0, 10240, "data", "ssd"),
			volume(0, 0, 10240, "logs", "ssd"),
		}

		diags := validate(model)
		if !diags.HasError() {
			t.Fatal("two disks sharing a number must be refused")
		}
		if got := diags.Errors()[0].Summary(); got != "Duplicate Volume Number" {
			t.Errorf("unexpected error: %q", got)
		}
	})
}

// gpuObject is the `gpu` attribute a configuration would carry — the whole triple,
// which is the only shape the API accepts.
func gpuObject(t *testing.T, modelID, vramMB, cardCount int64) types.Object {
	t.Helper()
	obj, diags := types.ObjectValue(gpuAttrTypes, map[string]attr.Value{
		"model_id":   types.Int64Value(modelID),
		"vram_mb":    types.Int64Value(vramMB),
		"card_count": types.Int64Value(cardCount),
	})
	if diags.HasError() {
		t.Fatalf("building the gpu object: %v", diags)
	}
	return obj
}
