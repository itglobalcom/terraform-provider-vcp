package vmware

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Enum values of an IPsec tunnel, in the spelling the contract documents. The
// API answers in another one — `aes256` comes back as `AES_256` and `dh14` as
// `DH14` (NET-5) — which is what looseEqual and loosePlanModifier are for.
var (
	vpnEncryptionTypes = []string{
		entities.VmwareEdgeVPNEncryptionAES,
		entities.VmwareEdgeVPNEncryptionAES256,
		entities.VmwareEdgeVPNEncryptionAESGCM,
		entities.VmwareEdgeVPNEncryptionTripDES,
	}
	vpnDiffieHellmanGroups = []string{
		entities.VmwareEdgeVPNDiffieHellmanGroup2,
		entities.VmwareEdgeVPNDiffieHellmanGroup5,
		entities.VmwareEdgeVPNDiffieHellmanGroup14,
		entities.VmwareEdgeVPNDiffieHellmanGroup15,
		entities.VmwareEdgeVPNDiffieHellmanGroup16,
	}
)

// looseEqual compares two spellings of the same enum value: case is ignored and
// so is anything that is not a letter or a digit. "aes256" and "AES_256" are the
// same value written twice; "aes256" and "aes" are not.
func looseEqual(a, b string) bool {
	return normalizeEnum(a) == normalizeEnum(b)
}

func normalizeEnum(s string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// keepIfLooselyEqual returns the configured value when the API answered with the
// same value in its own spelling, and the API's value when it answered with a
// different one. Without it a tunnel would diff for ever against the spelling it
// was written in; with it, a value the platform really did change still shows.
func keepIfLooselyEqual(configured, fresh types.String) types.String {
	if fresh.IsNull() || !isSet(configured) {
		return configured
	}
	if looseEqual(configured.ValueString(), fresh.ValueString()) {
		return configured
	}
	return fresh
}

// loosePlanModifier suppresses a plan difference between two spellings of one
// enum value — the plan-time half of keepIfLooselyEqual, for the case where state
// holds the API's spelling because the tunnel was imported.
type loosePlanModifier struct{}

func (loosePlanModifier) Description(context.Context) string {
	return "Suppresses a difference that is only a matter of spelling (the API answers aes256 as AES_256, NET-5)."
}

func (m loosePlanModifier) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (loosePlanModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if looseEqual(req.StateValue.ValueString(), req.PlanValue.ValueString()) {
		resp.PlanValue = req.StateValue
	}
}

// ============================================================================
// Attribute validators
// ============================================================================

// ipv4Only validates a bare IPv4 address. A tunnel endpoint takes no hostname
// and no mask, and the API's refusal says neither.
func ipv4Only() validator.String { return ipv4OnlyValidator{} }

type ipv4OnlyValidator struct{}

func (ipv4OnlyValidator) Description(context.Context) string {
	return "value must be an IPv4 address, e.g. 203.0.113.10"
}

func (v ipv4OnlyValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (ipv4OnlyValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !isSet(req.ConfigValue) {
		return
	}
	s := req.ConfigValue.ValueString()

	addr, err := netip.ParseAddr(s)
	switch {
	case err != nil:
		if prefix, prefixErr := netip.ParsePrefix(s); prefixErr == nil {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP Address",
				fmt.Sprintf("Expected an IPv4 address without a mask, got a network %q. Use %q.", s, prefix.Addr().String()))
			return
		}
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP Address",
			fmt.Sprintf("Expected an IPv4 address (203.0.113.10), got: %q. A hostname is not accepted here.", s))
	case !addr.Is4():
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP Address",
			fmt.Sprintf("The API supports IPv4 only, got: %q.", s))
	}
}

// ipv4CIDROnly validates an IPv4 network in canonical form.
func ipv4CIDROnly() validator.String { return ipv4CIDROnlyValidator{} }

type ipv4CIDROnlyValidator struct{}

func (ipv4CIDROnlyValidator) Description(context.Context) string {
	return "value must be an IPv4 network in CIDR notation, e.g. 192.168.10.0/24"
}

func (v ipv4CIDROnlyValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (ipv4CIDROnlyValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !isSet(req.ConfigValue) {
		return
	}
	s := req.ConfigValue.ValueString()

	prefix, err := netip.ParsePrefix(s)
	switch {
	case err != nil:
		if addr, addrErr := netip.ParseAddr(s); addrErr == nil && addr.Is4() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR Address",
				fmt.Sprintf("Expected an IPv4 network in CIDR notation, got a bare address %q. Use %q for a single host.",
					s, s+"/32"))
			return
		}
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR Address",
			fmt.Sprintf("Expected an IPv4 network in CIDR notation (192.168.10.0/24), got: %q.", s))
	case !prefix.Addr().Is4():
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR Address",
			fmt.Sprintf("The API supports IPv4 only, got: %q.", s))
	case prefix.Masked() != prefix:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR Address",
			fmt.Sprintf("The address must be the network address of its prefix, got: %q. Use %q.",
				s, prefix.Masked().String()))
	}
}

// vpnSharedKey mirrors the backend's rule for a pre-shared key (error -12013),
// whose message names neither the length nor the character classes it wants.
func vpnSharedKey() validator.String { return vpnSharedKeyValidator{} }

type vpnSharedKeyValidator struct{}

func (vpnSharedKeyValidator) Description(context.Context) string {
	return fmt.Sprintf("value must be %d–%d alphanumeric characters with at least one upper-case letter, "+
		"one lower-case letter and one digit",
		entities.VmwareEdgeVPNSharedKeyMinLength, entities.VmwareEdgeVPNSharedKeyMaxLength)
}

func (v vpnSharedKeyValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (vpnSharedKeyValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !isSet(req.ConfigValue) {
		return
	}
	if problem := vpnSharedKeyProblem(req.ConfigValue.ValueString()); problem != "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Pre-Shared Key", problem)
	}
}

// vpnSharedKeyProblem reports why a key is unacceptable, or "" when it is fine.
func vpnSharedKeyProblem(key string) string {
	if l := len(key); l < entities.VmwareEdgeVPNSharedKeyMinLength || l > entities.VmwareEdgeVPNSharedKeyMaxLength {
		return fmt.Sprintf("The key must be between %d and %d characters long, got %d.",
			entities.VmwareEdgeVPNSharedKeyMinLength, entities.VmwareEdgeVPNSharedKeyMaxLength, l)
	}

	var hasUpper, hasLower, hasDigit bool
	for _, r := range key {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			return "The key must be alphanumeric — no punctuation, spaces or symbols."
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return "The key must contain at least one upper-case letter, one lower-case letter and one digit."
	}
	return ""
}
