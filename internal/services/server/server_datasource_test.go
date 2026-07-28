package vstack_server_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// TestAccServerDataSource_basic verifies both server data sources: the
// singular one mirrors the resource (id/name/cpu/ram_mb) and reports the boot
// volume, and the plural one includes the created server.
func TestAccServerDataSource_basic(t *testing.T) {
	resourceName := "vcp_server.test"
	dataSourceName := "data.vcp_server.test"
	serverName := "test-acc-srv-ds-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")

	config := testAccServerConfig_basic(serverName, locationID, imageID) + `
data "vcp_server" "test" {
  id = vcp_server.test.id
}

data "vcp_servers" "all" {
  depends_on = [vcp_server.test]
}
`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					// Singular data source mirrors the resource.
					statecheck.CompareValuePairs(
						dataSourceName, tfjsonpath.New("id"),
						resourceName, tfjsonpath.New("id"),
						compare.ValuesSame(),
					),
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("name"), knownvalue.StringExact(serverName)),
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("location_id"), knownvalue.StringExact(locationID)),
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("cpu"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("ram_mb"), knownvalue.Int64Exact(2048)),
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("state"), knownvalue.NotNull()),
					// The boot volume is reported with its size.
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("volumes"), knownvalue.ListSizeExact(1)),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("volumes").AtSliceIndex(0).AtMapKey("size_mb"),
						knownvalue.Int64Exact(30720),
					),
					// Plural data source includes the created server.
					acctest.CheckListNotEmpty("data.vcp_servers.all", "servers"),
					checkServerInList{
						dataSourceAddress: "data.vcp_servers.all",
						expectedName:      serverName,
					},
				},
			},
		},
	})
}

// checkServerInList verifies a server with the expected name is present in the
// plural data source result.
type checkServerInList struct {
	dataSourceAddress string
	expectedName      string
}

func (c checkServerInList) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	res := findResource(req.State, c.dataSourceAddress)
	if res == nil {
		resp.Error = fmt.Errorf("resource not found: %s", c.dataSourceAddress)
		return
	}

	serversRaw, ok := res.AttributeValues["servers"].([]any)
	if !ok {
		resp.Error = fmt.Errorf("attribute 'servers' not found or not a list")
		return
	}

	for _, srvRaw := range serversRaw {
		srv, ok := srvRaw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := srv["name"].(string); name == c.expectedName {
			if id, _ := srv["id"].(string); id == "" {
				resp.Error = fmt.Errorf("server %s found but id is empty", c.expectedName)
			}
			return
		}
	}

	resp.Error = fmt.Errorf("server with name %s not found in list", c.expectedName)
}
