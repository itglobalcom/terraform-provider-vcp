package vstack_server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// ============================================================================
// CUSTOM STATE CHECKS
// ============================================================================

// checkServerExists - custom state check to verify server exists in API
type checkServerExists struct {
	resourceAddress string
}

// checkServerHasNoNICs asserts via the API that the server carries no network
// interfaces at all. Guards the create request's explicit empty `networks`
// list: with `"networks": null` the platform silently attaches a public NIC
// (with a public IP), which a config that declares no interface resources must
// never get.
type checkServerHasNoNICs struct {
	resourceAddress string
}

func (c checkServerHasNoNICs) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	res := findResource(req.State, c.resourceAddress)
	if res == nil {
		resp.Error = fmt.Errorf("server not found in state: %s", c.resourceAddress)
		return
	}
	serverID, _ := res.AttributeValues["id"].(string)
	if serverID == "" {
		resp.Error = fmt.Errorf("no id on %s", c.resourceAddress)
		return
	}

	nics, err := acctest.GetTestClient().GetServerNICs(ctx, serverID)
	if err != nil {
		resp.Error = fmt.Errorf("listing NICs of server %s: %w", serverID, err)
		return
	}
	if len(nics) != 0 {
		details := make([]string, 0, len(nics))
		for _, n := range nics {
			details = append(details, fmt.Sprintf("id=%d network_id=%q ip=%q", n.ID, n.NetworkID, n.IPAddress))
		}
		resp.Error = fmt.Errorf("server %s must have no NICs but has %d: %s",
			serverID, len(nics), strings.Join(details, "; "))
	}
}

func (c checkServerExists) CheckState(
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
		resp.Error = fmt.Errorf("No Server ID is set")
		return
	}

	client := acctest.GetTestClient()
	server, err := client.GetServer(ctx, id)
	if err != nil {
		resp.Error = fmt.Errorf("Error fetching server: %s", err)
		return
	}

	if server.ID != id {
		resp.Error = fmt.Errorf("Server not found in API")
	}
}

// checkServerVolumeCount - verifies the number of volumes in API
type checkServerVolumeCount struct {
	resourceAddress string
	expectedCount   int
}

func (c checkServerVolumeCount) CheckState(
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
	client := acctest.GetTestClient()
	server, err := client.GetServer(ctx, id)
	if err != nil {
		resp.Error = fmt.Errorf("Error fetching server: %s", err)
		return
	}

	if len(server.Volumes) != c.expectedCount {
		resp.Error = fmt.Errorf("Expected %d volumes, got %d", c.expectedCount, len(server.Volumes))
	}
}

// checkServerVolumeByNumber - verifies volume attributes by number
type checkServerVolumeByNumber struct {
	resourceAddress string
	volumeNumber    int64
	expectedName    string
	expectedSizeMB  int64
}

func (c checkServerVolumeByNumber) CheckState(
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

	volumes, ok := resource.AttributeValues["volumes"].([]any)
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'volumes' not found or not a list")
		return
	}

	for _, volRaw := range volumes {
		vol, ok := volRaw.(map[string]any)
		if !ok {
			continue
		}

		number, _ := vol["number"].(json.Number).Int64()
		if number != c.volumeNumber {
			continue
		}

		// Found
		name, _ := vol["name"].(string)
		if name != c.expectedName {
			resp.Error = fmt.Errorf("Volume %d: expected name %q, got %q",
				c.volumeNumber, c.expectedName, name)
			return
		}

		sizeMB, _ := vol["size_mb"].(json.Number).Int64()
		if sizeMB != c.expectedSizeMB {
			resp.Error = fmt.Errorf("Volume %d: expected size %d MB, got %d MB",
				c.volumeNumber, c.expectedSizeMB, sizeMB)
			return
		}

		return
	}
	resp.Error = fmt.Errorf("Volume with number %d not found", c.volumeNumber)
}

// checkVolumeIDByNumber - extracts and compares volume ID by number across steps
type checkVolumeIDByNumber struct {
	resourceAddress string
	volumeNumber    int64
	savedID         *int64
	expectSame      bool
}

func (c *checkVolumeIDByNumber) CheckState(
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

	volumesRaw, _ := resource.AttributeValues["volumes"].([]any)

	for _, volRaw := range volumesRaw {
		vol, ok := volRaw.(map[string]any)
		if !ok {
			continue
		}

		number, _ := vol["number"].(json.Number).Int64()

		if number == c.volumeNumber {

			volumeID, _ := vol["id"].(json.Number).Int64()

			if *c.savedID == 0 {
				// First call - save the ID
				*c.savedID = volumeID
			} else {
				// Subsequent call - compare
				if c.expectSame && volumeID != *c.savedID {
					resp.Error = fmt.Errorf("Volume %d ID changed: was %d, now %d",
						c.volumeNumber, *c.savedID, volumeID)
				} else if !c.expectSame && volumeID == *c.savedID {
					resp.Error = fmt.Errorf("Volume %d ID should have changed but didn't: still %d",
						c.volumeNumber, volumeID)
				}
			}
			return
		}
	}

	resp.Error = fmt.Errorf("Volume with number %d not found", c.volumeNumber)
}

// testAccCheckServerDestroy verifies server is deleted
func testAccCheckServerDestroy(s *terraform.State) error {
	client := acctest.GetTestClient()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "vcp_server" {
			continue
		}

		_, err := client.GetServer(context.Background(), rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("Server %s still exists", rs.Primary.ID)
		}

		if !sdk.IsNotFound(err) {
			return fmt.Errorf("Error checking server destruction: %s", err)
		}
	}

	return nil
}

// ============================================================================
// CONFIGURATION TEMPLATES
// ============================================================================

func testAccServerConfig_basic(name, locationID, imageID string) string {
	return fmt.Sprintf(`
resource "vcp_server" "test" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = 1
  ram_mb      = 2048

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    }
  ]
}
`, name, locationID, imageID)
}

func testAccServerConfig_multipleVolumes(name, locationID, imageID string, volumes []map[string]any) string {
	var volumesHCL string
	for _, vol := range volumes {
		volumesHCL += fmt.Sprintf(`
    {
      number  = %d
      name    = %q
      size_mb = %d
    },`, vol["number"], vol["name"], vol["size_mb"])
	}

	return fmt.Sprintf(`
resource "vcp_server" "test" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = 1
  ram_mb      = 2048

  volumes = [%s
  ]
}
`, name, locationID, imageID, volumesHCL)
}

// ============================================================================
// ACCEPTANCE TESTS
// ============================================================================

// TestAccServer_basic tests basic server creation with boot volume
func TestAccServer_basic(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-srv-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServerConfig_basic(serverName, locationID, imageID),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
					// Regression: a config with no interface resources must not
					// get one. The API assigns a default public NIC unless the
					// create request carries an explicit empty `networks` list.
					checkServerHasNoNICs{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(serverName),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("cpu"),
						knownvalue.Int64Exact(1),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("ram_mb"),
						knownvalue.Int64Exact(2048),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("state"),
						knownvalue.NotNull(),
					),

					// Check volumes
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("volumes"),
						knownvalue.ListSizeExact(1),
					),
					checkServerVolumeCount{
						resourceAddress: resourceName,
						expectedCount:   1,
					},
					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						expectedName:    "boot",
						expectedSizeMB:  30720,
					},
				},
			},
			// Idempotency check
			{
				Config: testAccServerConfig_basic(serverName, locationID, imageID),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// TestAccServer_multipleVolumes tests server with multiple volumes
func TestAccServer_multipleVolumes(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-server-multi-vol-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	volumes := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data1", "size_mb": 10240},
		{"number": 2, "name": "data2", "size_mb": 10240},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumes),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("volumes"),
						knownvalue.ListSizeExact(3),
					),
					checkServerVolumeCount{
						resourceAddress: resourceName,
						expectedCount:   3,
					},
					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						expectedName:    "boot",
						expectedSizeMB:  30720,
					},
					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    1,
						expectedName:    "data1",
						expectedSizeMB:  10240,
					},
					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    2,
						expectedName:    "data2",
						expectedSizeMB:  10240,
					},
				},
			},
			// Idempotency check
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumes),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// TestAccServer_volumeResize tests increasing volume size
func TestAccServer_volumeResize(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-server-resize-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	compareServerIDsSame := statecheck.CompareValue(compare.ValuesSame())
	var bootVolumeID int64

	volumesInitial := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data", "size_mb": 10240},
	}

	volumesResized := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 40960}, // Increased
		{"number": 1, "name": "data", "size_mb": 30720}, // Increased
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create with initial sizes
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesInitial),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},

					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						expectedName:    "boot",
						expectedSizeMB:  30720,
					},
					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    1,
						expectedName:    "data",
						expectedSizeMB:  10240,
					},

					// Save server ID for comparison
					compareServerIDsSame.AddStateValue(
						resourceName,
						tfjsonpath.New("id"),
					),

					// Save boot volume ID
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						savedID:         &bootVolumeID,
						expectSame:      true,
					},
				},
			},
			// Step 2: Resize volumes
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesResized),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(
							resourceName,
							plancheck.ResourceActionUpdate,
						),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},

					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						expectedName:    "boot",
						expectedSizeMB:  40960,
					},
					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    1,
						expectedName:    "data",
						expectedSizeMB:  30720,
					},

					// Server ID should not change
					compareServerIDsSame.AddStateValue(
						resourceName,
						tfjsonpath.New("id"),
					),

					// Volume ID should remain the same (resize, not recreate)
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						savedID:         &bootVolumeID,
						expectSame:      true,
					},
				},
			},
		},
	})
}

// TestAccServer_addRemoveVolumes tests adding and removing volumes
func TestAccServer_addRemoveVolumes(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-server-add-rm-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	var bootVolumeID int64

	volumesInitial := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data1", "size_mb": 10240},
	}

	volumesAdded := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data1", "size_mb": 10240},
		{"number": 2, "name": "data2", "size_mb": 10240}, // Added
	}

	volumesRemoved := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		// data1 (number=1) removed
		{"number": 2, "name": "data2", "size_mb": 10240},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create with 2 volumes
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesInitial),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
					checkServerVolumeCount{
						resourceAddress: resourceName,
						expectedCount:   2,
					},

					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						savedID:         &bootVolumeID,
						expectSame:      true,
					},
				},
			},
			// Step 2: Add third volume
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesAdded),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
					checkServerVolumeCount{
						resourceAddress: resourceName,
						expectedCount:   3,
					},
					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    2,
						expectedName:    "data2",
						expectedSizeMB:  10240,
					},

					// Boot volume ID unchanged
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						savedID:         &bootVolumeID,
						expectSame:      true,
					},
				},
			},
			// Step 3: Remove middle volume
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesRemoved),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
					checkServerVolumeCount{
						resourceAddress: resourceName,
						expectedCount:   2,
					},

					// Boot volume unchanged
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						savedID:         &bootVolumeID,
						expectSame:      true,
					},
				},
			},
		},
	})
}

// TestAccServer_volumeNumberChange tests that changing number recreates volume
func TestAccServer_volumeNumberChange(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-server-num-chg-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	var dataVolumeID int64

	volumesInitial := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data", "size_mb": 10240},
	}

	// Change number from 1 to 5 - should recreate volume
	volumesNumberChanged := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 5, "name": "data", "size_mb": 10240}, // Number changed
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create with number=1
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesInitial),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},

					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    1,
						expectedName:    "data",
						expectedSizeMB:  10240,
					},

					// Save data volume ID
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    1,
						savedID:         &dataVolumeID,
						expectSame:      true,
					},
				},
			},
			// Step 2: Change number - volume should be recreated
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesNumberChanged),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},

					// Old number=1 should not exist
					checkServerVolumeCount{
						resourceAddress: resourceName,
						expectedCount:   2,
					},

					// New number=5 should exist with new ID
					checkServerVolumeByNumber{
						resourceAddress: resourceName,
						volumeNumber:    5,
						expectedName:    "data",
						expectedSizeMB:  10240,
					},
					// The volume at the new number must be a NEW volume, not the
					// old one renumbered in place.
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    5,
						savedID:         &dataVolumeID,
						expectSame:      false,
					},
				},
			},
		},
	})
}

// TestAccServer_volumeOrderStability verifies that reordering volume blocks in
// the config is an in-place update that keeps every volume ID, and the plan
// converges to empty afterwards.
func TestAccServer_volumeOrderStability(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-server-order-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	// Save volume IDs between steps
	var bootVolumeID, data1VolumeID, data2VolumeID int64

	// Order: 0, 1, 2
	volumesOrderA := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data1", "size_mb": 10240},
		{"number": 2, "name": "data2", "size_mb": 10240},
	}

	// Different order in config: 2, 0, 1 - but same volumes
	volumesOrderB := []map[string]any{
		{"number": 2, "name": "data2", "size_mb": 10240},
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data1", "size_mb": 10240},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesOrderA),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
					checkServerVolumeCount{
						resourceAddress: resourceName,
						expectedCount:   3,
					},
					// Save all volume IDs
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						savedID:         &bootVolumeID,
						expectSame:      true,
					},
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    1,
						savedID:         &data1VolumeID,
						expectSame:      true,
					},
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    2,
						savedID:         &data2VolumeID,
						expectSame:      true,
					},
				},
			},
			// Step 2: Reorder in config - volumes should keep same IDs
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesOrderB),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// The plan will be an in-place update (reordering in state),
						// but there should be no replace
						plancheck.ExpectResourceAction(
							resourceName,
							plancheck.ResourceActionUpdate,
						),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
					checkServerVolumeCount{
						resourceAddress: resourceName,
						expectedCount:   3,
					},
					// Verify that volume IDs have NOT changed
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    0,
						savedID:         &bootVolumeID,
						expectSame:      true, // ID should remain the same
					},
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    1,
						savedID:         &data1VolumeID,
						expectSame:      true,
					},
					&checkVolumeIDByNumber{
						resourceAddress: resourceName,
						volumeNumber:    2,
						savedID:         &data2VolumeID,
						expectSame:      true,
					},
				},
			},
			// Step 3: Idempotency - now the plan should be empty
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesOrderB),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// TestAccServer_volumeShrinkRejected verifies the provider refuses to shrink a
// volume with a clear error (the check fires before any API call, so the
// server is left untouched and the original size survives).
func TestAccServer_volumeShrinkRejected(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-srv-shrink-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	volumesInitial := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
	}
	// 20480 is a valid size on its own (multiple of 10240 — the plan-time
	// validator), so the plan goes through and the apply-time shrink check fires.
	volumesShrunk := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 20480},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesInitial),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
				},
			},
			// Shrinking must fail with the provider's validation error.
			{
				Config:      testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesShrunk),
				ExpectError: regexp.MustCompile(`cannot decrease size`),
			},
			// The original size must have survived the failed apply.
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumesInitial),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// TestAccServer_import tests import functionality
func TestAccServer_import(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-server-import-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	volumes := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data", "size_mb": 10240},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServerConfig_multipleVolumes(serverName, locationID, imageID, volumes),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
				},
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				// password/login are sensitive and may differ.
				// `volumes` is skipped because ImportStateVerify compares lists
				// POSITIONALLY: state after create keeps the config's volume
				// order, while import has no config and maps volumes in the
				// order the API returns them (by volume id). The two coincide
				// only when the boot volume happens to get the lowest id, which
				// made this test a coin flip. Volumes are verified as an
				// unordered set below instead.
				ImportStateVerifyIgnore: []string{"password", "login", "volumes"},
				ImportStateCheck:        checkImportedVolumes(volumes),
			},
		},
	})
}

// checkImportedVolumes verifies that the imported state carries exactly the
// expected volumes, comparing them as an unordered set of name+size pairs and
// requiring every volume to have an id. `number` is deliberately not checked:
// import auto-generates numbers by index (documented in the import warning),
// so they need not match the config.
func checkImportedVolumes(expected []map[string]any) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		if len(states) != 1 {
			return fmt.Errorf("expected exactly 1 imported state, got %d", len(states))
		}
		attrs := states[0].Attributes

		count, err := strconv.Atoi(attrs["volumes.#"])
		if err != nil {
			return fmt.Errorf("imported state has no volume count: %s", err)
		}
		if count != len(expected) {
			return fmt.Errorf("imported %d volume(s), expected %d", count, len(expected))
		}

		got := make(map[string]bool, count)
		for i := range count {
			name := attrs[fmt.Sprintf("volumes.%d.name", i)]
			size := attrs[fmt.Sprintf("volumes.%d.size_mb", i)]
			if id := attrs[fmt.Sprintf("volumes.%d.id", i)]; id == "" || id == "0" {
				return fmt.Errorf("imported volume %q has no id", name)
			}
			got[name+"/"+size] = true
		}

		for _, v := range expected {
			key := fmt.Sprintf("%v/%v", v["name"], v["size_mb"])
			if !got[key] {
				return fmt.Errorf("imported volumes are missing %q (imported: %v)", key, got)
			}
		}
		return nil
	}
}
