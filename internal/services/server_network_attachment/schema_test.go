package server_network_attachment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"

	server_attachment "github.com/itglobalcom/terraform-provider-vcp/internal/services/server_network_attachment"
)

// TestServerAttachmentSchema_ForceNewFields guards the replace semantics: the
// API recreates the NIC when the server, the network or the IP changes, so all
// three must carry RequiresReplace. Without it Terraform would call Update —
// which is a deliberate no-op here — and silently keep the old NIC while
// reporting the new config as applied.
func TestServerAttachmentSchema_ForceNewFields(t *testing.T) {
	var resp resource.SchemaResponse
	server_attachment.NewResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	for _, name := range []string{"server_id", "network_id", "ip_address"} {
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

	// Negative control: `mac` is purely computed (UseStateForUnknown only), so a
	// helper that reported RequiresReplace everywhere would be caught here.
	if mac, ok := resp.Schema.Attributes["mac"].(schema.StringAttribute); ok {
		if hasRequiresReplace(mac) {
			t.Error(`"mac" must NOT have RequiresReplace (the detector is matching everything)`)
		}
		if len(mac.PlanModifiers) == 0 {
			t.Error(`"mac" is expected to carry UseStateForUnknown; the negative control is vacuous`)
		}
	} else {
		t.Error(`schema has no "mac" string attribute for the negative control`)
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
