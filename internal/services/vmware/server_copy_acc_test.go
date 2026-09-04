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
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

const copyResourceName = "vcp_vmware_server.copy"

// TestAccVmwareServerCopy_lifecycle orders a machine, copies it, and lives with
// the copy: the specification comes from the source, the plan is empty
// afterwards, and destroying the copy leaves the source alone.
//
// This orders two machines and waits for a copy, so it is one of the slowest
// tests in the suite — a copy was measured at a little over two minutes.
func TestAccVmwareServerCopy_lifecycle(t *testing.T) {
	name := testName("copy")
	var sourceID, copyID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccServerCopyConfig(t, name),
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_server.test", "id", &sourceID),
					captureAttr(copyResourceName, "id", &copyID),
					statecheck.ExpectKnownValue(copyResourceName, tfjsonpath.New("name"),
						knownvalue.StringExact(name+"-copy")),
					// The specification is the source's, read back from the platform
					// rather than restated in the configuration.
					statecheck.ExpectKnownValue(copyResourceName, tfjsonpath.New("cpu"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(copyResourceName, tfjsonpath.New("image_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(copyResourceName, tfjsonpath.New("system_disk_mb"), knownvalue.NotNull()),
				},
				Check: resource.ComposeTestCheckFunc(
					checkCopyIsItsOwnMachine(&sourceID, &copyID, name+"-copy"),
					checkCopyMatchesSource(&sourceID, &copyID),
				),
			},
			// A copy has to plan empty like anything else — and a create-only
			// argument the API never reports back is the likeliest thing to make it
			// not.
			{Config: testAccServerCopyConfig(t, name), PlanOnly: true},
			// Resizing the copy is an ordinary in-place change: the create-time
			// restriction on the arguments beside copy_from_server_id must not
			// outlive the create.
			{
				Config: testAccServerCopyResizedConfig(t, name),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(copyResourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: checkServerIDUnchanged(copyResourceName, &copyID),
			},
			// The copy goes; the source stays.
			{
				Config: catalogConfig(t) + serverConfig(t, "test", name, ""),
				Check:  checkServerGone(&copyID),
			},
		},
	})
}

// TestAccVmwareServerCopy_refusesOrderArguments checks the plan-time refusal on a
// real configuration: a copy carries a name and nothing else, so an order-time
// argument beside it has to be reported before anything is created.
func TestAccVmwareServerCopy_refusesOrderArguments(t *testing.T) {
	name := testName("copyargs")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config:      testAccServerCopyWithImageConfig(t, name),
				PlanOnly:    true,
				ExpectError: regexpAny(),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

// testAccServerCopyConfig declares a machine and a copy of it. The copy states
// only where it comes from and what it is called — everything else is the
// source's, and the plan refuses it here.
func testAccServerCopyConfig(t *testing.T, name string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, "") + fmt.Sprintf(`
resource "vcp_vmware_server" "copy" {
  copy_from_server_id = vcp_vmware_server.test.id
  name                = %q
}
`, name+"-copy")
}

// testAccServerCopyResizedConfig gives the copy a CPU count of its own, which is
// the ordinary in-place change every other server takes.
func testAccServerCopyResizedConfig(t *testing.T, name string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, "") + fmt.Sprintf(`
resource "vcp_vmware_server" "copy" {
  copy_from_server_id = vcp_vmware_server.test.id
  name                = %q
  cpu                 = 2
}
`, name+"-copy")
}

// testAccServerCopyWithImageConfig asks for a copy and an image at once, which
// the platform cannot do: the copy request carries neither an image nor a size.
func testAccServerCopyWithImageConfig(t *testing.T, name string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, "") + fmt.Sprintf(`
resource "vcp_vmware_server" "copy" {
  copy_from_server_id = vcp_vmware_server.test.id
  name                = %q
  image_id            = %s
}
`, name+"-copy", vmwareImageID(t))
}

// ============================================================================
// Checks
// ============================================================================

// checkCopyIsItsOwnMachine asserts, from the API, that the copy is a separate
// server under the name it was asked for. State agreeing with itself would pass
// a provider that recorded the source's id and copied nothing.
func checkCopyIsItsOwnMachine(sourceID, copyID *string, wantName string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if *copyID == *sourceID {
			return fmt.Errorf("the copy is recorded under the source's id %s", *sourceID)
		}
		server, err := acctest.GetTestClient().GetVmwareServer(context.Background(), mustAtoiErr(*copyID))
		if err != nil {
			return fmt.Errorf("reading the copy %s: %w", *copyID, err)
		}
		if server.Name != wantName {
			return fmt.Errorf("the copy %s is named %q, want %q", *copyID, server.Name, wantName)
		}
		return nil
	}
}

// checkCopyMatchesSource asserts the copy carries the source's specification —
// which is the whole point of the operation, and the reason the order-time
// arguments are refused beside it.
func checkCopyMatchesSource(sourceID, copyID *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := acctest.GetTestClient()
		source, err := client.GetVmwareServer(context.Background(), mustAtoiErr(*sourceID))
		if err != nil {
			return fmt.Errorf("reading the source %s: %w", *sourceID, err)
		}
		copied, err := client.GetVmwareServer(context.Background(), mustAtoiErr(*copyID))
		if err != nil {
			return fmt.Errorf("reading the copy %s: %w", *copyID, err)
		}
		if copied.CPU != source.CPU || copied.RamMB != source.RamMB || copied.SystemDiskMB != source.SystemDiskMB {
			return fmt.Errorf("the copy is %d vCPU / %d MB / %d MB disk, the source is %d / %d / %d",
				copied.CPU, copied.RamMB, copied.SystemDiskMB, source.CPU, source.RamMB, source.SystemDiskMB)
		}
		if copied.ImageID != source.ImageID || copied.LocationID != source.LocationID {
			return fmt.Errorf("the copy carries image %d in location %d, the source image %d in location %d",
				copied.ImageID, copied.LocationID, source.ImageID, source.LocationID)
		}
		return nil
	}
}

// checkServerGone asserts a server is no longer on the platform.
func checkServerGone(serverID *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		server, err := acctest.GetTestClient().GetVmwareServer(context.Background(), mustAtoiErr(*serverID))
		if sdk.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading server %s: %w", *serverID, err)
		}
		// A delete task finishes before the object disappears, so a server still
		// reported as deleting is the expected answer rather than a failure.
		if server.State == entities.VmwareServerStateDeleting {
			return nil
		}
		return fmt.Errorf("server %s is still on the platform in state %q", *serverID, server.State)
	}
}
