package vstack_server_test

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

// TestAccServer_updateComputeAndName pins the in-place update semantics of
// cpu, ram_mb and name: none of them may trigger a replace, and the server ID
// must survive the update.
func TestAccServer_updateComputeAndName(t *testing.T) {
	resourceName := "vcp_server.test"
	serverName := "test-acc-srv-upd-" + acctest.RandomString(6)
	serverNameUpdated := serverName + "-renamed"
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	compareServerIDsSame := statecheck.CompareValue(compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			// Step 1: Create with the initial compute size.
			{
				Config: testAccServerConfig_compute(serverName, locationID, imageID, 1, 1024),
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("cpu"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ram_mb"), knownvalue.Int64Exact(1024)),
					compareServerIDsSame.AddStateValue(resourceName, tfjsonpath.New("id")),
				},
			},
			// Step 2: Grow cpu/ram and rename — must be an in-place update.
			{
				Config: testAccServerConfig_compute(serverNameUpdated, locationID, imageID, 2, 2048),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: resourceName},
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("name"), knownvalue.StringExact(serverNameUpdated)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("cpu"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ram_mb"), knownvalue.Int64Exact(2048)),
					compareServerIDsSame.AddStateValue(resourceName, tfjsonpath.New("id")),
				},
			},
			// Step 3: Idempotency — the plan should be empty.
			{
				Config: testAccServerConfig_compute(serverNameUpdated, locationID, imageID, 2, 2048),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

func testAccServerConfig_compute(name, locationID, imageID string, cpu, ramMB int) string {
	return fmt.Sprintf(`
resource "vcp_server" "test" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = %d
  ram_mb      = %d

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    }
  ]
}
`, name, locationID, imageID, cpu, ramMB)
}
