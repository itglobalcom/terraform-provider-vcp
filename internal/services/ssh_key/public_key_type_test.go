package ssh_key

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestPublicKeySemanticEquals: keys are equal when they match after trimming
// surrounding whitespace (the backend strips it on create — verified live);
// body or comment differences are significant; null/unknown never compare equal.
func TestPublicKeySemanticEquals(t *testing.T) {
	ctx := context.Background()
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMGzAeuTRIIw1j98rmCsakvwX2FdYq5K8Wg/Jzkgpzj5 example@example.com"

	equal := [][2]string{
		{key, key},
		{key + "\n", key},   // trailing newline from file(...)
		{key + "\r\n", key}, // CRLF from a Windows-edited file
		{"  " + key + "  ", key},
		{key + "\n", "  " + key},
	}
	for _, c := range equal {
		ok, diags := NewPublicKeyValue(c[0]).StringSemanticEquals(ctx, NewPublicKeyValue(c[1]))
		if diags.HasError() {
			t.Fatalf("unexpected diags for %q vs %q: %v", c[0], c[1], diags)
		}
		if !ok {
			t.Errorf("expected %q == %q", c[0], c[1])
		}
	}

	notEqual := [][2]string{
		{key, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMGzAeuTRIIw1j98rmCsakvwX2FdYq5K8Wg/Jzkgpzj5 other@example.com"}, // comment differs — stored by the backend
		{key, "ssh-ed25519 BBBBC3NzaC1lZDI1NTE5AAAAIMGzAeuTRIIw1j98rmCsakvwX2FdYq5K8Wg/Jzkgpzj5 example@example.com"},
		{key, ""},
	}
	for _, c := range notEqual {
		ok, _ := NewPublicKeyValue(c[0]).StringSemanticEquals(ctx, NewPublicKeyValue(c[1]))
		if ok {
			t.Errorf("expected %q != %q", c[0], c[1])
		}
	}

	if ok, _ := (PublicKeyValue{StringValue: types.StringNull()}).StringSemanticEquals(ctx, NewPublicKeyValue(key)); ok {
		t.Error("null must not be semantically equal to a known value")
	}
	if ok, _ := (PublicKeyValue{StringValue: types.StringUnknown()}).StringSemanticEquals(ctx, NewPublicKeyValue(key)); ok {
		t.Error("unknown must not be semantically equal to a known value")
	}
}
