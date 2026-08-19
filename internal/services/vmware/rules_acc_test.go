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
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// Edge firewall
// ============================================================================

// TestAccVmwareEdgeFirewall_basic is the life of a rule set: write it, prove it
// settled, change it, import it, and finally drop the resource while the network
// stays — which is the only way to see that destroying the resource really does
// turn the firewall off and clear the rules.
func TestAccVmwareEdgeFirewall_basic(t *testing.T) {
	resourceName := "vcp_vmware_edge_firewall.test"
	name := testName("edgefw")
	initial := testAccEdgeFirewallConfig(t, name, "10.233.1.0", "deny", testAccEdgeFirewallRulesAllFields)

	var networkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			// Every field the API accepts, in one go: the flatten path has to
			// cover all of them, not just the ones a minimal rule sets.
			{
				Config: initial,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_network.test", "id", &networkID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("default_action"),
						knownvalue.StringExact("deny")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(3)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("name"), knownvalue.StringExact("allow-https")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("destination_port"), knownvalue.StringExact("443")),
					// A port range has to survive the round trip as written — the API
					// takes ports as strings precisely so it can carry one.
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(2).AtMapKey("source_port"), knownvalue.StringExact("1024-65535")),
					// What happens to the attributes rule 1 leaves out — whether the
					// platform echoes "any" or nothing — is not asserted here: either
					// is fine, and the PlanOnly step below is what proves the answer
					// does not turn into a permanent difference.
				},
				Check: checkEdgeFirewallRuleCount(&networkID, 3),
			},
			{Config: initial, PlanOnly: true},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "network_id"),
			},
			// A shorter list, a different default action: both are in-place edits
			// of the same firewall.
			{
				PreConfig: waitForBackend,
				Config:    testAccEdgeFirewallConfig(t, name, "10.233.1.0", "allow", testAccEdgeFirewallRulesShort),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("default_action"),
						knownvalue.StringExact("allow")),
				},
				Check: checkEdgeFirewallRuleCount(&networkID, 1),
			},
			// An empty list is a legitimate configuration: the firewall stays on
			// and default_action becomes its whole behaviour.
			{
				PreConfig: waitForBackend,
				Config:    testAccEdgeFirewallConfig(t, name, "10.233.1.0", "deny", ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(0)),
				},
				Check: resource.ComposeTestCheckFunc(
					checkEdgeFirewallRuleCount(&networkID, 0),
					checkEdgeFirewallEnabled(&networkID, true),
				),
			},
			// The resource goes, the network stays: the firewall is switched off.
			{
				PreConfig: waitForBackend,
				Config:    routedNetworkConfig(t, name, "10.233.1.0"),
				Check:     checkEdgeFirewallEnabled(&networkID, false),
			},
		},
	})
}

// TestAccVmwareEdgeFirewall_drift covers the rules changed behind Terraform's
// back. The resource owns the whole list, so a rule that vanished and a rule
// that appeared both have to show up as a plan — there is no merge mode.
func TestAccVmwareEdgeFirewall_drift(t *testing.T) {
	resourceName := "vcp_vmware_edge_firewall.test"
	name := testName("fwdrift")
	config := testAccEdgeFirewallConfig(t, name, "10.233.2.0", "deny", testAccEdgeFirewallRulesShort)

	var networkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_network.test", "id", &networkID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
				},
			},
			// Rules wiped elsewhere → the plan has to restore them.
			{
				PreConfig:          func() { setEdgeFirewallOutOfBand(t, networkID, nil) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// Applying restores exactly the configured set.
			{
				Config: config,
				Check:  checkEdgeFirewallRuleCount(&networkID, 1),
			},
			// A rule added elsewhere → the plan has to remove it.
			{
				PreConfig: func() {
					setEdgeFirewallOutOfBand(t, networkID, append(testAccOutOfBandFirewallRules(),
						entities.VmwareUpdateEdgeFirewallRule{
							Name:            strPtr("stray"),
							Action:          entities.VmwareEdgeFirewallActionAllow,
							Protocol:        strPtr("udp"),
							Source:          strPtr("any"),
							Destination:     strPtr("any"),
							DestinationPort: strPtr("9999"),
						}))
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccVmwareEdgeFirewall_adoptsExistingRules covers taking over an edge that
// somebody already configured — from the panel, or from another Terraform state.
// The API cannot merge, so the resource replaces what it finds and says so in a
// warning; what matters here is that it ends up holding exactly the configured
// set.
func TestAccVmwareEdgeFirewall_adoptsExistingRules(t *testing.T) {
	name := testName("fwadopt")

	var networkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			// 1. The network alone — no rules resource yet.
			{
				Config: routedNetworkConfig(t, name, "10.233.3.0"),
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_network.test", "id", &networkID),
				},
			},
			// 2. Two rules appear out of band, then the resource is declared with
			// one: it adopts the edge and leaves exactly what the configuration says.
			{
				PreConfig: func() { setEdgeFirewallOutOfBand(t, networkID, testAccOutOfBandFirewallRules()) },
				Config:    testAccEdgeFirewallConfig(t, name, "10.233.3.0", "deny", testAccEdgeFirewallRulesShort),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("vcp_vmware_edge_firewall.test",
						tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
				},
				Check: checkEdgeFirewallRuleCount(&networkID, 1),
			},
		},
	})
}

// ============================================================================
// Edge NAT
// ============================================================================

// TestAccVmwareEdgeNAT_basic covers publishing a service and giving a private
// network its way out — the two things NAT is for — and the platform's own
// substitution: NET-4 means the address a DNAT rule translates from is always
// the edge's external one, so the resource must record what the platform chose
// rather than what the configuration implied.
func TestAccVmwareEdgeNAT_basic(t *testing.T) {
	resourceName := "vcp_vmware_edge_nat.test"
	name := testName("edgenat")
	config := testAccEdgeNATConfig(t, name, "10.233.4.0", testAccEdgeNATRulesPair)

	var networkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_network.test", "id", &networkID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("type"), knownvalue.StringExact("dnat")),
					// NET-4: the configuration says nothing about original_ip and
					// the platform fills in the edge's external address.
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("original_ip"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("translated_ip"),
						knownvalue.StringExact("10.233.4.10")),
					// NET-2: a rule is created passing traffic unless asked otherwise.
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(1).AtMapKey("type"), knownvalue.StringExact("snat")),
				},
				Check: checkNATRuleCount(&networkID, 2),
			},
			// The substituted address must not turn into a perpetual diff.
			{Config: config, PlanOnly: true},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "network_id"),
			},
			// The resource goes, the network stays: the rules are gone with it.
			{
				PreConfig: waitForBackend,
				Config:    routedNetworkConfig(t, name, "10.233.4.0"),
				Check:     checkNATRuleCount(&networkID, 0),
			},
		},
	})
}

// TestAccVmwareEdgeNAT_growAndShrink is the reconciler's own test. NAT has no
// bulk endpoint, so the provider walks the list: entries are paired with existing
// rules by position, extra entries are created, and rules the configuration no
// longer has are deleted. Growing and then shrinking exercises all three, and the
// count is checked against the API each time — state alone would agree with
// itself even if nothing had been written.
func TestAccVmwareEdgeNAT_growAndShrink(t *testing.T) {
	resourceName := "vcp_vmware_edge_nat.test"
	name := testName("natgrow")

	var networkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			// One rule.
			{
				Config: testAccEdgeNATConfig(t, name, "10.233.5.0", testAccEdgeNATRuleDNAT("443", "10.233.5.10")),
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_network.test", "id", &networkID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
				},
				Check: checkNATRuleCount(&networkID, 1),
			},
			// Three: the first is overwritten in place, two are created.
			{
				PreConfig: waitForBackend,
				Config: testAccEdgeNATConfig(t, name, "10.233.5.0",
					testAccEdgeNATRuleDNAT("443", "10.233.5.10")+
						testAccEdgeNATRuleDNAT("8080", "10.233.5.11")+
						testAccEdgeNATRuleDNAT("8443", "10.233.5.12")),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(3)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(2).AtMapKey("original_port"), knownvalue.StringExact("8443")),
				},
				Check: checkNATRuleCount(&networkID, 3),
			},
			{
				PreConfig: waitForBackend,
				Config: testAccEdgeNATConfig(t, name, "10.233.5.0",
					testAccEdgeNATRuleDNAT("443", "10.233.5.10")+
						testAccEdgeNATRuleDNAT("8080", "10.233.5.11")+
						testAccEdgeNATRuleDNAT("8443", "10.233.5.12")),
				PlanOnly: true,
			},
			// Back to one: the two extra rules must be deleted, not left behind.
			{
				PreConfig: waitForBackend,
				Config:    testAccEdgeNATConfig(t, name, "10.233.5.0", testAccEdgeNATRuleDNAT("443", "10.233.5.10")),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
				},
				Check: checkNATRuleCount(&networkID, 1),
			},
			// Rewriting a rule in place: same position, different content, still
			// one rule on the edge.
			{
				PreConfig: waitForBackend,
				Config:    testAccEdgeNATConfig(t, name, "10.233.5.0", testAccEdgeNATRuleDNAT("9443", "10.233.5.20")),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("original_port"), knownvalue.StringExact("9443")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("translated_ip"),
						knownvalue.StringExact("10.233.5.20")),
				},
				Check: checkNATRuleCount(&networkID, 1),
			},
		},
	})
}

// TestAccVmwareEdgeNAT_disabledRule covers a rule kept in the configuration
// without passing traffic. NET-2 makes `enabled` the one flag the API does
// round-trip on a NAT rule, so it has to survive both ways.
func TestAccVmwareEdgeNAT_disabledRule(t *testing.T) {
	resourceName := "vcp_vmware_edge_nat.test"
	name := testName("natoff")

	disabled := `
    {
      type            = "dnat"
      protocol        = "tcp"
      original_port   = "2222"
      translated_ip   = "10.233.6.10"
      translated_port = "22"
      enabled         = false
    },`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccEdgeNATConfig(t, name, "10.233.6.0", disabled),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("enabled"), knownvalue.Bool(false)),
				},
			},
			{Config: testAccEdgeNATConfig(t, name, "10.233.6.0", disabled), PlanOnly: true},
			// Switching it back on is an edit of the same rule.
			{
				PreConfig: waitForBackend,
				Config: testAccEdgeNATConfig(t, name, "10.233.6.0", `
    {
      type            = "dnat"
      protocol        = "tcp"
      original_port   = "2222"
      translated_ip   = "10.233.6.10"
      translated_port = "22"
      enabled         = true
    },`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("enabled"), knownvalue.Bool(true)),
				},
			},
		},
	})
}

// TestAccVmwareEdgeNAT_drift covers a rule added from the panel: the resource
// owns the whole set, so the extra rule has to show up as a plan and be removed
// by the next apply.
func TestAccVmwareEdgeNAT_drift(t *testing.T) {
	name := testName("natdrift")
	config := testAccEdgeNATConfig(t, name, "10.233.7.0", testAccEdgeNATRuleDNAT("443", "10.233.7.10"))

	var networkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_network.test", "id", &networkID),
				},
			},
			{
				PreConfig: func() {
					addEdgeNATRuleOutOfBand(t, networkID, &entities.VmwareUpsertNATRuleRequest{
						Type:           entities.VmwareEdgeNATTypeDNAT,
						Protocol:       "tcp",
						Description:    "stray",
						OriginalIP:     entities.VmwareEdgeFirewallAny,
						OriginalPort:   "9999",
						TranslatedIP:   "10.233.7.99",
						TranslatedPort: "9999",
					})
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// The apply brings the edge back to the configured set.
			{
				Config: config,
				Check:  checkNATRuleCount(&networkID, 1),
			},
		},
	})
}

// TestAccVmwareEdgeNAT_rejectsIsolatedNetwork covers the applicability check: an
// isolated network has no edge, and the API's own refusal does not say so. The
// provider reads the network first and answers with the network's type.
func TestAccVmwareEdgeNAT_rejectsIsolatedNetwork(t *testing.T) {
	name := testName("natiso")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: networkConfig(t, "test", name, "isolated", `  address     = "10.233.8.0"
  mask        = 24
  enable_dhcp = false
`) + `
resource "vcp_vmware_edge_nat" "test" {
  network_id = vcp_vmware_network.test.id

  rules = [
    { type = "snat", protocol = "any", original_ip = "10.233.8.0/24" },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)routed`),
			},
		},
	})
}

// TestAccVmwareEdgeNAT_rejectsOriginalIPOnDNAT covers the plan-time check for
// NET-4. No object is created: the configuration never gets past validation,
// which is the whole point — the alternative is finding out at the end of a long
// apply that the platform stored a different address.
func TestAccVmwareEdgeNAT_rejectsOriginalIPOnDNAT(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "vcp_vmware_edge_nat" "test" {
  network_id = 1

  rules = [
    {
      type          = "dnat"
      protocol      = "tcp"
      original_ip   = "10.0.0.1"
      original_port = "443"
      translated_ip = "10.0.0.10"
    },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Attribute Not Applicable To DNAT`),
			},
			// An SNAT rule without the address it translates is the mirror case.
			{
				Config: `
resource "vcp_vmware_edge_nat" "test" {
  network_id = 1

  rules = [
    { type = "snat", protocol = "any" },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Missing Attribute`),
			},
			// And a malformed address must be caught before any call is made.
			{
				Config: `
resource "vcp_vmware_edge_nat" "test" {
  network_id = 1

  rules = [
    { type = "snat", protocol = "any", original_ip = "10.0.0.5/24" },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Invalid Rule Address`),
			},
		},
	})
}

// TestAccVmwareEdgeRules_parallel declares the firewall and the NAT rules of one
// network in a single apply. Terraform runs them concurrently, the platform
// re-pushes the whole set of edge objects to vCloud for either change, and a
// second change arriving mid-flight is refused — so this is the test that
// justifies locks.VmwareNetwork.
func TestAccVmwareEdgeRules_parallel(t *testing.T) {
	name := testName("edgepar")
	config := testAccEdgeRulesParallelConfig(t, name, "10.233.9.0")

	var networkID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareNetworksDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_network.test", "id", &networkID),
					statecheck.ExpectKnownValue("vcp_vmware_edge_firewall.test",
						tfjsonpath.New("rules"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue("vcp_vmware_edge_nat.test",
						tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
				},
				Check: resource.ComposeTestCheckFunc(
					checkEdgeFirewallRuleCount(&networkID, 2),
					checkNATRuleCount(&networkID, 1),
				),
			},
			// Both have to settle together.
			{Config: config, PlanOnly: true},
		},
	})
}

// ============================================================================
// Server firewall
// ============================================================================

// TestAccVmwareServerFirewall_basic covers the firewall of the machine itself:
// write a rule set, prove it settled, change it, import it, and drop the
// resource while the server stays — which shows the rules really are cleared.
func TestAccVmwareServerFirewall_basic(t *testing.T) {
	resourceName := "vcp_vmware_server_firewall.test"
	name := testName("srvfw")
	config := testAccServerFirewallConfig(t, name, testAccServerFirewallRulesFull)

	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_server.test", "id", &serverID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(3)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("traffic_direction"),
						knownvalue.StringExact("incoming")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(1).AtMapKey("traffic_direction"),
						knownvalue.StringExact("outgoing")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(2).AtMapKey("action"), knownvalue.StringExact("deny")),
				},
				Check: checkServerFirewallRuleCount(&serverID, 3),
			},
			{Config: config, PlanOnly: true},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "server_id"),
			},
			// A single rule replaces the set.
			{
				PreConfig: waitForBackend,
				Config:    testAccServerFirewallConfig(t, name, testAccServerFirewallRulesShort),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
				},
				Check: checkServerFirewallRuleCount(&serverID, 1),
			},
			// The resource goes, the machine stays: SRV-2 says a server with no
			// rules answers with an empty set, so this is checkable.
			{
				PreConfig: waitForBackend,
				Config:    catalogConfig(t) + serverConfig(t, "test", name, ""),
				Check:     checkServerFirewallRuleCount(&serverID, 0),
			},
		},
	})
}

// TestAccVmwareServerFirewall_drift covers rules changed from the panel — the
// same contract as the edge firewall, on a different object.
func TestAccVmwareServerFirewall_drift(t *testing.T) {
	name := testName("srvfwdrift")
	config := testAccServerFirewallConfig(t, name, testAccServerFirewallRulesShort)

	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_vmware_server.test", "id", &serverID),
				},
			},
			{
				PreConfig: func() {
					setServerFirewallOutOfBand(t, serverID, []entities.VmwareServerFirewallRule{{
						Name:             "stray",
						TrafficDirection: entities.VmwareTrafficDirectionIncoming,
						Action:           entities.VmwareFirewallActionAllow,
						Protocol:         "udp",
						DestinationPort:  strPtr("9999"),
					}})
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config,
				Check:  checkServerFirewallRuleCount(&serverID, 1),
			},
		},
	})
}

// TestAccVmwareServerFirewall_rejectsDuplicateNames covers the plan-time check
// for a rule set the API would refuse with a bare -2002 that names neither the
// field nor the value.
func TestAccVmwareServerFirewall_rejectsDuplicateNames(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "vcp_vmware_server_firewall" "test" {
  server_id = 1

  rules = [
    { name = "ssh", traffic_direction = "incoming", action = "allow", protocol = "tcp", destination_port = "22" },
    { name = "ssh", traffic_direction = "incoming", action = "allow", protocol = "tcp", destination_port = "2222" },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Duplicate Rule Name`),
			},
			// A port that is not a port is caught by the attribute validator.
			{
				Config: `
resource "vcp_vmware_server_firewall" "test" {
  server_id = 1

  rules = [
    { name = "ssh", traffic_direction = "incoming", action = "allow", protocol = "tcp", destination_port = "not-a-port" },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Invalid Rule Port`),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

func strPtr(s string) *string { return &s }

// testAccEdgeFirewallRulesAllFields exercises every attribute a rule has, so the
// read path is proved against a rule that carries all of them rather than the
// minimum.
const testAccEdgeFirewallRulesAllFields = `
    {
      name             = "allow-https"
      action           = "allow"
      protocol         = "tcp"
      source           = "any"
      source_port      = "any"
      destination      = "10.233.1.10"
      destination_port = "443"
    },
    {
      name     = "allow-icmp"
      action   = "allow"
      protocol = "icmp"
    },
    {
      name             = "office-ssh"
      action           = "allow"
      protocol         = "tcp"
      source           = "203.0.113.0/24"
      source_port      = "1024-65535"
      destination      = "10.233.1.0/24"
      destination_port = "22"
    },`

const testAccEdgeFirewallRulesShort = `
    {
      name             = "allow-https"
      action           = "allow"
      protocol         = "tcp"
      source           = "any"
      destination      = "any"
      destination_port = "443"
    },`

func testAccEdgeFirewallConfig(t *testing.T, name, address, defaultAction, rules string) string {
	t.Helper()
	return routedNetworkConfig(t, name, address) + fmt.Sprintf(`
resource "vcp_vmware_edge_firewall" "test" {
  network_id     = vcp_vmware_network.test.id
  default_action = %[1]q

  rules = [%[2]s
  ]
}
`, defaultAction, rules)
}

// testAccOutOfBandFirewallRules is the rule set the drift tests write straight
// through the API — the same shape testAccEdgeFirewallRulesShort declares.
func testAccOutOfBandFirewallRules() []entities.VmwareUpdateEdgeFirewallRule {
	return []entities.VmwareUpdateEdgeFirewallRule{{
		Name:            strPtr("allow-https"),
		Action:          entities.VmwareEdgeFirewallActionAllow,
		Protocol:        strPtr("tcp"),
		Source:          strPtr("any"),
		Destination:     strPtr("any"),
		DestinationPort: strPtr("443"),
	}}
}

// testAccEdgeNATRuleDNAT builds one published-service rule. original_ip is left
// out on purpose: the platform substitutes the edge's external address (NET-4)
// and the resource refuses a value there.
func testAccEdgeNATRuleDNAT(port, target string) string {
	return fmt.Sprintf(`
    {
      type            = "dnat"
      protocol        = "tcp"
      original_port   = %[1]q
      translated_ip   = %[2]q
      translated_port = %[1]q
    },`, port, target)
}

// testAccEdgeNATRulesPair is one rule of each kind: a service published inwards
// and the whole network let outwards.
const testAccEdgeNATRulesPair = `
    {
      type            = "dnat"
      protocol        = "tcp"
      description     = "publish https"
      original_port   = "443"
      translated_ip   = "10.233.4.10"
      translated_port = "443"
    },
    {
      type        = "snat"
      protocol    = "any"
      description = "outbound"
      original_ip = "10.233.4.0/24"
    },`

func testAccEdgeNATConfig(t *testing.T, name, address, rules string) string {
	t.Helper()
	return routedNetworkConfig(t, name, address) + fmt.Sprintf(`
resource "vcp_vmware_edge_nat" "test" {
  network_id = vcp_vmware_network.test.id

  rules = [%s
  ]
}
`, rules)
}

func testAccEdgeRulesParallelConfig(t *testing.T, name, address string) string {
	t.Helper()
	return routedNetworkConfig(t, name, address) + `
resource "vcp_vmware_edge_firewall" "test" {
  network_id     = vcp_vmware_network.test.id
  default_action = "deny"

  rules = [
    { name = "allow-https", action = "allow", protocol = "tcp", destination_port = "443" },
    { name = "allow-icmp", action = "allow", protocol = "icmp" },
  ]
}

resource "vcp_vmware_edge_nat" "test" {
  network_id = vcp_vmware_network.test.id

  rules = [
    { type = "snat", protocol = "any", original_ip = "10.233.9.0/24" },
  ]
}
`
}

const testAccServerFirewallRulesFull = `
    {
      name              = "ssh-office"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      source            = "203.0.113.0/24"
      destination_port  = "22"
    },
    {
      name              = "dns-out"
      traffic_direction = "outgoing"
      action            = "allow"
      protocol          = "udp"
      destination_port  = "53"
    },
    {
      name              = "drop-the-rest"
      traffic_direction = "incoming"
      action            = "deny"
      protocol          = "any"
    },`

const testAccServerFirewallRulesShort = `
    {
      name              = "ssh-office"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      source            = "203.0.113.0/24"
      destination_port  = "22"
    },`

func testAccServerFirewallConfig(t *testing.T, name, rules string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, "") + fmt.Sprintf(`
resource "vcp_vmware_server_firewall" "test" {
  server_id = vcp_vmware_server.test.id

  rules = [%s
  ]
}
`, rules)
}
