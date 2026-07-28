package gateway_network_attachment_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// TestAccGatewayNetworkAttachment_basic covers the attachment lifecycle:
// attach a network to a gateway (id and ip_address get set), an empty plan
// afterwards, and import via the composite "gateway_id:network_id" ID.
func TestAccGatewayNetworkAttachment_basic(t *testing.T) {
	resourceName := "vcp_gateway_network_attachment.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-gwattach-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckGatewaysDestroyed,
			acctest.CheckNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			{
				Config: testAccGatewayAttachmentConfig(name, locationID),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ip_address"), knownvalue.NotNull()),
				},
			},
			{
				Config:   testAccGatewayAttachmentConfig(name, locationID),
				PlanOnly: true,
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "gateway_id", "network_id"),
			},
		},
	})
}

func testAccGatewayAttachmentConfig(name, locationID string) string {
	return fmt.Sprintf(`
resource "vcp_network" "extra" {
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
  network_id = vcp_network.extra.id
}
`, name+"-extra", locationID, name, locationID)
}

// TestAccGatewayNetworkAttachment_twoNetworks pins NIC selection when a gateway
// has MORE than one isolated network attached — the realistic topology, and the
// only way to catch a wrong-NIC bug: with a single NIC, code that returns "the
// first isolated NIC" instead of "the NIC of this network" passes anyway. Each
// attachment must get an IP from its OWN network's subnet, and detaching one
// must leave the other one untouched.
func TestAccGatewayNetworkAttachment_twoNetworks(t *testing.T) {
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-gwattach-two-" + acctest.RandomString(6)
	addrA := "vcp_gateway_network_attachment.a"
	addrB := "vcp_gateway_network_attachment.b"

	// The NIC id of attachment A must survive B's removal.
	nicAUnchanged := statecheck.CompareValue(compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckGatewaysDestroyed,
			acctest.CheckNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			// 1. Both networks attached: each attachment's IP must come from its
			// own subnet — swapped NICs would show up here.
			{
				Config: testAccGatewayTwoNetworksConfig(name, locationID, true),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addrA, tfjsonpath.New("ip_address"),
						knownvalue.StringRegexp(regexp.MustCompile(`^10\.31\.0\.`))),
					statecheck.ExpectKnownValue(addrB, tfjsonpath.New("ip_address"),
						knownvalue.StringRegexp(regexp.MustCompile(`^10\.32\.0\.`))),
					// Distinct NICs.
					statecheck.CompareValuePairs(addrA, tfjsonpath.New("id"), addrB, tfjsonpath.New("id"), compare.ValuesDiffer()),
					nicAUnchanged.AddStateValue(addrA, tfjsonpath.New("id")),
				},
			},
			// 2. Idempotency with two NICs present.
			{
				Config:   testAccGatewayTwoNetworksConfig(name, locationID, true),
				PlanOnly: true,
			},
			// 3. Detach only B. A must keep the very same NIC and IP, and the
			// gateway must end up with exactly A's network attached — this is
			// what catches Delete disconnecting the wrong NIC.
			{
				Config: testAccGatewayTwoNetworksConfig(name, locationID, false),
				ConfigStateChecks: []statecheck.StateCheck{
					nicAUnchanged.AddStateValue(addrA, tfjsonpath.New("id")),
					statecheck.ExpectKnownValue(addrA, tfjsonpath.New("ip_address"),
						knownvalue.StringRegexp(regexp.MustCompile(`^10\.31\.0\.`))),
					&checkGatewayIsolatedNetworks{
						gatewayAddress:    "vcp_gateway.test",
						networkAddresses:  []string{"vcp_network.a"},
						forbiddenNetworks: []string{"vcp_network.b"},
					},
				},
			},
			// 4. Convergence.
			{
				Config:   testAccGatewayTwoNetworksConfig(name, locationID, false),
				PlanOnly: true,
			},
		},
	})
}

func testAccGatewayTwoNetworksConfig(name, locationID string, withB bool) string {
	cfg := fmt.Sprintf(`
resource "vcp_network" "a" {
  name           = %q
  location_id    = %q
  network_prefix = "10.31.0.0"
  mask           = 24
}

resource "vcp_network" "b" {
  name           = %q
  location_id    = %q
  network_prefix = "10.32.0.0"
  mask           = 24
}

resource "vcp_gateway" "test" {
  name           = %q
  location_id    = %q
  bandwidth_mbps = 100
}

resource "vcp_gateway_network_attachment" "a" {
  gateway_id = vcp_gateway.test.id
  network_id = vcp_network.a.id
}
`, name+"-a", locationID, name+"-b", locationID, name, locationID)

	if withB {
		cfg += `
resource "vcp_gateway_network_attachment" "b" {
  gateway_id = vcp_gateway.test.id
  network_id = vcp_network.b.id
}
`
	}
	return cfg
}

// checkGatewayIsolatedNetworks verifies via the API which isolated networks the
// gateway is actually connected to: every network in networkAddresses must have
// a NIC, and none of forbiddenNetworks may have one.
type checkGatewayIsolatedNetworks struct {
	gatewayAddress    string
	networkAddresses  []string
	forbiddenNetworks []string
}

func (c *checkGatewayIsolatedNetworks) CheckState(ctx context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	id := func(address string) (string, error) {
		res := acctest.FindResource(req.State, address)
		if res == nil {
			return "", fmt.Errorf("resource not found in state: %s", address)
		}
		v, _ := res.AttributeValues["id"].(string)
		if v == "" {
			return "", fmt.Errorf("no id on %s", address)
		}
		return v, nil
	}

	gatewayID, err := id(c.gatewayAddress)
	if err != nil {
		resp.Error = err
		return
	}
	gw, err := acctest.GetTestClient().GetGateway(ctx, gatewayID)
	if err != nil {
		resp.Error = fmt.Errorf("reading gateway %s: %w", gatewayID, err)
		return
	}
	attached := make(map[string]bool, len(gw.NICs))
	for _, nic := range gw.NICs {
		attached[nic.NetworkID] = true
	}

	for _, address := range c.networkAddresses {
		netID, err := id(address)
		if err != nil {
			resp.Error = err
			return
		}
		if !attached[netID] {
			resp.Error = fmt.Errorf("gateway %s is NOT connected to network %s (%s); connected: %v",
				gatewayID, netID, address, attached)
			return
		}
	}
	for _, address := range c.forbiddenNetworks {
		// The network may already be gone from state; skip if so.
		res := acctest.FindResource(req.State, address)
		if res == nil {
			continue
		}
		netID, _ := res.AttributeValues["id"].(string)
		if netID != "" && attached[netID] {
			resp.Error = fmt.Errorf("gateway %s is still connected to network %s (%s), which should have been detached",
				gatewayID, netID, address)
			return
		}
	}
}

// TestAccGatewayNetworkAttachment_disappears disconnects the network from the
// gateway out-of-band and checks the provider detects the attachment is gone
// (Read → RemoveResource) and plans to recreate it.
func TestAccGatewayNetworkAttachment_disappears(t *testing.T) {
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-gwattach-dis-" + acctest.RandomString(6)
	config := testAccGatewayAttachmentConfig(name, locationID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckGatewaysDestroyed,
			acctest.CheckNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { disconnectGatewayNICOutOfBand(t, name, name+"-extra") },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// disconnectGatewayNICOutOfBand finds the gateway and its NIC on the named
// network and disconnects it directly via the API, waiting for completion.
func disconnectGatewayNICOutOfBand(t *testing.T, gatewayName, networkName string) {
	client := acctest.GetTestClient()
	ctx := context.Background()

	gateways, err := client.GetGatewayList(ctx)
	if err != nil {
		t.Fatalf("out-of-band gateway list failed: %v", err)
	}
	var gatewayID string
	for _, gw := range gateways {
		if gw.Name == gatewayName {
			gatewayID = gw.ID
			break
		}
	}
	if gatewayID == "" {
		t.Fatalf("gateway %s not found for out-of-band disconnect", gatewayName)
	}

	networks, err := client.GetNetworkList(ctx)
	if err != nil {
		t.Fatalf("out-of-band network list failed: %v", err)
	}
	var networkID string
	for _, network := range networks {
		if network.Name == networkName {
			networkID = network.ID
			break
		}
	}
	if networkID == "" {
		t.Fatalf("network %s not found for out-of-band disconnect", networkName)
	}

	gw, err := client.GetGateway(ctx, gatewayID)
	if err != nil {
		t.Fatalf("out-of-band gateway read failed: %v", err)
	}
	for _, nic := range gw.NICs {
		if nic.NetworkID == networkID {
			if err := client.DisconnectNetworkAndWait(ctx, gatewayID, nic.ID); err != nil {
				t.Fatalf("out-of-band disconnect failed: %v", err)
			}
			return
		}
	}
	t.Fatalf("NIC on network %s not found on gateway %s", networkID, gatewayID)
}

// TestAccNetworkDeleteWithAttachments_oneApply — the key test: a network that has
// both a server and a gateway connected to it via attachment resources is deleted together with
// its connections in a SINGLE apply without a -19511 error. Attachments depend on the network, so
// Terraform is guaranteed to tear them down before the network.
func TestAccNetworkDeleteWithAttachments_oneApply(t *testing.T) {
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")
	name := "test-acc-shared-" + acctest.RandomString(6)

	var sharedNetworkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckGatewaysDestroyed,
			acctest.CheckServersDestroyed,
		),
		Steps: []resource.TestStep{
			// 1. The shared network is connected to both the server and the gateway.
			{
				Config: testAccSharedNetworkConfig(name, locationID, imageID, true),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("vcp_server_network_attachment.shared", tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("vcp_gateway_network_attachment.shared", tfjsonpath.New("id"), knownvalue.NotNull()),
					// Remember the network ID: after step 2 removes it from the
					// config (and thus from state), only the saved ID can prove
					// the network is really gone from the API.
					&saveResourceID{resourceAddress: "vcp_network.shared", dst: &sharedNetworkID},
				},
			},
			// 2. Delete the shared network and both attachments in a single apply — this should succeed,
			// and the network must actually be gone from the API.
			{
				Config: testAccSharedNetworkConfig(name, locationID, imageID, false),
				ConfigStateChecks: []statecheck.StateCheck{
					&checkNetworkGoneByID{networkID: &sharedNetworkID},
				},
			},
			// 3. Convergence.
			{
				Config:   testAccSharedNetworkConfig(name, locationID, imageID, false),
				PlanOnly: true,
			},
		},
	})
}

// saveResourceID captures the "id" attribute of a resource into dst for use in
// later steps (e.g. after the resource has left the configuration and state).
type saveResourceID struct {
	resourceAddress string
	dst             *string
}

func (c *saveResourceID) CheckState(ctx context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	res := acctest.FindResource(req.State, c.resourceAddress)
	if res == nil {
		resp.Error = fmt.Errorf("resource not found: %s", c.resourceAddress)
		return
	}
	id, _ := res.AttributeValues["id"].(string)
	if id == "" {
		resp.Error = fmt.Errorf("no ID set on %s", c.resourceAddress)
		return
	}
	*c.dst = id
}

// checkNetworkGoneByID verifies via the API that the network with the saved ID
// no longer exists.
type checkNetworkGoneByID struct {
	networkID *string
}

func (c *checkNetworkGoneByID) CheckState(ctx context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	if *c.networkID == "" {
		resp.Error = fmt.Errorf("network ID was not captured in a previous step")
		return
	}
	_, err := acctest.GetTestClient().GetNetwork(ctx, *c.networkID)
	if err == nil {
		resp.Error = fmt.Errorf("network %s still exists in the API", *c.networkID)
		return
	}
	if !sdk.IsNotFound(err) {
		resp.Error = fmt.Errorf("error checking network %s: %w", *c.networkID, err)
	}
}

func testAccSharedNetworkConfig(name, locationID, imageID string, withShared bool) string {
	base := fmt.Sprintf(`
resource "vcp_server" "s" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = 1
  ram_mb      = 2048
  volumes = [
    { number = 0, name = "boot", size_mb = 30720 }
  ]
}

resource "vcp_gateway" "gw" {
  name           = %q
  location_id    = %q
  bandwidth_mbps = 100
}
`, name+"-srv", locationID, imageID, name+"-gw", locationID)

	if !withShared {
		return base
	}

	shared := fmt.Sprintf(`
resource "vcp_network" "shared" {
  name           = %q
  location_id    = %q
  network_prefix = "10.30.0.0"
  mask           = 24
}

resource "vcp_server_network_attachment" "shared" {
  server_id  = vcp_server.s.id
  network_id = vcp_network.shared.id
}

resource "vcp_gateway_network_attachment" "shared" {
  gateway_id = vcp_gateway.gw.id
  network_id = vcp_network.shared.id
}
`, name+"-shared", locationID)

	return base + shared
}
