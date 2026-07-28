package vstack_server

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// TestServerResourceSchema_TagsIsSet guards against a regression: `tags` must
// be a Set (unordered). It used to be a List, and since the API returns tags
// in a different order, apply failed with "Provider produced inconsistent
// result after apply".
func TestServerResourceSchema_TagsIsSet(t *testing.T) {
	var resp resource.SchemaResponse
	NewServerResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	attr, ok := resp.Schema.Attributes["tags"]
	if !ok {
		t.Fatal(`server resource schema has no "tags" attribute`)
	}
	if _, ok := attr.(schema.SetAttribute); !ok {
		t.Fatalf(`server "tags" must be schema.SetAttribute (order-insensitive), got %T`, attr)
	}
}

// TestServerResourceSchema_UnorderedCollectionsAreSets guards ssh_key_ids and
// applications_ids: both are order-insensitive to the API and carry
// RequiresReplace, so as Lists a pure reorder in config would plan a
// destructive server replacement.
func TestServerResourceSchema_UnorderedCollectionsAreSets(t *testing.T) {
	var resp resource.SchemaResponse
	NewServerResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	for _, name := range []string{"ssh_key_ids", "applications_ids"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Fatalf("server resource schema has no %q attribute", name)
		}
		if _, ok := attr.(schema.SetAttribute); !ok {
			t.Errorf("server %q must be schema.SetAttribute (order-insensitive), got %T", name, attr)
		}
	}
}
