package gateway_network_attachment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"

	gateway_attachment "github.com/itglobalcom/terraform-provider-vcp/internal/services/gateway_network_attachment"
)

// TestGatewayAttachmentSchema_ForceNewFields guards the replace semantics: both
// endpoints identify the NIC, so changing either must recreate the resource.
// Without RequiresReplace Terraform would call Update — a deliberate no-op here
// — leaving the old NIC in place while reporting the new config as applied.
func TestGatewayAttachmentSchema_ForceNewFields(t *testing.T) {
	var resp resource.SchemaResponse
	gateway_attachment.NewResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	for _, name := range []string{"gateway_id", "network_id"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Errorf("schema has no %q attribute", name)
			continue
		}
		sa, ok := attr.(schema.StringAttribute)
		if !ok {
			t.Errorf("%q is %T, expected schema.StringAttribute", name, attr)
			continue
		}
		if !hasRequiresReplace(sa) {
			t.Errorf("%q must have stringplanmodifier.RequiresReplace()", name)
		}
	}

	// Negative control: `ip_address` is purely computed (UseStateForUnknown
	// only), so a detector that matched everything would be caught here.
	if ip, ok := resp.Schema.Attributes["ip_address"].(schema.StringAttribute); ok {
		if hasRequiresReplace(ip) {
			t.Error(`"ip_address" must NOT have RequiresReplace (the detector is matching everything)`)
		}
		if len(ip.PlanModifiers) == 0 {
			t.Error(`"ip_address" is expected to carry UseStateForUnknown; the negative control is vacuous`)
		}
	} else {
		t.Error(`schema has no "ip_address" string attribute for the negative control`)
	}
}

// hasRequiresReplace reports whether the attribute carries a RequiresReplace
// plan modifier, identified by its public description text.
func hasRequiresReplace(a schema.StringAttribute) bool {
	for _, pm := range a.PlanModifiers {
		if strings.Contains(pm.Description(context.Background()), "destroy and recreate the resource") {
			return true
		}
	}
	return false
}
