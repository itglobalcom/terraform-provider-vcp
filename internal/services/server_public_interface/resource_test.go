package server_public_interface_test

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

// TestAccServerPublicInterface_basic covers the public NIC lifecycle: create
// with bandwidth 50, in-place bandwidth update to 80 (same NIC id, plan is
// Update), an empty plan afterwards, and import via "server_id:nic_id".
func TestAccServerPublicInterface_basic(t *testing.T) {
	resourceName := "vcp_server_public_interface.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")
	name := "test-acc-srvpub-" + acctest.RandomString(6)

	compareNICIDsSame := statecheck.CompareValue(compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckServersDestroyed,
		Steps: []resource.TestStep{
			// 1. Create with bandwidth 50.
			{
				Config: testAccPublicInterfaceConfig(name, locationID, imageID, 50),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"), knownvalue.Int64Exact(50)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ip_address"), knownvalue.NotNull()),
					compareNICIDsSame.AddStateValue(resourceName, tfjsonpath.New("id")),
				},
			},
			// 2. Update bandwidth in place — must not replace the NIC.
			{
				Config: testAccPublicInterfaceConfig(name, locationID, imageID, 80),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"), knownvalue.Int64Exact(80)),
					compareNICIDsSame.AddStateValue(resourceName, tfjsonpath.New("id")),
				},
			},
			// 3. Idempotency.
			{
				Config:   testAccPublicInterfaceConfig(name, locationID, imageID, 80),
				PlanOnly: true,
			},
			// 4. Import.
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "server_id", "id"),
			},
		},
	})
}

func testAccPublicInterfaceConfig(name, locationID, imageID string, bandwidth int) string {
	return fmt.Sprintf(`
resource "vcp_server" "test" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = 1
  ram_mb      = 2048
  volumes = [
    { number = 0, name = "boot", size_mb = 30720 }
  ]
}

resource "vcp_server_public_interface" "test" {
  server_id      = vcp_server.test.id
  bandwidth_mbps = %d
}
`, name, locationID, imageID, bandwidth)
}
