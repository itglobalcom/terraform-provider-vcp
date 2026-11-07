// Package llm_test holds the acceptance tests of the LLM resources and data
// sources. They apply real configurations against a live API and are skipped
// unless TF_ACC is set.
//
// Environment: VCP_API_URL, VCP_API_TOKEN and VCP_LLM_PROJECT_ID — the project
// the token belongs to, already connected to the LLM service. The tests never
// connect a project: the service cannot be disconnected, so a connection made
// by a test would stay billed for good. The catalog has to offer at least two
// models available to groups.
//
// Groups and keys are named "test-acc-llm-…". A group cannot be deleted, only
// disabled, and a disabled group keeps its name: the random suffix keeps runs
// apart.
package llm_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// Environment
// ============================================================================

// preCheckLLM fails a run that would otherwise connect a project or test
// against one it cannot see: the project id has to be the token's, and the
// project has to be connected already.
func preCheckLLM(t *testing.T) {
	t.Helper()
	if os.Getenv("VCP_API_TOKEN") == "" || os.Getenv("VCP_API_URL") == "" {
		t.Fatal("VCP_API_TOKEN and VCP_API_URL must be set for acceptance tests")
	}
	projectID := os.Getenv("VCP_LLM_PROJECT_ID")
	if projectID == "" {
		t.Fatal("VCP_LLM_PROJECT_ID must be set for the LLM acceptance tests: " +
			"the ID of the project the token belongs to, connected to the LLM service")
	}

	client := acctest.GetTestClient()
	ctx := context.Background()
	project, err := client.GetProject(ctx)
	if err != nil {
		t.Fatalf("reading the project: %v", err)
	}
	if strconv.Itoa(project.ID) != projectID {
		t.Fatalf("VCP_LLM_PROJECT_ID is %s, but the token belongs to project %d", projectID, project.ID)
	}
	connection, err := client.GetLLMConnection(ctx)
	if err != nil {
		t.Fatalf("reading the LLM connection: %v", err)
	}
	if connection.State != entities.LLMConnectionStateConnected {
		t.Fatalf("project %s is %s; connect it to the LLM service in the panel first — "+
			"the tests do not connect projects", projectID, connection.State)
	}
}

func llmProjectID() string { return os.Getenv("VCP_LLM_PROJECT_ID") }

// testName builds a sweepable name with a random suffix.
func testName(kind string) string {
	return "test-acc-llm-" + kind + "-" + acctest.RandomString(6)
}

// ============================================================================
// Configuration fragments
// ============================================================================

// modelsConfig takes the models from the catalog, the way a user would: ids
// differ from stand to stand.
const modelsConfig = `
data "vcp_llm_models" "all" {}

locals {
  model_ids = [for m in data.vcp_llm_models.all.models : m.id if m.is_available_to_groups]
}
`

// groupConfig declares one group with the first `models` catalog models.
func groupConfig(name string, models int, tags []string) string {
	return fmt.Sprintf(`
resource "vcp_llm_group" "test" {
  name      = %q
  model_ids = slice(local.model_ids, 0, %d)
  tags      = %s
}
`, name, models, hclStrings(tags))
}

func hclStrings(values []string) string {
	out := "["
	for i, v := range values {
		if i > 0 {
			out += ", "
		}
		out += strconv.Quote(v)
	}
	return out + "]"
}

// ============================================================================
// State helpers
// ============================================================================

// attr reads an attribute of a resource out of the Terraform state.
func attr(s *terraform.State, address, name string) (string, error) {
	rs, ok := s.RootModule().Resources[address]
	if !ok {
		return "", fmt.Errorf("resource not found: %s", address)
	}
	value, ok := rs.Primary.Attributes[name]
	if !ok || value == "" {
		return "", fmt.Errorf("attribute %q is not set on %s", name, address)
	}
	return value, nil
}

func intAttr(s *terraform.State, address, name string) (int, error) {
	raw, err := attr(s, address, name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("attribute %q of %s is not numeric (%q): %w", name, address, raw, err)
	}
	return value, nil
}

// captureAttr stores an attribute for a later step's PreConfig, which runs
// before that step's state exists.
func captureAttr(address, name string, dst *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		value, err := attr(s, address, name)
		if err != nil {
			return err
		}
		*dst = value
		return nil
	}
}

// keyImportID builds the "<group_id>/<access_key_id>" import id.
func keyImportID(address string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		group, err := attr(s, address, "group_id")
		if err != nil {
			return "", err
		}
		id, err := attr(s, address, "id")
		if err != nil {
			return "", err
		}
		return group + "/" + id, nil
	}
}

// ============================================================================
// API-side assertions
// ============================================================================
//
// State agreeing with itself would pass a resource that wrote nothing: these
// ask the platform.

func checkGroupInAPI(address string, wantModels int, wantTags []string, wantState entities.LLMGroupState) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id, err := intAttr(s, address, "id")
		if err != nil {
			return err
		}
		group, err := acctest.GetTestClient().GetLLMGroup(context.Background(), entities.LLMGroupID(id))
		if err != nil {
			return fmt.Errorf("reading LLM group %d: %w", id, err)
		}
		if len(group.Models) != wantModels {
			return fmt.Errorf("LLM group %d has %d models, want %d", id, len(group.Models), wantModels)
		}
		tags := slices.Clone(group.Tags)
		slices.Sort(tags)
		want := slices.Clone(wantTags)
		slices.Sort(want)
		if !slices.Equal(tags, want) {
			return fmt.Errorf("LLM group %d has tags %v, want %v", id, tags, want)
		}
		if group.State == nil || *group.State != wantState {
			return fmt.Errorf("LLM group %d is %v, want %s", id, group.State, wantState)
		}
		return nil
	}
}

func checkKeyLockInAPI(address string, wantLocked bool, wantReason string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		key, err := keyFromAPI(s, address)
		if err != nil {
			return err
		}
		locked := key.Locks != nil && key.Locks.ByClient
		if locked != wantLocked {
			return fmt.Errorf("access key %s is locked by the client = %v, want %v", address, locked, wantLocked)
		}
		reason := ""
		if locked && key.Locks.ClientReason != nil {
			reason = *key.Locks.ClientReason
		}
		if reason != wantReason {
			return fmt.Errorf("access key %s has lock reason %q, want %q", address, reason, wantReason)
		}
		return nil
	}
}

func keyFromAPI(s *terraform.State, address string) (*entities.LLMAccessKey, error) {
	group, err := intAttr(s, address, "group_id")
	if err != nil {
		return nil, err
	}
	id, err := intAttr(s, address, "id")
	if err != nil {
		return nil, err
	}
	key, err := acctest.GetTestClient().GetLLMAccessKey(context.Background(),
		entities.LLMGroupID(group), entities.LLMAccessKeyID(id))
	if err != nil {
		return nil, fmt.Errorf("reading access key %d of LLM group %d: %w", id, group, err)
	}
	return key, nil
}

// checkLLMDestroyed is the CheckDestroy of every LLM test: a destroyed group is
// disabled (it cannot be deleted), a destroyed key is gone or on its way out,
// and the project stays connected.
func checkLLMDestroyed(s *terraform.State) error {
	client := acctest.GetTestClient()
	ctx := context.Background()
	for _, rs := range s.RootModule().Resources {
		switch rs.Type {
		case "vcp_llm_group":
			id, _ := strconv.Atoi(rs.Primary.ID)
			group, err := client.GetLLMGroup(ctx, entities.LLMGroupID(id))
			if sdk.IsNotFound(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("reading LLM group %d: %w", id, err)
			}
			if group.State != nil && *group.State == entities.LLMGroupStateActive {
				return fmt.Errorf("LLM group %d is still active after destroy", id)
			}
		case "vcp_llm_access_key":
			group, _ := strconv.Atoi(rs.Primary.Attributes["group_id"])
			id, _ := strconv.Atoi(rs.Primary.ID)
			key, err := client.GetLLMAccessKey(ctx, entities.LLMGroupID(group), entities.LLMAccessKeyID(id))
			if sdk.IsNotFound(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("reading access key %d: %w", id, err)
			}
			if key.State != nil && *key.State == entities.LLMAccessKeyStateActive {
				return fmt.Errorf("access key %d is still active after destroy", id)
			}
		case "vcp_llm_connection":
			connection, err := client.GetLLMConnection(ctx)
			if err != nil {
				return fmt.Errorf("reading the LLM connection: %w", err)
			}
			if connection.State != entities.LLMConnectionStateConnected {
				return fmt.Errorf("the project is %s after destroy; the service must stay connected", connection.State)
			}
		}
	}
	return nil
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	v, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("expected a numeric id, got %q: %v", s, err)
	}
	return v
}

// mustAtoiErr converts a captured id, returning 0 for anything unparseable —
// the caller then fails on the API error, which names the id.
func mustAtoiErr(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}
