package gateway

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func TestGatewayResourceSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewGatewayResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attrs := resp.Schema.Attributes

	for _, name := range []string{
		"id", "location_id", "name", "tags",
		"bandwidth_mbps", "public_ip",
		"state", "powered_on", "created",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("gateway resource schema is missing attribute %q", name)
		}
	}

	// Network arguments removed — isolated networks are managed entirely by the
	// vcp_gateway_network_attachment resource. The create-time network is a
	// provider-internal detail (a throwaway network detached right after create).
	for _, name := range []string{"isolated_net_nics", "public_net_nics", "network_ids", "nics", "bootstrap_network_id"} {
		if _, ok := attrs[name]; ok {
			t.Errorf("gateway resource schema must NOT have %q (moved to attachment / provider-internal)", name)
		}
	}

	if _, ok := attrs["tags"].(schema.SetAttribute); !ok {
		t.Errorf(`"tags" must be schema.SetAttribute, got %T`, attrs["tags"])
	}

	if a := attrs["bandwidth_mbps"]; a == nil || !a.IsRequired() {
		t.Error(`"bandwidth_mbps" must be Required`)
	}

	// public_ip is read from the WAN NIC. Every NAT rule has to name this
	// address, so the resource must expose it without forcing users through a
	// data source on their own gateway.
	if a := attrs["public_ip"]; a == nil || !a.IsComputed() || a.IsRequired() || a.IsOptional() {
		t.Error(`"public_ip" must be Computed only`)
	}
}
