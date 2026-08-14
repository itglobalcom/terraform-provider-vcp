package vmware_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// TestAccVmwareNetwork_isolated is the full life of an isolated network as a
// user lives it: create, read back what the platform filled in, rename without
// losing the network, import, destroy.
//
// The `PlanOnly` step is the one that matters most. `enable_dhcp` is a write-only
// input the API never returns, so a resource that recorded it wrongly would
// produce a plan on every run for as long as it existed.
func TestAccVmwareNetwork_isolated(t *testing.T) {
	resourceName := "vcp_vmware_network.test"
	name := testName("iso")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			// 1. Create, and check what only the platform can know.
			{
				Config: testAccNetworkIsolatedConfig(t, name, "10.231.1.0"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("type"), knownvalue.StringExact("isolated")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("address"), knownvalue.StringExact("10.231.1.0")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("mask"), knownvalue.Int64Exact(24)),
					// The API answers with a granular type (private_client); the
					// resource has to map it back to the selector the user wrote,
					// or every apply ends in "inconsistent result".
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("gateway"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("state"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("nics_count"), knownvalue.Int64Exact(0)),
					// NET-3: an isolated network has no bandwidth to report.
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"), knownvalue.Null()),
				},
			},
			// 2. Nothing changed — the plan has to be empty.
			{Config: testAccNetworkIsolatedConfig(t, name, "10.231.1.0"), PlanOnly: true},
			// 3. Reading the same network back through the data source has to agree
			// with the resource — the two share a mapper, and this is what proves
			// the data source is wired to it.
			{
				Config: testAccNetworkWithDataSourceConfig(t, name, "10.231.1.0"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.CompareValuePairs(
						"data.vcp_vmware_network.by_id", tfjsonpath.New("name"),
						resourceName, tfjsonpath.New("name"),
						compare.ValuesSame(),
					),
					statecheck.CompareValuePairs(
						"data.vcp_vmware_network.by_id", tfjsonpath.New("address"),
						resourceName, tfjsonpath.New("address"),
						compare.ValuesSame(),
					),
					// The data source reports the API's granular type, not the coarse
					// selector the resource takes — a difference worth pinning, since
					// it is exactly what the resource has to map back.
					statecheck.ExpectKnownValue("data.vcp_vmware_network.by_id",
						tfjsonpath.New("type"), knownvalue.StringExact("private_client")),
				},
			},
			// 4. Renaming is an in-place edit, not a new network.
			{
				Config: testAccNetworkIsolatedConfig(t, name+"-renamed", "10.231.1.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("name"),
						knownvalue.StringExact(name+"-renamed")),
				},
			},
			// 5. Import: the id is all a user has from the panel, and everything
			// else has to come from the API. enable_dhcp is write-only, so an
			// imported network cannot know it.
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"enable_dhcp", "capacity"},
			},
		},
	})
}

// TestAccVmwareNetwork_routed covers the flavour that carries an edge, and the
// one field of it that is editable in place. NET-8: network bandwidth and edge
// bandwidth are the same value, and this is the only way to set it — the
// dedicated endpoint confirms success without storing anything.
func TestAccVmwareNetwork_routed(t *testing.T) {
	resourceName := "vcp_vmware_network.test"
	name := testName("routed")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: routedNetworkConfig(t, name, "10.231.2.0"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("type"), knownvalue.StringExact("routed")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"),
						knownvalue.Int64Exact(testAccNetworkBandwidth)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("gateway"), knownvalue.NotNull()),
				},
			},
			{Config: routedNetworkConfig(t, name, "10.231.2.0"), PlanOnly: true},
			// The bandwidth is raised in place — the network (and its edge, and
			// anything attached to it) survives the change.
			{
				Config: testAccNetworkRoutedBandwidthConfig(t, name, "10.231.2.0", testAccNetworkBandwidth+10),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"),
						knownvalue.Int64Exact(testAccNetworkBandwidth+10)),
				},
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"enable_dhcp", "capacity"},
			},
		},
	})
}

// TestAccVmwareNetwork_public covers the public flavour, which is ordered by
// capacity rather than by address range.
//
// Gated behind VCP_VMWARE_TEST_PUBLIC_NETWORK: a public network consumes one of
// the location's free address blocks, and a location that has none answers
// -12043 — a failure of the stand, not of the provider.
func TestAccVmwareNetwork_public(t *testing.T) {
	if os.Getenv("VCP_VMWARE_TEST_PUBLIC_NETWORK") == "" {
		t.Skip("set VCP_VMWARE_TEST_PUBLIC_NETWORK=1 to order a public network (consumes an address block)")
	}

	resourceName := "vcp_vmware_network.test"
	name := testName("pub")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccNetworkPublicConfig(t, name, "1"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("type"), knownvalue.StringExact("public")),
					// A public network is handed an address range by the platform.
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("address"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"),
						knownvalue.Int64Exact(testAccNetworkBandwidth)),
				},
			},
			// capacity is write-only, so it is the field most likely to produce a
			// perpetual diff.
			{Config: testAccNetworkPublicConfig(t, name, "1"), PlanOnly: true},
		},
	})
}

// TestAccVmwareNetwork_forcesReplacement covers the attributes a user cannot
// change on an existing network. The API has no endpoint for any of them, so the
// resource has to replace rather than silently ignore the change.
func TestAccVmwareNetwork_forcesReplacement(t *testing.T) {
	resourceName := "vcp_vmware_network.test"
	name := testName("repl")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{Config: testAccNetworkIsolatedConfig(t, name, "10.231.3.0")},
			// A different address range is a different network.
			{
				Config: testAccNetworkIsolatedConfig(t, name, "10.231.4.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("address"),
						knownvalue.StringExact("10.231.4.0")),
				},
			},
			// So is a different flavour.
			{
				Config: routedNetworkConfig(t, name, "10.231.4.0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("type"), knownvalue.StringExact("routed")),
				},
			},
		},
	})
}

// TestAccVmwareNetwork_disappears covers a network deleted from the panel: the
// refresh has to survive the 404 and drop the resource from state, so the next
// plan offers to create it again instead of failing.
func TestAccVmwareNetwork_disappears(t *testing.T) {
	resourceName := "vcp_vmware_network.test"
	name := testName("gone")
	config := testAccNetworkIsolatedConfig(t, name, "10.231.5.0")

	var networkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "id", &networkID),
				},
			},
			{
				PreConfig:          func() { deleteNetworkOutOfBand(t, networkID) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccVmwareNetwork_rejectsBandwidthOnIsolated covers an attribute that does
// not belong to the flavour in hand. Whoever refuses it — the provider at plan
// time or the API at apply time — the one outcome a user must never get is a
// network that reports success while quietly ignoring what was asked for.
//
// Today the refusal comes from the API as a generic "bandwidth outside allowable
// limits" (NET-3), which names neither the field nor the reason; §6 of the plan
// moves the check into the provider. The test holds either way, and the message
// is what improves.
func TestAccVmwareNetwork_rejectsBandwidthOnIsolated(t *testing.T) {
	name := testName("isobw")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: networkConfig(t, "test", name, "isolated", fmt.Sprintf(`  address        = "10.231.6.0"
  mask           = 24
  bandwidth_mbps = %d
`, testAccNetworkBandwidth)),
				// Whoever refuses first — the provider at plan time or the API at
				// apply time — the configuration must not silently succeed.
				ExpectError: regexpAny(),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

func testAccNetworkIsolatedConfig(t *testing.T, name, address string) string {
	t.Helper()
	return networkConfig(t, "test", name, "isolated", fmt.Sprintf(`  address     = %q
  mask        = 24
  enable_dhcp = false
`, address))
}

// testAccNetworkWithDataSourceConfig reads the network back through the
// by-id data source, next to the resource that created it.
func testAccNetworkWithDataSourceConfig(t *testing.T, name, address string) string {
	t.Helper()
	return testAccNetworkIsolatedConfig(t, name, address) + `
data "vcp_vmware_network" "by_id" {
  id = vcp_vmware_network.test.id
}
`
}

func testAccNetworkRoutedBandwidthConfig(t *testing.T, name, address string, bandwidth int) string {
	t.Helper()
	return networkConfig(t, "test", name, "routed", fmt.Sprintf(`  address        = %q
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = %d
`, address, bandwidth))
}

func testAccNetworkPublicConfig(t *testing.T, name, capacity string) string {
	t.Helper()
	return networkConfig(t, "test", name, "public", fmt.Sprintf(`  capacity       = %q
  bandwidth_mbps = %d
`, capacity, testAccNetworkBandwidth))
}
