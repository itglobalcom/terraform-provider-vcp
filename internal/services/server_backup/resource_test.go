// Package server_backup_test holds the acceptance tests of vcp_server_backup
// and vcp_server_backup_storages. They drive the provider against a live API
// and are skipped unless TF_ACC is set.
//
// Environment: VCP_API_URL, VCP_API_TOKEN, VCP_LOCATION_ID, VCP_IMAGE_ID. The
// location has to offer at least one backup storage. Every object is named
// "test-acc-…" so `make sweep` can clean up after a run that failed half-way;
// the backup service needs no sweeper of its own — it goes with the server.
package server_backup_test

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
)

const (
	backupResourceName     = "vcp_server_backup.test"
	storagesDataSourceName = "data.vcp_server_backup_storages.test"
	serverResourceName     = "vcp_server.test"
)

// TestAccServerBackup_lifecycle walks the whole life of a schedule: enabled,
// stable, edited in place, imported, and disabled once dropped from the
// configuration — each state checked against the API and not only against what
// the provider recorded.
func TestAccServerBackup_lifecycle(t *testing.T) {
	name := "test-acc-backup-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckServersDestroyed,
		Steps: []resource.TestStep{
			// 1. Enable the service with a daily rule.
			{
				Config: testAccBackupDailyConfig(name),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(backupResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(backupResourceName, tfjsonpath.New("hour"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(backupResourceName, tfjsonpath.New("daily").AtMapKey("keep"),
						knownvalue.Int64Exact(3)),
					statecheck.ExpectKnownValue(backupResourceName, tfjsonpath.New("weekly"), knownvalue.Null()),
				},
				Check: resource.ComposeAggregateTestCheckFunc(checkBackupSchedule(2, 3, false), captureServerID()),
			},
			// 2. The commonest defect: a perpetual diff straight after create.
			{
				Config: testAccBackupDailyConfig(name),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// 3. An edit of the schedule is applied in place: a replacement would
			// disable the service and delete the server's copies.
			{
				Config: testAccBackupDailyWeeklyConfig(name),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(backupResourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(backupResourceName, tfjsonpath.New("weekly").AtMapKey("weekday"),
						knownvalue.StringExact("sunday")),
				},
				Check: checkBackupSchedule(3, 5, true),
			},
			// 4. Import by the server id.
			{
				ResourceName:      backupResourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(backupResourceName, "server_id"),
			},
			// 5. The schedule edited out of band shows up as drift.
			{
				PreConfig:          func() { moveBackupHourOutOfBand(t, 4) },
				Config:             testAccBackupDailyWeeklyConfig(name),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// 6. Dropped from the configuration while the server stays: this is
			// what proves the disable reached the platform. CheckDestroy alone
			// would pass on a disable that never ran, because the server takes
			// its backup service with it.
			{
				Config: testAccServerConfig(name),
				Check:  checkBackupDisabled(),
			},
		},
	})
}

// TestAccServerBackup_disappears disables the service out of band and checks
// the refresh survives it: Read must remove the resource from state and the
// next plan must offer to enable the service again, rather than failing.
func TestAccServerBackup_disappears(t *testing.T) {
	name := "test-acc-backup-dis-" + acctest.RandomString(6)
	config := testAccBackupDailyConfig(name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureServerID(),
			},
			{
				PreConfig:          func() { disableBackupOutOfBand(t) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccServerBackupStorages_dataSource reads the storages of a fresh server
// and compares them with what the API lists for it.
func TestAccServerBackupStorages_dataSource(t *testing.T) {
	name := "test-acc-backup-ds-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccStoragesConfig(name),
				ConfigStateChecks: []statecheck.StateCheck{
					acctest.CheckListNotEmpty(storagesDataSourceName, "storages"),
					statecheck.ExpectKnownValue(storagesDataSourceName,
						tfjsonpath.New("storages").AtSliceIndex(0).AtMapKey("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(storagesDataSourceName,
						tfjsonpath.New("storages").AtSliceIndex(0).AtMapKey("location_id"), knownvalue.NotNull()),
				},
				Check: checkStoragesMatchAPI(),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

// testAccServerConfig declares the server the backup service is attached to,
// with the smallest disk a vStack server takes.
func testAccServerConfig(name string) string {
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

func testAccStoragesConfig(name string) string {
	return testAccServerConfig(name) + `
data "vcp_server_backup_storages" "test" {
  server_id = vcp_server.test.id
}
`
}

// testAccBackupDailyConfig enables the service with one daily rule in the
// first storage the server is offered.
func testAccBackupDailyConfig(name string) string {
	return testAccStoragesConfig(name) + `
resource "vcp_server_backup" "test" {
  server_id = vcp_server.test.id
  hour      = 2
  minute    = 0

  daily = {
    keep              = 3
    backup_storage_id = data.vcp_server_backup_storages.test.storages[0].id
  }
}
`
}

func testAccBackupDailyWeeklyConfig(name string) string {
	return testAccStoragesConfig(name) + `
resource "vcp_server_backup" "test" {
  server_id = vcp_server.test.id
  hour      = 3
  minute    = 15

  daily = {
    keep              = 5
    backup_storage_id = data.vcp_server_backup_storages.test.storages[0].id
  }

  weekly = {
    keep              = 2
    backup_storage_id = data.vcp_server_backup_storages.test.storages[0].id
    weekday           = "sunday"
  }
}
`
}

// ============================================================================
// API-side assertions
// ============================================================================

// serverIDFromState reads the id of the test server out of the Terraform state.
func serverIDFromState(s *terraform.State) (string, error) {
	rs, ok := s.RootModule().Resources[serverResourceName]
	if !ok {
		return "", fmt.Errorf("resource not found: %s", serverResourceName)
	}
	if rs.Primary.ID == "" {
		return "", fmt.Errorf("%s has no id in state", serverResourceName)
	}
	return rs.Primary.ID, nil
}

// checkBackupSchedule asserts, straight from the API, that the service is
// enabled with the given hour and daily keep, and with or without a weekly
// rule.
func checkBackupSchedule(hour, dailyKeep int, weekly bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		serverID, err := serverIDFromState(s)
		if err != nil {
			return err
		}
		backup, err := acctest.GetTestClient().GetServerBackup(context.Background(), serverID)
		if err != nil {
			return fmt.Errorf("reading the backup service of server %s: %w", serverID, err)
		}
		if !backup.Enabled || backup.Schedule == nil {
			return fmt.Errorf("server %s: backup enabled=%v with schedule %v, want an enabled schedule",
				serverID, backup.Enabled, backup.Schedule)
		}
		sch := backup.Schedule
		if sch.Hour != hour {
			return fmt.Errorf("server %s: backup hour %d, want %d", serverID, sch.Hour, hour)
		}
		if sch.Daily == nil || sch.Daily.Keep != dailyKeep {
			return fmt.Errorf("server %s: daily rule %+v, want keep %d", serverID, sch.Daily, dailyKeep)
		}
		if (sch.Weekly != nil) != weekly {
			return fmt.Errorf("server %s: weekly rule %+v, want present=%v", serverID, sch.Weekly, weekly)
		}
		return nil
	}
}

// checkBackupDisabled asserts, straight from the API, that the service of the
// server is off.
func checkBackupDisabled() resource.TestCheckFunc {
	return func(s *terraform.State) error {
		serverID, err := serverIDFromState(s)
		if err != nil {
			return err
		}
		backup, err := acctest.GetTestClient().GetServerBackup(context.Background(), serverID)
		if err != nil {
			return fmt.Errorf("reading the backup service of server %s: %w", serverID, err)
		}
		if backup.Enabled {
			return fmt.Errorf("server %s still has the backup service enabled", serverID)
		}
		return nil
	}
}

// checkStoragesMatchAPI asserts that the data source lists the same storages,
// in the same order, as the API does for the server.
func checkStoragesMatchAPI() resource.TestCheckFunc {
	return func(s *terraform.State) error {
		srv, ok := s.RootModule().Resources[serverResourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", serverResourceName)
		}
		ds, ok := s.RootModule().Resources[storagesDataSourceName]
		if !ok {
			return fmt.Errorf("data source not found: %s", storagesDataSourceName)
		}
		catalog, err := acctest.GetTestClient().GetServerBackupStorages(context.Background(), srv.Primary.ID)
		if err != nil {
			return fmt.Errorf("listing the backup storages of server %s: %w", srv.Primary.ID, err)
		}
		if got := ds.Primary.Attributes["storages.#"]; got != strconv.Itoa(len(catalog.Storages)) {
			return fmt.Errorf("data source lists %s storage(s), the API %d", got, len(catalog.Storages))
		}
		for i, storage := range catalog.Storages {
			key := fmt.Sprintf("storages.%d.id", i)
			if got := ds.Primary.Attributes[key]; got != strconv.Itoa(storage.ID) {
				return fmt.Errorf("%s = %s, the API lists storage %d there", key, got, storage.ID)
			}
		}
		return nil
	}
}

// ============================================================================
// Out-of-band changes
// ============================================================================

// capturedServerID carries the server id into PreConfig, which runs before the
// state of its own step is available.
var capturedServerID string

func captureServerID() resource.TestCheckFunc {
	return func(s *terraform.State) error {
		serverID, err := serverIDFromState(s)
		if err != nil {
			return err
		}
		capturedServerID = serverID
		return nil
	}
}

// disableBackupOutOfBand disables the service behind Terraform's back, with
// the same SDK the provider uses — as close to "somebody disabled it in the
// panel" as a test can get.
func disableBackupOutOfBand(t *testing.T) {
	t.Helper()
	if capturedServerID == "" {
		t.Fatal("the server id was not captured in the previous step")
	}
	if err := acctest.GetTestClient().DisableServerBackupAndWait(context.Background(), capturedServerID); err != nil {
		t.Fatalf("out-of-band backup disable failed: %v", err)
	}
}

// moveBackupHourOutOfBand changes the hour of the schedule behind Terraform's
// back and keeps the rest of it.
func moveBackupHourOutOfBand(t *testing.T, hour int) {
	t.Helper()
	if capturedServerID == "" {
		t.Fatal("the server id was not captured in an earlier step")
	}
	client := acctest.GetTestClient()
	backup, err := client.GetServerBackup(context.Background(), capturedServerID)
	if err != nil || backup.Schedule == nil {
		t.Fatalf("reading the backup schedule of server %s: %v", capturedServerID, err)
	}
	schedule := *backup.Schedule
	schedule.Hour = hour
	if _, err := client.UpdateServerBackupAndWait(context.Background(), capturedServerID, &schedule); err != nil {
		t.Fatalf("out-of-band backup update failed: %v", err)
	}
}
