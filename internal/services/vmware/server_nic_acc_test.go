package vmware_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// TestAccVmwareServerNetworkAttachment_basic is the scenario the resource exists
// for: two machines that need to talk to each other privately, over a network
// that is not the public one every VMware server is born with.
//
// The last step removes the attachment while the server and the network stay,
// which is the only way to prove the interface was really detached rather than
// taken down together with the machine.
func TestAccVmwareServerNetworkAttachment_basic(t *testing.T) {
	resourceName := "vcp_vmware_server_network_attachment.test"
	name := testName("attach")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckVmwareServersDestroyed,
			acctest.CheckVmwareNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			{
				Config: testAccAttachmentConfig(t, name, "10.232.1.0", `  ip = "10.232.1.10"`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ip"),
						knownvalue.StringExact("10.232.1.10")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("mac"), knownvalue.NotNull()),
					// The interface added here is never the primary one — that one
					// belongs to the server resource.
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("is_primary"), knownvalue.Bool(false)),
					statecheck.CompareValuePairs(
						resourceName, tfjsonpath.New("network_id"),
						"vcp_vmware_network.test", tfjsonpath.New("id"),
						compare.ValuesSame(),
					),
				},
				// Public interface + this one.
				Check: checkServerNICCount("vcp_vmware_server.test", 2),
			},
			{Config: testAccAttachmentConfig(t, name, "10.232.1.0", `  ip = "10.232.1.10"`), PlanOnly: true},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "server_id", "id"),
			},
			// The attachment goes, the machine and the network stay: the server
			// must be back to its public interface alone.
			{
				Config: testAccAttachmentBaseConfig(t, name, "10.232.1.0"),
				Check:  checkServerNICCount("vcp_vmware_server.test", 1),
			},
		},
	})
}

// TestAccVmwareServerNetworkAttachment_dhcp covers the other half of the API's
// rule: an address can only be asked for on a network with DHCP switched off. On
// a DHCP network the platform assigns one, and the resource has to record what
// it assigned instead of leaving the attribute empty.
func TestAccVmwareServerNetworkAttachment_dhcp(t *testing.T) {
	resourceName := "vcp_vmware_server_network_attachment.test"
	name := testName("dhcp")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckVmwareServersDestroyed,
			acctest.CheckVmwareNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			{
				Config: testAccAttachmentDHCPConfig(t, name, "10.232.2.0"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ip"), knownvalue.NotNull()),
				},
			},
			// An address the provider did not ask for must not turn into a
			// perpetual diff.
			{Config: testAccAttachmentDHCPConfig(t, name, "10.232.2.0"), PlanOnly: true},
		},
	})
}

// TestAccVmwareServerNetworkAttachment_ipForcesReplacement covers a change the
// API has no endpoint for: moving an interface to another address means a new
// interface, and the resource must say so rather than silently keep the old one.
func TestAccVmwareServerNetworkAttachment_ipForcesReplacement(t *testing.T) {
	resourceName := "vcp_vmware_server_network_attachment.test"
	name := testName("attachip")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckVmwareServersDestroyed,
			acctest.CheckVmwareNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			{Config: testAccAttachmentConfig(t, name, "10.232.3.0", `  ip = "10.232.3.10"`)},
			{
				Config: testAccAttachmentConfig(t, name, "10.232.3.0", `  ip = "10.232.3.11"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ip"),
						knownvalue.StringExact("10.232.3.11")),
				},
				// Still one private interface — replaced, not added.
				Check: checkServerNICCount("vcp_vmware_server.test", 2),
			},
		},
	})
}

// TestAccVmwareServerNetworkAttachment_disappears covers an interface removed
// from the panel: the refresh has to drop the resource from state rather than
// fail, and it must find the interface by id among all the server's interfaces
// rather than assume a position.
func TestAccVmwareServerNetworkAttachment_disappears(t *testing.T) {
	resourceName := "vcp_vmware_server_network_attachment.test"
	name := testName("attachgone")
	config := testAccAttachmentConfig(t, name, "10.232.4.0", `  ip = "10.232.4.10"`)

	var serverID, nicID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckVmwareServersDestroyed,
			acctest.CheckVmwareNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "server_id", &serverID),
					captureAttr(resourceName, "id", &nicID),
				},
			},
			{
				PreConfig:          func() { deleteNICOutOfBand(t, serverID, nicID) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccVmwareServerPublicInterface_basic covers the second public address: the
// platform picks the network and the address, the user picks the bandwidth — and
// the bandwidth is the one thing that changes without replacing the interface
// (and therefore without the address changing).
func TestAccVmwareServerPublicInterface_basic(t *testing.T) {
	resourceName := "vcp_vmware_server_public_interface.test"
	name := testName("pubif")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccPublicInterfaceConfig(t, name, 10),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("ip"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("network_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("mac"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"),
						knownvalue.Int64Exact(10)),
				},
				Check: checkServerNICCount("vcp_vmware_server.test", 2),
			},
			{Config: testAccPublicInterfaceConfig(t, name, 10), PlanOnly: true},
			// More bandwidth, same interface and same address.
			{
				Config: testAccPublicInterfaceConfig(t, name, 20),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("bandwidth_mbps"),
						knownvalue.Int64Exact(20)),
				},
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateIdFunc:       acctest.ImportIDFunc(resourceName, "server_id", "id"),
				ImportStateVerifyIgnore: []string{"is_ipv6"}, // write-only order option
			},
			// Dropped from the configuration: the extra address goes away and the
			// machine keeps the one it was born with.
			{
				Config: catalogConfig(t) + serverConfig(t, "test", name, ""),
				Check:  checkServerNICCount("vcp_vmware_server.test", 1),
			},
		},
	})
}

// TestAccVmwareServerNICs_parallel adds a private attachment and a public
// interface to one machine in a single apply. Terraform runs independent
// resources concurrently, the API serializes changes per server and answers
// -4000 to the second one — so this is the test that justifies locks.VmwareServer.
func TestAccVmwareServerNICs_parallel(t *testing.T) {
	name := testName("nicpar")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: acctest.ComposeCheckDestroy(
			acctest.CheckVmwareServersDestroyed,
			acctest.CheckVmwareNetworksDestroyed,
		),
		Steps: []resource.TestStep{
			{
				Config: testAccNICsParallelConfig(t, name, "10.232.5.0"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("vcp_vmware_server_network_attachment.test",
						tfjsonpath.New("ip"), knownvalue.StringExact("10.232.5.10")),
					statecheck.ExpectKnownValue("vcp_vmware_server_public_interface.test",
						tfjsonpath.New("ip"), knownvalue.NotNull()),
				},
				// Primary + private + extra public: three interfaces, and the two
				// added here must not have overwritten one another.
				Check: checkServerNICCount("vcp_vmware_server.test", 3),
			},
			{Config: testAccNICsParallelConfig(t, name, "10.232.5.0"), PlanOnly: true},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

// testAccAttachmentBaseConfig is a server and an isolated network with DHCP off,
// without any attachment between them.
func testAccAttachmentBaseConfig(t *testing.T, name, address string) string {
	t.Helper()
	return catalogConfig(t) +
		networkConfig(t, "test", name+"-net", "isolated", fmt.Sprintf(`  address     = %q
  mask        = 24
  enable_dhcp = false
`, address)) +
		serverConfig(t, "test", name, "")
}

func testAccAttachmentConfig(t *testing.T, name, address, extra string) string {
	t.Helper()
	return testAccAttachmentBaseConfig(t, name, address) + fmt.Sprintf(`
resource "vcp_vmware_server_network_attachment" "test" {
  server_id  = vcp_vmware_server.test.id
  network_id = vcp_vmware_network.test.id
%s
}
`, extra)
}

// testAccAttachmentDHCPConfig attaches to a network that hands out addresses, so
// the resource has nothing to ask for and everything to read back.
func testAccAttachmentDHCPConfig(t *testing.T, name, address string) string {
	t.Helper()
	return catalogConfig(t) +
		networkConfig(t, "test", name+"-net", "isolated", fmt.Sprintf(`  address     = %q
  mask        = 24
  enable_dhcp = true
`, address)) +
		serverConfig(t, "test", name, "") + `
resource "vcp_vmware_server_network_attachment" "test" {
  server_id  = vcp_vmware_server.test.id
  network_id = vcp_vmware_network.test.id
}
`
}

func testAccPublicInterfaceConfig(t *testing.T, name string, bandwidth int) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, "") + fmt.Sprintf(`
resource "vcp_vmware_server_public_interface" "test" {
  server_id      = vcp_vmware_server.test.id
  bandwidth_mbps = %d
}
`, bandwidth)
}

func testAccNICsParallelConfig(t *testing.T, name, address string) string {
	t.Helper()
	return testAccAttachmentConfig(t, name, address, `  ip = "10.232.5.10"`) + `
resource "vcp_vmware_server_public_interface" "test" {
  server_id      = vcp_vmware_server.test.id
  bandwidth_mbps = 10
}
`
}
