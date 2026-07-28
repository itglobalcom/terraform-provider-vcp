package metadata_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// ----------------------------------------------------------------------
// project
// ----------------------------------------------------------------------

// TestAccProjectDataSource_basic verifies the project data source exposes the
// account fields (id, balance, currency, state, created).
func TestAccProjectDataSource_basic(t *testing.T) {
	dataSourceName := "data.vcp_project.current"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "vcp_project" "current" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that all required fields are set
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("balance"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("currency"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("state"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("created"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// ----------------------------------------------------------------------
// locations
// ----------------------------------------------------------------------

// TestAccLocationsDataSource_basic verifies the locations list is non-empty and
// elements carry the fields consumers rely on (id, system_volume_min).
func TestAccLocationsDataSource_basic(t *testing.T) {
	dataSourceName := "data.vcp_locations.all"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "vcp_locations" "all" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the locations list is not empty
					acctest.CheckListNotEmpty(dataSourceName, "locations"),

					// Verify that the first element has the required fields
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("locations").AtSliceIndex(0).AtMapKey("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("locations").AtSliceIndex(0).AtMapKey("system_volume_min"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// ----------------------------------------------------------------------
// images
// ----------------------------------------------------------------------

// TestAccImagesDataSource_basic verifies the OS images list is non-empty and
// elements carry id and os_version.
func TestAccImagesDataSource_basic(t *testing.T) {
	dataSourceName := "data.vcp_images.all"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "vcp_images" "all" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the images list is not empty
					acctest.CheckListNotEmpty(dataSourceName, "images"),

					// Verify that the first element has the required fields
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("images").AtSliceIndex(0).AtMapKey("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("images").AtSliceIndex(0).AtMapKey("os_version"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// TestAccApplicationsDataSource_all verifies the unfiltered applications list is
// non-empty and elements carry id, location_id and images.
func TestAccApplicationsDataSource_all(t *testing.T) {
	dataSourceName := "data.vcp_applications.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccApplicationsDataSourceConfig_all(),
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the applications list is not empty (a plain
					// NotNull would pass vacuously for an empty list, and the
					// index-0 checks below would fail with an obscure path error)
					acctest.CheckListNotEmpty(dataSourceName, "applications"),

					// Verify that the first element has all required fields
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("applications").AtSliceIndex(0).AtMapKey("id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("applications").AtSliceIndex(0).AtMapKey("location_id"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("applications").AtSliceIndex(0).AtMapKey("images"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

// TestAccApplicationsDataSource_filteredByLocation verifies the location_id
// filter: the result is non-empty and every application belongs to the location.
func TestAccApplicationsDataSource_filteredByLocation(t *testing.T) {
	dataSourceName := "data.vcp_applications.test"
	locationID := os.Getenv("VCP_LOCATION_ID")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccApplicationsDataSourceConfig_filtered(locationID),
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the filter is applied
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("location_id"),
						knownvalue.StringExact(locationID),
					),

					// Verify that the list is not empty — otherwise the
					// per-application location check below passes vacuously
					acctest.CheckListNotEmpty(dataSourceName, "applications"),

					// Custom check: all applications must be from the specified location
					checkAllApplicationsHaveLocation{
						dataSourceAddress:  dataSourceName,
						expectedLocationID: locationID,
					},
				},
			},
		},
	})
}

// checkAllApplicationsHaveLocation - custom state check
type checkAllApplicationsHaveLocation struct {
	dataSourceAddress  string
	expectedLocationID string
}

func (c checkAllApplicationsHaveLocation) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	var resource *tfjson.StateResource
	for _, r := range req.State.Values.RootModule.Resources {
		if r.Address == c.dataSourceAddress {
			resource = r
			break
		}
	}

	if resource == nil {
		resp.Error = fmt.Errorf("Resource not found: %s", c.dataSourceAddress)
		return
	}

	applicationsRaw, ok := resource.AttributeValues["applications"]
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'applications' not found")
		return
	}

	applications, ok := applicationsRaw.([]interface{})
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'applications' is not a list")
		return
	}

	for i, appRaw := range applications {
		app, ok := appRaw.(map[string]interface{})
		if !ok {
			continue
		}

		locationID, _ := app["location_id"].(string)
		if locationID != c.expectedLocationID {
			resp.Error = fmt.Errorf(
				"Application at index %d has location_id %s, expected %s",
				i,
				locationID,
				c.expectedLocationID,
			)
			return
		}
	}
}

func testAccApplicationsDataSourceConfig_all() string {
	return `
data "vcp_applications" "test" {}
`
}

func testAccApplicationsDataSourceConfig_filtered(locationID string) string {
	return fmt.Sprintf(`
data "vcp_applications" "test" {
  location_id = %q
}
`, locationID)
}
