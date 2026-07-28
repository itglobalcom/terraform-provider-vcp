package isolated_network

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// TestNetworkResourceSchema_TagsIsSet guards against a regression: `tags` must be
// a Set (order-independent). It used to be a List, and since the API returns tags in a
// different order, apply used to fail with "Provider produced inconsistent result after apply".
func TestNetworkResourceSchema_TagsIsSet(t *testing.T) {
	var resp resource.SchemaResponse
	NewNetworkResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	attr, ok := resp.Schema.Attributes["tags"]
	if !ok {
		t.Fatal(`network resource schema has no "tags" attribute`)
	}
	if _, ok := attr.(schema.SetAttribute); !ok {
		t.Fatalf(`network "tags" must be schema.SetAttribute (order-insensitive), got %T`, attr)
	}
}
