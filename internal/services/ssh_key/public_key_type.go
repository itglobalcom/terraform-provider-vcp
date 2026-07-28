package ssh_key

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// PublicKeyType — a string type for OpenSSH public keys with semantic
// equality: surrounding whitespace is not significant. The backend trims the
// key on create (verified live: trailing "\n"/"\r\n" and surrounding spaces
// are stripped, the comment is preserved verbatim), so without semantic
// equality the ubiquitous `public_key = file("~/.ssh/id_ed25519.pub")` —
// which carries a trailing newline — would fail the apply with "Provider
// produced inconsistent result" and then plan a replace on every refresh.
// With it, the framework keeps the user's literal value whenever it is
// trim-equal to what the API returns.
type PublicKeyType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = PublicKeyType{}

func (t PublicKeyType) Equal(o attr.Type) bool {
	other, ok := o.(PublicKeyType)
	if !ok {
		return false
	}
	return t.StringType.Equal(other.StringType)
}

func (t PublicKeyType) String() string {
	return "ssh_key.PublicKeyType"
}

func (t PublicKeyType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return PublicKeyValue{StringValue: in}, nil
}

func (t PublicKeyType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
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

func (t PublicKeyType) ValueType(context.Context) attr.Value {
	return PublicKeyValue{}
}

// PublicKeyValue — an OpenSSH public key; comparison ignores surrounding
// whitespace. The comment field is significant (the backend stores it), so
// keys differing only in the comment are NOT equal and correctly plan a replace.
type PublicKeyValue struct {
	basetypes.StringValue
}

var (
	_ basetypes.StringValuable                   = PublicKeyValue{}
	_ basetypes.StringValuableWithSemanticEquals = PublicKeyValue{}
)

// NewPublicKeyValue creates a known public key value.
func NewPublicKeyValue(s string) PublicKeyValue {
	return PublicKeyValue{StringValue: basetypes.NewStringValue(s)}
}

func (v PublicKeyValue) Equal(o attr.Value) bool {
	other, ok := o.(PublicKeyValue)
	if !ok {
		return false
	}
	return v.StringValue.Equal(other.StringValue)
}

func (v PublicKeyValue) Type(context.Context) attr.Type {
	return PublicKeyType{}
}

// StringSemanticEquals tells the framework that two keys are equal if they
// match after trimming surrounding whitespace — mirroring exactly what the
// backend does to the stored key.
func (v PublicKeyValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	newValue, ok := newValuable.(PublicKeyValue)
	if !ok {
		diags.AddError(
			"Semantic Equality Check Error",
			fmt.Sprintf("Expected PublicKeyValue, got: %T. Please report this issue to the provider developers.", newValuable),
		)
		return false, diags
	}
	if v.IsNull() || v.IsUnknown() || newValue.IsNull() || newValue.IsUnknown() {
		return false, diags
	}
	return strings.TrimSpace(v.ValueString()) == strings.TrimSpace(newValue.ValueString()), diags
}
