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

// The tests here settle at plan time and create nothing. That is the point: each
// one is a configuration the API would also refuse, but with a message that names
// neither the field nor the reason — and only after the apply is under way.
//
// They still need the provider configured, hence the PreCheck; they do not need
// a location with room in it.

// TestAccVmwareNetworkValidation_fieldsOfAnotherType covers the attributes that
// belong to another flavour of network.
func TestAccVmwareNetworkValidation_fieldsOfAnotherType(t *testing.T) {
	network := func(netType, body string) string {
		return fmt.Sprintf(`
resource "vcp_vmware_network" "test" {
  type        = %[1]q
  location_id = 1
  name        = "test-acc-vmw-validation"
%[2]s}
`, netType, body)
	}

	cases := map[string]struct {
		config string
		expect *regexp.Regexp
	}{
		"an isolated network has no bandwidth to shape": {
			config: network("isolated", `  address        = "10.0.0.0"
  mask           = 24
  bandwidth_mbps = 20
`),
			expect: regexp.MustCompile(`(?s)bandwidth_mbps does not apply to a isolated network`),
		},
		"capacity orders public addresses an isolated network has none of": {
			config: network("isolated", `  address  = "10.0.0.0"
  mask     = 24
  capacity = "1"
`),
			expect: regexp.MustCompile(`(?s)capacity does not apply`),
		},
		"a routed network is not ordered by capacity either": {
			config: network("routed", `  address  = "10.0.0.0"
  mask     = 24
  capacity = "1"
`),
			expect: regexp.MustCompile(`(?s)capacity does not apply`),
		},
		"the range of a public network is not the user's to pick": {
			config: network("public", `  capacity = "1"
  address  = "10.0.0.0"
`),
			expect: regexp.MustCompile(`(?s)address does not apply`),
		},
		"nor is its DHCP": {
			config: network("public", `  capacity    = "1"
  enable_dhcp = true
`),
			expect: regexp.MustCompile(`(?s)enable_dhcp does not apply`),
		},
		"an isolated network needs the range it hands out": {
			config: network("isolated", `  mask = 24
`),
			expect: regexp.MustCompile(`(?s)address is required`),
		},
		"a public network needs to know how many addresses to order": {
			config: network("public", `  bandwidth_mbps = 20
`),
			expect: regexp.MustCompile(`(?s)capacity is required`),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { acctest.PreCheckVmware(t) },
				ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
				Steps:                    []resource.TestStep{{Config: tc.config, ExpectError: tc.expect}},
			})
		})
	}
}

// TestAccVmwareServerValidation_conflictingBandwidth covers two attributes that
// describe one interface in two ways. The API takes both and honours one, which
// is the worst of the three possible outcomes.
func TestAccVmwareServerValidation_conflictingBandwidth(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "vcp_vmware_server" "test" {
  location_id            = 1
  name                   = "test-acc-vmw-validation"
  image_id               = 1
  cpu                    = 1
  ram_mb                 = 1024
  system_disk_mb         = 51200
  public_network_id      = 100
  network_bandwidth_mbps = 100
}
`,
				ExpectError: regexp.MustCompile(`(?s)Conflicting Attributes`),
			},
		},
	})
}

// TestAccVmwareServerValidation_volumes covers the disk list a user can get
// wrong without the API ever being asked.
func TestAccVmwareServerValidation_volumes(t *testing.T) {
	server := func(volumes string) string {
		return fmt.Sprintf(`
resource "vcp_vmware_server" "test" {
  location_id    = 1
  name           = "test-acc-vmw-validation"
  image_id       = 1
  cpu            = 1
  ram_mb         = 1024
  system_disk_mb = 51200

  volumes = [%s
  ]
}
`, volumes)
	}

	cases := map[string]struct {
		volumes string
		expect  *regexp.Regexp
	}{
		"two disks cannot share a number": {
			volumes: `
    { number = 0, name = "data", size_mb = 10240, disk_type = "ssd" },
    { number = 0, name = "logs", size_mb = 10240, disk_type = "ssd" },`,
			expect: regexp.MustCompile(`(?s)Duplicate Volume Number`),
		},
		"two disks cannot share a name": {
			volumes: `
    { number = 0, name = "data", size_mb = 10240, disk_type = "ssd" },
    { number = 1, name = "data", size_mb = 10240, disk_type = "ssd" },`,
			expect: regexp.MustCompile(`(?s)Duplicate Volume Name`),
		},
		"the boot disk is ordered with the machine": {
			volumes: `
    { number = 0, name = "boot", size_mb = 51200, disk_type = "ssd" },`,
			expect: regexp.MustCompile(`(?s)Boot Disk Is Not A Volume`),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { acctest.PreCheckVmware(t) },
				ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
				Steps:                    []resource.TestStep{{Config: server(tc.volumes), ExpectError: tc.expect}},
			})
		})
	}
}

// TestAccVmwareServer_bandwidthInPlace covers the bandwidth of the interface every
// VMware server is born with. The API takes it on the interface rather than on
// the server, and getting that wrong would mean replacing the machine — and its
// disk — to change a number.
func TestAccVmwareServer_bandwidthInPlace(t *testing.T) {
	resourceName := "vcp_vmware_server.test"
	name := testName("bw")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccServerBandwidthConfig(t, name, 10),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("network_bandwidth_mbps"), knownvalue.Int64Exact(10)),
				},
			},
			{Config: testAccServerBandwidthConfig(t, name, 10), PlanOnly: true},
			{
				Config: testAccServerBandwidthConfig(t, name, 20),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("network_bandwidth_mbps"), knownvalue.Int64Exact(20)),
					// SRV-5: read back from the interface, not echoed from the plan.
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("nics").AtSliceIndex(0).AtMapKey("bandwidth_mbps"), knownvalue.Int64Exact(20)),
				},
			},
		},
	})
}

// TestAccVmwareServer_dataSourceByName covers the way a person actually looks a
// machine up: by the name on the screen, not by an id they would have to go and
// find first.
func TestAccVmwareServer_dataSourceByName(t *testing.T) {
	name := testName("byname")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccServerByNameConfig(t, name),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.vcp_vmware_server.by_name",
						tfjsonpath.New("name"), knownvalue.StringExact(name)),
					statecheck.ExpectKnownValue("data.vcp_vmware_server.by_name",
						tfjsonpath.New("id"), knownvalue.NotNull()),
				},
			},
		},
	})
}

// TestAccVmwareServerDataSource_needsExactlyOneKey covers the two ways of asking
// that are not a way of asking.
func TestAccVmwareServerDataSource_needsExactlyOneKey(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmware(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      `data "vcp_vmware_server" "test" {}`,
				ExpectError: regexp.MustCompile(`(?s)Missing Attribute`),
			},
			{
				Config: `
data "vcp_vmware_server" "test" {
  id   = 1
  name = "whatever"
}
`,
				ExpectError: regexp.MustCompile(`(?s)Conflicting Attributes`),
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

func testAccServerBandwidthConfig(t *testing.T, name string, bandwidth int) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name,
		fmt.Sprintf("  network_bandwidth_mbps = %d\n", bandwidth))
}

func testAccServerByNameConfig(t *testing.T, name string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, "") + `
data "vcp_vmware_server" "by_name" {
  name = vcp_vmware_server.test.name
}
`
}
