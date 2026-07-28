package gateway_test

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

// TestAccGatewayDataSource_basic verifies both gateway data sources: the
// singular one mirrors the resource and classifies NICs by IP — the attached
// isolated network lands in isolated_net_nics, the WAN NIC (with the gateway
// bandwidth) in public_net_nics; the plural one includes the created gateway.
func TestAccGatewayDataSource_basic(t *testing.T) {
	resourceName := "vcp_gateway.test"
	dataSourceName := "data.vcp_gateway.test"
	gwName := "test-acc-gwds-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")

	config := fmt.Sprintf(`
resource "vcp_network" "test" {
  name        = %q
  location_id = %q
}

resource "vcp_gateway" "test" {
  name           = %q
  location_id    = %q
  bandwidth_mbps = 100
}

resource "vcp_gateway_network_attachment" "test" {
  gateway_id = vcp_gateway.test.id
  network_id = vcp_network.test.id
}

data "vcp_gateway" "test" {
  id         = vcp_gateway.test.id
  depends_on = [vcp_gateway_network_attachment.test]
}

data "vcp_gateways" "all" {
  depends_on = [vcp_gateway_network_attachment.test]
}
`, gwName+"-net", locationID, gwName, locationID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckGatewaysDestroyed,
			acctest.CheckNetworksDestroyed,
		),
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
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("name"), knownvalue.StringExact(gwName)),
					// The attached network is classified as an isolated NIC.
					statecheck.ExpectKnownValue(dataSourceName, tfjsonpath.New("isolated_net_nics"), knownvalue.ListSizeExact(1)),
					statecheck.CompareValuePairs(
						dataSourceName, tfjsonpath.New("isolated_net_nics").AtSliceIndex(0).AtMapKey("network_id"),
						"vcp_network.test", tfjsonpath.New("id"),
						compare.ValuesSame(),
					),
					// The WAN NIC is public and carries the gateway bandwidth.
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("public_net_nics").AtSliceIndex(0).AtMapKey("ip_address"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("public_net_nics").AtSliceIndex(0).AtMapKey("bandwidth_mbps"),
						knownvalue.Int64Exact(100),
					),
					// Plural data source includes the created gateway.
					acctest.CheckListNotEmpty("data.vcp_gateways.all", "gateways"),
					checkGatewayInList{
						dataSourceAddress: "data.vcp_gateways.all",
						expectedName:      gwName,
					},
				},
			},
		},
	})
}

// checkGatewayInList verifies a gateway with the expected name is present in
// the plural data source result.
type checkGatewayInList struct {
	dataSourceAddress string
	expectedName      string
}

func (c checkGatewayInList) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	res := acctest.FindResource(req.State, c.dataSourceAddress)
	if res == nil {
		resp.Error = fmt.Errorf("resource not found: %s", c.dataSourceAddress)
		return
	}

	gatewaysRaw, ok := res.AttributeValues["gateways"].([]any)
	if !ok {
		resp.Error = fmt.Errorf("attribute 'gateways' not found or not a list")
		return
	}

	for _, gwRaw := range gatewaysRaw {
		gw, ok := gwRaw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := gw["name"].(string); name == c.expectedName {
			if id, _ := gw["id"].(string); id == "" {
				resp.Error = fmt.Errorf("gateway %s found but id is empty", c.expectedName)
			}
			return
		}
	}

	resp.Error = fmt.Errorf("gateway with name %s not found in list", c.expectedName)
}
