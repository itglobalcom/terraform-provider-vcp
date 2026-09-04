// Package server_snapshot_test holds the acceptance tests of
// vcp_server_snapshot and vcp_server_snapshots. They drive the provider against
// a live API and are skipped unless TF_ACC is set.
//
// Environment: VCP_API_URL, VCP_API_TOKEN, VCP_LOCATION_ID, VCP_IMAGE_ID.
// Every object is named "test-acc-…" so `make sweep` can clean up after a run
// that failed half-way; a snapshot needs no sweeper of its own — it is deleted
// with the server that owns it.
package server_snapshot_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
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

const (
	snapshotResourceName = "vcp_server_snapshot.test"
	serverResourceName   = "vcp_server.test"
)

// TestAccServerSnapshot_lifecycle walks the whole life of a snapshot: taken,
// stable, replaced when renamed, imported, and gone once dropped from the
// configuration — each state checked against the API and not only against what
// the provider recorded.
func TestAccServerSnapshot_lifecycle(t *testing.T) {
	name := "test-acc-snap-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckServersDestroyed,
		Steps: []resource.TestStep{
			// 1. Take the snapshot.
			{
				Config: testAccSnapshotConfig(name, "before-upgrade"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(snapshotResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(snapshotResourceName, tfjsonpath.New("name"),
						knownvalue.StringExact("before-upgrade")),
					statecheck.ExpectKnownValue(snapshotResourceName, tfjsonpath.New("created"), knownvalue.NotNull()),
				},
				Check: checkSnapshotName("before-upgrade"),
			},
			// 2. The commonest defect: a perpetual diff straight after create.
			{
				Config: testAccSnapshotConfig(name, "before-upgrade"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// 3. Import by "server_id:snapshot_id".
			{
				ResourceName:      snapshotResourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(snapshotResourceName, "server_id", "id"),
			},
			// 4. A rename has to replace the snapshot: the API cannot rename one,
			// so anything else would report a change that never happened.
			{
				Config: testAccSnapshotConfig(name, "after-upgrade"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(snapshotResourceName, plancheck.ResourceActionReplace),
					},
				},
				Check: checkSnapshotName("after-upgrade"),
			},
			// 5. Dropped from the configuration while the server stays: this is
			// what proves the delete reached the platform. CheckDestroy alone
			// would pass on a delete that never ran, because the server takes its
			// snapshots with it.
			{
				Config: testAccServerOnlyConfig(name),
				Check:  checkServerHasNoSnapshots(),
			},
		},
	})
}

// TestAccServerSnapshot_disappears deletes the snapshot out of band and checks
// the refresh survives it: Read must remove the resource from state and the next
// plan must offer to take another snapshot, rather than failing on a 404.
func TestAccServerSnapshot_disappears(t *testing.T) {
	name := "test-acc-snap-dis-" + acctest.RandomString(6)
	config := testAccSnapshotConfig(name, "doomed")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureSnapshotIDs(),
				},
			},
			{
				PreConfig:          func() { deleteSnapshotOutOfBand(t) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccServerSnapshot_parallel takes two snapshots of one server in a single
// apply. Two resources touching one parent is the only thing that exercises
// locks.Server — without it the second call meets a busy server.
func TestAccServerSnapshot_parallel(t *testing.T) {
	name := "test-acc-snap-par-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccSnapshotPairConfig(name),
				Check:  checkServerSnapshotCount(2),
			},
			{
				Config: testAccSnapshotPairConfig(name),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// TestAccServerSnapshots_dataSource reads the snapshots back through the data
// source, including one taken outside Terraform: the list is the platform's, not
// the provider's.
func TestAccServerSnapshots_dataSource(t *testing.T) {
	name := "test-acc-snap-ds-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccSnapshotDataSourceConfig(name, "listed"),
				ConfigStateChecks: []statecheck.StateCheck{
					acctest.CheckListNotEmpty("data.vcp_server_snapshots.test", "snapshots"),
					statecheck.ExpectKnownValue("data.vcp_server_snapshots.test",
						tfjsonpath.New("snapshots").AtSliceIndex(0).AtMapKey("name"),
						knownvalue.StringExact("listed")),
					statecheck.ExpectKnownValue("data.vcp_server_snapshots.test",
						tfjsonpath.New("snapshots").AtSliceIndex(0).AtMapKey("size_mb"),
						knownvalue.NotNull()),
				},
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

// testAccServerOnlyConfig declares the server the snapshots are taken of. The
// disk is the smallest a vStack server takes; a snapshot's cost follows what the
// server writes, not the disk's declared size.
func testAccServerOnlyConfig(name string) string {
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
`, name, os.Getenv("VCP_LOCATION_ID"), os.Getenv("VCP_IMAGE_ID"))
}

func testAccSnapshotConfig(name, snapshotName string) string {
	return testAccServerOnlyConfig(name) + fmt.Sprintf(`
resource "vcp_server_snapshot" "test" {
  server_id = vcp_server.test.id
  name      = %q
}
`, snapshotName)
}

func testAccSnapshotPairConfig(name string) string {
	return testAccServerOnlyConfig(name) + `
resource "vcp_server_snapshot" "test" {
  server_id = vcp_server.test.id
  name      = "first"
}

resource "vcp_server_snapshot" "second" {
  server_id = vcp_server.test.id
  name      = "second"
}
`
}

func testAccSnapshotDataSourceConfig(name, snapshotName string) string {
	return testAccSnapshotConfig(name, snapshotName) + `
data "vcp_server_snapshots" "test" {
  server_id  = vcp_server.test.id
  depends_on = [vcp_server_snapshot.test]
}
`
}

// ============================================================================
// API-side assertions
// ============================================================================
//
// State says what the provider recorded; these say what the platform holds. A
// test that only compared state with itself would pass a resource that wrote
// nothing at all.

// snapshotFromState reads the server and snapshot ids out of the Terraform state.
func snapshotFromState(s *terraform.State) (serverID string, snapshotID int, err error) {
	rs, ok := s.RootModule().Resources[snapshotResourceName]
	if !ok {
		return "", 0, fmt.Errorf("resource not found: %s", snapshotResourceName)
	}
	serverID = rs.Primary.Attributes["server_id"]
	if serverID == "" {
		return "", 0, fmt.Errorf("%s has no server_id in state", snapshotResourceName)
	}
	snapshotID, err = strconv.Atoi(rs.Primary.Attributes["id"])
	if err != nil {
		return "", 0, fmt.Errorf("%s has a non-numeric id %q: %w",
			snapshotResourceName, rs.Primary.Attributes["id"], err)
	}
	return serverID, snapshotID, nil
}

// checkSnapshotName asserts, straight from the API, that the snapshot in state
// exists and carries the expected name. After a replacement this is what tells a
// new snapshot from the old one still being reported.
func checkSnapshotName(want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		serverID, snapshotID, err := snapshotFromState(s)
		if err != nil {
			return err
		}
		snapshot, err := acctest.GetTestClient().GetServerSnapshot(context.Background(), serverID, snapshotID)
		if err != nil {
			return fmt.Errorf("reading snapshot %d of server %s: %w", snapshotID, serverID, err)
		}
		if snapshot.Name != want {
			return fmt.Errorf("snapshot %d of server %s is named %q, want %q",
				snapshotID, serverID, snapshot.Name, want)
		}
		return nil
	}
}

// checkServerSnapshotCount asserts how many snapshots the server holds.
func checkServerSnapshotCount(want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[serverResourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", serverResourceName)
		}
		snapshots, err := acctest.GetTestClient().GetServerSnapshots(context.Background(), rs.Primary.ID)
		if err != nil {
			return fmt.Errorf("listing the snapshots of server %s: %w", rs.Primary.ID, err)
		}
		if len(snapshots) != want {
			return fmt.Errorf("server %s holds %d snapshot(s), want %d", rs.Primary.ID, len(snapshots), want)
		}
		return nil
	}
}

// checkServerHasNoSnapshots is checkServerSnapshotCount(0) by another name: it
// is what proves a destroy reached the platform while the server survived.
func checkServerHasNoSnapshots() resource.TestCheckFunc { return checkServerSnapshotCount(0) }

// ============================================================================
// Out-of-band changes
// ============================================================================

// capturedServerID and capturedSnapshotID carry the ids into PreConfig, which
// runs before the state of its own step is available.
var (
	capturedServerID   string
	capturedSnapshotID int
)

func captureSnapshotIDs() statecheck.StateCheck { return captureIDsCheck{} }

type captureIDsCheck struct{}

func (captureIDsCheck) CheckState(_ context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	res := acctest.FindResource(req.State, snapshotResourceName)
	if res == nil {
		resp.Error = fmt.Errorf("resource not found: %s", snapshotResourceName)
		return
	}
	serverID, ok := res.AttributeValues["server_id"].(string)
	if !ok || serverID == "" {
		resp.Error = fmt.Errorf("%s has no server_id in state", snapshotResourceName)
		return
	}
	// Numbers arrive from the JSON state as float64.
	id, ok := res.AttributeValues["id"].(float64)
	if !ok {
		resp.Error = fmt.Errorf("%s has no numeric id in state", snapshotResourceName)
		return
	}
	capturedServerID, capturedSnapshotID = serverID, int(id)
}

// deleteSnapshotOutOfBand removes the snapshot behind Terraform's back, with the
// same SDK the provider uses — as close to "somebody deleted it in the panel" as
// a test can get.
func deleteSnapshotOutOfBand(t *testing.T) {
	t.Helper()
	if capturedServerID == "" || capturedSnapshotID == 0 {
		t.Fatal("the snapshot ids were not captured in the previous step")
	}
	err := acctest.GetTestClient().DeleteServerSnapshotAndWait(context.Background(),
		capturedServerID, capturedSnapshotID)
	if err != nil && !sdk.IsNotFound(err) {
		t.Fatalf("out-of-band snapshot delete failed: %v", err)
	}
}
