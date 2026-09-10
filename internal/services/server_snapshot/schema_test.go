package server_snapshot

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func TestSnapshotResourceSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema validation: %v", diags)
	}

	attrs := resp.Schema.Attributes
	for _, name := range []string{"id", "server_id", "name", "size_mb", "created"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("snapshot schema is missing attribute %q", name)
		}
	}

	// The API has no update for a snapshot: PUT does not exist. Without
	// RequiresReplace on both inputs Terraform would call Update — a deliberate
	// no-op here — and report a renamed snapshot as applied while the platform
	// still holds the old one.
	if !hasRequiresReplace(attrs["server_id"]) {
		t.Error(`"server_id" must carry RequiresReplace`)
	}
	if !hasRequiresReplace(attrs["name"]) {
		t.Error(`"name" must carry RequiresReplace — the API cannot rename a snapshot`)
	}

	// Negative control: a purely computed attribute must not report
	// RequiresReplace, or the detector above is matching everything.
	if hasRequiresReplace(attrs["created"]) {
		t.Error(`"created" must NOT carry RequiresReplace (the detector matches everything)`)
	}

	// size_mb grows on its own as the server writes to disk, so it can only ever
	// be a reading — a settable size would diff forever.
	if size := attrs["size_mb"]; size.IsRequired() || size.IsOptional() {
		t.Error(`"size_mb" must be Computed only: the platform reports it and it changes without an apply`)
	}
}

// TestSnapshotNameLengthIsValidated guards the plan-time check that stands in
// for the API's SnapshotNameTooLong: without it the refusal arrives only after
// the apply has started, without naming the attribute.
func TestSnapshotNameLengthIsValidated(t *testing.T) {
	var resp resource.SchemaResponse
	NewResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	name, ok := resp.Schema.Attributes["name"].(schema.StringAttribute)
	if !ok {
		t.Fatalf(`"name" is %T, expected schema.StringAttribute`, resp.Schema.Attributes["name"])
	}
	if len(name.Validators) == 0 {
		t.Fatal(`"name" carries no validators; the API limit of 25 characters is unchecked at plan time`)
	}
}

func TestSnapshotsDataSourceSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewSnapshotsDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema validation: %v", diags)
	}

	// The API lists snapshots per server; without server_id the data source would
	// have nothing to ask.
	if !resp.Schema.Attributes["server_id"].IsRequired() {
		t.Error(`"server_id" must be Required`)
	}
	if _, ok := resp.Schema.Attributes["snapshots"]; !ok {
		t.Error(`data source schema is missing "snapshots"`)
	}
}

// TestParseImportID covers the composite import id: a snapshot is addressed by
// its server as well as by itself, so half an id has to be refused rather than
// silently importing snapshot 0 of an empty server.
func TestParseImportID(t *testing.T) {
	cases := []struct {
		in         string
		wantServer string
		wantID     int64
		wantOK     bool
	}{
		{"l1s2:7", "l1s2", 7, true},
		{"l1s2:7:8", "", 0, false}, // the snapshot id must be the whole rest
		{"l1s2", "", 0, false},
		{"l1s2:", "", 0, false},
		{":7", "", 0, false},
		{"l1s2:0", "", 0, false},
		{"l1s2:-1", "", 0, false},
		{"l1s2:abc", "", 0, false},
		{"", "", 0, false},
	}
	for _, tc := range cases {
		serverID, snapshotID, ok := parseImportID(tc.in)
		if ok != tc.wantOK || serverID != tc.wantServer || snapshotID != tc.wantID {
			t.Errorf("parseImportID(%q) = (%q, %d, %v), want (%q, %d, %v)",
				tc.in, serverID, snapshotID, ok, tc.wantServer, tc.wantID, tc.wantOK)
		}
	}
}

// hasRequiresReplace reports whether an attribute carries a RequiresReplace plan
// modifier, identified by its public description text.
func hasRequiresReplace(attr any) bool {
	const marker = "destroy and recreate the resource"
	switch a := attr.(type) {
	case schema.StringAttribute:
		for _, pm := range a.PlanModifiers {
			if strings.Contains(pm.Description(context.Background()), marker) {
				return true
			}
		}
	case schema.Int64Attribute:
		for _, pm := range a.PlanModifiers {
			if strings.Contains(pm.Description(context.Background()), marker) {
				return true
			}
		}
	}
	return false
}
