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
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

const (
	storagesDataSourceName = "data.vcp_server_backup_storages.test"
	serverResourceName     = "vcp_server.test"
)

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

// ============================================================================
// API-side assertions
// ============================================================================

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
