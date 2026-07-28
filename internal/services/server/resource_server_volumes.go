package vstack_server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// VOLUME PLAN MODIFIER
// ============================================================================

type volumesPlanModifier struct{}

func VolumesNumberChangeModifier() planmodifier.List {
	return volumesPlanModifier{}
}

func (m volumesPlanModifier) Description(ctx context.Context) string {
	return "Handles volume number-based identity"
}

func (m volumesPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m volumesPlanModifier) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
		return
	}

	var planVolumes []VolumeAttrModel
	resp.Diagnostics.Append(req.PlanValue.ElementsAs(ctx, &planVolumes, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// State map: number -> volume
	stateVolByNumber := make(map[int64]VolumeAttrModel)
	if !req.StateValue.IsNull() {
		var stateVolumes []VolumeAttrModel
		resp.Diagnostics.Append(req.StateValue.ElementsAs(ctx, &stateVolumes, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		for _, vol := range stateVolumes {
			if !vol.Number.IsNull() {
				stateVolByNumber[vol.Number.ValueInt64()] = vol
			}
		}
	}

	planNumbers := make(map[int64]bool)
	for _, vol := range planVolumes {
		if !vol.Number.IsNull() {
			planNumbers[vol.Number.ValueInt64()] = true
		}
	}

	var createdVolumes, deletedVolumes []string

	for i := range planVolumes {
		planVol := &planVolumes[i]
		planNumber := planVol.Number.ValueInt64()

		if stateVol, exists := stateVolByNumber[planNumber]; exists {
			// Volume exists - copy computed fields from state
			planVol.ID = stateVol.ID
			planVol.Created = stateVol.Created
		} else {
			// New volume
			planVol.ID = types.Int64Unknown()
			planVol.Created = types.StringUnknown()

			if !req.StateValue.IsNull() {
				createdVolumes = append(createdVolumes,
					fmt.Sprintf("number=%d, name=%q", planNumber, planVol.Name.ValueString()))
			}
		}
	}

	// Check for deleted volumes
	if !req.StateValue.IsNull() {
		for stateNumber, stateVol := range stateVolByNumber {
			if !planNumbers[stateNumber] {
				deletedVolumes = append(deletedVolumes,
					fmt.Sprintf("number=%d, name=%q, id=%d",
						stateNumber, stateVol.Name.ValueString(), stateVol.ID.ValueInt64()))
			}
		}
	}

	if len(createdVolumes) > 0 || len(deletedVolumes) > 0 {
		var msg strings.Builder
		if len(deletedVolumes) > 0 {
			fmt.Fprintf(&msg, "Volumes to be DELETED: %s. ", strings.Join(deletedVolumes, "; "))
		}
		if len(createdVolumes) > 0 {
			fmt.Fprintf(&msg, "Volumes to be CREATED: %s.", strings.Join(createdVolumes, "; "))
		}
		resp.Diagnostics.AddWarning("Volume Changes Detected", msg.String())
	}

	newPlanValue, diags := types.ListValueFrom(ctx, req.PlanValue.ElementType(ctx), planVolumes)
	resp.Diagnostics.Append(diags...)
	if !diags.HasError() {
		resp.PlanValue = newPlanValue
	}
}

// ============================================================================
// VOLUME VALIDATION
// ============================================================================

func (r *ServerResource) findBootVolume(volumes []VolumeAttrModel) *VolumeAttrModel {
	for i := range volumes {
		if volumes[i].Name.ValueString() == "boot" {
			return &volumes[i]
		}
	}
	return nil
}

func (r *ServerResource) validateUniqueVolumeNumbers(volumes []VolumeAttrModel) error {
	seen := make(map[int64]bool)
	for _, vol := range volumes {
		number := vol.Number.ValueInt64()
		if seen[number] {
			return fmt.Errorf("duplicate volume number %d: each volume must have a unique number", number)
		}
		seen[number] = true
	}
	return nil
}

func (r *ServerResource) validateVolumeSize(vol VolumeAttrModel) error {
	if vol.SizeMB.ValueInt64()%10240 != 0 {
		return fmt.Errorf("volume %d (%q): size must be a multiple of 10240 MB (10 GB), got %d MB",
			vol.Number.ValueInt64(), vol.Name.ValueString(), vol.SizeMB.ValueInt64())
	}
	return nil
}

// ============================================================================
// VOLUME UPDATE LOGIC
// ============================================================================

// updateVolumes applies the volume diff. It mutates planVolumes in place, so
// even when it returns an error the entries processed so far carry their real
// IDs — the caller persists them to state.
func (r *ServerResource) updateVolumes(ctx context.Context, serverID string, stateVolumes []VolumeAttrModel, planVolumes *[]VolumeAttrModel) error {
	// Index state volumes by number
	stateVolumeByNumber := make(map[int64]VolumeAttrModel)
	for _, vol := range stateVolumes {
		stateVolumeByNumber[vol.Number.ValueInt64()] = vol
	}

	processedNumbers := make(map[int64]bool)

	// Process each planned volume
	for i := range *planVolumes {
		planVol := &(*planVolumes)[i]
		number := planVol.Number.ValueInt64()

		stateVol, exists := stateVolumeByNumber[number]

		if exists {
			// Volume exists - copy ID from state
			planVol.ID = stateVol.ID
			planVol.Created = stateVol.Created
			processedNumbers[number] = true

			volumeID := planVol.ID.ValueInt64()
			nameChanged := !planVol.Name.Equal(stateVol.Name)
			sizeChanged := !planVol.SizeMB.Equal(stateVol.SizeMB)

			if nameChanged || sizeChanged {
				newSize := int(planVol.SizeMB.ValueInt64())
				oldSize := int(stateVol.SizeMB.ValueInt64())

				// Validate size change
				if sizeChanged {
					if newSize < oldSize {
						return fmt.Errorf("volume %d (%q, ID %d): cannot decrease size from %d MB to %d MB",
							number, stateVol.Name.ValueString(), volumeID, oldSize, newSize)
					}
					if newSize%10240 != 0 {
						return fmt.Errorf("volume %d (%q, ID %d): size must be a multiple of 10240 MB (10 GB), got %d MB",
							number, stateVol.Name.ValueString(), volumeID, newSize)
					}
				}

				// Update volume
				updateReq := &entities.UpdateVolumeRequest{
					Name:   planVol.Name.ValueString(),
					SizeMB: newSize,
				}
				updatedVol, err := r.client.UpdateServerVolumeAndWait(ctx, serverID, int(volumeID), updateReq)
				if err != nil {
					return fmt.Errorf("failed to update volume %d (%q, ID %d): %w",
						number, stateVol.Name.ValueString(), volumeID, err)
				}
				planVol.Created = types.StringValue(updatedVol.Created)
			}
		} else {
			// New volume - create it
			createReq := &entities.CreateVolumeRequest{
				Name:   planVol.Name.ValueString(),
				SizeMB: int(planVol.SizeMB.ValueInt64()),
			}
			createdVol, err := r.client.CreateServerVolumeAndWait(ctx, serverID, createReq)
			if err != nil {
				return fmt.Errorf("failed to create volume %d (%q): %w", number, planVol.Name.ValueString(), err)
			}
			planVol.ID = types.Int64Value(int64(createdVol.ID))
			planVol.Created = types.StringValue(createdVol.Created)
			processedNumbers[number] = true
		}
	}

	// Check for deleted volumes (in state but not in plan)
	for number, stateVol := range stateVolumeByNumber {
		if !processedNumbers[number] {
			volName := stateVol.Name.ValueString()

			// Prevent deletion of boot volume
			if volName == "boot" {
				return fmt.Errorf("cannot remove boot volume (number=%d, ID=%d)", number, stateVol.ID.ValueInt64())
			}

			// Delete volume
			volumeID := stateVol.ID.ValueInt64()
			err := r.client.DeleteServerVolumeAndWait(ctx, serverID, int(volumeID))
			if err != nil {
				return fmt.Errorf("failed to delete volume %d (%q, ID %d): %w", number, volName, volumeID, err)
			}
		}
	}

	return nil
}

// ============================================================================
// VOLUME MAPPING & ORDERING
// ============================================================================

func (r *ServerResource) mapVolumesFromServer(serverVolumes []entities.Volume) []VolumeAttrModel {
	result := make([]VolumeAttrModel, len(serverVolumes))
	for i, vol := range serverVolumes {
		result[i] = VolumeAttrModel{
			// Number will be assigned later
			ID:      types.Int64Value(int64(vol.ID)),
			Name:    types.StringValue(vol.Name),
			SizeMB:  types.Int64Value(int64(vol.SizeMB)),
			Created: types.StringValue(vol.Created),
		}
	}
	return result
}

func (r *ServerResource) extractVolumeNumbers(volumes []VolumeAttrModel) map[int64]int64 {
	// Map: API ID -> Number
	numbersByID := make(map[int64]int64)
	for _, vol := range volumes {
		if !vol.ID.IsNull() && vol.ID.ValueInt64() != 0 && !vol.Number.IsNull() {
			numbersByID[vol.ID.ValueInt64()] = vol.Number.ValueInt64()
		}
	}
	return numbersByID
}

func (r *ServerResource) restoreAndAssignVolumeNumbers(volumes *[]VolumeAttrModel, numbersByID map[int64]int64, usedNumbers map[int64]bool) {
	// First restore known numbers
	for i := range *volumes {
		vol := &(*volumes)[i]
		if !vol.ID.IsNull() && vol.ID.ValueInt64() != 0 {
			apiID := vol.ID.ValueInt64()
			if number, exists := numbersByID[apiID]; exists {
				vol.Number = types.Int64Value(number)
			}
		}
	}

	// Then assign free numbers to new volumes
	nextFreeNumber := int64(0)
	for i := range *volumes {
		vol := &(*volumes)[i]
		if vol.Number.IsNull() || vol.Number.IsUnknown() {
			// Find next free number
			for usedNumbers[nextFreeNumber] {
				nextFreeNumber++
			}
			vol.Number = types.Int64Value(nextFreeNumber)
			usedNumbers[nextFreeNumber] = true
			nextFreeNumber++
		}
	}
}

func (r *ServerResource) getVolumeOrder(volumes []VolumeAttrModel) []int64 {
	order := make([]int64, len(volumes))
	for i, vol := range volumes {
		order[i] = vol.Number.ValueInt64()
	}
	return order
}

func (r *ServerResource) sortVolumesByPlanOrder(volumes []VolumeAttrModel, planOrder []int64) []VolumeAttrModel {
	volByNumber := make(map[int64]VolumeAttrModel)
	for _, vol := range volumes {
		volByNumber[vol.Number.ValueInt64()] = vol
	}

	result := make([]VolumeAttrModel, 0, len(planOrder))
	for _, num := range planOrder {
		if vol, exists := volByNumber[num]; exists {
			result = append(result, vol)
			delete(volByNumber, num)
		}
	}

	// Append any remaining volumes (new ones not in plan order)
	var remaining []VolumeAttrModel
	for _, vol := range volByNumber {
		remaining = append(remaining, vol)
	}
	sort.Slice(remaining, func(i, j int) bool {
		return remaining[i].Number.ValueInt64() < remaining[j].Number.ValueInt64()
	})
	result = append(result, remaining...)

	return result
}

func (r *ServerResource) buildSortedVolumeSpecs(volumes []VolumeAttrModel) []entities.VolumeSpec {
	planVolumeByNumber := make(map[int64]VolumeAttrModel)
	for _, vol := range volumes {
		planVolumeByNumber[vol.Number.ValueInt64()] = vol
	}

	// Sort numbers for deterministic API order
	sortedNumbers := make([]int64, 0, len(volumes))
	for num := range planVolumeByNumber {
		sortedNumbers = append(sortedNumbers, num)
	}
	sort.Slice(sortedNumbers, func(i, j int) bool {
		return sortedNumbers[i] < sortedNumbers[j]
	})

	specs := make([]entities.VolumeSpec, len(sortedNumbers))
	for i, num := range sortedNumbers {
		vol := planVolumeByNumber[num]
		specs[i] = entities.VolumeSpec{
			Name:   vol.Name.ValueString(),
			SizeMB: int(vol.SizeMB.ValueInt64()),
		}
	}

	return specs
}

// assignVolumeNumbersAfterCreate matches the volumes returned by the API to
// the plan volumes and copies their numbers. The API does not guarantee that
// volumes get IDs (or list positions) in the order of the create specs, so
// match by name and size; volumes with identical name and size are matched in
// API ID order.
func (r *ServerResource) assignVolumeNumbersAfterCreate(volumes *[]VolumeAttrModel, planVolumes []VolumeAttrModel) {
	// Deterministic base order for same-name duplicates
	sort.Slice(*volumes, func(i, j int) bool {
		return (*volumes)[i].ID.ValueInt64() < (*volumes)[j].ID.ValueInt64()
	})

	used := make([]bool, len(planVolumes))

	match := func(vol *VolumeAttrModel, checkSize bool) bool {
		for j := range planVolumes {
			if used[j] || !planVolumes[j].Name.Equal(vol.Name) {
				continue
			}
			if checkSize && !planVolumes[j].SizeMB.Equal(vol.SizeMB) {
				continue
			}
			vol.Number = planVolumes[j].Number
			used[j] = true
			return true
		}
		return false
	}

	var unmatched []*VolumeAttrModel

	// Pass 1: match by name + size
	for i := range *volumes {
		vol := &(*volumes)[i]
		if !match(vol, true) {
			unmatched = append(unmatched, vol)
		}
	}

	// Pass 2: match by name only (the API may adjust the size)
	for _, vol := range unmatched {
		if match(vol, false) {
			continue
		}

		// Fallback: pair remaining volumes with unused plan entries in order
		for j := range planVolumes {
			if !used[j] {
				vol.Number = planVolumes[j].Number
				used[j] = true
				break
			}
		}
	}
}
