package isolated_network_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// ============================================================================
// CUSTOM STATE CHECKS
// ============================================================================

// checkNetworkExists - custom state check to verify network exists in API
type checkNetworkExists struct {
	resourceAddress string
}

func (c checkNetworkExists) CheckState(
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
		resp.Error = fmt.Errorf("No Network ID is set")
		return
	}

	client := acctest.GetTestClient()
	network, err := client.GetNetwork(ctx, id)
	if err != nil {
		resp.Error = fmt.Errorf("Error fetching network: %s", err)
		return
	}

	if network.ID != id {
		resp.Error = fmt.Errorf("Network not found in API")
	}
}

// checkNetworkAttributes - custom state check to verify network attributes in API
type checkNetworkAttributes struct {
	resourceAddress string
	expectedName    string
}

func (c checkNetworkAttributes) CheckState(
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
	network, err := client.GetNetwork(ctx, id)
	if err != nil {
		resp.Error = err
		return
	}

	if network.Name != c.expectedName {
		resp.Error = fmt.Errorf("Expected name %s, got %s", c.expectedName, network.Name)
	}
}

// deleteNetworkOutOfBand removes the network with the given name directly via
// the API and waits until it is actually gone (deletion may be asynchronous).
func deleteNetworkOutOfBand(t *testing.T, name string) {
	client := acctest.GetTestClient()
	ctx := context.Background()

	networks, err := client.GetNetworkList(ctx)
	if err != nil {
		t.Fatalf("out-of-band network list failed: %v", err)
	}

	var networkID string
	for _, network := range networks {
		if network.Name == name {
			networkID = network.ID
			break
		}
	}
	if networkID == "" {
		t.Fatalf("network %s not found to delete out-of-band", name)
	}

	if err := client.DeleteNetwork(ctx, networkID); err != nil {
		t.Fatalf("out-of-band network delete failed: %v", err)
	}

	for i := 0; i < 30; i++ {
		_, err := client.GetNetwork(ctx, networkID)
		if sdk.IsNotFound(err) {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("out-of-band deleted network did not disappear in time")
}

// ============================================================================
// CONFIGURATION TEMPLATES
// ============================================================================

func testAccNetworkConfig_basic(name, locationID string) string {
	return fmt.Sprintf(`
resource "vcp_network" "test" {
  name        = "%s"
  location_id = "%s"
}
`, name, locationID)
}

func testAccNetworkConfig_full(name, description, locationID, networkPrefix string, mask int) string {
	return fmt.Sprintf(`
resource "vcp_network" "test" {
  name           = "%s"
  description    = "%s"
  location_id    = "%s"
  network_prefix = "%s"
  mask           = %d
}
`, name, description, locationID, networkPrefix, mask)
}

func testAccNetworkConfig_tags(name, locationID string, tags []string) string {
	var tagsHCL string
	if len(tags) > 0 {
		tagsHCL = "\n  tags = [\n"
		for _, tag := range tags {
			tagsHCL += fmt.Sprintf("    %q,\n", tag)
		}
		tagsHCL += "  ]"
	}

	return fmt.Sprintf(`
resource "vcp_network" "test" {
  name        = %[1]q
  location_id = %[2]q%[3]s
}
`, name, locationID, tagsHCL)
}

// ============================================================================
// ACCEPTANCE TESTS
// ============================================================================

// TestAccNetwork_basic tests basic network creation
func TestAccNetwork_basic(t *testing.T) {
	resourceName := "vcp_network.test"
	networkName := "test-acc-network-basic-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccNetworkConfig_basic(networkName, locationID),
				ConfigStateChecks: []statecheck.StateCheck{
					// Custom check: verify network exists in API
					checkNetworkExists{resourceAddress: resourceName},

					// Check required attributes
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(networkName),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID),
					),

					// Check computed attributes are set
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("network_prefix"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("mask"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("created"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// TestAccNetwork_full tests network creation with all optional parameters
func TestAccNetwork_full(t *testing.T) {
	resourceName := "vcp_network.test"
	networkName := "test-acc-network-full-" + acctest.RandomString(6)
	description := "Test network with all parameters"
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccNetworkConfig_full(networkName, description, locationID, "10.100.0.0", 24),
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(networkName),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("description"),
						knownvalue.StringExact(description),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("network_prefix"),
						knownvalue.StringExact("10.100.0.0"),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("mask"),
						knownvalue.Int64Exact(24),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("created"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// TestAccNetwork_update tests updating network attributes
func TestAccNetwork_update(t *testing.T) {
	resourceName := "vcp_network.test"
	networkNameOriginal := "test-acc-network-update-orig-" + acctest.RandomString(6)
	networkNameUpdated := "test-acc-network-update-upd-" + acctest.RandomString(6)
	descriptionOriginal := "Original description"
	descriptionUpdated := "Updated description"
	locationID := os.Getenv("VCP_LOCATION_ID")

	// Comparator to check ID stability
	compareIDsSame := statecheck.CompareValue(compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			// Step 1: Create with original values
			{
				Config: testAccNetworkConfig_full(networkNameOriginal, descriptionOriginal, locationID, "10.100.0.0", 24),
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(networkNameOriginal),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("description"),
						knownvalue.StringExact(descriptionOriginal),
					),

					// Save ID for comparison
					compareIDsSame.AddStateValue(
						resourceName,
						tfjsonpath.New("id"),
					),
				},
			},
			// Step 2: Update attributes
			{
				Config: testAccNetworkConfig_full(networkNameUpdated, descriptionUpdated, locationID, "10.100.0.0", 24),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// Should be update, not replace
						plancheck.ExpectResourceAction(
							resourceName,
							plancheck.ResourceActionUpdate,
						),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},
					checkNetworkAttributes{
						resourceAddress: resourceName,
						expectedName:    networkNameUpdated,
					},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(networkNameUpdated),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("description"),
						knownvalue.StringExact(descriptionUpdated),
					),

					// Verify ID didn't change (update, not replace)
					compareIDsSame.AddStateValue(
						resourceName,
						tfjsonpath.New("id"),
					),
				},
			},
			// Step 3: Idempotency check
			{
				Config: testAccNetworkConfig_full(networkNameUpdated, descriptionUpdated, locationID, "10.100.0.0", 24),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// TestAccNetwork_tags tests network tag management
func TestAccNetwork_tags(t *testing.T) {
	resourceName := "vcp_network.test"
	networkName := "test-acc-network-tags-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			// Step 1: Create with initial tags
			{
				Config: testAccNetworkConfig_tags(networkName, locationID, []string{"tag1", "tag2"}),
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},

					// Check tags list size
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("tags"),
						knownvalue.SetSizeExact(2),
					),
					// Check specific tags exist
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("tags"),
						knownvalue.SetExact([]knownvalue.Check{
							knownvalue.StringExact("tag1"),
							knownvalue.StringExact("tag2"),
						}),
					),
				},
			},
			// Step 2: Update tags
			{
				Config: testAccNetworkConfig_tags(networkName, locationID, []string{"tag2", "tag3", "tag4"}),
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("tags"),
						knownvalue.SetSizeExact(3),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("tags"),
						knownvalue.SetExact([]knownvalue.Check{
							knownvalue.StringExact("tag2"),
							knownvalue.StringExact("tag3"),
							knownvalue.StringExact("tag4"),
						}),
					),
				},
			},
			// Step 3: Remove all tags
			{
				Config: testAccNetworkConfig_basic(networkName, locationID),
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("tags"),
						knownvalue.SetSizeExact(0),
					),
				},
			},
		},
	})
}

// TestAccNetwork_import tests import functionality
func TestAccNetwork_import(t *testing.T) {
	resourceName := "vcp_network.test"
	networkName := "test-acc-network-import-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccNetworkConfig_basic(networkName, locationID),
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},
				},
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccNetwork_replaceOnMaskChange verifies that changing the subnet mask
// plans a replace and recreates the network (network_prefix and mask are
// RequiresReplace); unlike the location test this needs no second location.
func TestAccNetwork_replaceOnMaskChange(t *testing.T) {
	resourceName := "vcp_network.test"
	networkName := "test-acc-network-mask-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	compareIDsDiffer := statecheck.CompareValue(compare.ValuesDiffer())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccNetworkConfig_full(networkName, "mask replace test", locationID, "10.101.0.0", 24),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("mask"), knownvalue.Int64Exact(24)),
					compareIDsDiffer.AddStateValue(resourceName, tfjsonpath.New("id")),
				},
			},
			{
				Config: testAccNetworkConfig_full(networkName, "mask replace test", locationID, "10.101.0.0", 25),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("mask"), knownvalue.Int64Exact(25)),
					compareIDsDiffer.AddStateValue(resourceName, tfjsonpath.New("id")),
				},
			},
		},
	})
}

// TestAccNetwork_disappears deletes the network out-of-band and checks the
// provider detects it is gone (Read → RemoveResource) and plans to recreate it.
func TestAccNetwork_disappears(t *testing.T) {
	networkName := "test-acc-network-dis-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	config := testAccNetworkConfig_basic(networkName, locationID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { deleteNetworkOutOfBand(t, networkName) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccNetwork_requiresReplace tests that changing location_id forces replacement
func TestAccNetwork_requiresReplace(t *testing.T) {
	resourceName := "vcp_network.test"
	networkName := "test-acc-network-replace-" + acctest.RandomString(6)
	locationID1 := os.Getenv("VCP_LOCATION_ID")
	locationID2 := os.Getenv("VCP_LOCATION_ID_ALT")

	if locationID2 == "" {
		t.Skip("VCP_LOCATION_ID_ALT not set, skipping requires replace test")
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccNetworkConfig_basic(networkName, locationID1),
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID1),
					),
				},
			},
			{
				Config: testAccNetworkConfig_basic(networkName, locationID2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						// location_id is ForceNew, should trigger replace
						plancheck.ExpectResourceAction(
							resourceName,
							plancheck.ResourceActionReplace,
						),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},

					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID2),
					),
				},
			},
		},
	})
}

// ============================================================================
// DATA SOURCE TESTS
// ============================================================================

// TestAccDataSourceNetwork_basic verifies the singular data source mirrors the
// resource's id/name/location_id.
func TestAccDataSourceNetwork_basic(t *testing.T) {
	resourceName := "vcp_network.test"
	dataSourceName := "data.vcp_network.by_id"
	name := "test-acc-ds-net-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	cfgCreate := testAccNetworkConfig_basic(name, locationID)
	cfgDS := fmt.Sprintf(`
%s

data "vcp_network" "by_id" {
  id = vcp_network.test.id
}
`, cfgCreate)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfgDS,
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
				},
			},
		},
	})
}

// TestAccDataSourceNetworks_list verifies the plural data source includes two
// freshly created networks with the right location.
func TestAccDataSourceNetworks_list(t *testing.T) {
	dataSourceName := "data.vcp_networks.all"
	name1 := "test-acc-ds-nets-a-" + acctest.RandomString(4)
	name2 := "test-acc-ds-nets-b-" + acctest.RandomString(4)
	locationID := os.Getenv("VCP_LOCATION_ID")

	cfg := fmt.Sprintf(`
resource "vcp_network" "a" {
  name        = %q
  location_id = %q
}

resource "vcp_network" "b" {
  name        = %q
  location_id = %q
}

data "vcp_networks" "all" {
  depends_on = [vcp_network.a, vcp_network.b]
}
`, name1, locationID, name2, locationID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{

					// Custom check: look for the network with name1
					checkNetworkInList{
						dataSourceAddress:  dataSourceName,
						expectedName:       name1,
						expectedLocationID: locationID,
					},

					// Custom check: look for the network with name2
					checkNetworkInList{
						dataSourceAddress:  dataSourceName,
						expectedName:       name2,
						expectedLocationID: locationID,
					},
				},
			},
		},
	})
}

// checkNetworkInList - custom state check to verify a network with specific name exists in the list
type checkNetworkInList struct {
	dataSourceAddress  string
	expectedName       string
	expectedLocationID string
}

func (c checkNetworkInList) CheckState(
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

	// Get the list of networks
	networksRaw, ok := resource.AttributeValues["networks"]
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'networks' not found")
		return
	}

	networks, ok := networksRaw.([]any)
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'networks' is not a list")
		return
	}

	// Look for the network with the desired name
	found := false
	for _, netRaw := range networks {
		net, ok := netRaw.(map[string]any)
		if !ok {
			continue
		}

		name, _ := net["name"].(string)
		locationID, _ := net["location_id"].(string)

		if name == c.expectedName {
			found = true

			// Verify location_id
			if locationID != c.expectedLocationID {
				resp.Error = fmt.Errorf(
					"Network %s found but location_id mismatch: expected %s, got %s",
					c.expectedName,
					c.expectedLocationID,
					locationID,
				)
				return
			}

			break
		}
	}

	if !found {
		resp.Error = fmt.Errorf("Network with name %s not found in list", c.expectedName)
	}
}
