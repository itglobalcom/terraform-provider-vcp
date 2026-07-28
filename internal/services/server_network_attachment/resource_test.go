package server_network_attachment_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// TestAccServerNetworkAttachment_basic covers the attachment lifecycle: attach
// a server to a network with a specific IP (mac gets computed), an empty plan
// afterwards, and import via the composite "server_id:nic_id" ID.
func TestAccServerNetworkAttachment_basic(t *testing.T) {
	resourceName := "vcp_server_network_attachment.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")
	name := "test-acc-srvnet-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckServersDestroyed,
			acctest.CheckNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			// 1. Attach with a specific IP.
			{
				Config: testAccAttachmentConfig(name, locationID, imageID, "10.20.0.10"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ip_address"), knownvalue.StringExact("10.20.0.10")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("mac"), knownvalue.NotNull()),
				},
			},
			// 2. Idempotency.
			{
				Config:   testAccAttachmentConfig(name, locationID, imageID, "10.20.0.10"),
				PlanOnly: true,
			},
			// 3. Import.
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "server_id", "id"),
			},
		},
	})
}

// TestAccServerNetworkAttachment_autoIP covers the main user path: attach
// without specifying ip_address and let the platform assign one from the
// network's subnet; the computed IP must not cause a perpetual diff, and the
// attachment must import cleanly.
func TestAccServerNetworkAttachment_autoIP(t *testing.T) {
	resourceName := "vcp_server_network_attachment.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")
	name := "test-acc-srvnet-auto-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckServersDestroyed,
			acctest.CheckNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			// 1. Attach without ip_address — the platform assigns one from 10.21.0.0/24.
			{
				Config: testAccAttachmentConfigAutoIP(name, locationID, imageID),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("ip_address"),
						knownvalue.StringRegexp(regexp.MustCompile(`^10\.21\.0\.`)),
					),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("mac"), knownvalue.NotNull()),
				},
			},
			// 2. Idempotency — the auto-assigned IP must not produce a diff.
			{
				Config:   testAccAttachmentConfigAutoIP(name, locationID, imageID),
				PlanOnly: true,
			},
			// 3. Import.
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "server_id", "id"),
			},
		},
	})
}

// TestAccServerNetworkAttachment_disappears deletes the server's NIC out of
// band and checks the provider notices the attachment is gone (Read →
// RemoveResource) and plans to recreate it. This is a different code path from
// the gateway attachment's disappears test: here the NIC is fetched by id and
// the 404 is mapped via sdk.IsNotFound, rather than being missed in a list.
func TestAccServerNetworkAttachment_disappears(t *testing.T) {
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")
	name := "test-acc-srvnet-dis-" + acctest.RandomString(6)
	config := testAccAttachmentConfigAutoIP(name, locationID, imageID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckServersDestroyed,
			acctest.CheckNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { deleteServerNICOutOfBand(t, name, name+"-net") },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// deleteServerNICOutOfBand finds the named server's NIC on the named network and
// deletes it straight through the API, waiting for the server to settle.
func deleteServerNICOutOfBand(t *testing.T, serverName, networkName string) {
	client := acctest.GetTestClient()
	ctx := context.Background()

	servers, err := client.GetServerList(ctx)
	if err != nil {
		t.Fatalf("out-of-band server list failed: %v", err)
	}
	var serverID string
	for _, s := range servers {
		if s.Name == serverName {
			serverID = s.ID
			break
		}
	}
	if serverID == "" {
		t.Fatalf("server %s not found for out-of-band NIC delete", serverName)
	}

	networks, err := client.GetNetworkList(ctx)
	if err != nil {
		t.Fatalf("out-of-band network list failed: %v", err)
	}
	var networkID string
	for _, n := range networks {
		if n.Name == networkName {
			networkID = n.ID
			break
		}
	}
	if networkID == "" {
		t.Fatalf("network %s not found for out-of-band NIC delete", networkName)
	}

	nics, err := client.GetServerNICs(ctx, serverID)
	if err != nil {
		t.Fatalf("out-of-band NIC list failed: %v", err)
	}
	for _, nic := range nics {
		if nic.NetworkID == networkID {
			if err := client.DeleteServerNICAndWait(ctx, serverID, nic.ID); err != nil {
				t.Fatalf("out-of-band NIC delete failed: %v", err)
			}
			return
		}
	}
	t.Fatalf("NIC on network %s not found on server %s", networkID, serverID)
}

func testAccAttachmentConfigAutoIP(name, locationID, imageID string) string {
	return fmt.Sprintf(`
resource "vcp_network" "test" {
  name           = %q
  location_id    = %q
  network_prefix = "10.21.0.0"
  mask           = 24
}

resource "vcp_server" "test" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = 1
  ram_mb      = 2048
  volumes = [
    { number = 0, name = "boot", size_mb = 30720 }
  ]
}

resource "vcp_server_network_attachment" "test" {
  server_id  = vcp_server.test.id
  network_id = vcp_network.test.id
}
`, name+"-net", locationID, name, locationID, imageID)
}

func testAccAttachmentConfig(name, locationID, imageID, ip string) string {
	return fmt.Sprintf(`
resource "vcp_network" "test" {
  name           = %q
  location_id    = %q
  network_prefix = "10.20.0.0"
  mask           = 24
}

resource "vcp_server" "test" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = 1
  ram_mb      = 2048
  volumes = [
    { number = 0, name = "boot", size_mb = 30720 }
  ]
}

resource "vcp_server_network_attachment" "test" {
  server_id  = vcp_server.test.id
  network_id = vcp_network.test.id
  ip_address = %q
}
`, name+"-net", locationID, name, locationID, imageID, ip)
}
