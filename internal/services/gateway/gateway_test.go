package gateway_test

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

// TestAccGateway_basic covers the gateway lifecycle: create without network
// arguments (the throwaway bootstrap network must be gone — zero isolated
// NICs), in-place update of name, bandwidth and tags, empty plan, and import.
func TestAccGateway_basic(t *testing.T) {
	resourceName := "vcp_gateway.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	gwName := "test-acc-gateway-" + acctest.RandomString(6)
	gwNameUpd := gwName + "-upd"
	tagsUpd := `["zebra", "alpha"]` // unordered on purpose — tags is a Set

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckGatewaysDestroyed,
		Steps: []resource.TestStep{
			// 1. Create: gateway-wide bandwidth, no network arguments. The provider
			// creates and immediately removes a throwaway bootstrap network, so the
			// gateway must come up with zero isolated NICs. No tags → empty set.
			{
				Config: testAccGatewayConfig(gwName, locationID, 100, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					checkGatewayExists{resourceAddress: resourceName},
					checkGatewayNoIsolatedNICs{resourceAddress: resourceName},
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("name"), knownvalue.StringExact(gwName)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"), knownvalue.Int64Exact(100)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("tags"), knownvalue.SetSizeExact(0)),
				},
			},
			// 2. Update name, bandwidth and tags (all in-place).
			{
				Config: testAccGatewayConfig(gwNameUpd, locationID, 200, tagsUpd),
				ConfigStateChecks: []statecheck.StateCheck{
					checkGatewayExists{resourceAddress: resourceName},
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("name"), knownvalue.StringExact(gwNameUpd)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"), knownvalue.Int64Exact(200)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("tags"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("alpha"),
						knownvalue.StringExact("zebra"),
					})),
				},
			},
			// 3. Idempotency — the plan should be empty.
			{
				Config:   testAccGatewayConfig(gwNameUpd, locationID, 200, tagsUpd),
				PlanOnly: true,
			},
			// 4. Import by ID (bandwidth is re-read from the WAN NIC, tags from the API).
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// testAccGatewayConfig declares a gateway with no network arguments. Isolated
// networks are attached via the vcp_gateway_network_attachment resource (see
// its tests). tags is an optional HCL list literal (e.g. `["a", "b"]`).
func testAccGatewayConfig(gwName, locationID string, bandwidth int, tags string) string {
	tagsHCL := ""
	if tags != "" {
		tagsHCL = "\n  tags           = " + tags
	}
	return fmt.Sprintf(`
resource "vcp_gateway" "test" {
  name           = %q
  location_id    = %q
  bandwidth_mbps = %d%s
}
`, gwName, locationID, bandwidth, tagsHCL)
}

type checkGatewayExists struct {
	resourceAddress string
}

func (c checkGatewayExists) CheckState(ctx context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	var res *tfjson.StateResource
	for _, r := range req.State.Values.RootModule.Resources {
		if r.Address == c.resourceAddress {
			res = r
			break
		}
	}
	if res == nil {
		resp.Error = fmt.Errorf("resource not found: %s", c.resourceAddress)
		return
	}
	id, _ := res.AttributeValues["id"].(string)
	if id == "" {
		resp.Error = fmt.Errorf("no gateway ID is set")
		return
	}
	gw, err := acctest.GetTestClient().GetGateway(ctx, id)
	if err != nil {
		resp.Error = fmt.Errorf("error fetching gateway: %w", err)
		return
	}
	if gw.ID != id {
		resp.Error = fmt.Errorf("gateway not found in API")
	}
}

// checkGatewayNoIsolatedNICs asserts the gateway has no isolated (private-IP)
// NICs — i.e. the throwaway bootstrap network was detached and deleted during
// Create. This is the live verification that the backend allows removing the
// last network at create time.
type checkGatewayNoIsolatedNICs struct {
	resourceAddress string
}

func (c checkGatewayNoIsolatedNICs) CheckState(ctx context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	var res *tfjson.StateResource
	for _, r := range req.State.Values.RootModule.Resources {
		if r.Address == c.resourceAddress {
			res = r
			break
		}
	}
	if res == nil {
		resp.Error = fmt.Errorf("resource not found: %s", c.resourceAddress)
		return
	}
	id, _ := res.AttributeValues["id"].(string)
	gw, err := acctest.GetTestClient().GetGateway(ctx, id)
	if err != nil {
		resp.Error = fmt.Errorf("error fetching gateway: %w", err)
		return
	}
	for _, nic := range gw.NICs {
		if isPrivateIPv4(nic.IPAddress) {
			resp.Error = fmt.Errorf("gateway %s still has an isolated NIC (network %s, ip %s); "+
				"the bootstrap network was not detached", id, nic.NetworkID, nic.IPAddress)
			return
		}
	}
}

// isPrivateIPv4 reports whether ip is an RFC1918 address (mirrors the data
// source's NIC classification; kept local to the external test package).
func isPrivateIPv4(ip string) bool {
	if ip == "" {
		return false
	}
	var a, b int
	if _, err := fmt.Sscanf(ip, "%d.%d.", &a, &b); err != nil {
		return false
	}
	switch {
	case a == 10:
		return true
	case a == 172 && b >= 16 && b <= 31:
		return true
	case a == 192 && b == 168:
		return true
	}
	return false
}
