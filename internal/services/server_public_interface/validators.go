package server_public_interface

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// multipleOf returns a validator "value is a multiple of n". The API only
// accepts bandwidth that is a multiple of 10 — we catch this at plan-time
// instead of a raw SDK error on apply.
func multipleOf(n int64) validator.Int64 {
	return multipleOfValidator{n: n}
}

type multipleOfValidator struct {
	n int64
}

func (v multipleOfValidator) Description(context.Context) string {
	return fmt.Sprintf("value must be a multiple of %d", v.n)
}

func (v multipleOfValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v multipleOfValidator) ValidateInt64(_ context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueInt64()%v.n != 0 {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Value",
			fmt.Sprintf("Value must be a multiple of %d, got: %d.", v.n, req.ConfigValue.ValueInt64()),
		)
	}
}
