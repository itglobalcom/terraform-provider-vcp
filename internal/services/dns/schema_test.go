package dns

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func TestRecordSetResourceSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewRecordSetResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attrs := resp.Schema.Attributes

	for _, name := range []string{"id", "domain", "name", "type", "ttl", "values"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("record_set schema is missing attribute %q", name)
		}
	}
	// SRV service/protocol are encoded in the name, not separate attributes.
	for _, name := range []string{"service", "protocol"} {
		if _, ok := attrs[name]; ok {
			t.Errorf("record_set schema must NOT have top-level %q (encoded in the SRV name)", name)
		}
	}

	// values must be a set of nested objects (RRset — order-insensitive).
	vals, ok := attrs["values"].(schema.SetNestedAttribute)
	if !ok {
		t.Fatalf(`"values" must be schema.SetNestedAttribute, got %T`, attrs["values"])
	}
	if !vals.IsRequired() {
		t.Error(`"values" must be Required`)
	}
	for _, f := range []string{"ip", "mail_host", "priority", "canonical_name", "name_server_host", "text", "weight", "port", "target"} {
		if _, ok := vals.NestedObject.Attributes[f]; !ok {
			t.Errorf("values[] is missing field %q", f)
		}
	}

	// ttl is optional+computed (shared per RRset, API may default it).
	if a := attrs["ttl"]; !a.IsOptional() || !a.IsComputed() {
		t.Error(`"ttl" must be Optional+Computed`)
	}
}

func TestDomainResourceSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewDomainResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attrs := resp.Schema.Attributes

	for _, name := range []string{"id", "name", "is_delegated"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("domain schema is missing attribute %q", name)
		}
	}
	if !attrs["name"].IsRequired() {
		t.Error(`"name" must be Required`)
	}
	if !attrs["is_delegated"].IsComputed() {
		t.Error(`"is_delegated" must be Computed`)
	}
}
