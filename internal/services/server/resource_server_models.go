package vstack_server

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ServerResourceModel describes the resource data model.
type ServerResourceModel struct {
	ID              types.String      `tfsdk:"id"`
	Name            types.String      `tfsdk:"name"`
	LocationID      types.String      `tfsdk:"location_id"`
	ImageID         types.String      `tfsdk:"image_id"`
	CPU             types.Int64       `tfsdk:"cpu"`
	RamMB           types.Int64       `tfsdk:"ram_mb"`
	SSHKeyIDs       []types.Int64     `tfsdk:"ssh_key_ids"`
	InitScript      types.String      `tfsdk:"init_script"`
	ApplicationsIDs []types.String    `tfsdk:"applications_ids"`
	Tags            types.Set         `tfsdk:"tags"`
	AffinityGroupID types.String      `tfsdk:"affinity_group_id"`
	Volumes         []VolumeAttrModel `tfsdk:"volumes"`

	// Computed attributes
	State     types.String `tfsdk:"state"`
	IsPowerOn types.Bool   `tfsdk:"is_power_on"`
	Created   types.String `tfsdk:"created"`
	Login     types.String `tfsdk:"login"`
	Password  types.String `tfsdk:"password"`
}

// VolumeAttrModel represents a server volume
type VolumeAttrModel struct {
	Number  types.Int64  `tfsdk:"number"`
	ID      types.Int64  `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	SizeMB  types.Int64  `tfsdk:"size_mb"`
	Created types.String `tfsdk:"created"`
}
