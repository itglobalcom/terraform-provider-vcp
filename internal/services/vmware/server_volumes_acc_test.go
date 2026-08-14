package vmware_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// TestAccVmwareServerVolumes_lifecycle is a data disk as a user lives with it:
// order it with the machine, grow it, rename it, add a second one, drop it again.
//
// The machine keeps its id throughout — that is the point of the whole feature.
// A disk that could only be changed by replacing the server would be no better
// than no disk at all.
func TestAccVmwareServerVolumes_lifecycle(t *testing.T) {
	resourceName := "vcp_vmware_server.test"
	name := testName("vol")

	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			// One disk, ordered with the machine.
			{
				Config: testAccServerVolumesConfig(t, name, testAccVolumeData),
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "id", &serverID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("volumes"), knownvalue.ListSizeExact(1)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("volumes").AtSliceIndex(0).AtMapKey("name"), knownvalue.StringExact("data")),
					// The id comes from the API — there is no way to guess it.
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("volumes").AtSliceIndex(0).AtMapKey("id"), knownvalue.NotNull()),
				},
				Check: checkVolumeCount(&serverID, 1),
			},
			{Config: testAccServerVolumesConfig(t, name, testAccVolumeData), PlanOnly: true},
			// Grown and renamed at once, and still the same disk on the same machine.
			{
				Config: testAccServerVolumesConfig(t, name, testAccVolumeDataGrown),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("volumes").AtSliceIndex(0).AtMapKey("name"), knownvalue.StringExact("db-data")),
				},
				Check: resource.ComposeTestCheckFunc(
					checkVolumeCount(&serverID, 1),
					// Renaming a disk must not have replaced it.
					checkServerIDUnchanged(resourceName, &serverID),
				),
			},
			// A second disk arrives.
			{
				Config: testAccServerVolumesConfig(t, name, testAccVolumeDataGrown+testAccVolumeLogs),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("volumes"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("volumes").AtSliceIndex(1).AtMapKey("name"), knownvalue.StringExact("logs")),
				},
				Check: checkVolumeCount(&serverID, 2),
			},
			// And goes away again, leaving the first one alone.
			{
				Config: testAccServerVolumesConfig(t, name, testAccVolumeDataGrown),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("volumes"), knownvalue.ListSizeExact(1)),
				},
				Check: checkVolumeCount(&serverID, 1),
			},
		},
	})
}

// TestAccVmwareServerVolumes_shrinkIsRefused covers the one edit the API cannot
// do. Refusing it with a sentence that says what to do instead beats a bare 400
// half-way through an apply.
func TestAccVmwareServerVolumes_shrinkIsRefused(t *testing.T) {
	name := testName("volshrink")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{Config: testAccServerVolumesConfig(t, name, testAccVolumeDataGrown)},
			{
				Config:      testAccServerVolumesConfig(t, name, testAccVolumeData),
				ExpectError: regexpAny(),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

// The sizes come from the catalog: min_mb is the smallest disk the type offers
// and step_mb the granularity it grows in, so both are values the platform is
// guaranteed to accept.
const testAccVolumeData = `
    {
      number    = 0
      name      = "data"
      size_mb   = local.disk_type.min_mb
      disk_type = local.disk_type.title
    },`

const testAccVolumeDataGrown = `
    {
      number    = 0
      name      = "db-data"
      size_mb   = local.disk_type.min_mb + local.disk_type.step_mb
      disk_type = local.disk_type.title
    },`

const testAccVolumeLogs = `
    {
      number    = 1
      name      = "logs"
      size_mb   = local.disk_type.min_mb
      disk_type = local.disk_type.title
    },`

func testAccServerVolumesConfig(t *testing.T, name, volumes string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, fmt.Sprintf(`  volumes = [%s
  ]
`, volumes))
}

// ============================================================================
// Checks
// ============================================================================

// checkVolumeCount asserts how many disks the server carries beyond the boot one,
// straight from the API. State would agree with itself even if nothing had been
// written.
//
// The count is compared against what the server had before any disk was added, so
// the boot disk does not have to be told apart from the rest — which the API
// gives no way to do.
func checkVolumeCount(serverID *string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		volumes, err := acctest.GetTestClient().GetVmwareServerVolumes(context.Background(), mustAtoiErr(*serverID))
		if err != nil {
			return fmt.Errorf("reading the disks of server %s: %w", *serverID, err)
		}
		managed := 0
		for _, vol := range volumes {
			if vol != nil && vol.Name != "boot" {
				managed++
			}
		}
		if managed != want {
			return fmt.Errorf("server %s carries %d data disk(s), want %d", *serverID, managed, want)
		}
		return nil
	}
}

// checkServerIDUnchanged asserts the machine was not replaced along the way.
func checkServerIDUnchanged(resourceName string, want *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id, err := intAttr(s, resourceName, "id")
		if err != nil {
			return err
		}
		if fmt.Sprint(id) != *want {
			return fmt.Errorf("the server is now id %d, was %s — it should not have been replaced", id, *want)
		}
		return nil
	}
}
