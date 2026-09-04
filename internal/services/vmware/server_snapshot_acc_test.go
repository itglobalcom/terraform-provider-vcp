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
)

const snapshotResourceName = "vcp_vmware_server_snapshot.test"

// TestAccVmwareServerSnapshot_lifecycle is the snapshot as a user lives with it:
// taken, stable, imported, replaced when renamed, and gone once dropped from the
// configuration while the machine stays.
//
// The last step is the one that cannot be left out: CheckDestroy alone would
// pass a Delete that never ran, because a destroyed server takes its snapshot
// with it whatever the provider did.
func TestAccVmwareServerSnapshot_lifecycle(t *testing.T) {
	name := testName("snap")
	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			// Taken with the machine already in place.
			{
				Config: testAccServerSnapshotConfig(t, name, "before-upgrade"),
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_server.test", "id", &serverID),
					statecheck.ExpectKnownValue(snapshotResourceName, tfjsonpath.New("created"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(snapshotResourceName, tfjsonpath.New("name"),
						knownvalue.StringExact("before-upgrade")),
				},
				Check: checkVmwareSnapshotName(&serverID, "before-upgrade"),
			},
			// A perpetual diff straight after create is the commonest defect and
			// invisible without this step.
			{Config: testAccServerSnapshotConfig(t, name, "before-upgrade"), PlanOnly: true},
			// Imported by the server's id: the snapshot has none of its own.
			{
				ResourceName:      snapshotResourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(snapshotResourceName, "server_id"),
			},
			// The API cannot rename a snapshot, so a rename has to be a
			// replacement — anything else would report a change that never happened.
			{
				Config: testAccServerSnapshotConfig(t, name, "after-upgrade"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(snapshotResourceName, plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					checkVmwareSnapshotName(&serverID, "after-upgrade"),
					// Replacing the snapshot must not have replaced the machine.
					checkServerIDUnchanged("vcp_vmware_server.test", &serverID),
				),
			},
			// Dropped from the configuration: the machine survives, the snapshot
			// does not.
			{
				Config: catalogConfig(t) + serverConfig(t, "test", name, ""),
				Check:  checkVmwareServerHasNoSnapshot(&serverID),
			},
		},
	})
}

// TestAccVmwareServerSnapshot_disappears deletes the snapshot out of band and
// checks the refresh survives it. This is the path the empty-body read guards: a
// server with no snapshot answers 200 with `{}` rather than 404, so a Read that
// only handled a 404 would keep a snapshot in state that no longer exists.
func TestAccVmwareServerSnapshot_disappears(t *testing.T) {
	name := testName("snapdis")
	config := testAccServerSnapshotConfig(t, name, "doomed")
	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigStateChecks: []statecheck.StateCheck{captureAttr("vcp_vmware_server.test", "id", &serverID)},
			},
			{
				PreConfig:          func() { deleteVmwareSnapshotOutOfBand(t, &serverID) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccVmwareServerSnapshot_secondIsRefused covers the refusal a user can act
// on: a VMware server holds one snapshot, and the platform's own error names
// neither the existing snapshot nor a way out.
func TestAccVmwareServerSnapshot_secondIsRefused(t *testing.T) {
	name := testName("snap2")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{Config: testAccServerSnapshotConfig(t, name, "first")},
			{
				Config:      testAccServerSnapshotPairConfig(t, name),
				ExpectError: regexpAny(),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

func testAccServerSnapshotConfig(t *testing.T, name, snapshotName string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, "") + fmt.Sprintf(`
resource "vcp_vmware_server_snapshot" "test" {
  server_id = vcp_vmware_server.test.id
  name      = %q
}
`, snapshotName)
}

// testAccServerSnapshotPairConfig declares a second snapshot of the same server,
// which the platform allows no machine to hold.
func testAccServerSnapshotPairConfig(t *testing.T, name string) string {
	t.Helper()
	return testAccServerSnapshotConfig(t, name, "first") + `
resource "vcp_vmware_server_snapshot" "second" {
  server_id = vcp_vmware_server.test.id
  name      = "second"
}
`
}

// ============================================================================
// Checks
// ============================================================================

// checkVmwareSnapshotName asserts, straight from the API, that the server holds
// a snapshot under the expected name. After a replacement this is what tells the
// new snapshot from the old one still being reported.
func checkVmwareSnapshotName(serverID *string, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		snapshot, err := acctest.GetTestClient().GetVmwareSnapshot(context.Background(), mustAtoiErr(*serverID))
		if err != nil {
			return fmt.Errorf("reading the snapshot of server %s: %w", *serverID, err)
		}
		if snapshot.Name != want {
			return fmt.Errorf("server %s holds a snapshot named %q, want %q", *serverID, snapshot.Name, want)
		}
		return nil
	}
}

// checkVmwareServerHasNoSnapshot asserts the server holds no snapshot — the API
// reports that as a 200 with an empty body, which the SDK turns into ErrNotFound.
func checkVmwareServerHasNoSnapshot(serverID *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		snapshot, err := acctest.GetTestClient().GetVmwareSnapshot(context.Background(), mustAtoiErr(*serverID))
		if sdk.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the snapshot of server %s: %w", *serverID, err)
		}
		return fmt.Errorf("server %s still holds a snapshot named %q after the destroy", *serverID, snapshot.Name)
	}
}

// ============================================================================
// Out-of-band changes
// ============================================================================

// deleteVmwareSnapshotOutOfBand removes the snapshot behind Terraform's back,
// with the same SDK the provider uses.
func deleteVmwareSnapshotOutOfBand(t *testing.T, serverID *string) {
	t.Helper()
	err := acctest.GetTestClient().DeleteVmwareSnapshotAndWait(context.Background(), mustAtoi(t, *serverID))
	if err != nil && !sdk.IsNotFound(err) {
		t.Fatalf("out-of-band snapshot delete failed: %v", err)
	}
}
