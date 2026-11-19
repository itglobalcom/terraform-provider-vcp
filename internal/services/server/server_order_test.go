package vstack_server

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// orderError is one error ValidateConfig reports: its summary and, for an
// attribute error, the attribute; an error without a path has an empty one.
type orderError struct {
	summary string
	path    string
}

func orderErrors(t *testing.T, model ServerResourceModel) []orderError {
	t.Helper()
	resp := validateModel(t, model)
	var got []orderError
	for _, d := range resp.Diagnostics.Errors() {
		e := orderError{summary: d.Summary()}
		if withPath, ok := d.(interface{ Path() path.Path }); ok {
			e.path = withPath.Path().String()
		}
		got = append(got, e)
	}
	return got
}

// TestServerOrderRequiresItsArguments pins the order arguments that are no
// longer Required in the schema: leaving one out must still be refused at plan
// time, on that attribute, rather than reach the API as an order without a
// location, an image, a size or a disk. A value not yet known at plan time is
// set and must not be refused.
func TestServerOrderRequiresItsArguments(t *testing.T) {
	missing := func(arg string) []orderError {
		return []orderError{{summary: "Missing Attribute", path: arg}}
	}

	cases := map[string]struct {
		edit func(*ServerResourceModel)
		want []orderError
	}{
		"complete order": {
			edit: func(*ServerResourceModel) {},
		},
		"without location_id": {
			edit: func(m *ServerResourceModel) { m.LocationID = types.StringNull() },
			want: missing("location_id"),
		},
		"without image_id": {
			edit: func(m *ServerResourceModel) { m.ImageID = types.StringNull() },
			want: missing("image_id"),
		},
		"without cpu": {
			edit: func(m *ServerResourceModel) { m.CPU = types.Int64Null() },
			want: missing("cpu"),
		},
		"without ram_mb": {
			edit: func(m *ServerResourceModel) { m.RamMB = types.Int64Null() },
			want: missing("ram_mb"),
		},
		// A null volumes is a missing argument, not a missing boot volume:
		// the user is told which argument to add, once.
		"without volumes": {
			edit: func(m *ServerResourceModel) { m.Volumes = nil },
			want: missing("volumes"),
		},
		// An empty list is set: the argument is there, the boot volume is not.
		"empty volumes": {
			edit: func(m *ServerResourceModel) { m.Volumes = []VolumeAttrModel{} },
			want: []orderError{{summary: "Missing Boot Volume"}},
		},
		"volumes without a boot volume": {
			edit: func(m *ServerResourceModel) { m.Volumes[0].Name = types.StringValue("data") },
			want: []orderError{{summary: "Missing Boot Volume"}},
		},
		"nothing of the order": {
			edit: func(m *ServerResourceModel) {
				m.LocationID, m.ImageID = types.StringNull(), types.StringNull()
				m.CPU, m.RamMB = types.Int64Null(), types.Int64Null()
				m.Volumes = nil
			},
			want: []orderError{
				{summary: "Missing Attribute", path: "location_id"},
				{summary: "Missing Attribute", path: "image_id"},
				{summary: "Missing Attribute", path: "cpu"},
				{summary: "Missing Attribute", path: "ram_mb"},
				{summary: "Missing Attribute", path: "volumes"},
			},
		},
		"values known only at apply": {
			edit: func(m *ServerResourceModel) {
				m.LocationID, m.ImageID = types.StringUnknown(), types.StringUnknown()
				m.CPU, m.RamMB = types.Int64Unknown(), types.Int64Unknown()
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			model := orderModel()
			tc.edit(&model)

			got := orderErrors(t, model)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("errors %v, want %v", got, tc.want)
			}
		})
	}
}
