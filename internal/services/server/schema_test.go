package vstack_server

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

// TestServerResourceSchema_Applications pins the applications block and its
// outcome: the attr.Type maps must match the nested schema (a mismatch fails
// only at apply), parameter values are secrets, and the outcome carries no
// application sign-in.
func TestServerResourceSchema_Applications(t *testing.T) {
	ctx := context.Background()
	s := serverSchema(t)

	apps, ok := s.Attributes["applications"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf(`"applications" must be schema.ListNestedAttribute, got %T`, s.Attributes["applications"])
	}
	if !apps.Optional || apps.Computed {
		t.Errorf("applications: Optional=%v Computed=%v, want an optional, non-computed block", apps.Optional, apps.Computed)
	}
	if got := apps.NestedObject.Type().(types.ObjectType).AttrTypes; !reflect.DeepEqual(got, applicationAttrTypes) {
		t.Errorf("applications schema types %v differ from applicationAttrTypes %v", got, applicationAttrTypes)
	}
	params := apps.NestedObject.Attributes["parameters"].(schema.MapAttribute)
	if !params.Sensitive {
		t.Error("applications[].parameters must be Sensitive: it carries application secrets")
	}
	issue := apps.NestedObject.Attributes["issue_llm_key"].(schema.BoolAttribute)
	if issue.Default == nil {
		t.Fatal("applications[].issue_llm_key has no default")
	}
	var defResp defaults.BoolResponse
	issue.Default.DefaultBool(ctx, defaults.BoolRequest{}, &defResp)
	if !defResp.PlanValue.Equal(types.BoolValue(false)) {
		t.Errorf("applications[].issue_llm_key defaults to %s, want false", defResp.PlanValue)
	}
	if !strings.Contains(apps.MarkdownDescription, "applications_ids") {
		t.Error("applications description must name the conflict with applications_ids")
	}
	if ids := s.Attributes["applications_ids"].(schema.SetAttribute); !strings.Contains(ids.MarkdownDescription, "`applications`") {
		t.Error("applications_ids description must name the conflict with applications")
	}

	inst, ok := s.Attributes["application_installations"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf(`"application_installations" must be schema.ListNestedAttribute, got %T`, s.Attributes["application_installations"])
	}
	if !inst.Computed || inst.Optional || inst.Required {
		t.Errorf("application_installations must be computed only")
	}
	if got := inst.NestedObject.Type().(types.ObjectType).AttrTypes; !reflect.DeepEqual(got, applicationInstallationAttrTypes) {
		t.Errorf("application_installations schema types %v differ from applicationInstallationAttrTypes %v", got, applicationInstallationAttrTypes)
	}
	for name := range inst.NestedObject.Attributes {
		if name != "id" && name != "state" && name != "outcome_reason" && name != "llm_key_id" {
			t.Errorf("application_installations carries %q: the application sign-in is not kept in state", name)
		}
	}
}
