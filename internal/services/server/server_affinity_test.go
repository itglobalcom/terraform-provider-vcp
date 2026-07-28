package vstack_server_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// Resource-level affinity group tests (create/update/import/replace semantics)
// live in internal/services/affinity_group. This file covers the integration
// between servers and affinity groups only.

// ============================================================================
// CUSTOM STATE CHECKS
// ============================================================================

// checkAffinityGroupExists - verifies affinity group exists in API
type checkAffinityGroupExists struct {
	resourceAddress string
}

func (c checkAffinityGroupExists) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	resource := findResource(req.State, c.resourceAddress)
	if resource == nil {
		resp.Error = fmt.Errorf("Resource not found: %s", c.resourceAddress)
		return
	}

	id, ok := resource.AttributeValues["id"].(string)
	if !ok || id == "" {
		resp.Error = fmt.Errorf("Affinity group ID not found or empty")
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

// checkAffinityGroupPolicy - verifies affinity group policy
type checkAffinityGroupPolicy struct {
	resourceAddress string
	expectedPolicy  string
}

func (c checkAffinityGroupPolicy) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	resource := findResource(req.State, c.resourceAddress)
	if resource == nil {
		resp.Error = fmt.Errorf("Resource not found: %s", c.resourceAddress)
		return
	}

	policy, ok := resource.AttributeValues["policy"].(string)
	if !ok {
		resp.Error = fmt.Errorf("Policy attribute not found")
		return
	}

	if policy != c.expectedPolicy {
		resp.Error = fmt.Errorf("Expected policy %q, got %q", c.expectedPolicy, policy)
	}
}

// ============================================================================
// CONFIGURATION TEMPLATES
// ============================================================================

func testAccAffinityGroupConfig_withServer(groupName, serverName, locationID, imageID, policy string) string {
	return fmt.Sprintf(`
resource "vcp_affinity_group" "test" {
  name        = %[1]q
  location_id = %[3]q
  policy      = %[5]q
}

resource "vcp_server" "test" {
  name              = %[2]q
  location_id       = %[3]q
  image_id          = %[4]q
  cpu               = 1
  ram_mb            = 1024
  affinity_group_id = vcp_affinity_group.test.id

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    }
  ]
}
`, groupName, serverName, locationID, imageID, policy)
}

func testAccAffinityGroupConfig_withTwoServers(groupName, serverName1, serverName2, locationID, imageID, policy string) string {
	return fmt.Sprintf(`
resource "vcp_affinity_group" "test" {
  name        = %[1]q
  location_id = %[4]q
  policy      = %[6]q
}

resource "vcp_server" "test1" {
  name              = %[2]q
  location_id       = %[4]q
  image_id          = %[5]q
  cpu               = 1
  ram_mb            = 1024
  affinity_group_id = vcp_affinity_group.test.id

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    }
  ]
}

resource "vcp_server" "test2" {
  name              = %[3]q
  location_id       = %[4]q
  image_id          = %[5]q
  cpu               = 1
  ram_mb            = 1024
  affinity_group_id = vcp_affinity_group.test.id

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    }
  ]
}
`, groupName, serverName1, serverName2, locationID, imageID, policy)
}

// ============================================================================
// INTEGRATION TESTS - AFFINITY GROUPS & SERVERS
// ============================================================================

// TestAccAffinityGroup_withServer verifies a server can be created directly
// into an affinity group (affinity_group_id set) and the pair converges to an
// empty plan.
func TestAccAffinityGroup_withServer(t *testing.T) {
	groupResourceName := "vcp_affinity_group.test"
	serverResourceName := "vcp_server.test"
	groupName := "test-acc-aff-srv-" + acctest.RandomString(6)
	serverName := "test-acc-srv-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckServersDestroyed,
			acctest.CheckAffinityGroupsDestroyed,
		),
		Steps: []resource.TestStep{
			{
				Config: testAccAffinityGroupConfig_withServer(groupName, serverName, locationID, imageID, "affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					checkAffinityGroupExists{resourceAddress: groupResourceName},
					// Verify server has affinity_group_id set
					statecheck.ExpectKnownValue(
						serverResourceName,
						tfjsonpath.New("affinity_group_id"),
						knownvalue.NotNull(),
					),
				},
			},
			// Idempotency
			{
				Config: testAccAffinityGroupConfig_withServer(groupName, serverName, locationID, imageID, "affinity"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// TestAccAffinityGroup_withTwoServers verifies two servers can share one
// anti-affinity group created in the same apply.
func TestAccAffinityGroup_withTwoServers(t *testing.T) {
	groupResourceName := "vcp_affinity_group.test"
	groupName := "test-acc-aff-2srv-" + acctest.RandomString(6)
	serverName1 := "test-acc-srv1-" + acctest.RandomString(6)
	serverName2 := "test-acc-srv2-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckServersDestroyed,
			acctest.CheckAffinityGroupsDestroyed,
		),
		Steps: []resource.TestStep{
			{
				Config: testAccAffinityGroupConfig_withTwoServers(groupName, serverName1, serverName2, locationID, imageID, "anti-affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					checkAffinityGroupExists{resourceAddress: groupResourceName},
					checkAffinityGroupPolicy{
						resourceAddress: groupResourceName,
						expectedPolicy:  "anti-affinity",
					},
				},
			},
		},
	})
}

// TestAccServer_AffinityIntegration_multipleServers - verifies that both servers are in the group
func TestAccServer_AffinityIntegration_multipleServers(t *testing.T) {
	groupResourceName := "vcp_affinity_group.test"
	serverResourceName1 := "vcp_server.test1"
	serverResourceName2 := "vcp_server.test2"
	serverName1 := "test-acc-srv-aff1-" + acctest.RandomString(6)
	serverName2 := "test-acc-srv-aff2-" + acctest.RandomString(6)
	groupName := "test-acc-aff-grp-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	var server1ID, server2ID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create two servers in affinity group
			{
				Config: testAccConfig_TwoServersInAffinityGroup(serverName1, serverName2, groupName, locationID, imageID, "affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: serverResourceName1},
					checkServerExists{resourceAddress: serverResourceName2},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName1,
						previousID:      &server1ID,
					},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName2,
						previousID:      &server2ID,
					},
				},
			},
			// Step 2: Re-apply to refresh the group. Its computed server_ids can't
			// see servers created after it in the same apply, so membership is
			// only visible after a refresh.
			{
				Config: testAccConfig_TwoServersInAffinityGroup(serverName1, serverName2, groupName, locationID, imageID, "affinity"),
				ConfigStateChecks: []statecheck.StateCheck{
					checkAffinityGroupHasServer{
						affinityGroupResourceAddress: groupResourceName,
						serverResourceAddress:        serverResourceName1,
					},
					checkAffinityGroupHasServer{
						affinityGroupResourceAddress: groupResourceName,
						serverResourceAddress:        serverResourceName2,
					},
					statecheck.ExpectKnownValue(
						groupResourceName,
						tfjsonpath.New("server_ids"),
						knownvalue.SetSizeExact(2),
					),
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName1,
						previousID:      &server1ID,
					},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName2,
						previousID:      &server2ID,
					},
				},
			},
			// Step 3: Change affinity group policy - BOTH servers should be REPLACED
			{
				Config: testAccConfig_TwoServersInAffinityGroup(serverName1, serverName2, groupName, locationID, imageID, "anti-affinity"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(groupResourceName, plancheck.ResourceActionReplace),
						plancheck.ExpectResourceAction(serverResourceName1, plancheck.ResourceActionReplace),
						plancheck.ExpectResourceAction(serverResourceName2, plancheck.ResourceActionReplace),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					&checkServerIDChanged{
						resourceAddress: serverResourceName1,
						previousID:      &server1ID,
					},
					&checkServerIDChanged{
						resourceAddress: serverResourceName2,
						previousID:      &server2ID,
					},
				},
			},
		},
	})
}

// TestAccServer_AffinityIntegration_reassignToNewGroup - reassigning a server to a new group
func TestAccServer_AffinityIntegration_reassignToNewGroup(t *testing.T) {
	serverResourceName := "vcp_server.test"
	groupResourceName1 := "vcp_affinity_group.group1"
	groupResourceName2 := "vcp_affinity_group.group2"
	serverName := "test-acc-srv-reass-" + acctest.RandomString(6)
	groupName1 := "test-acc-group1-" + acctest.RandomString(6)
	groupName2 := "test-acc-group2-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	var serverID string

	cfgGroup1 := fmt.Sprintf(`
resource "vcp_affinity_group" "group1" {
  name        = %[1]q
  location_id = %[3]q
  policy      = "affinity"
}

resource "vcp_server" "test" {
  name              = %[2]q
  location_id       = %[3]q
  image_id          = %[4]q
  cpu               = 1
  ram_mb            = 1024
  affinity_group_id = vcp_affinity_group.group1.id

  volumes = [{
    number  = 0
    name    = "boot"
    size_mb = 30720
  }]
}
`, groupName1, serverName, locationID, imageID)

	cfgGroup2 := fmt.Sprintf(`
resource "vcp_affinity_group" "group1" {
  name        = %[1]q
  location_id = %[3]q
  policy      = "affinity"
}

resource "vcp_affinity_group" "group2" {
  name        = %[4]q
  location_id = %[3]q
  policy      = "anti-affinity"
}

resource "vcp_server" "test" {
  name              = %[2]q
  location_id       = %[3]q
  image_id          = %[5]q
  cpu               = 1
  ram_mb            = 1024
  affinity_group_id = vcp_affinity_group.group2.id

  volumes = [{
    number  = 0
    name    = "boot"
    size_mb = 30720
  }]
}
`, groupName1, serverName, locationID, groupName2, imageID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create server in group1
			{
				Config: cfgGroup1,
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: serverResourceName},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
			// Step 2: Re-apply to refresh group1. Its computed server_ids can't
			// see the server created after it in the same apply, so membership
			// is only visible after a refresh.
			{
				Config: cfgGroup1,
				ConfigStateChecks: []statecheck.StateCheck{
					checkAffinityGroupHasServer{
						affinityGroupResourceAddress: groupResourceName1,
						serverResourceAddress:        serverResourceName,
					},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
			// Step 3: Reassign to group2 - server REPLACED
			{
				Config: cfgGroup2,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(serverResourceName, plancheck.ResourceActionReplace),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					&checkServerIDChanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
			// Step 4: Re-apply to refresh group2 and verify membership
			{
				Config: cfgGroup2,
				ConfigStateChecks: []statecheck.StateCheck{
					checkAffinityGroupHasServer{
						affinityGroupResourceAddress: groupResourceName2,
						serverResourceAddress:        serverResourceName,
					},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
		},
	})
}
