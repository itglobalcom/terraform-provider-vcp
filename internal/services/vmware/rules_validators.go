package vmware

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Allowed values — exactly the API's. NET-10: the API normalizes these enums to
// lower case on read and accepts them case-insensitively on write, so the
// constants round-trip byte for byte and no normalisation is needed on this side.
var (
	edgeFirewallActions   = []string{entities.VmwareEdgeFirewallActionAllow, entities.VmwareEdgeFirewallActionDeny}
	edgeNATTypes          = []string{entities.VmwareEdgeNATTypeDNAT, entities.VmwareEdgeNATTypeSNAT}
	ruleProtocols         = []string{entities.VmwareEdgeFirewallProtocolAny, "tcp", "udp", "icmp"}
	serverFirewallActions = []string{entities.VmwareFirewallActionAllow, entities.VmwareFirewallActionDeny}
	serverDirections      = []string{entities.VmwareTrafficDirectionIncoming, entities.VmwareTrafficDirectionOutgoing}
)

// rulesCountWarnThreshold — the platform re-pushes the whole set of edge objects
// to vCloud on every change, so a large set turns one apply into a long backend
// task. There is no measured hard limit for the VMware edge (unlike the vStack
// gateway), so this is a warning, not a check.
const rulesCountWarnThreshold = 100

// isEdgeBusyError reports a rejection caused by a change that is still running on
// the same object. The API serializes mutations per object and answers -4000 for
// the second one; the SDK retries it a few times on its own, so seeing it here
// means something outside this apply is holding the edge.
func isEdgeBusyError(err error) bool {
	return sdk.IsConflict(err)
}

// applyFailureHint turns the failure modes of a rule-set change into something the
// reader can act on. subject names the object in the message ("edge firewall",
// "server firewall").
//
// The cases are told apart without a typed SDK check: an HTTP-level error means
// the API refused the request, anything else means it accepted it and the backend
// task then failed.
func applyFailureHint(err error, subject string) string {
	var reqErr *sdk.RequestError

	switch {
	case isEdgeBusyError(err):
		return "The API is still applying another change to this object — most often one made from the panel, " +
			"by another Terraform run, or by an operation of its own that has just failed. Wait and run " +
			"terraform apply again."
	case errors.As(err, &reqErr):
		return fmt.Sprintf("The API rejected the request (see the message above) — this is a problem with the %s "+
			"rules themselves, so re-applying will not help.", subject)
	default:
		return "The API accepted the change and the backend task then failed. That is usually transient, so run " +
			"terraform apply again. If it keeps failing, check the number of rules: the platform re-pushes the " +
			"whole set to vCloud on every change and very large sets time out."
	}
}

// ============================================================================
// Attribute validators
// ============================================================================

// vmwareRuleAddress validates the source/destination of a rule. The edge and the
// server firewall accept the same four forms: the "any" wildcard, a bare IPv4
// address, an IPv4 network in CIDR notation, and an inclusive IPv4 range.
func vmwareRuleAddress() validator.String { return ruleAddressValidator{} }

type ruleAddressValidator struct{}

func (ruleAddressValidator) Description(context.Context) string {
	return `value must be "any", an IPv4 address (10.0.0.5), an IPv4 network (10.0.0.0/24) or an IPv4 range (10.0.0.5-10.0.0.9)`
}

func (v ruleAddressValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v ruleAddressValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if problem := ruleAddressProblem(s); problem != "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Rule Address", problem)
	}
}

// ruleAddressProblem reports why s is not an acceptable rule address, or "" when
// it is. The result is diagnostic prose shown to the user rather than an error
// for a caller to inspect, which is why it is a sentence and not an error value.
func ruleAddressProblem(s string) string {
	if s == entities.VmwareFirewallAny {
		return ""
	}
	if s == "" {
		return fmt.Sprintf("Expected %q, an IPv4 address, network or range, got an empty string. "+
			"Omit the attribute instead.", entities.VmwareFirewallAny)
	}

	// Range: two addresses, low first.
	if lo, hi, ok := strings.Cut(s, "-"); ok {
		loAddr, loErr := netip.ParseAddr(lo)
		hiAddr, hiErr := netip.ParseAddr(hi)
		switch {
		case loErr != nil || hiErr != nil || !loAddr.Is4() || !hiAddr.Is4():
			return fmt.Sprintf("Expected an IPv4 range of two addresses (10.0.0.5-10.0.0.9), got: %q.", s)
		case hiAddr.Less(loAddr):
			return fmt.Sprintf("The range must start with the lower address, got: %q. Use %q.", s, hi+"-"+lo)
		}
		return ""
	}

	// Network in CIDR notation: must be the network address of its prefix, or the
	// API stores something other than what the configuration says.
	if strings.Contains(s, "/") {
		prefix, err := netip.ParsePrefix(s)
		switch {
		case err != nil || !prefix.Addr().Is4():
			return fmt.Sprintf("Expected an IPv4 network in CIDR notation (10.0.0.0/24), got: %q.", s)
		case prefix.Masked() != prefix:
			return fmt.Sprintf("The address must be the network address of its prefix, got: %q. "+
				"Use %q for the network or %q for the single host.",
				s, prefix.Masked().String(), prefix.Addr().String())
		}
		return ""
	}

	addr, err := netip.ParseAddr(s)
	switch {
	case err != nil:
		return fmt.Sprintf("Expected %q, an IPv4 address (10.0.0.5), network (10.0.0.0/24) or range "+
			"(10.0.0.5-10.0.0.9), got: %q.", entities.VmwareFirewallAny, s)
	case !addr.Is4():
		return fmt.Sprintf("The API supports IPv4 only, got: %q.", s)
	}
	return ""
}

// vmwareRulePort validates a port specification. Ports are strings here because
// that is what the API takes: besides a single port it accepts the "any"
// wildcard, an inclusive range, and a comma-separated list of either.
func vmwareRulePort() validator.String { return rulePortValidator{} }

type rulePortValidator struct{}

func (rulePortValidator) Description(context.Context) string {
	return `value must be "any", a port (443), a range (1000-2000) or a comma-separated list of those`
}

func (v rulePortValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v rulePortValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if problem := rulePortProblem(req.ConfigValue.ValueString()); problem != "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Rule Port", problem)
	}
}

// rulePortProblem reports why s is not an acceptable port specification, or ""
// when it is — diagnostic prose, like ruleAddressProblem.
func rulePortProblem(s string) string {
	if s == entities.VmwareFirewallAny {
		return ""
	}
	if s == "" {
		return fmt.Sprintf("Expected %q, a port or a range, got an empty string. Omit the attribute instead.",
			entities.VmwareFirewallAny)
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		loPort, loErr := parsePort(lo)
		if loErr != nil {
			return fmt.Sprintf("Expected %q, a port (443), a range (1000-2000) or a comma-separated list "+
				"of those, got: %q.", entities.VmwareFirewallAny, s)
		}
		if !isRange {
			continue
		}
		hiPort, hiErr := parsePort(hi)
		if hiErr != nil {
			return fmt.Sprintf("Expected a port range of two ports (1000-2000), got: %q.", part)
		}
		if hiPort < loPort {
			return fmt.Sprintf("The range must start with the lower port, got: %q.", part)
		}
	}
	return ""
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port out of range: %d", n)
	}
	return n, nil
}

// ============================================================================
// Applicability of an edge feature to a network type
// ============================================================================

// checkEdgeCapableNetwork verifies that the network carries an edge that supports
// the feature, and reports a sentence naming the network type when it does not.
// Which features apply depends on the flavour of the network, and the API answers
// an inapplicable request with an error that does not mention the type.
//
// A failure to read the network is not treated as a failure of the check: this is
// a courtesy pre-flight, and refusing to apply because a read did not go through
// would be worse than letting the API answer. Returns false only when the network
// was read and does not support the feature.
func checkEdgeCapableNetwork(ctx context.Context, client *sdk.CloudClient, networkID int, feature string,
	allowed []string, diags *diag.Diagnostics) bool {
	network, err := client.GetVmwareNetwork(ctx, networkID)
	if err != nil {
		return true
	}
	coarse := coarseNetworkType(network.Type)
	for _, ok := range allowed {
		if coarse == ok {
			return true
		}
	}
	diags.AddAttributeError(path.Root("network_id"), "Feature Not Available On This Network",
		fmt.Sprintf("Network %d (%q) is a %s network, and %s applies to %s networks only. "+
			"Only a network with an edge of its own carries these objects.",
			networkID, network.Name, coarse, feature, strings.Join(allowed, "/")))
	return false
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

// isSet reports whether a string attribute carries a value the provider can act
// on. Unknown counts as "not set": it may only be resolvable after another
// resource is created, and validating it would reject a valid configuration.
func isSet(v types.String) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// isSetInt is isSet for an integer attribute.
func isSetInt(v types.Int64) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// isDeclared reports whether the configuration mentions an attribute at all,
// whatever its value turns out to be.
//
// This is the opposite reading of unknown from isSet, and the two are not
// interchangeable: a check on the *value* has to skip an unknown one, while a
// check on the *presence* has to count it. A size derived from a catalog data
// source is unknown until the data source is read, and treating that as "not
// declared" would report a required argument missing from a configuration that
// plainly has it.
func isDeclared(v attr.Value) bool { return !v.IsNull() }

// validateEdgeNATRules mirrors the rules the API enforces on a NAT rule, plus the
// one it does not: NET-4, where original_ip of a DNAT rule is silently replaced
// with the external address of the edge. Reporting that at plan time is better
// than letting the apply finish and then failing with "inconsistent result".
func validateEdgeNATRules(rules []edgeNATRuleModel, diags *diag.Diagnostics) {
	warnRuleCount(len(rules), "NAT", diags)

	for i, rule := range rules {
		rulePath := path.Root("rules").AtListIndex(i)
		if !isSet(rule.Type) {
			continue
		}

		switch rule.Type.ValueString() {
		case entities.VmwareEdgeNATTypeDNAT:
			// NET-4: whatever the request carries here, the backend stores the edge's
			// own external address. Accepting the value would mean either a silent
			// substitution or an "inconsistent result after apply" at the end of a
			// long task.
			if isSet(rule.OriginalIP) {
				diags.AddAttributeError(rulePath.AtName("original_ip"), "Attribute Not Applicable To DNAT",
					"The platform always translates from the external address of the edge and replaces whatever "+
						"original_ip carries (API defect NET-4). Leave the attribute unset — the address the platform "+
						"chose is recorded in state after apply.")
			}
			if !isSet(rule.TranslatedIP) {
				diags.AddAttributeError(rulePath.AtName("translated_ip"), "Missing Attribute",
					"A DNAT rule translates an inbound connection to an address inside the network, so translated_ip "+
						"is required — it is the private address of the target server.")
			}
		case entities.VmwareEdgeNATTypeSNAT:
			if !isSet(rule.OriginalIP) {
				diags.AddAttributeError(rulePath.AtName("original_ip"), "Missing Attribute",
					"An SNAT rule translates traffic leaving the network, so original_ip is required — it is the "+
						"private address or subnet being translated.")
			}
		}
	}
}

// validateServerFirewallRules checks what the API requires of a server firewall
// rule but reports only as a bare -2002 ("the request body is not formatted"),
// which names neither the field nor the value (SRV-1).
func validateServerFirewallRules(rules []serverFirewallRuleModel, diags *diag.Diagnostics) {
	warnRuleCount(len(rules), "firewall", diags)

	seen := make(map[string]int, len(rules))
	for i, rule := range rules {
		if !isSet(rule.Name) {
			continue
		}
		name := rule.Name.ValueString()
		if first, dup := seen[name]; dup {
			diags.AddAttributeError(path.Root("rules").AtListIndex(i).AtName("name"), "Duplicate Rule Name",
				fmt.Sprintf("Rule %q is already declared at rules[%d]. The API identifies a rule by its name, so two "+
					"rules cannot share one.", name, first))
			continue
		}
		seen[name] = i
	}
}

// validateEdgeFirewallRules checks the edge firewall rule set.
func validateEdgeFirewallRules(rules []edgeFirewallRuleModel, diags *diag.Diagnostics) {
	warnRuleCount(len(rules), "firewall", diags)
}

func warnRuleCount(n int, kind string, diags *diag.Diagnostics) {
	if n <= rulesCountWarnThreshold {
		return
	}
	diags.AddAttributeWarning(path.Root("rules"), "Large Rule Set",
		fmt.Sprintf("This configuration declares %d %s rules. The platform re-pushes the whole set to vCloud on "+
			"every change, so sets much larger than %d turn each apply into a long backend task and can time out.",
			n, kind, rulesCountWarnThreshold))
}
