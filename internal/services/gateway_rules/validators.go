package gateway_rules

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Allowed values — exactly the API's, including case. The API also accepts
// lower case, but always answers with the canonical form, so accepting
// "tcp" here would produce a permanent diff after every refresh.
var (
	natTypes     = []string{entities.NATTypeSNAT, entities.NATTypeDNAT, entities.NATTypeBINAT}
	protocols    = []string{entities.ProtocolTCP, entities.ProtocolUDP, entities.ProtocolICMP, entities.ProtocolIP}
	fwActions    = []string{entities.FirewallActionAllow, entities.FirewallActionDeny}
	fwDirections = []string{entities.FirewallDirectionIn, entities.FirewallDirectionOut}
)

// portsMustBeAnyProtocols — protocols for which the API requires every port to
// be 0 ("any"): "The destination port must be set to 0 for the specified
// protocol".
var portsMustBeAnyProtocols = map[string]bool{
	entities.ProtocolICMP: true,
	entities.ProtocolIP:   true,
}

// rulesCountWarnThreshold — rule counts above this are untested against the
// API. 200 rules apply fine; 500 makes the backend task fail *and* leaves the
// gateway Busy for tens of minutes, so warn rather than let it happen quietly.
const rulesCountWarnThreshold = 200

// API error codes for a gateway that is busy with another change. Both are
// transient: the SDK already retries them a few times, and the resources wait
// for the gateway to become ready before writing, so hitting one here means
// something outside this apply grabbed the gateway in between.
const (
	apiCodeCompetitiveChange = -19803
	apiCodeObjectBusy        = -19605
)

// isGatewayBusyError reports a rejection caused by a concurrent change.
func isGatewayBusyError(err error) bool {
	return sdk.HasAPICode(err, apiCodeCompetitiveChange) ||
		sdk.HasAPICode(err, apiCodeObjectBusy) ||
		sdk.IsConflict(err)
}

// applyFailureHint turns the failure modes of a rule-set change into something
// the reader can act on. The rule set is replaced as a whole, so a failed change
// always leaves the previous one in place.
//
// The three cases are told apart without asking the SDK for a typed check, so
// that the provider keeps building against the released SDK: an HTTP-level error
// means the API refused the request, anything else means it accepted it and the
// backend task then failed.
func applyFailureHint(err error) string {
	const unchanged = "The rule set was left unchanged. "

	var reqErr *sdk.RequestError

	switch {
	case isGatewayBusyError(err):
		return unchanged + "The gateway is busy with another change — most often one made from the panel, " +
			"by another Terraform run, or by an operation of its own that has just failed. A gateway can stay " +
			"in that state for several minutes; wait and run terraform apply again."
	case errors.As(err, &reqErr):
		return unchanged + "The API rejected the request (see the message above) — this is a problem with the " +
			"rules themselves, so re-applying will not help. Note that `destination` of a `DNAT` rule and " +
			"`translated` of a `SNAT`/`BINAT` rule have to be the gateway's own external address."
	default:
		return unchanged + "The API accepted the change and the backend task then failed. That is usually " +
			"transient — the same rules normally apply on the next attempt, and one retry has already been " +
			"made — so run terraform apply again. If it keeps failing, check the number of rules: very large " +
			"sets are rejected this way."
	}
}

// ============================================================================
// Attribute validators
// ============================================================================

// cidrIPv4 validates an IPv4 CIDR in canonical form. The API rejects both a
// bare address ("10.0.0.5") and a non-canonical prefix ("10.0.0.5/24"), and it
// silently rewrites an accepted bare address to /32 — which would show up as a
// permanent diff. Catching it at plan time turns both into a clear error.
func cidrIPv4() validator.String { return cidrIPv4Validator{} }

type cidrIPv4Validator struct{}

func (cidrIPv4Validator) Description(context.Context) string {
	return "value must be an IPv4 network in CIDR notation, e.g. 10.0.0.0/24, 10.0.0.5/32 or 0.0.0.0/0"
}

func (v cidrIPv4Validator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (cidrIPv4Validator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()

	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		// A bare address is the most common mistake (e.g. an ip_address
		// attribute of another resource) — point straight at the fix.
		if addr, addrErr := netip.ParseAddr(s); addrErr == nil && addr.Is4() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR Address",
				fmt.Sprintf("Expected an IPv4 network in CIDR notation, got a bare address %q. Use %q for a single host.", s, s+"/32"))
			return
		}
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR Address",
			fmt.Sprintf("Expected an IPv4 network in CIDR notation (e.g. 10.0.0.0/24, 10.0.0.5/32, 0.0.0.0/0), got: %q.", s))
		return
	}
	if !prefix.Addr().Is4() {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR Address",
			fmt.Sprintf("The API supports IPv4 only, got: %q.", s))
		return
	}
	if prefix.Masked() != prefix {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR Address",
			fmt.Sprintf("The address must be the network address of its prefix, got: %q. Use %q for the network or %q for the single host.",
				s, prefix.Masked().String(), prefix.Addr().String()+"/32"))
	}
}

// ipv4Address validates a bare IPv4 address. The `translated` field of a NAT
// rule takes no mask — a CIDR there is rejected by the API.
func ipv4Address() validator.String { return ipv4AddressValidator{} }

type ipv4AddressValidator struct{}

func (ipv4AddressValidator) Description(context.Context) string {
	return "value must be an IPv4 address without a mask, e.g. 10.0.0.5"
}

func (v ipv4AddressValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (ipv4AddressValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()

	addr, err := netip.ParseAddr(s)
	if err != nil {
		if prefix, prefixErr := netip.ParsePrefix(s); prefixErr == nil {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP Address",
				fmt.Sprintf("Expected an IPv4 address without a mask, got a CIDR %q. Use %q.", s, prefix.Addr().String()))
			return
		}
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP Address",
			fmt.Sprintf("Expected an IPv4 address without a mask (e.g. 10.0.0.5), got: %q.", s))
		return
	}
	if !addr.Is4() {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP Address",
			fmt.Sprintf("The API supports IPv4 only, got: %q.", s))
	}
}

// ============================================================================
// Cross-field rule validation (ValidateConfig)
// ============================================================================

// configRules pulls the `rules` list out of a resource configuration for
// cross-field validation. It reports false when the list is absent or not known
// yet (e.g. built from another resource's attributes) — validation then simply
// does not run, and the per-attribute validators still apply.
func configRules[T any](ctx context.Context, config tfsdk.Config) ([]T, bool) {
	var list types.List
	if diags := config.GetAttribute(ctx, path.Root("rules"), &list); diags.HasError() {
		return nil, false
	}
	if list.IsNull() || list.IsUnknown() {
		return nil, false
	}
	var rules []T
	if diags := list.ElementsAs(ctx, &rules, false); diags.HasError() {
		return nil, false
	}
	return rules, true
}

// checkPortIsAny reports a port that must be 0 but isn't. Null and unknown are
// skipped: a port may reference a value that is only known after another
// resource is created, and a missing port defaults to 0 anyway.
func checkPortIsAny(port types.Int64, rulePath path.Path, attr, why string, diags *diag.Diagnostics) {
	if port.IsNull() || port.IsUnknown() || port.ValueInt64() == 0 {
		return
	}
	diags.AddAttributeError(rulePath.AtName(attr), "Port Must Be Any",
		fmt.Sprintf("%s, so %s must be 0 (any), got: %d.", why, attr, port.ValueInt64()))
}

// validateNATRules mirrors the API's cross-field checks so they surface in
// terraform plan instead of half-way through apply.
func validateNATRules(rules []natRuleModel, diags *diag.Diagnostics) {
	warnRuleCount(len(rules), "NAT", diags)

	for i, rule := range rules {
		rulePath := path.Root("rules").AtListIndex(i)

		if !rule.Protocol.IsNull() && !rule.Protocol.IsUnknown() && portsMustBeAnyProtocols[rule.Protocol.ValueString()] {
			why := fmt.Sprintf("protocol is %q", rule.Protocol.ValueString())
			checkPortIsAny(rule.DestinationPort, rulePath, "destination_port", why, diags)
			checkPortIsAny(rule.TranslatedPort, rulePath, "translated_port", why, diags)
		}

		// BINAT is a 1:1 translation — the API rejects any port on it.
		if !rule.Type.IsNull() && !rule.Type.IsUnknown() && rule.Type.ValueString() == entities.NATTypeBINAT {
			why := `type is "BINAT" (1:1 translation)`
			checkPortIsAny(rule.DestinationPort, rulePath, "destination_port", why, diags)
			checkPortIsAny(rule.TranslatedPort, rulePath, "translated_port", why, diags)
		}
	}
}

func validateFirewallRules(rules []firewallRuleModel, diags *diag.Diagnostics) {
	warnRuleCount(len(rules), "firewall", diags)

	for i, rule := range rules {
		rulePath := path.Root("rules").AtListIndex(i)

		if !rule.Protocol.IsNull() && !rule.Protocol.IsUnknown() && portsMustBeAnyProtocols[rule.Protocol.ValueString()] {
			why := fmt.Sprintf("protocol is %q", rule.Protocol.ValueString())
			checkPortIsAny(rule.SourcePort, rulePath, "source_port", why, diags)
			checkPortIsAny(rule.DestinationPort, rulePath, "destination_port", why, diags)
		}
	}
}

func warnRuleCount(n int, kind string, diags *diag.Diagnostics) {
	if n <= rulesCountWarnThreshold {
		return
	}
	diags.AddAttributeWarning(path.Root("rules"), "Large Rule Set",
		fmt.Sprintf("This configuration declares %d %s rules. Lists of up to %d rules are known to apply cleanly; "+
			"much larger ones (500 have been observed) make the backend task fail without applying anything and can "+
			"leave the gateway Busy for tens of minutes.", n, kind, rulesCountWarnThreshold))
}
