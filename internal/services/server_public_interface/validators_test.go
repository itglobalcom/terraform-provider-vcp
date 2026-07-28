package server_public_interface

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestMultipleOf covers the custom bandwidth validator: the API only accepts
// multiples of 10, and null/unknown must be skipped (they are only knowable at
// apply time). Note that 0 passes here on purpose — it IS a multiple of 10; the
// lower bound is enforced by the int64validator.AtLeast(10) next to it in the
// schema.
func TestMultipleOf(t *testing.T) {
	v := multipleOf(10)

	cases := []struct {
		name    string
		value   types.Int64
		wantErr bool
	}{
		{"multiple", types.Int64Value(100), false},
		{"exactly the divisor", types.Int64Value(10), false},
		{"zero is a multiple", types.Int64Value(0), false},
		{"not a multiple", types.Int64Value(105), true},
		{"off by one", types.Int64Value(11), true},
		{"negative multiple", types.Int64Value(-20), false},
		{"negative non-multiple", types.Int64Value(-25), true},
		{"null is skipped", types.Int64Null(), false},
		{"unknown is skipped", types.Int64Unknown(), false},
	}

	for _, c := range cases {
		resp := &validator.Int64Response{}
		v.ValidateInt64(context.Background(), validator.Int64Request{
			Path:        path.Root("bandwidth_mbps"),
			ConfigValue: c.value,
		}, resp)

		if got := resp.Diagnostics.HasError(); got != c.wantErr {
			t.Errorf("%s: value %v → HasError()=%v, want %v (diags: %v)",
				c.name, c.value, got, c.wantErr, resp.Diagnostics)
		}
	}

	// The description is user-facing (shown in docs and errors).
	if d := v.Description(context.Background()); d != "value must be a multiple of 10" {
		t.Errorf("unexpected description: %q", d)
	}
}
