package affinity_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// ============================================================================
// CUSTOM STATE CHECKS
// ============================================================================

// checkAffinityGroupExists - checks that the affinity group exists in the API
type checkAffinityGroupExists struct {
	resourceAddress string
}

func (c checkAffinityGroupExists) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	var resource *tfjson.StateResource
	for _, r := range req.State.Values.RootModule.Resources {
		if r.Address == c.resourceAddress {
			resource = r
			break
		}
	}

	if resource == nil {
		resp.Error = fmt.Errorf("Resource not found: %s", c.resourceAddress)
		return
	}

	id := resource.AttributeValues["id"].(string)
	if id == "" {
		resp.Error = fmt.Errorf("No affinity group ID is set")
		return
	}

	client := acctest.GetTestClient()
	group, err := client.GetAffinityGroup(ctx, id)
	if err != nil {
		resp.Error = fmt.Errorf("Error fetching affinity group: %s", err)
		return
	}

	if group.ID != id {
		resp.Error = fmt.Errorf("Affinity group not found in API")
	}
}

// ============================================================================
// TESTS
// ============================================================================

// TestAccAffinityGroupResource_affinity covers the group lifecycle with the
// "affinity" policy: create with checked attributes (verified in the API too),
// an empty plan afterwards, and import round-trip.
func TestAccAffinityGroupResource_affinity(t *testing.T) {
	resourceName := "vcp_affinity_group.test"
	name := "test-acc-affinity-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckAffinityGroupsDestroyed,
		Steps: []resource.TestStep{
			// Create affinity group
			{
				Config: testAccAffinityGroupConfig(name, locationID, "affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					checkAffinityGroupExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(name),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("policy"),
						knownvalue.StringExact("affinity"),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("server_ids"),
						knownvalue.NotNull(),
					),
				},
			},
			// Idempotency check
			{
				Config: testAccAffinityGroupConfig(name, locationID, "affinity"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			// Import
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccAffinityGroupResource_changePolicy verifies that changing the policy
// recreates the group (policy is RequiresReplace).
func TestAccAffinityGroupResource_changePolicy(t *testing.T) {
	resourceName := "vcp_affinity_group.test"
	name := "test-acc-aff-chg-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	compareIDsDiffer := statecheck.CompareValue(compare.ValuesDiffer())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckAffinityGroupsDestroyed,
		Steps: []resource.TestStep{
			// Create with affinity
			{
				Config: testAccAffinityGroupConfig(name, locationID, "affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("policy"),
						knownvalue.StringExact("affinity"),
					),
					compareIDsDiffer.AddStateValue(resourceName, tfjsonpath.New("id")),
				},
			},
			// Change to anti-affinity - should recreate
			{
				Config: testAccAffinityGroupConfig(name, locationID, "anti-affinity"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("policy"),
						knownvalue.StringExact("anti-affinity"),
					),
					compareIDsDiffer.AddStateValue(resourceName, tfjsonpath.New("id")),
				},
			},
		},
	})
}

// TestAccAffinityGroupResource_disappears deletes the group out-of-band and
// checks the provider detects it is gone (Read → RemoveResource) and plans to
// recreate it.
func TestAccAffinityGroupResource_disappears(t *testing.T) {
	name := "test-acc-aff-dis-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	config := testAccAffinityGroupConfig(name, locationID, "affinity")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckAffinityGroupsDestroyed,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { deleteAffinityGroupOutOfBand(t, name) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// deleteAffinityGroupOutOfBand removes the group with the given name directly
// via the API.
func deleteAffinityGroupOutOfBand(t *testing.T, name string) {
	client := acctest.GetTestClient()
	ctx := context.Background()

	groups, err := client.GetAffinityGroupList(ctx)
	if err != nil {
		t.Fatalf("out-of-band affinity group list failed: %v", err)
	}
	for _, group := range groups {
		if group.Name == name {
			if err := client.DeleteAffinityGroup(ctx, group.ID); err != nil {
				t.Fatalf("out-of-band affinity group delete failed: %v", err)
			}
			return
		}
	}
	t.Fatalf("affinity group %s not found to delete out-of-band", name)
}

// TestAccAffinityGroupResource_antiAffinity verifies a group is created with
// the "anti-affinity" policy.
func TestAccAffinityGroupResource_antiAffinity(t *testing.T) {
	resourceName := "vcp_affinity_group.test"
	name := "test-acc-anti-aff-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckAffinityGroupsDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAffinityGroupConfig(name, locationID, "anti-affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					checkAffinityGroupExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("policy"),
						knownvalue.StringExact("anti-affinity"),
					),
				},
			},
		},
	})
}

// TestAccAffinityGroupResource_requiresReplaceName verifies that changing the
// name plans a replace (name is RequiresReplace).
func TestAccAffinityGroupResource_requiresReplaceName(t *testing.T) {
	resourceName := "vcp_affinity_group.test"
	nameOriginal := "test-acc-orig-" + acctest.RandomString(6)
	nameUpdated := "test-acc-upd-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckAffinityGroupsDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAffinityGroupConfig(nameOriginal, locationID, "affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(nameOriginal),
					),
				},
			},
			{
				Config: testAccAffinityGroupConfig(nameUpdated, locationID, "affinity"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Changing name MUST trigger replace
						plancheck.ExpectResourceAction(
							resourceName,
							plancheck.ResourceActionReplace,
						),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(nameUpdated),
					),
				},
			},
		},
	})
}

// TestAccAffinityGroupResource_requiresReplaceLocation verifies that moving the
// group to another location plans a replace. Needs VCP_LOCATION_ID_ALT.
func TestAccAffinityGroupResource_requiresReplaceLocation(t *testing.T) {
	resourceName := "vcp_affinity_group.test"
	name := "test-acc-aff-loc-" + acctest.RandomString(6)
	locationID1 := os.Getenv("VCP_LOCATION_ID")
	locationID2 := os.Getenv("VCP_LOCATION_ID_ALT")

	if locationID2 == "" {
		t.Skip("VCP_LOCATION_ID_ALT not set")
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckAffinityGroupsDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAffinityGroupConfig(name, locationID1, "affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID1),
					),
				},
			},
			{
				Config: testAccAffinityGroupConfig(name, locationID2, "affinity"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Changing location should trigger replace
						plancheck.ExpectResourceAction(
							resourceName,
							plancheck.ResourceActionReplace,
						),
					},
				},
			},
		},
	})
}

// TestAccAffinityGroupResource_import verifies import round-trip for a group
// with the "anti-affinity" policy.
func TestAccAffinityGroupResource_import(t *testing.T) {
	resourceName := "vcp_affinity_group.test"
	name := "test-acc-aff-imp-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckAffinityGroupsDestroyed,
		Steps: []resource.TestStep{
			// Step 1: Create the resource
			{
				Config: testAccAffinityGroupConfig(name, locationID, "anti-affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					checkAffinityGroupExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(name),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("policy"),
						knownvalue.StringExact("anti-affinity"),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
				},
			},
			// Step 2: Import
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// ============================================================================
// DATA SOURCE TESTS
// ============================================================================

// TestAccAffinityGroupDataSource_basic verifies the singular data source
// mirrors the resource's id/name/location_id/policy.
func TestAccAffinityGroupDataSource_basic(t *testing.T) {
	resourceName := "vcp_affinity_group.test"
	dataSourceName := "data.vcp_affinity_group.test"
	name := "test-acc-ds-aff-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccAffinityGroupDataSourceConfig(name, locationID, "anti-affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					// Resource checks
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(name),
					),

					// Data source matches resource
					statecheck.CompareValuePairs(
						dataSourceName,
						tfjsonpath.New("id"),
						resourceName,
						tfjsonpath.New("id"),
						compare.ValuesSame(),
					),
					statecheck.CompareValuePairs(
						dataSourceName,
						tfjsonpath.New("name"),
						resourceName,
						tfjsonpath.New("name"),
						compare.ValuesSame(),
					),
					statecheck.CompareValuePairs(
						dataSourceName,
						tfjsonpath.New("location_id"),
						resourceName,
						tfjsonpath.New("location_id"),
						compare.ValuesSame(),
					),
					statecheck.CompareValuePairs(
						dataSourceName,
						tfjsonpath.New("policy"),
						resourceName,
						tfjsonpath.New("policy"),
						compare.ValuesSame(),
					),
				},
			},
		},
	})
}

// TestAccAffinityGroupsDataSource_list verifies the plural data source includes
// two freshly created groups with their policies.
func TestAccAffinityGroupsDataSource_list(t *testing.T) {
	dataSourceName := "data.vcp_affinity_groups.test"
	name1 := "test-acc-list-a-" + acctest.RandomString(4)
	name2 := "test-acc-list-b-" + acctest.RandomString(4)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccAffinityGroupsDataSourceConfig(name1, name2, locationID),
				ConfigStateChecks: []statecheck.StateCheck{
					// Check list not empty
					acctest.CheckListNotEmpty(dataSourceName, "affinity_groups"),

					// Check specific groups exist
					checkAffinityGroupInList{
						dataSourceAddress: dataSourceName,
						expectedName:      name1,
						expectedPolicy:    "affinity",
					},
					checkAffinityGroupInList{
						dataSourceAddress: dataSourceName,
						expectedName:      name2,
						expectedPolicy:    "anti-affinity",
					},
				},
			},
		},
	})
}

// checkAffinityGroupInList - checks that the group is present in the list
type checkAffinityGroupInList struct {
	dataSourceAddress string
	expectedName      string
	expectedPolicy    string
}

func (c checkAffinityGroupInList) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	var resource *tfjson.StateResource
	for _, r := range req.State.Values.RootModule.Resources {
		if r.Address == c.dataSourceAddress {
			resource = r
			break
		}
	}

	if resource == nil {
		resp.Error = fmt.Errorf("Resource not found: %s", c.dataSourceAddress)
		return
	}

	groupsRaw, ok := resource.AttributeValues["affinity_groups"]
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'affinity_groups' not found")
		return
	}

	groups, ok := groupsRaw.([]interface{})
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'affinity_groups' is not a list")
		return
	}

	for _, grpRaw := range groups {
		grp, ok := grpRaw.(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := grp["name"].(string)
		policy, _ := grp["policy"].(string)

		if name == c.expectedName && policy == c.expectedPolicy {
			return // Found!
		}
	}

	resp.Error = fmt.Errorf(
		"Affinity group with name=%s and policy=%s not found",
		c.expectedName,
		c.expectedPolicy,
	)
}

// ============================================================================
// CONFIG TEMPLATES
// ============================================================================

func testAccAffinityGroupConfig(name, locationID, policy string) string {
	return fmt.Sprintf(`
resource "vcp_affinity_group" "test" {
  name        = %[1]q
  location_id = %[2]q
  policy      = %[3]q
}
`, name, locationID, policy)
}

func testAccAffinityGroupDataSourceConfig(name, locationID, policy string) string {
	return fmt.Sprintf(`
%s

data "vcp_affinity_group" "test" {
  id = vcp_affinity_group.test.id
}
`, testAccAffinityGroupConfig(name, locationID, policy))
}

func testAccAffinityGroupsDataSourceConfig(name1, name2, locationID string) string {
	return fmt.Sprintf(`
resource "vcp_affinity_group" "a" {
  name        = %[1]q
  location_id = %[3]q
  policy      = "affinity"
}

resource "vcp_affinity_group" "b" {
  name        = %[2]q
  location_id = %[3]q
  policy      = "anti-affinity"
}

data "vcp_affinity_groups" "test" {
  depends_on = [vcp_affinity_group.a, vcp_affinity_group.b]
}
`, name1, name2, locationID)
}
