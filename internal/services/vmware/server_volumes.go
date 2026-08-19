package vmware

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Data volumes of a VMware server.
//
// The boot disk is not one of these: it is ordered together with the machine
// (system_disk_mb / system_disk_type) and the API has no endpoint that would
// separate it out again. `volumes` therefore describes the *additional* disks,
// and the pair of attributes for the boot disk stays where it is.
//
// The set is owned by number, not by what the API happens to list. A volume the
// provider never created is left alone and reported once per apply: the API
// gives no flag that tells a data disk from the boot disk, so deleting an
// untracked volume because it is absent from the configuration would risk
// deleting the disk the machine boots from.

// serverVolumeModel is one entry of the `volumes` list.
type serverVolumeModel struct {
	// Number is the stable key the user chooses. It is what makes renaming a
	// volume a rename rather than "delete and create another one", and what
	// pairs a configuration entry with the volume the API already holds.
	Number   types.Int64  `tfsdk:"number"`
	ID       types.Int64  `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	SizeMB   types.Int64  `tfsdk:"size_mb"`
	DiskType types.String `tfsdk:"disk_type"`
}

// serverVolumesAttribute is the `volumes` attribute of vcp_vmware_server.
func serverVolumesAttribute() schema.Attribute {
	return schema.ListNestedAttribute{
		Optional: true,
		Description: "Additional data disks. The boot disk is not listed here — it is ordered with the server " +
			"through system_disk_mb / system_disk_type.",
		MarkdownDescription: "Additional data disks attached to the server.\n\n" +
			"~> The boot disk is **not** one of these: it is ordered together with the machine through " +
			"`system_disk_mb` / `system_disk_type`.\n\n" +
			"~> `number` is a key you choose and keep. It is what lets the provider tell a renamed volume from a " +
			"replaced one, so changing a `number` means \"delete that disk and create another\", with the data " +
			"going the way of the disk.\n\n" +
			"~> Only the disks declared here are managed. A disk created in the panel is left alone and reported " +
			"in a warning — the API does not mark which disk a machine boots from, so removing an unknown one " +
			"is not a risk worth taking.",
		PlanModifiers: []planmodifier.List{serverVolumesPlanModifier{}},
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"number": schema.Int64Attribute{
					Description: "Stable key of the volume, unique within the server. Changing it replaces the disk.",
					Required:    true,
					Validators:  []validator.Int64{int64validator.AtLeast(0)},
				},
				"id": schema.Int64Attribute{
					Description: "ID of the volume in the API.",
					Computed:    true,
				},
				"name": schema.StringAttribute{
					Description: "Name of the volume. Editable in place.",
					Required:    true,
					Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
				},
				"size_mb": schema.Int64Attribute{
					Description: "Size in MB. Can be increased in place; shrinking is refused.",
					Required:    true,
					Validators:  []validator.Int64{int64validator.AtLeast(1)},
				},
				"disk_type": schema.StringAttribute{
					Description: "Disk type, by title, as offered by the location (vcp_vmware_locations). " +
						"Changing it replaces the disk and loses its data.",
					Required:   true,
					Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
				},
			},
		},
	}
}

// ============================================================================
// Plan
// ============================================================================

// serverVolumesPlanModifier carries the API id of a volume across a plan, keyed
// by number, and says out loud what an apply is about to destroy. Without it
// every planned volume would show an unknown id and a rename would be
// indistinguishable from a replacement.
type serverVolumesPlanModifier struct{}

func (serverVolumesPlanModifier) Description(context.Context) string {
	return "Keeps a volume's identity across a plan by its number, and warns about disks an apply would destroy."
}

func (m serverVolumesPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m serverVolumesPlanModifier) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
		return
	}

	var planned []serverVolumeModel
	resp.Diagnostics.Append(req.PlanValue.ElementsAs(ctx, &planned, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	prior := map[int64]serverVolumeModel{}
	if !req.StateValue.IsNull() && !req.StateValue.IsUnknown() {
		var stateVolumes []serverVolumeModel
		resp.Diagnostics.Append(req.StateValue.ElementsAs(ctx, &stateVolumes, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		for _, vol := range stateVolumes {
			if !vol.Number.IsNull() {
				prior[vol.Number.ValueInt64()] = vol
			}
		}
	}

	var destroyed []string
	kept := map[int64]bool{}

	for i := range planned {
		vol := &planned[i]
		number := vol.Number.ValueInt64()
		kept[number] = true

		existing, ok := prior[number]
		if !ok {
			vol.ID = types.Int64Unknown() // a new disk, the API assigns the id
			continue
		}

		// The API can rename and grow a volume, but not restripe it onto another
		// disk type — that is a new disk, and the data does not come along.
		if !vol.DiskType.IsUnknown() && !vol.DiskType.Equal(existing.DiskType) {
			destroyed = append(destroyed, fmt.Sprintf("number=%d (%q): disk_type %s → %s",
				number, existing.Name.ValueString(),
				existing.DiskType.ValueString(), vol.DiskType.ValueString()))
			vol.ID = types.Int64Unknown()
			continue
		}
		vol.ID = existing.ID
	}

	for number, vol := range prior {
		if !kept[number] {
			destroyed = append(destroyed, fmt.Sprintf("number=%d (%q, id=%d): removed from the configuration",
				number, vol.Name.ValueString(), vol.ID.ValueInt64()))
		}
	}

	if len(destroyed) > 0 {
		resp.Diagnostics.AddAttributeWarning(req.Path, "Disks Will Be Destroyed",
			"Applying this plan deletes the following disks and everything on them:\n  "+
				strings.Join(destroyed, "\n  "))
	}

	updated, diags := types.ListValueFrom(ctx, req.PlanValue.ElementType(ctx), planned)
	resp.Diagnostics.Append(diags...)
	if !diags.HasError() {
		resp.PlanValue = updated
	}
}

// validateServerVolumes checks what the configuration can be judged on by itself.
func validateServerVolumes(volumes []serverVolumeModel, diags *diag.Diagnostics) {
	seenNumber := map[int64]int{}
	seenName := map[string]int{}

	for i, vol := range volumes {
		volPath := path.Root("volumes").AtListIndex(i)

		if !vol.Number.IsNull() && !vol.Number.IsUnknown() {
			number := vol.Number.ValueInt64()
			if first, dup := seenNumber[number]; dup {
				diags.AddAttributeError(volPath.AtName("number"), "Duplicate Volume Number",
					fmt.Sprintf("number %d is already used by volumes[%d]. The number is what identifies a disk "+
						"across applies, so two disks cannot share one.", number, first))
			} else {
				seenNumber[number] = i
			}
		}

		if isSet(vol.Name) {
			name := vol.Name.ValueString()
			if first, dup := seenName[name]; dup {
				diags.AddAttributeError(volPath.AtName("name"), "Duplicate Volume Name",
					fmt.Sprintf("a disk named %q is already declared at volumes[%d].", name, first))
			} else {
				seenName[name] = i
			}
			// The boot disk is ordered with the machine, not here; a volume called
			// "boot" is almost certainly an attempt to describe it.
			if strings.EqualFold(name, "boot") {
				diags.AddAttributeError(volPath.AtName("name"), "Boot Disk Is Not A Volume",
					"The disk the machine boots from is ordered with the server — set system_disk_mb and "+
						"system_disk_type instead. This list is for the additional disks.")
			}
		}
	}
}

// ============================================================================
// Apply
// ============================================================================

// syncServerVolumes brings the server's data disks to `planned`, starting from
// what `prior` recorded.
//
// It returns the disks that exist and are tracked at the moment it returns —
// including when a step failed, so the caller records the ones already created
// instead of leaving a disk behind that nothing knows about. The entries it
// never reached are simply absent, which is what makes the next apply pick up
// where this one stopped.
func syncServerVolumes(ctx context.Context, client *sdk.CloudClient, serverID int,
	prior, planned []serverVolumeModel, diags *diag.Diagnostics) []serverVolumeModel {
	byNumber := map[int64]serverVolumeModel{}
	for _, vol := range prior {
		byNumber[vol.Number.ValueInt64()] = vol
	}

	applied := make([]serverVolumeModel, 0, len(planned))
	kept := map[int64]bool{}

	for _, want := range planned {
		number := want.Number.ValueInt64()
		kept[number] = true
		existing, exists := byNumber[number]

		// A disk type cannot be changed in place: the old disk goes first, and the
		// plan warned about it.
		if exists && !want.DiskType.Equal(existing.DiskType) {
			if !deleteServerVolume(ctx, client, serverID, existing, diags) {
				return applied
			}
			exists = false
		}

		if exists {
			updated, ok := updateServerVolume(ctx, client, serverID, existing, want, diags)
			if !ok {
				return applied
			}
			applied = append(applied, updated)
			continue
		}

		created, ok := createServerVolume(ctx, client, serverID, want, diags)
		if !ok {
			return applied
		}
		applied = append(applied, created)
	}

	// Disks the configuration dropped. Only the tracked ones — an untracked disk
	// may well be the one the machine boots from.
	for number, vol := range byNumber {
		if kept[number] {
			continue
		}
		if !deleteServerVolume(ctx, client, serverID, vol, diags) {
			return applied
		}
	}

	return applied
}

func createServerVolume(ctx context.Context, client *sdk.CloudClient, serverID int,
	want serverVolumeModel, diags *diag.Diagnostics) (serverVolumeModel, bool) {
	tflog.Info(ctx, "Creating a VMware server volume", map[string]any{
		"server_id": serverID, "name": want.Name.ValueString(), "size_mb": want.SizeMB.ValueInt64(),
	})

	err := client.CreateVmwareVolumeAndWait(ctx, serverID, &entities.VmwareCreateVolumeRequest{
		Name:     want.Name.ValueString(),
		DiskType: want.DiskType.ValueString(),
		SizeMB:   int(want.SizeMB.ValueInt64()),
	})
	if err != nil {
		diags.AddError("Error Creating VMware Server Volume",
			fmt.Sprintf("Could not create disk %q (number %d) on server %d: %s",
				want.Name.ValueString(), want.Number.ValueInt64(), serverID, err.Error()))
		return want, false
	}

	// Neither the response nor the task carries the new id, so the disk is found
	// by the name it was created with.
	created, err := findVolumeByName(ctx, client, serverID, want.Name.ValueString())
	if err != nil {
		diags.AddError("Error Reading Created VMware Server Volume",
			fmt.Sprintf("Disk %q was created on server %d, but reading it back failed: %s\n\n"+
				"Run terraform apply again to record it.", want.Name.ValueString(), serverID, err.Error()))
		return want, false
	}

	out := want
	out.ID = types.Int64Value(int64(created.ID))
	out.SizeMB = types.Int64Value(int64(created.SizeMB))
	if created.DiskType != nil {
		out.DiskType = types.StringValue(*created.DiskType)
	}
	return out, true
}

func updateServerVolume(ctx context.Context, client *sdk.CloudClient, serverID int,
	existing, want serverVolumeModel, diags *diag.Diagnostics) (serverVolumeModel, bool) {
	out := want
	out.ID = existing.ID

	if want.Name.Equal(existing.Name) && want.SizeMB.Equal(existing.SizeMB) {
		return out, true
	}

	// The API can grow a disk, never shrink one; saying so here beats a bare 400.
	if want.SizeMB.ValueInt64() < existing.SizeMB.ValueInt64() {
		diags.AddAttributeError(path.Root("volumes"), "Disk Cannot Be Shrunk",
			fmt.Sprintf("Disk %q (number %d) is %d MB and the configuration asks for %d MB. A disk can only be "+
				"grown. To end up with a smaller one, remove this entry and add it back at the size you want — "+
				"which destroys the data on it.",
				existing.Name.ValueString(), want.Number.ValueInt64(),
				existing.SizeMB.ValueInt64(), want.SizeMB.ValueInt64()))
		return out, false
	}

	volumeID := int(existing.ID.ValueInt64())
	tflog.Info(ctx, "Editing a VMware server volume", map[string]any{
		"server_id": serverID, "volume_id": volumeID, "size_mb": want.SizeMB.ValueInt64(),
	})

	updated, err := client.EditVmwareVolumeAndWait(ctx, serverID, volumeID, &entities.VmwareEditVolumeRequest{
		Name:   want.Name.ValueString(),
		SizeMB: int(want.SizeMB.ValueInt64()),
	})
	if err != nil {
		diags.AddError("Error Editing VMware Server Volume",
			fmt.Sprintf("Could not edit disk %q (id %d) on server %d: %s",
				existing.Name.ValueString(), volumeID, serverID, err.Error()))
		return out, false
	}
	if updated != nil {
		out.SizeMB = types.Int64Value(int64(updated.SizeMB))
		out.Name = types.StringValue(updated.Name)
	}
	return out, true
}

func deleteServerVolume(ctx context.Context, client *sdk.CloudClient, serverID int,
	vol serverVolumeModel, diags *diag.Diagnostics) bool {
	volumeID := int(vol.ID.ValueInt64())
	if volumeID <= 0 {
		return true // never created; nothing to remove
	}

	tflog.Info(ctx, "Deleting a VMware server volume", map[string]any{
		"server_id": serverID, "volume_id": volumeID, "name": vol.Name.ValueString(),
	})

	if err := client.DeleteVmwareVolumeAndWait(ctx, serverID, volumeID); err != nil {
		if sdk.IsNotFound(err) {
			return true
		}
		diags.AddError("Error Deleting VMware Server Volume",
			fmt.Sprintf("Could not delete disk %q (id %d) on server %d: %s",
				vol.Name.ValueString(), volumeID, serverID, err.Error()))
		return false
	}
	return true
}

// findVolumeByName locates a volume by the name it was created with.
func findVolumeByName(ctx context.Context, client *sdk.CloudClient, serverID int, name string) (*entities.VmwareVolume, error) {
	volumes, err := client.GetVmwareServerVolumes(ctx, serverID)
	if err != nil {
		return nil, err
	}
	for _, vol := range volumes {
		if vol != nil && vol.Name == name {
			return vol, nil
		}
	}
	return nil, fmt.Errorf("no volume named %q on server %d: %w", name, serverID, sdk.ErrNotFound)
}

// ============================================================================
// Read
// ============================================================================

// refreshServerVolumes updates the tracked disks from the API: sizes and names as
// they now are, and entries whose disk is gone dropped so the next plan offers to
// create them again. Disks the provider does not track are not adopted.
func refreshServerVolumes(ctx context.Context, client *sdk.CloudClient, serverID int,
	tracked []serverVolumeModel) ([]serverVolumeModel, error) {
	if len(tracked) == 0 {
		return tracked, nil
	}

	volumes, err := client.GetVmwareServerVolumes(ctx, serverID)
	if err != nil {
		return nil, err
	}
	byID := map[int]*entities.VmwareVolume{}
	for _, vol := range volumes {
		if vol != nil {
			byID[vol.ID] = vol
		}
	}

	out := make([]serverVolumeModel, 0, len(tracked))
	for _, want := range tracked {
		vol, ok := byID[int(want.ID.ValueInt64())]
		if !ok {
			continue // deleted elsewhere: let the plan offer to create it again
		}
		refreshed := want
		refreshed.Name = types.StringValue(vol.Name)
		refreshed.SizeMB = types.Int64Value(int64(vol.SizeMB))
		if vol.DiskType != nil {
			refreshed.DiskType = types.StringValue(*vol.DiskType)
		}
		out = append(out, refreshed)
	}
	return out, nil
}

// warnUntrackedVolumes reports the disks the server carries that this resource
// does not manage. It runs on apply rather than on refresh: it is a note about
// the shape of the machine, not a difference to settle, and repeating it on
// every plan would be noise.
func warnUntrackedVolumes(ctx context.Context, client *sdk.CloudClient, serverID int,
	tracked []serverVolumeModel, diags *diag.Diagnostics) {
	volumes, err := client.GetVmwareServerVolumes(ctx, serverID)
	if err != nil {
		tflog.Warn(ctx, "Could not list server volumes", map[string]any{
			"server_id": serverID, "error": err.Error(),
		})
		return
	}

	trackedIDs := map[int]bool{}
	for _, vol := range tracked {
		trackedIDs[int(vol.ID.ValueInt64())] = true
	}

	var untracked []string
	for _, vol := range volumes {
		if vol == nil || trackedIDs[vol.ID] {
			continue
		}
		untracked = append(untracked, fmt.Sprintf("%q (id=%d, %d MB)", vol.Name, vol.ID, vol.SizeMB))
	}
	if len(untracked) == 0 {
		return
	}

	diags.AddAttributeWarning(path.Root("volumes"), "Disks Not Managed By Terraform",
		fmt.Sprintf("Server %d also carries %s. They are left untouched — the API does not say which disk the "+
			"machine boots from, so a disk this resource did not create is never deleted. Add it to `volumes` "+
			"with its own `number` to manage it, or ignore this if it is the boot disk.",
			serverID, strings.Join(untracked, ", ")))
}
