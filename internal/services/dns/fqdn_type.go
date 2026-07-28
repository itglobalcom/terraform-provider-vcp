package dns

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// FQDNType — a string type for DNS names with semantic equality: case and the
// trailing dot are not significant (the API canonicalizes names to lowercase + trailing dot).
// Thanks to semantic equality, the user's config ("WWW.Example.com") does not produce a
// diff against the API's canonical form ("www.example.com.") — the framework keeps the
// value from the plan/prior state when they are semantically equal. The value cannot be
// canonicalized by a plan modifier: Terraform core requires the plan to match
// the config for attributes that are set ("Provider produced invalid plan").
type FQDNType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = FQDNType{}

func (t FQDNType) Equal(o attr.Type) bool {
	other, ok := o.(FQDNType)
	if !ok {
		return false
	}
	return t.StringType.Equal(other.StringType)
}

func (t FQDNType) String() string {
	return "dns.FQDNType"
}

func (t FQDNType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return FQDNValue{StringValue: in}, nil
}

func (t FQDNType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	stringValue, ok := attrValue.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", attrValue)
	}
	stringValuable, diags := t.ValueFromString(ctx, stringValue)
	if diags.HasError() {
		return nil, fmt.Errorf("unexpected error converting StringValue to StringValuable: %v", diags)
	}
	return stringValuable, nil
}

func (t FQDNType) ValueType(context.Context) attr.Value {
	return FQDNValue{}
}

// FQDNValue — a DNS name value; comparison is case-insensitive and ignores the trailing dot.
type FQDNValue struct {
	basetypes.StringValue
}

var (
	_ basetypes.StringValuable                   = FQDNValue{}
	_ basetypes.StringValuableWithSemanticEquals = FQDNValue{}
)

// NewFQDNValue creates a known FQDN value.
func NewFQDNValue(s string) FQDNValue {
	return FQDNValue{StringValue: basetypes.NewStringValue(s)}
}

func (v FQDNValue) Equal(o attr.Value) bool {
	other, ok := o.(FQDNValue)
	if !ok {
		return false
	}
	return v.StringValue.Equal(other.StringValue)
}

func (v FQDNValue) Type(context.Context) attr.Type {
	return FQDNType{}
}

// StringSemanticEquals tells the framework that two names are equal if their
// canonical forms (lowercase + trailing dot) are equal.
func (v FQDNValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	newValue, ok := newValuable.(FQDNValue)
	if !ok {
		diags.AddError(
			"Semantic Equality Check Error",
			fmt.Sprintf("Expected FQDNValue, got: %T. Please report this issue to the provider developers.", newValuable),
		)
		return false, diags
	}
	if v.IsNull() || v.IsUnknown() || newValue.IsNull() || newValue.IsUnknown() {
		return false, diags
	}
	return normalizeName(v.ValueString()) == normalizeName(newValue.ValueString()), diags
}
