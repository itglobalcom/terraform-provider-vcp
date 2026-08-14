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

// TestAccGatewayNAT_basic covers the NAT rule set against a real gateway: all
// three rule types (SNAT, DNAT, BINAT), each built on vcp_gateway.public_ip
// because the API accepts no other address in `destination` / `translated`, plus
// updates, idempotency, import and clearing the list.
func TestAccGatewayNAT_basic(t *testing.T) {
	resourceName := "vcp_gateway_nat.test"
	gatewayName := "vcp_gateway.test"
	locationID := os.Getenv("VCP_LOCATION_ID")
	name := "test-acc-nat-" + acctest.RandomString(6)

	// SNAT for the whole private range. protocol = "IP" means any protocol, and
	// then both ports have to stay 0 — so they are simply omitted.
	snat := `
    {
      type        = "SNAT"
      protocol    = "IP"
      source      = "10.220.0.0/24"
      destination = "0.0.0.0/0"
      translated  = vcp_gateway.test.public_ip
    },`
	// DNAT publishing a port on the gateway's own external address.
	dnat := `
    {
      type             = "DNAT"
      protocol         = "TCP"
      source           = "0.0.0.0/0"
      destination      = "${vcp_gateway.test.public_ip}/32"
      destination_port = 443
      translated       = "10.220.0.5"
      translated_port  = 8443
    },`
	// BINAT — 1:1 translation. The third rule type, and the only one the other
	// tests never send to the API: they only check that it is rejected with a
	// port. Both ports have to stay 0 here, so they are omitted.
	binat := `
    {
      type        = "BINAT"
      protocol    = "IP"
      source      = "10.220.0.6/32"
      destination = "0.0.0.0/0"
      translated  = vcp_gateway.test.public_ip
    },`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckGatewaysDestroyed,
		Steps: []resource.TestStep{
			// 1. Create. public_ip is unknown at plan time on the first apply, so
			// this also proves the validators tolerate unknown values.
			{
				Config: testAccNATConfig(name, locationID, snat),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(gatewayName, tfjsonpath.New("public_ip"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(1)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("type"), knownvalue.StringExact("SNAT")),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(0).AtMapKey("destination_port"), knownvalue.Int64Exact(0)),
				},
			},
			// 2. Idempotency: the API must echo the rules exactly as sent.
			{
				Config:   testAccNATConfig(name, locationID, snat),
				PlanOnly: true,
			},
			// 3. Publish a service on top of the SNAT rule, and add a BINAT one so
			// that all three rule types have gone through the API at least once.
			{
				Config: testAccNATConfig(name, locationID, snat+dnat+binat),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(3)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(1).AtMapKey("translated_port"), knownvalue.Int64Exact(8443)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("rules").AtSliceIndex(2), knownvalue.ObjectPartial(map[string]knownvalue.Check{
							"type":             knownvalue.StringExact("BINAT"),
							"protocol":         knownvalue.StringExact("IP"),
							"source":           knownvalue.StringExact("10.220.0.6/32"),
							"destination":      knownvalue.StringExact("0.0.0.0/0"),
							"destination_port": knownvalue.Int64Exact(0),
							"translated_port":  knownvalue.Int64Exact(0),
						})),
				},
			},
			// 4. Idempotency with all three types present — the step that would
			// catch the API normalising anything in a BINAT rule.
			{
				Config:   testAccNATConfig(name, locationID, snat+dnat+binat),
				PlanOnly: true,
			},
			// 5. Import by gateway id — the imported state has to match a rule set that
			// contains all three types.
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: acctest.ImportIDFunc(resourceName, "gateway_id"),
			},
			// 6. An empty list clears every rule.
			{
				Config: testAccNATConfig(name, locationID, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("rules"), knownvalue.ListSizeExact(0)),
				},
			},
		},
	})
}

// TestAccGatewayNAT_invalidRules checks the plan-time errors. No gateway is
// created, so this is cheap.
func TestAccGatewayNAT_invalidRules(t *testing.T) {
	locationID := os.Getenv("VCP_LOCATION_ID")

	cases := []struct {
		name        string
		rule        string
		expectError *regexp.Regexp
	}{
		{
			name: "mask in translated",
			rule: `{ type = "SNAT", protocol = "IP", source = "10.220.0.0/24",
			         destination = "0.0.0.0/0", translated = "10.220.0.5/32" },`,
			expectError: regexp.MustCompile(`(?s)without a mask`),
		},
		{
			name: "port on BINAT",
			rule: `{ type = "BINAT", protocol = "TCP", source = "10.220.0.5/32",
			         destination = "0.0.0.0/0", destination_port = 80, translated = "203.0.113.5" },`,
			expectError: regexp.MustCompile(`(?s)destination_port must be 0`),
		},
		{
			name: "port on any protocol",
			rule: `{ type = "SNAT", protocol = "IP", source = "10.220.0.0/24",
			         destination = "0.0.0.0/0", destination_port = 80, translated = "203.0.113.5" },`,
			expectError: regexp.MustCompile(`(?s)destination_port must be 0`),
		},
		{
			name: "lower case type",
			rule: `{ type = "dnat", protocol = "TCP", source = "0.0.0.0/0",
			         destination = "203.0.113.5/32", translated = "10.220.0.5" },`,
			expectError: regexp.MustCompile(`(?s)Attribute rules\[0\]\.type value must be one of`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { acctest.PreCheck(t) },
				ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      testAccNATConfig("test-acc-nat-invalid", locationID, tc.rule),
					PlanOnly:    true,
					ExpectError: tc.expectError,
				}},
			})
		})
	}
}

func testAccNATConfig(name, locationID, rules string) string {
	// bandwidth_mbps = 50 is the smallest value accepted across the locations
	// used for tests (some have bandwidth_min = 50).
	return fmt.Sprintf(`
resource "vcp_gateway" "test" {
  name           = %[1]q
  location_id    = %[2]q
  bandwidth_mbps = 50
}

resource "vcp_gateway_nat" "test" {
  gateway_id = vcp_gateway.test.id

  rules = [%[3]s
  ]
}
`, name, locationID, rules)
}
