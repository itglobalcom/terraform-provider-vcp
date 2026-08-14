package gateway_rules_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// TestAccGatewayRules_parallel applies both rule sets and a network attachment
// to one gateway in a single apply. All three touch the same gateway, and the
// backend answers -19803 ("competitive change conflict") to concurrent changes,
// so this is the test that justifies locks.Gateway.
//
// The rules deliberately reference vcp_gateway.test.id rather than the
// attachment (which the documentation recommends): that removes the ordering
// dependency and lets Terraform run the attachment and both PUTs at the same
// time — the case the lock has to survive.
func TestAccGatewayRules_parallel(t *testing.T) {
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-rules-par-" + acctest.RandomString(6)
	config := testAccRulesParallelConfig(name, locationID)

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
					statecheck.ExpectKnownValue("vcp_gateway_nat.test",
						tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
					statecheck.ExpectKnownValue("vcp_gateway_firewall.test",
						tfjsonpath.New("rules"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue("vcp_gateway_network_attachment.test",
						tfjsonpath.New("ip_address"), knownvalue.NotNull()),
				},
			},
			// Both rule sets and the attachment must settle into an empty plan.
			{Config: config, PlanOnly: true},
		},
	})
}

// TestAccGatewayFirewall_ruleDrift covers rules changed behind Terraform's back:
// the resource owns the whole list, so both a rule that disappeared and a rule
// that appeared have to show up as a plan.
//
// Split out from the gateway-disappears test on purpose. Applying a rule set is
// an asynchronous backend task that fails transiently every now and then, and a
// failed task leaves the gateway rejecting changes for minutes — so a test that
// chains many applies onto one gateway fails from time to time for reasons that
// have nothing to do with the provider. Fewer applies per test, fewer such
// failures.
func TestAccGatewayFirewall_ruleDrift(t *testing.T) {
	resourceName := "vcp_gateway_firewall.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-fw-drift-" + acctest.RandomString(6)
	config := testAccFirewallOutOfBandConfig(name, locationID)

	var gatewayID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckGatewaysDestroyed,
		Steps: []resource.TestStep{
			// 1. Create and remember the gateway for the out-of-band calls.
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "gateway_id", &gatewayID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(2)),
				},
			},
			// 2. Rules wiped elsewhere → the plan has to restore them.
			{
				PreConfig:          func() { setFirewallRules(t, gatewayID, nil) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// 3. Applying restores exactly the configured set.
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(2)),
				},
			},
			// 4. A rule added elsewhere → the plan has to remove it. The resource
			// owns the whole list; there is no "merge" mode.
			{
				PreConfig: func() {
					setFirewallRules(t, gatewayID, append(outOfBandFirewallRules(), entities.FirewallRule{
						Action: "Allow", Direction: "In", Protocol: "UDP",
						Source: "0.0.0.0/0", Destination: "10.221.0.0/24", DestinationPort: 9999,
					}))
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccGatewayFirewall_gatewayDisappears covers the gateway itself being
// deleted behind Terraform's back: Read has to treat the 404 as "the object is
// gone" and drop the resource from state, rather than fail the refresh.
//
// One apply and one out-of-band call, so it is the cheapest of the drift tests
// and the least exposed to a transient task failure.
func TestAccGatewayFirewall_gatewayDisappears(t *testing.T) {
	resourceName := "vcp_gateway_firewall.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-fw-gone-" + acctest.RandomString(6)
	config := testAccFirewallOutOfBandConfig(name, locationID)

	var gatewayID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckGatewaysDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "gateway_id", &gatewayID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(2)),
				},
			},
			// The gateway is deleted elsewhere: the plan has to come back
			// non-empty (both the gateway and its rules need re-creating) and the
			// refresh has to survive the 404.
			{
				PreConfig:          func() { deleteGatewayOutOfBand(t, gatewayID) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccGatewayNAT_adoptsExistingRules covers taking over a gateway that
// already has rules (rules made in the panel, or by another Terraform state),
// starting from an empty rule set, and — in the last step — that destroying the
// resource clears the rule set. The gateway outlives the rules resource here,
// which is what makes that last check possible at all.
func TestAccGatewayNAT_adoptsExistingRules(t *testing.T) {
	resourceName := "vcp_gateway_nat.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-nat-adopt-" + acctest.RandomString(6)

	var gatewayID string

	snat := `
    {
      type        = "SNAT"
      protocol    = "IP"
      source      = "10.221.0.0/24"
      destination = "0.0.0.0/0"
      translated  = vcp_gateway.test.public_ip
    },`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckGatewaysDestroyed,
		Steps: []resource.TestStep{
			// 1. Gateway alone — no rule resource yet.
			{
				Config: testAccNATAdoptConfig(name, locationID, false, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr("vcp_gateway.test", "id", &gatewayID),
				},
			},
			// 2. Two rules appear out of band, then the resource is declared with
			// an empty list: it adopts the gateway (warning in the diagnostics)
			// and clears what it found.
			{
				PreConfig: func() { setNATRules(t, gatewayID, outOfBandNATRules(t, gatewayID)) },
				Config:    testAccNATAdoptConfig(name, locationID, true, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(0)),
				},
				Check: checkNATRuleCount(&gatewayID, 0),
			},
			// 3. Growing from empty to a real rule set.
			{
				Config: testAccNATAdoptConfig(name, locationID, true, snat),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
				},
				Check: checkNATRuleCount(&gatewayID, 1),
			},
			// 4. The resource is removed from the configuration while the gateway
			// stays: destroying it has to clear the rule set in one step, without
			// asking for an `rules = []` apply first.
			{
				Config: testAccNATAdoptConfig(name, locationID, false, ""),
				Check:  checkNATRuleCount(&gatewayID, 0),
			},
		},
	})
}

// TestAccGatewayFirewall_allFieldsAndLargeSet exercises every field the API
// accepts (both ports non-zero, all four protocols, both directions, /32, /24
// and 0.0.0.0/0) and imports the result — which proves the flatten path covers
// everything a gateway can return, not just what the earlier tests write. The
// last step applies 200 rules: that is the largest list known to apply cleanly,
// and it checks the order survives at scale.
func TestAccGatewayFirewall_allFieldsAndLargeSet(t *testing.T) {
	resourceName := "vcp_gateway_firewall.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-fw-all-" + acctest.RandomString(6)

	allFields := `
    {
      action           = "Allow"
      direction        = "In"
      protocol         = "TCP"
      source           = "192.0.2.0/24"
      source_port      = 12345
      destination      = "10.221.0.5/32"
      destination_port = 443
    },
    {
      action           = "Deny"
      direction        = "Out"
      protocol         = "UDP"
      source           = "10.221.0.0/24"
      source_port      = 53
      destination      = "0.0.0.0/0"
      destination_port = 53
    },
    {
      action      = "Allow"
      direction   = "In"
      protocol    = "ICMP"
      source      = "0.0.0.0/0"
      destination = "10.221.0.0/24"
    },
    {
      action      = "Deny"
      direction   = "In"
      protocol    = "IP"
      source      = "0.0.0.0/0"
      destination = "0.0.0.0/0"
    },`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckGatewaysDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccFirewallRulesConfig(name, locationID, allFields),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(4)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("source_port"), knownvalue.Int64Exact(12345)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(1).AtMapKey("action"), knownvalue.StringExact("Deny")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(2).AtMapKey("protocol"), knownvalue.StringExact("ICMP")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(3).AtMapKey("protocol"), knownvalue.StringExact("IP")),
				},
			},
			// Every field has to survive a round-trip through the API.
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "gateway_id"),
			},
			// 200 rules — the documented ceiling — must apply in order.
			{
				Config: testAccFirewallLargeSetConfig(name, locationID, 200),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(200)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("destination_port"), knownvalue.Int64Exact(1000)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(199).AtMapKey("destination_port"), knownvalue.Int64Exact(1199)),
				},
			},
		},
	})
}

// TestAccGatewayNAT_gatewayIDForcesReplacement checks that pointing the resource
// at another gateway replaces it: the rules move, which means the gateway left
// behind is cleared and the new one carries them.
func TestAccGatewayNAT_gatewayIDForcesReplacement(t *testing.T) {
	resourceName := "vcp_gateway_nat.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-nat-repl-" + acctest.RandomString(6)

	var firstGatewayID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckGatewaysDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccNATReplaceConfig(name, locationID, "a"),
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "gateway_id", &firstGatewayID),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
				},
			},
			{
				Config: testAccNATReplaceConfig(name, locationID, "b"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.CompareValuePairs(
						resourceName, tfjsonpath.New("gateway_id"),
						"vcp_gateway.b", tfjsonpath.New("id"),
						compare.ValuesSame(),
					),
				},
				Check: resource.ComposeTestCheckFunc(
					// The replacement destroys the old resource, which clears the
					// rule set of the gateway it pointed at…
					checkNATRuleCount(&firstGatewayID, 0),
					// …and creates it on the new one.
					checkNATRuleCountOf(resourceName, 1),
				),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

const testAccRulesGatewayBandwidth = 50 // some locations have bandwidth_min = 50

func testAccRulesParallelConfig(name, locationID string) string {
	return fmt.Sprintf(`
resource "vcp_network" "test" {
  name           = "%[1]s-net"
  location_id    = %[2]q
  network_prefix = "10.221.0.0"
  mask           = 24
}

resource "vcp_gateway" "test" {
  name           = %[1]q
  location_id    = %[2]q
  bandwidth_mbps = %[3]d
}

resource "vcp_gateway_network_attachment" "test" {
  gateway_id = vcp_gateway.test.id
  network_id = vcp_network.test.id
}

resource "vcp_gateway_nat" "test" {
  gateway_id = vcp_gateway.test.id

  rules = [
    {
      type        = "SNAT"
      protocol    = "IP"
      source      = "10.221.0.0/24"
      destination = "0.0.0.0/0"
      translated  = vcp_gateway.test.public_ip
    },
  ]
}

resource "vcp_gateway_firewall" "test" {
  gateway_id = vcp_gateway.test.id

  rules = [
    { action = "Deny", direction = "In", protocol = "IP", source = "0.0.0.0/0", destination = "0.0.0.0/0" },
    {
      action           = "Allow"
      direction        = "In"
      protocol         = "TCP"
      source           = "0.0.0.0/0"
      destination      = "10.221.0.0/24"
      destination_port = 443
    },
  ]
}
`, name, locationID, testAccRulesGatewayBandwidth)
}

func testAccFirewallOutOfBandConfig(name, locationID string) string {
	return testAccFirewallRulesConfig(name, locationID, `
    { action = "Deny", direction = "In", protocol = "IP", source = "0.0.0.0/0", destination = "0.0.0.0/0" },
    {
      action           = "Allow"
      direction        = "In"
      protocol         = "TCP"
      source           = "0.0.0.0/0"
      destination      = "10.221.0.0/24"
      destination_port = 443
    },`)
}

func testAccFirewallRulesConfig(name, locationID, rules string) string {
	return fmt.Sprintf(`
resource "vcp_gateway" "test" {
  name           = %[1]q
  location_id    = %[2]q
  bandwidth_mbps = %[3]d
}

resource "vcp_gateway_firewall" "test" {
  gateway_id = vcp_gateway.test.id

  rules = [%[4]s
  ]
}
`, name, locationID, testAccRulesGatewayBandwidth, rules)
}

// testAccFirewallLargeSetConfig builds the rule list with a Terraform `for`
// expression — the list is an attribute, not a block, so the configuration
// stays short whatever the count.
func testAccFirewallLargeSetConfig(name, locationID string, count int) string {
	return fmt.Sprintf(`
resource "vcp_gateway" "test" {
  name           = %[1]q
  location_id    = %[2]q
  bandwidth_mbps = %[3]d
}

resource "vcp_gateway_firewall" "test" {
  gateway_id = vcp_gateway.test.id

  rules = [for p in range(%[4]d) : {
    action           = "Allow"
    direction        = "In"
    protocol         = "TCP"
    source           = "0.0.0.0/0"
    destination      = "10.221.0.0/24"
    destination_port = 1000 + p
  }]
}
`, name, locationID, testAccRulesGatewayBandwidth, count)
}

func testAccNATAdoptConfig(name, locationID string, withResource bool, rules string) string {
	config := fmt.Sprintf(`
resource "vcp_gateway" "test" {
  name           = %[1]q
  location_id    = %[2]q
  bandwidth_mbps = %[3]d
}
`, name, locationID, testAccRulesGatewayBandwidth)

	if withResource {
		config += fmt.Sprintf(`
resource "vcp_gateway_nat" "test" {
  gateway_id = vcp_gateway.test.id

  rules = [%s
  ]
}
`, rules)
	}
	return config
}

func testAccNATReplaceConfig(name, locationID, target string) string {
	return fmt.Sprintf(`
resource "vcp_gateway" "a" {
  name           = "%[1]s-a"
  location_id    = %[2]q
  bandwidth_mbps = %[3]d
}

resource "vcp_gateway" "b" {
  name           = "%[1]s-b"
  location_id    = %[2]q
  bandwidth_mbps = %[3]d
}

resource "vcp_gateway_nat" "test" {
  gateway_id = vcp_gateway.%[4]s.id

  rules = [
    {
      type        = "SNAT"
      protocol    = "IP"
      source      = "10.221.0.0/24"
      destination = "0.0.0.0/0"
      translated  = vcp_gateway.%[4]s.public_ip
    },
  ]
}
`, name, locationID, testAccRulesGatewayBandwidth, target)
}

// ============================================================================
// Out-of-band helpers and checks
// ============================================================================

func outOfBandFirewallRules() []entities.FirewallRule {
	return []entities.FirewallRule{
		{Action: "Deny", Direction: "In", Protocol: "IP", Source: "0.0.0.0/0", Destination: "0.0.0.0/0"},
		{Action: "Allow", Direction: "In", Protocol: "TCP", Source: "0.0.0.0/0",
			Destination: "10.221.0.0/24", DestinationPort: 443},
	}
}

// outOfBandNATRules builds rules the API will accept: SNAT has to translate to
// the gateway's own external address.
func outOfBandNATRules(t *testing.T, gatewayID string) []entities.NATRule {
	t.Helper()
	gw, err := acctest.GetTestClient().GetGateway(context.Background(), gatewayID)
	if err != nil {
		t.Fatalf("out-of-band gateway read failed: %v", err)
	}
	var public string
	for _, nic := range gw.NICs {
		if nic.BandwidthMbps > 0 {
			public = nic.IPAddress
		}
	}
	if public == "" {
		t.Fatalf("gateway %s has no public NIC", gatewayID)
	}
	return []entities.NATRule{
		{Type: "SNAT", Protocol: "IP", Source: "10.221.0.0/24", Destination: "0.0.0.0/0", Translated: public},
		{Type: "SNAT", Protocol: "TCP", Source: "10.221.0.0/24", Destination: "0.0.0.0/0",
			DestinationPort: 25, Translated: public, TranslatedPort: 25},
	}
}

func setFirewallRules(t *testing.T, gatewayID string, rules []entities.FirewallRule) {
	t.Helper()
	if rules == nil {
		rules = []entities.FirewallRule{}
	}
	err := retryOutOfBand(t, func() error {
		return acctest.GetTestClient().UpdateFirewallRulesAndWait(context.Background(), gatewayID,
			&entities.UpdateFirewallRulesRequest{FirewallRules: rules})
	})
	if err != nil {
		t.Fatalf("out-of-band firewall update failed: %v", err)
	}
}

// retryOutOfBand exists only as a witness: writing a rule set is an asynchronous
// backend task that fails transiently now and then even for a payload the API
// accepts. The SDK retries that itself, so this wrapper normally calls write()
// once — if it ever has to log a retry, the SDK's own retry has been exhausted
// and that is worth seeing in the test output.
func retryOutOfBand(t *testing.T, write func() error) error {
	t.Helper()
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		if err = write(); err == nil {
			return nil
		}
		t.Logf("out-of-band rule write attempt %d failed after the SDK's own retries: %v", attempt, err)
		time.Sleep(15 * time.Second)
	}
	return err
}

func setNATRules(t *testing.T, gatewayID string, rules []entities.NATRule) {
	t.Helper()
	if rules == nil {
		rules = []entities.NATRule{}
	}
	err := retryOutOfBand(t, func() error {
		return acctest.GetTestClient().UpdateNATRulesAndWait(context.Background(), gatewayID,
			&entities.UpdateNATRulesRequest{NATRules: rules})
	})
	if err != nil {
		t.Fatalf("out-of-band NAT update failed: %v", err)
	}
}

// deleteGatewayOutOfBand removes the gateway directly via the API and waits
// until it is really gone (deletion is asynchronous).
func deleteGatewayOutOfBand(t *testing.T, gatewayID string) {
	t.Helper()
	client := acctest.GetTestClient()
	ctx := context.Background()

	if err := client.DeleteGateway(ctx, gatewayID); err != nil && !sdk.IsNotFound(err) {
		t.Fatalf("out-of-band gateway delete failed: %v", err)
	}
	// 5 minutes is the agreed ceiling for backend tasks.
	for i := 0; i < 60; i++ {
		if _, err := client.GetGateway(ctx, gatewayID); sdk.IsNotFound(err) {
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatal("out-of-band deleted gateway did not disappear in time")
}

// checkNATRuleCountOf asserts the rule count of the gateway the resource points
// at right now, reading the id out of state so it does not depend on the order
// in which checks run.
func checkNATRuleCountOf(resourceName string, want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", resourceName)
		}
		gatewayID := rs.Primary.Attributes["gateway_id"]
		if gatewayID == "" {
			return fmt.Errorf("gateway_id is not set on %s", resourceName)
		}
		return checkNATRuleCount(&gatewayID, want)(s)
	}
}

// checkNATRuleCount asserts, straight from the API, how many NAT rules a gateway
// has. Used for a gateway that is no longer in the resource's state.
func checkNATRuleCount(gatewayID *string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		rules, err := acctest.GetTestClient().GetNATRules(context.Background(), *gatewayID)
		if err != nil {
			return fmt.Errorf("reading NAT rules of gateway %s: %w", *gatewayID, err)
		}
		if len(rules) != want {
			return fmt.Errorf("gateway %s has %d NAT rule(s), want %d", *gatewayID, len(rules), want)
		}
		return nil
	}
}

// captureAttr stores a string attribute of a resource for use in a later step
// (PreConfig has no access to state).
func captureAttr(address, attribute string, dst *string) statecheck.StateCheck {
	return captureAttrCheck{address: address, attribute: attribute, dst: dst}
}

type captureAttrCheck struct {
	address   string
	attribute string
	dst       *string
}

func (c captureAttrCheck) CheckState(_ context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	res := acctest.FindResource(req.State, c.address)
	if res == nil {
		resp.Error = fmt.Errorf("resource not found: %s", c.address)
		return
	}
	value, ok := res.AttributeValues[c.attribute].(string)
	if !ok || value == "" {
		resp.Error = fmt.Errorf("attribute %q of %s is not a non-empty string: %v",
			c.attribute, c.address, res.AttributeValues[c.attribute])
		return
	}
	*c.dst = value
}
