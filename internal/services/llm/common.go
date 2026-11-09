// Package llm holds the resources and data sources of the LLM service: the
// project connection, groups, access keys, the model catalog and the credit
// packages.
package llm

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// stateSetter is the part of tfsdk.State a resource writes its state through.
type stateSetter interface {
	Set(ctx context.Context, val any) diag.Diagnostics
	SetAttribute(ctx context.Context, p path.Path, val any) diag.Diagnostics
}

// clientFrom takes the SDK client the provider configured.
func clientFrom(providerData any, diags *diag.Diagnostics) *sdk.CloudClient {
	if providerData == nil {
		return nil
	}
	client, ok := providerData.(*sdk.CloudClient)
	if !ok {
		diags.AddError("Unexpected Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", providerData))
		return nil
	}
	return client
}

func stringSet(values []string) types.Set {
	elems := make([]attr.Value, len(values))
	for i, v := range values {
		elems[i] = types.StringValue(v)
	}
	return types.SetValueMust(types.StringType, elems)
}

func stringList(values []string) types.List {
	elems := make([]attr.Value, len(values))
	for i, v := range values {
		elems[i] = types.StringValue(v)
	}
	return types.ListValueMust(types.StringType, elems)
}

// setStrings reads a set of strings; a null set reads as an empty, non-nil
// slice, which the API takes as "clear the set".
func setStrings(ctx context.Context, set types.Set, diags *diag.Diagnostics) []string {
	out := []string{}
	if set.IsNull() || set.IsUnknown() {
		return out
	}
	diags.Append(set.ElementsAs(ctx, &out, false)...)
	sort.Strings(out)
	return out
}

func modelIDSet(ids []entities.LLMModelID) types.Set {
	elems := make([]attr.Value, len(ids))
	for i, id := range ids {
		elems[i] = types.Int64Value(int64(id))
	}
	return types.SetValueMust(types.Int64Type, elems)
}

// setModelIDs reads a set of model IDs; a null set reads as an empty, non-nil slice.
func setModelIDs(ctx context.Context, set types.Set, diags *diag.Diagnostics) []entities.LLMModelID {
	out := []entities.LLMModelID{}
	if set.IsNull() || set.IsUnknown() {
		return out
	}
	var raw []int64
	diags.Append(set.ElementsAs(ctx, &raw, false)...)
	for _, id := range raw {
		out = append(out, entities.LLMModelID(id))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func optString(v *string) types.String {
	if v == nil {
		return types.StringNull()
	}
	return types.StringValue(*v)
}

// optNonEmptyString reads a string the API leaves out when it has no value.
func optNonEmptyString(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}

func optFloat(v *float64) types.Float64 {
	if v == nil {
		return types.Float64Null()
	}
	return types.Float64Value(*v)
}

func optInt(v *int) types.Int64 {
	if v == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*v))
}

func optBool(v *bool) types.Bool {
	if v == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*v)
}

func optTaxLabel(v *entities.LLMTaxLabel) types.String {
	if v == nil {
		return types.StringNull()
	}
	return types.StringValue(string(*v))
}
