package vmware_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// testAccVPNSharedKey satisfies the backend's rule for a pre-shared key: 32–128
// alphanumeric characters with an upper-case letter, a lower-case one and a digit.
const testAccVPNSharedKey = "TfAccVpnSharedKey0123456789abcdef"

// TestAccVmwareEdgeVPNTunnel_basic is a site-to-site tunnel end to end: raise it,
// prove it settled, change what can be changed, import it, tear it down.
//
// The step that matters most is the second one. The API answers `encryption_type`
// and `diffie_hellman_group` in a spelling of its own (NET-5), and a provider that
// took those answers at face value would report a difference on every plan that
// no apply could ever settle.
func TestAccVmwareEdgeVPNTunnel_basic(t *testing.T) {
	resourceName := "vcp_vmware_edge_vpn_tunnel.test"
	name := testName("vpn")
	config := testAccVPNTunnelConfig(t, name, "10.234.1.0", "aes256", "dh14", 1500, true)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("name"), knownvalue.StringExact(name)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("peer_network"), knownvalue.StringExact("192.168.240.0/24")),
					// The configuration keeps its own spelling of the two enums…
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("encryption_type"), knownvalue.StringExact("aes256")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("diffie_hellman_group"), knownvalue.StringExact("dh14")),
					// …and the local side, which is not the user's to choose, is read.
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("local_ip"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("local_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("digest_algorithm"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("vcloud_id"), knownvalue.NotNull()),
				},
			},
			// NET-5: the spelling the API answers with must not read as a change.
			{Config: config, PlanOnly: true},
			// MTU and the enabled flag change on the tunnel that is already there.
			{
				PreConfig: waitForBackend,
				Config:    testAccVPNTunnelConfig(t, name, "10.234.1.0", "aes256", "dh14", 1400, false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("mtu"), knownvalue.Int64Exact(1400)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
				},
			},
			// The key is write-only, so an imported tunnel cannot know it.
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateIdFunc:       acctest.ImportIDFunc(resourceName, "network_id", "id"),
				ImportStateVerifyIgnore: []string{"shared_key"},
			},
		},
	})
}

// TestAccVmwareEdgeVPNTunnel_nameForcesReplacement covers the attribute that
// identifies a tunnel: the API has no rename, so a new name is a new tunnel.
func TestAccVmwareEdgeVPNTunnel_nameForcesReplacement(t *testing.T) {
	resourceName := "vcp_vmware_edge_vpn_tunnel.test"
	name := testName("vpnname")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{Config: testAccVPNTunnelConfig(t, name, "10.234.2.0", "aes256", "dh14", 1500, true)},
			{
				PreConfig: waitForBackend,
				Config:    testAccVPNTunnelConfig(t, name+"-renamed", "10.234.2.0", "aes256", "dh14", 1500, true),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("name"),
						knownvalue.StringExact(name+"-renamed")),
				},
			},
		},
	})
}

// TestAccVmwareEdgeVPNTunnel_rejectsBadInput covers what the provider settles
// before anything reaches the API. None of these create a thing: the pre-shared
// key rules alone come back from the backend as a code with no field name in it.
func TestAccVmwareEdgeVPNTunnel_rejectsBadInput(t *testing.T) {
	tunnel := func(body string) string {
		return fmt.Sprintf(`
resource "vcp_vmware_edge_vpn_tunnel" "test" {
  network_id = 1
  name       = "office"
%s}
`, body)
	}

	const goodRest = `  peer_endpoint        = "203.0.113.10"
  peer_identificator   = "203.0.113.10"
  peer_network         = "192.168.240.0/24"
  encryption_type      = "aes256"
  diffie_hellman_group = "dh14"
  mtu                  = 1500
`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      tunnel(goodRest + `  shared_key = "tooshort"` + "\n"),
				ExpectError: regexp.MustCompile(`(?s)Invalid Pre-Shared Key`),
			},
			{
				// Long enough, but all one case and no digit.
				Config:      tunnel(goodRest + `  shared_key = "abcdefghijklmnopqrstuvwxyzabcdefgh"` + "\n"),
				ExpectError: regexp.MustCompile(`(?s)upper-case`),
			},
			{
				// A hostname where the contract wants an address.
				Config: tunnel(`  peer_endpoint        = "vpn.example.com"
  peer_identificator   = "203.0.113.10"
  peer_network         = "192.168.240.0/24"
  encryption_type      = "aes256"
  diffie_hellman_group = "dh14"
  mtu                  = 1500
  shared_key           = "` + testAccVPNSharedKey + `"
`),
				ExpectError: regexp.MustCompile(`(?s)Invalid IP Address`),
			},
			{
				// A single host where the contract wants a subnet.
				Config: tunnel(`  peer_endpoint        = "203.0.113.10"
  peer_identificator   = "203.0.113.10"
  peer_network         = "192.168.240.5"
  encryption_type      = "aes256"
  diffie_hellman_group = "dh14"
  mtu                  = 1500
  shared_key           = "` + testAccVPNSharedKey + `"
`),
				ExpectError: regexp.MustCompile(`(?s)Invalid CIDR Address`),
			},
			{
				// An encryption the platform does not offer.
				Config: tunnel(`  peer_endpoint        = "203.0.113.10"
  peer_identificator   = "203.0.113.10"
  peer_network         = "192.168.240.0/24"
  encryption_type      = "rot13"
  diffie_hellman_group = "dh14"
  mtu                  = 1500
  shared_key           = "` + testAccVPNSharedKey + `"
`),
				ExpectError: regexp.MustCompile(`(?s)encryption_type`),
			},
		},
	})
}

// TestAccVmwareEdgeVPNTunnel_rejectsIsolatedNetwork covers the applicability
// check: a network without an edge has nowhere to put a tunnel.
func TestAccVmwareEdgeVPNTunnel_rejectsIsolatedNetwork(t *testing.T) {
	name := testName("vpniso")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: networkConfig(t, "test", name, "isolated", `  address     = "10.234.3.0"
  mask        = 24
  enable_dhcp = false
`) + testAccVPNTunnelResourceConfig(name, "aes256", "dh14", 1500, true),
				ExpectError: regexp.MustCompile(`(?s)routed`),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

func testAccVPNTunnelConfig(t *testing.T, name, address, encryption, dhGroup string, mtu int, enabled bool) string {
	t.Helper()
	return routedNetworkConfig(t, name, address) +
		testAccVPNTunnelResourceConfig(name, encryption, dhGroup, mtu, enabled)
}

func testAccVPNTunnelResourceConfig(name, encryption, dhGroup string, mtu int, enabled bool) string {
	return fmt.Sprintf(`
resource "vcp_vmware_edge_vpn_tunnel" "test" {
  network_id = vcp_vmware_network.test.id
  name       = %[1]q
  enabled    = %[5]t

  peer_endpoint      = "203.0.113.10"
  peer_identificator = "203.0.113.10"
  peer_network       = "192.168.240.0/24"

  shared_key           = %[6]q
  encryption_type      = %[2]q
  diffie_hellman_group = %[3]q
  mtu                  = %[4]d
}
`, name, encryption, dhGroup, mtu, enabled, testAccVPNSharedKey)
}
