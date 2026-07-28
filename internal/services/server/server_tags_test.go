package vstack_server_test

import (
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

// TestAccServer_tagsUnordered — a regression test for the bug with tag
// ordering on a server. The tags are NOT in alphabetical order; the API
// returns them in a different order. While `tags` was a List, apply failed
// with "Provider produced inconsistent result after apply". With Set the
// order doesn't matter: apply succeeds, and a repeat plan is empty.
func TestAccServer_tagsUnordered(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-server-tags-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")
	tags := `["zebra", "mango", "alpha"]`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: create with unordered tags — should not fail.
			{
				Config: testAccServerConfig_tagsUnordered(serverName, locationID, imageID, tags),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("tags"),
						knownvalue.SetExact([]knownvalue.Check{
							knownvalue.StringExact("alpha"),
							knownvalue.StringExact("mango"),
							knownvalue.StringExact("zebra"),
						}),
					),
				},
			},
			// Step 2: the same configuration — the plan should be empty.
			{
				Config: testAccServerConfig_tagsUnordered(serverName, locationID, imageID, tags),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

func testAccServerConfig_tagsUnordered(name, locationID, imageID, tags string) string {
	return fmt.Sprintf(`
resource "vcp_server" "test" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = 1
  ram_mb      = 2048
  tags        = %s
  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    }
  ]
}
`, name, locationID, imageID, tags)
}
