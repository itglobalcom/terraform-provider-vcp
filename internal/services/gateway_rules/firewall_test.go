package gateway_rules_test

import (
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

// TestAccGatewayFirewall_basic covers the lifecycle of the firewall rule set:
// create, change a field, reorder (a real change — the last matching rule wins),
// clear with an empty list, and import.
//
// CheckDestroy verifies the gateway is gone: the gateway is destroyed together
// with the rules, and a gateway that no longer exists has no rules to check.
// That destroying the resource alone clears the rule set is covered by
// TestAccGatewayNAT_adoptsExistingRules, where the gateway outlives it.
func TestAccGatewayFirewall_basic(t *testing.T) {
	resourceName := "vcp_gateway_firewall.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-fw-" + acctest.RandomString(6)

	denyAll := `
    {
      action      = "Deny"
      direction   = "In"
      protocol    = "IP"
      source      = "0.0.0.0/0"
      destination = "0.0.0.0/0"
    },`
	allowHTTPS := `
    {
      action           = "Allow"
      direction        = "In"
      protocol         = "TCP"
      source           = "0.0.0.0/0"
      destination      = "10.220.0.5/32"
      destination_port = 443
    },`
	allowICMP := `
    {
      action      = "Allow"
      direction   = "In"
      protocol    = "ICMP"
      source      = "10.220.0.0/24"
      destination = "10.220.0.5/32"
    },`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckGatewaysDestroyed,
		Steps: []resource.TestStep{
			// 1. Create two rules; ports left out default to 0 ("any").
			{
				Config: testAccFirewallConfig(name, locationID, denyAll+allowHTTPS),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("action"), knownvalue.StringExact("Deny")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("destination_port"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(1).AtMapKey("destination_port"), knownvalue.Int64Exact(443)),
				},
			},
			// 2. Idempotency: the API must return the rules exactly as sent.
			{
				Config:   testAccFirewallConfig(name, locationID, denyAll+allowHTTPS),
				PlanOnly: true,
			},
			// 3. Add a rule and reorder: with last-match-wins semantics the order
			// is part of the configuration, so this is a real update.
			{
				Config: testAccFirewallConfig(name, locationID, denyAll+allowICMP+allowHTTPS),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(3)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(1).AtMapKey("protocol"), knownvalue.StringExact("ICMP")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(2).AtMapKey("destination_port"), knownvalue.Int64Exact(443)),
				},
			},
			// 4. Import by gateway id.
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "gateway_id"),
			},
			// 5. An empty list clears every rule.
			{
				Config: testAccFirewallConfig(name, locationID, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(0)),
				},
			},
		},
	})
}

// TestAccGatewayFirewall_invalidRules checks that the API's constraints are
// reported at plan time — no gateway is created, so this is cheap.
func TestAccGatewayFirewall_invalidRules(t *testing.T) {
	locationID := os.Getenv("VCP_LOCATION_ID")

	cases := []struct {
		name        string
		rule        string
		expectError *regexp.Regexp
	}{
		{
			name: "port on ICMP",
			rule: `{ action = "Allow", direction = "In", protocol = "ICMP",
			         source = "0.0.0.0/0", destination = "0.0.0.0/0", destination_port = 80 },`,
			expectError: regexp.MustCompile(`(?s)destination_port must be 0`),
		},
		{
			name: "non-canonical CIDR",
			rule: `{ action = "Allow", direction = "In", protocol = "TCP",
			         source = "10.220.0.5/24", destination = "0.0.0.0/0" },`,
			expectError: regexp.MustCompile(`(?s)must be the network address`),
		},
		{
			name: "bare address instead of CIDR",
			rule: `{ action = "Allow", direction = "In", protocol = "TCP",
			         source = "10.220.0.5", destination = "0.0.0.0/0" },`,
			expectError: regexp.MustCompile(`(?s)Use "10\.220\.0\.5/32"`),
		},
		{
			name: "lower case action",
			rule: `{ action = "allow", direction = "In", protocol = "TCP",
			         source = "0.0.0.0/0", destination = "0.0.0.0/0" },`,
			expectError: regexp.MustCompile(`(?s)Attribute rules\[0\]\.action value must be one of`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { acctest.PreCheck(t) },
				ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      testAccFirewallConfig("test-acc-fw-invalid", locationID, tc.rule),
					PlanOnly:    true,
					ExpectError: tc.expectError,
				}},
			})
		})
	}
}

func testAccFirewallConfig(name, locationID, rules string) string {
	// bandwidth_mbps = 50 is the smallest value accepted across the locations
	// used for tests (some have bandwidth_min = 50).
	return fmt.Sprintf(`
resource "vcp_gateway" "test" {
  name           = %[1]q
  location_id    = %[2]q
  bandwidth_mbps = 50
}

resource "vcp_gateway_firewall" "test" {
  gateway_id = vcp_gateway.test.id

  rules = [%[3]s
  ]
}
`, name, locationID, rules)
}
