package vstack_server_test

import (
	"context"
	"encoding/json"
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

// ============================================================================
// CUSTOM STATE CHECKS FOR INTEGRATION
// ============================================================================

// checkServerIDChanged - verifies server ID changed (was replaced)
type checkServerIDChanged struct {
	resourceAddress string
	previousID      *string
}

func (c *checkServerIDChanged) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	resource := findResource(req.State, c.resourceAddress)
	if resource == nil {
		resp.Error = fmt.Errorf("Server not found: %s", c.resourceAddress)
		return
	}

	currentID, _ := resource.AttributeValues["id"].(string)

	if *c.previousID == "" {
		*c.previousID = currentID
	} else {
		if currentID == *c.previousID {
			resp.Error = fmt.Errorf("Server ID should have changed (server should be replaced), but it's still %s", currentID)
		}
		*c.previousID = currentID
	}
}

// checkServerIDUnchanged - verifies server ID stayed the same (was updated, not replaced)
type checkServerIDUnchanged struct {
	resourceAddress string
	previousID      *string
}

func (c *checkServerIDUnchanged) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	resource := findResource(req.State, c.resourceAddress)
	if resource == nil {
		resp.Error = fmt.Errorf("Server not found: %s", c.resourceAddress)
		return
	}

	currentID, _ := resource.AttributeValues["id"].(string)

	if *c.previousID != "" && currentID != *c.previousID {
		resp.Error = fmt.Errorf("Server ID changed unexpectedly: was %s, now %s (server should NOT be replaced)", *c.previousID, currentID)
	}

	*c.previousID = currentID
}

// checkSSHKeyInUse - verifies SSH key exists and is used by server
type checkSSHKeyInUse struct {
	sshKeyResourceAddress string
	serverResourceAddress string
}

func (c checkSSHKeyInUse) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	keyResource := findResource(req.State, c.sshKeyResourceAddress)
	if keyResource == nil {
		resp.Error = fmt.Errorf("SSH key resource not found: %s", c.sshKeyResourceAddress)
		return
	}

	serverResource := findResource(req.State, c.serverResourceAddress)
	if serverResource == nil {
		resp.Error = fmt.Errorf("Server resource not found: %s", c.serverResourceAddress)
		return
	}

	keyIDNum, ok := keyResource.AttributeValues["id"].(json.Number)
	if !ok {
		resp.Error = fmt.Errorf("SSH key id has unexpected type %T", keyResource.AttributeValues["id"])
		return
	}
	keyID, err := keyIDNum.Int64()
	if err != nil {
		resp.Error = fmt.Errorf("SSH key id is not an integer: %s", err)
		return
	}

	sshKeyIDs, ok := serverResource.AttributeValues["ssh_key_ids"].([]any)
	if !ok || len(sshKeyIDs) == 0 {
		resp.Error = fmt.Errorf("Server has no SSH key IDs")
		return
	}

	found := false
	for _, val := range sshKeyIDs {
		idNum, ok := val.(json.Number)
		if !ok {
			resp.Error = fmt.Errorf("server ssh_key_ids element has unexpected type %T", val)
			return
		}
		id, err := idNum.Int64()
		if err != nil {
			resp.Error = fmt.Errorf("server ssh_key_ids element is not an integer: %s", err)
			return
		}
		if id == keyID {
			found = true
			break
		}
	}

	if !found {
		resp.Error = fmt.Errorf("SSH key %d not found in server's SSH key list", keyID)
	}
}

// checkAffinityGroupHasServer - verifies affinity group contains the server
type checkAffinityGroupHasServer struct {
	affinityGroupResourceAddress string
	serverResourceAddress        string
}

func (c checkAffinityGroupHasServer) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	groupResource := findResource(req.State, c.affinityGroupResourceAddress)
	if groupResource == nil {
		resp.Error = fmt.Errorf("Affinity group resource not found: %s", c.affinityGroupResourceAddress)
		return
	}

	serverResource := findResource(req.State, c.serverResourceAddress)
	if serverResource == nil {
		resp.Error = fmt.Errorf("Server resource not found: %s", c.serverResourceAddress)
		return
	}

	serverID, _ := serverResource.AttributeValues["id"].(string)

	serverIDs, ok := groupResource.AttributeValues["server_ids"].([]any)
	if !ok || len(serverIDs) == 0 {
		resp.Error = fmt.Errorf("Affinity group has no servers")
		return
	}

	found := false
	for _, val := range serverIDs {
		id, _ := val.(string)
		if id == serverID {
			found = true
			break
		}
	}

	if !found {
		resp.Error = fmt.Errorf("Server %s not found in affinity group's server list", serverID)
	}
}

// ============================================================================
// CONFIGURATION TEMPLATES WITH PROPER FORMATTING
// ============================================================================

const (
	// Valid test RSA keys (2048 bit) - for tests only!
	testSSHPublicKey1 = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQDQYdIgNBdV9emDJuPJ0OQlvoLpQucTDWb5DmtJ/KD+0nsVjLOYYGx3lHKBlKnmWtzEgvQk1Ll0kmUZ/zKYL650oQUwwHRYl6wT9IIz1IhTWrtvJwKfmSdSsjXSRkM6EUR20P5D4W0m8RNn59HwHQ/hJ529corjqe/UKCjwnQGBdQ== test1@example.com"

	testSSHPublicKey2 = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQDQYdIgNBdV9emDJuPJ0OQlvoLpQucTDWb5DmtJ/KD+0nsVjLOYYGx3lHKBlKnmWtzEgvQk1Ll0kmUZ/zKYL900oQUwwHRYl6wT9IIz1IhTWrtvJwKfmSdSsjXSRkM6EUR20P5D4W0m8RNn59HwHQ/hJ529corjqe/UKCjwnQGBdQ== test2@example.com"
)

// Server with a single SSH key
func testAccConfig_ServerWithSingleSSHKey(serverName, keyName, locationID, imageID string) string {
	return fmt.Sprintf(`
resource "vcp_ssh_key" "key1" {
  name       = %[1]q
  public_key = %[2]q
}

resource "vcp_server" "test" {
  name        = %[3]q
  location_id = %[4]q
  image_id    = %[5]q
  cpu         = 1
  ram_mb      = 1024
  ssh_key_ids = [vcp_ssh_key.key1.id]

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    }
  ]
}
`, keyName, testSSHPublicKey1, serverName, locationID, imageID)
}

// Server with multiple SSH keys
func testAccConfig_ServerWithMultipleSSHKeys(serverName, keyName1, keyName2, locationID, imageID string) string {
	return fmt.Sprintf(`
resource "vcp_ssh_key" "key1" {
  name       = %[1]q
  public_key = %[2]q
}

resource "vcp_ssh_key" "key2" {
  name       = %[3]q
  public_key = %[4]q
}

resource "vcp_server" "test" {
  name        = %[5]q
  location_id = %[6]q
  image_id    = %[7]q
  cpu         = 1
  ram_mb      = 1024
  ssh_key_ids = [
    vcp_ssh_key.key1.id,
    vcp_ssh_key.key2.id,
  ]

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    }
  ]
}
`, keyName1, testSSHPublicKey1, keyName2, testSSHPublicKey2, serverName, locationID, imageID)
}

// Server with two servers in affinity group
func testAccConfig_TwoServersInAffinityGroup(serverName1, serverName2, groupName, locationID, imageID, policy string) string {
	return fmt.Sprintf(`
resource "vcp_affinity_group" "test" {
  name        = %[3]q
  location_id = %[4]q
  policy      = %[6]q
}

resource "vcp_server" "test1" {
  name              = %[1]q
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
`, serverName1, serverName2, groupName, locationID, imageID, policy)
}

// ============================================================================
// INTEGRATION TESTS - SSH KEYS & SERVERS
// ============================================================================

// TestAccServer_SSHKeyIntegration_addRemove - test for adding/removing server SSH keys
func TestAccServer_SSHKeyIntegration_addRemove(t *testing.T) {
	serverResourceName := "vcp_server.test"
	keyResourceName1 := "vcp_ssh_key.key1"
	keyResourceName2 := "vcp_ssh_key.key2"
	serverName := "test-acc-srv-ssh-int-" + acctest.RandomString(6)
	keyName1 := "test-acc-key1-" + acctest.RandomString(3)
	keyName2 := "test-acc-key2-" + acctest.RandomString(3)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create server with one SSH key
			{
				Config: testAccConfig_ServerWithSingleSSHKey(serverName, keyName1, locationID, imageID),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: serverResourceName},
					statecheck.ExpectKnownValue(
						serverResourceName,
						tfjsonpath.New("ssh_key_ids"),
						knownvalue.SetSizeExact(1),
					),
					checkSSHKeyInUse{
						sshKeyResourceAddress: keyResourceName1,
						serverResourceAddress: serverResourceName,
					},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
			// Step 2: Add second SSH key - server should be REPLACED
			{
				Config: testAccConfig_ServerWithMultipleSSHKeys(serverName, keyName1, keyName2, locationID, imageID),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(serverResourceName, plancheck.ResourceActionReplace),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						serverResourceName,
						tfjsonpath.New("ssh_key_ids"),
						knownvalue.SetSizeExact(2),
					),
					checkSSHKeyInUse{
						sshKeyResourceAddress: keyResourceName1,
						serverResourceAddress: serverResourceName,
					},
					checkSSHKeyInUse{
						sshKeyResourceAddress: keyResourceName2,
						serverResourceAddress: serverResourceName,
					},
					&checkServerIDChanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
			// Step 3: Remove second SSH key - server should be REPLACED again
			{
				Config: testAccConfig_ServerWithSingleSSHKey(serverName, keyName1, locationID, imageID),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(serverResourceName, plancheck.ResourceActionReplace),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						serverResourceName,
						tfjsonpath.New("ssh_key_ids"),
						knownvalue.SetSizeExact(1),
					),
					&checkServerIDChanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
		},
	})
}

// TestAccServer_SSHKeyIntegration_updateInPlace - changing an unrelated server
// attribute (cpu) on a server that references an SSH key must be an in-place
// update: ssh_key_ids is create-only (RequiresReplace), but an unchanged key
// reference must not force recreation.
func TestAccServer_SSHKeyIntegration_updateInPlace(t *testing.T) {
	serverResourceName := "vcp_server.test"
	keyResourceName := "vcp_ssh_key.key1"
	serverName := "test-acc-srv-ssh-upd-" + acctest.RandomString(6)
	keyName := "test-acc-key-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	var serverID string

	config := func(cpu int) string {
		return fmt.Sprintf(`
resource "vcp_ssh_key" "key1" {
  name       = %[1]q
  public_key = %[2]q
}

resource "vcp_server" "test" {
  name        = %[3]q
  location_id = %[4]q
  image_id    = %[5]q
  cpu         = %[6]d
  ram_mb      = 1024
  ssh_key_ids = [vcp_ssh_key.key1.id]

  volumes = [{
    number  = 0
    name    = "boot"
    size_mb = 30720
  }]
}
`, keyName, testSSHPublicKey1, serverName, locationID, imageID, cpu)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create server with one SSH key and cpu = 1
			{
				Config: config(1),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: serverResourceName},
					checkSSHKeyInUse{
						sshKeyResourceAddress: keyResourceName,
						serverResourceAddress: serverResourceName,
					},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
			// Step 2: cpu 1 → 2 — must plan an UPDATE (not a replace); the same
			// server keeps its ID and the SSH key stays attached.
			{
				Config: config(2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(serverResourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						serverResourceName,
						tfjsonpath.New("cpu"),
						knownvalue.Int64Exact(2),
					),
					checkSSHKeyInUse{
						sshKeyResourceAddress: keyResourceName,
						serverResourceAddress: serverResourceName,
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

// TestAccServer_SSHKeyIntegration_replaceKey - replacing an SSH key with another causes the server to be recreated
func TestAccServer_SSHKeyIntegration_replaceKey(t *testing.T) {
	serverResourceName := "vcp_server.test"
	serverName := "test-acc-srv-ssh-repl-" + acctest.RandomString(6)
	keyName := "test-acc-key-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create with key1 (first public key)
			{
				Config: fmt.Sprintf(`
resource "vcp_ssh_key" "key1" {
  name       = %[1]q
  public_key = %[2]q
}

resource "vcp_server" "test" {
  name        = %[3]q
  location_id = %[4]q
  image_id    = %[5]q
  cpu         = 1
  ram_mb      = 1024
  ssh_key_ids = [vcp_ssh_key.key1.id]

  volumes = [{
    number  = 0
    name    = "boot"
    size_mb = 30720
  }]
}
`, keyName, testSSHPublicKey1, serverName, locationID, imageID),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: serverResourceName},
					&checkServerIDUnchanged{
						resourceAddress: serverResourceName,
						previousID:      &serverID,
					},
				},
			},
			// Step 2: Replace key (delete key1, create key2) - server REPLACED
			{
				Config: fmt.Sprintf(`
resource "vcp_ssh_key" "key1" {
  name       = %[1]q
  public_key = %[2]q
}

resource "vcp_server" "test" {
  name        = %[3]q
  location_id = %[4]q
  image_id    = %[5]q
  cpu         = 1
  ram_mb      = 1024
  ssh_key_ids = [vcp_ssh_key.key1.id]

  volumes = [{
    number  = 0
    name    = "boot"
    size_mb = 30720
  }]
}
`, keyName, testSSHPublicKey2, serverName, locationID, imageID),
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
		},
	})
}
