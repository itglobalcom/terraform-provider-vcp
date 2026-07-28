package isolated_network_test

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// TestAccNetwork_tagsUnordered — a regression test for the tag ordering bug.
// Tags are given NOT in alphabetical order; the API returns them in a different order. While `tags`
// was a List, the first apply used to fail with "Provider produced inconsistent result after
// apply (.tags[N] ...)". With a Set, order doesn't matter: apply succeeds and the subsequent
// plan is empty (idempotency).
func TestAccNetwork_tagsUnordered(t *testing.T) {
	resourceName := "vcp_network.test"
	networkName := "test-acc-network-tags-unordered-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	tags := []string{"zebra", "mango", "alpha", "delta"}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckNetworksDestroyed,
		Steps: []resource.TestStep{
			// Step 1: create with unordered tags — should not fail.
			{
				Config: testAccNetworkConfig_tags(networkName, locationID, tags),
				ConfigStateChecks: []statecheck.StateCheck{
					checkNetworkExists{resourceAddress: resourceName},
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("tags"),
						knownvalue.SetExact([]knownvalue.Check{
							knownvalue.StringExact("alpha"),
							knownvalue.StringExact("delta"),
							knownvalue.StringExact("mango"),
							knownvalue.StringExact("zebra"),
						}),
					),
				},
			},
			// Step 2: same configuration — plan should be empty (no perpetual diff).
			{
				Config: testAccNetworkConfig_tags(networkName, locationID, tags),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}
