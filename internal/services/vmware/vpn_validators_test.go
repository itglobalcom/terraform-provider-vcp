package vmware

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The API answers the two IPsec enums in a spelling of its own (NET-5). Without
// a comparison that sees past it, every tunnel would report a difference it can
// never settle: the configuration says aes256, state says AES_256, and applying
// writes aes256 again.
func TestLooseEqual(t *testing.T) {
	same := [][2]string{
		{"aes256", "AES_256"},
		{"dh14", "DH14"},
		{"tripledes", "TRIPLE_DES"},
		{"aesgcm", "AES-GCM"},
		{"aes", "aes"},
	}
	for _, pair := range same {
		if !looseEqual(pair[0], pair[1]) {
			t.Errorf("looseEqual(%q, %q) = false, want true — same value, different spelling", pair[0], pair[1])
		}
	}

	// Seeing past punctuation must not go as far as seeing past the value.
	different := [][2]string{
		{"aes256", "aes"},
		{"dh14", "dh15"},
		{"aes", "aesgcm"},
		{"", "aes"},
	}
	for _, pair := range different {
		if looseEqual(pair[0], pair[1]) {
			t.Errorf("looseEqual(%q, %q) = true, want false — these are different values", pair[0], pair[1])
		}
	}
}

func TestKeepIfLooselyEqual(t *testing.T) {
	// The API's spelling of the configured value: keep what the user wrote.
	got := keepIfLooselyEqual(types.StringValue("aes256"), types.StringValue("AES_256"))
	if got.ValueString() != "aes256" {
		t.Errorf("got %q, want the configured spelling aes256", got.ValueString())
	}

	// A value the platform really changed: take it, so the difference shows.
	got = keepIfLooselyEqual(types.StringValue("aes256"), types.StringValue("AES"))
	if got.ValueString() != "AES" {
		t.Errorf("got %q, want the platform's value AES", got.ValueString())
	}

	// Nothing to compare against.
	got = keepIfLooselyEqual(types.StringValue("aes256"), types.StringNull())
	if got.ValueString() != "aes256" {
		t.Errorf("got %q, want the configured value kept when the API answered with none", got.ValueString())
	}
}

func TestLoosePlanModifier(t *testing.T) {
	modify := func(state, plan types.String) types.String {
		req := planmodifier.StringRequest{Path: path.Root("encryption_type"), StateValue: state, PlanValue: plan}
		resp := planmodifier.StringResponse{PlanValue: plan}
		loosePlanModifier{}.PlanModifyString(context.Background(), req, &resp)
		return resp.PlanValue
	}

	// An imported tunnel holds the API's spelling in state; planning the
	// documented one must not read as a change.
	if got := modify(types.StringValue("AES_256"), types.StringValue("aes256")); got.ValueString() != "AES_256" {
		t.Errorf("plan value = %q, want the state value kept (no difference)", got.ValueString())
	}
	// A real change stays a change.
	if got := modify(types.StringValue("AES_256"), types.StringValue("aes")); got.ValueString() != "aes" {
		t.Errorf("plan value = %q, want the planned value aes", got.ValueString())
	}
	// Nothing in state yet: leave the plan alone.
	if got := modify(types.StringNull(), types.StringValue("aes256")); got.ValueString() != "aes256" {
		t.Errorf("plan value = %q, want the planned value on create", got.ValueString())
	}
}

func TestVPNSharedKeyProblem(t *testing.T) {
	const valid = "Abcdefghijklmnopqrstuvwxyz012345" // 32 chars, all three classes

	if problem := vpnSharedKeyProblem(valid); problem != "" {
		t.Errorf("vpnSharedKeyProblem(valid key) = %q, want no problem", problem)
	}

	cases := map[string]string{
		"short":         "Abc123",
		"long":          "A" + strings.Repeat("b1", 70),
		"no upper case": "abcdefghijklmnopqrstuvwxyz012345",
		"no lower case": "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
		"no digit":      "Abcdefghijklmnopqrstuvwxyzabcdef",
		"punctuation":   "Abcdefghijklmnopqrstuvwxyz01234!",
		"empty":         "",
	}
	for why, key := range cases {
		if vpnSharedKeyProblem(key) == "" {
			t.Errorf("vpnSharedKeyProblem accepted a key with %s", why)
		}
	}
}

func TestIPv4OnlyValidator(t *testing.T) {
	check := func(value string) bool {
		req := validator.StringRequest{Path: path.Root("peer_endpoint"), ConfigValue: types.StringValue(value)}
		resp := validator.StringResponse{}
		ipv4Only().ValidateString(context.Background(), req, &resp)
		return !resp.Diagnostics.HasError()
	}

	if !check("203.0.113.10") {
		t.Error("a plain IPv4 address must be accepted")
	}
	for _, bad := range []string{"vpn.example.com", "203.0.113.0/24", "2001:db8::1", "", "203.0.113.256"} {
		if check(bad) {
			t.Errorf("%q must be refused as a tunnel endpoint", bad)
		}
	}
}

func TestIPv4CIDROnlyValidator(t *testing.T) {
	check := func(value string) (bool, string) {
		req := validator.StringRequest{Path: path.Root("peer_network"), ConfigValue: types.StringValue(value)}
		resp := validator.StringResponse{}
		ipv4CIDROnly().ValidateString(context.Background(), req, &resp)
		if !resp.Diagnostics.HasError() {
			return true, ""
		}
		return false, resp.Diagnostics.Errors()[0].Detail()
	}

	if ok, _ := check("192.168.10.0/24"); !ok {
		t.Error("a canonical IPv4 network must be accepted")
	}
	// A bare address is the likeliest slip, so the message has to name the fix.
	ok, detail := check("192.168.10.5")
	if ok {
		t.Fatal("a bare address must be refused where a network is expected")
	}
	if !strings.Contains(detail, "192.168.10.5/32") {
		t.Errorf("the message should suggest 192.168.10.5/32, got: %s", detail)
	}
	// So does a prefix that is not its own network address.
	ok, detail = check("192.168.10.5/24")
	if ok {
		t.Fatal("a non-canonical prefix must be refused")
	}
	if !strings.Contains(detail, "192.168.10.0/24") {
		t.Errorf("the message should suggest 192.168.10.0/24, got: %s", detail)
	}
}
