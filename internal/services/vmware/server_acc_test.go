package vmware_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// serverImportIgnores lists the attributes an imported server cannot know. They
// are write-only order options: the API takes them when the machine is created
// and never reports them back, so a user who imports a server has to restate
// them in the configuration.
var serverImportIgnores = []string{
	"backup_enabled",
	"backup_period",
	"need_sysprep",
	"ssh_key_ids",
	"public_network_id",
	"network_bandwidth_mbps",
	// SRV-3: the backend upper-cases the hostname. Without a configuration to
	// compare against, an imported server holds the platform's spelling, which is
	// not the one the original state was created with.
	"computer_name",
}

// TestAccVmwareServer_basic is the ordinary path: order a machine, see what the
// platform made of it, prove the configuration has settled, import it, destroy
// it.
//
// The `nics` check is the one that documents the platform's rule — a VMware
// server is never created without a public interface, which is why the primary
// one is an attribute of the server rather than a resource of its own.
func TestAccVmwareServer_basic(t *testing.T) {
	resourceName := "vcp_vmware_server.test"
	name := testName("srv")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccServerBasicConfig(t, name, "web01"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("name"), knownvalue.StringExact(name)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("cpu"), knownvalue.Int64Exact(1)),
					// Reported, not asserted to a particular value: whether a freshly
					// ordered machine comes up running is the platform's business,
					// and pinning it here would make the test fail for something the
					// provider does not control.
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("state"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("is_power_on"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("created"), knownvalue.NotNull()),
					// Always at least the primary public interface.
					acctest.CheckListNotEmpty(resourceName, "nics"),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("nics").AtSliceIndex(0).AtMapKey("is_primary"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("nics").AtSliceIndex(0).AtMapKey("ip"), knownvalue.NotNull()),
					// SRV-5: the interface reports its bandwidth.
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("nics").AtSliceIndex(0).AtMapKey("bandwidth_mbps"), knownvalue.NotNull()),
				},
				Check: checkServerNICCount(resourceName, 1),
			},
			// Nothing changed — and in particular the uppercase hostname the
			// backend stores must not show up as a change.
			{Config: testAccServerBasicConfig(t, name, "web01"), PlanOnly: true},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: serverImportIgnores,
			},
		},
	})
}

// TestAccVmwareServer_computerNameIsCaseInsensitive pins SRV-3 on its own: the
// backend stores the hostname upper-cased, so a configuration written in lower
// case would diff against state forever. Writing the same name in a different
// case must be a no-op, and only a real change must reach the API.
func TestAccVmwareServer_computerNameIsCaseInsensitive(t *testing.T) {
	resourceName := "vcp_vmware_server.test"
	name := testName("hostname")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{Config: testAccServerBasicConfig(t, name, "srv01")},
			// Same hostname, different case: no plan at all.
			{Config: testAccServerBasicConfig(t, name, "SRV01"), PlanOnly: true},
			{Config: testAccServerBasicConfig(t, name, "Srv01"), PlanOnly: true},
			// A genuinely different hostname is an in-place change.
			{
				Config: testAccServerBasicConfig(t, name, "srv02"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
		},
	})
}

// TestAccVmwareServer_updateInPlace covers resize and rename. Both are separate
// API calls made from one Update, and neither may recreate the machine — a
// replacement would take the disk with it.
func TestAccVmwareServer_updateInPlace(t *testing.T) {
	resourceName := "vcp_vmware_server.test"
	name := testName("resize")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccServerBasicConfig(t, name, "res01"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("cpu"), knownvalue.Int64Exact(1)),
				},
			},
			// More CPU, more RAM, a bigger disk and a new display name, all at
			// once — and the same machine at the end of it.
			{
				Config: testAccServerResizedConfig(t, name+"-renamed", "res01"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("cpu"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("name"),
						knownvalue.StringExact(name+"-renamed")),
				},
			},
			{Config: testAccServerResizedConfig(t, name+"-renamed", "res01"), PlanOnly: true},
		},
	})
}

// TestAccVmwareServer_imageForcesReplacement covers the change a user must be
// warned about: the OS image cannot be swapped on a running machine, so asking
// for a different one destroys the server and its data with it.
func TestAccVmwareServer_imageForcesReplacement(t *testing.T) {
	resourceName := "vcp_vmware_server.test"
	name := testName("image")

	var firstServerID, secondServerID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccServerBasicConfig(t, name, "img01"),
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "id", &firstServerID),
				},
			},
			// The plan says the machine is destroyed and recreated, and the apply
			// proves it: the server that comes out carries a different id, so the
			// original one — and its disk — is really gone.
			{
				Config: testAccServerOtherImageConfig(t, name, "img01"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "id", &secondServerID),
				},
				Check: func(*terraform.State) error {
					if firstServerID == secondServerID {
						return fmt.Errorf("the server kept id %s across an image change; it should have been replaced",
							firstServerID)
					}
					return nil
				},
			},
		},
	})
}

// TestAccVmwareServer_disappears covers a server deleted from the panel: the
// refresh has to drop it from state instead of failing, so the next plan offers
// to order it again.
func TestAccVmwareServer_disappears(t *testing.T) {
	resourceName := "vcp_vmware_server.test"
	name := testName("srvgone")
	config := testAccServerBasicConfig(t, name, "gone01")

	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					captureAttr(resourceName, "id", &serverID),
				},
			},
			{
				PreConfig:          func() { deleteServerOutOfBand(t, serverID) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccVmwareServer_nestedHypervisor covers the life of one setting: ordered
// with it on, switched off in place on the machine that is already there,
// imported, and changed in the panel behind Terraform's back.
//
// The step that matters most is the second one. `nested_hypervisor` is
// Optional+Computed, and an Optional+Computed attribute without
// UseStateForUnknown plans as "known after apply" on every single run — an
// endless diff on a machine nobody touched. On this attribute a diff is not
// cosmetic: applying it power-cycles the guest.
//
// The third step is the other half: the platform edits the setting on the machine
// it is already on, so a plan that replaced the server would destroy the disk to
// change a checkbox. The id is asserted to be the same one afterwards, because a
// plan check alone would not notice a replacement the provider carried out for
// some other reason.
func TestAccVmwareServer_nestedHypervisor(t *testing.T) {
	resourceName := "vcp_vmware_server.test"
	name := testName("nested")

	var serverID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			// Ordered with the guest allowed a hypervisor of its own.
			{
				Config: testAccServerNestedHypervisorConfig(t, name, true),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("nested_hypervisor"), knownvalue.Bool(true)),
					captureAttr(resourceName, "id", &serverID),
				},
				// And the machine itself says so, not only the state file.
				Check: checkServerNestedHypervisor(resourceName, true),
			},
			// Nothing changed. Without this step the perpetual diff is invisible.
			{Config: testAccServerNestedHypervisorConfig(t, name, true), PlanOnly: true},
			// Switched off: an edit of the same machine, never a replacement.
			{
				PreConfig: waitForBackend,
				Config:    testAccServerNestedHypervisorConfig(t, name, false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName,
						tfjsonpath.New("nested_hypervisor"), knownvalue.Bool(false)),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					// The same machine: the id it was created with.
					resource.TestCheckResourceAttrPtr(resourceName, "id", &serverID),
					checkServerNestedHypervisor(resourceName, false),
				),
			},
			// Settled again after the edit.
			{Config: testAccServerNestedHypervisorConfig(t, name, false), PlanOnly: true},
			// The API reports the setting, so an imported machine knows it — which is
			// why it is deliberately absent from serverImportIgnores.
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: serverImportIgnores,
			},
			// Switched on in the panel: the refresh has to see it and offer to put it
			// back, rather than echoing the configuration back at itself.
			{
				PreConfig: func() {
					waitForBackend()
					setNestedHypervisorOutOfBand(t, serverID, true)
				},
				Config:             testAccServerNestedHypervisorConfig(t, name, false),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccVmwareServer_dataSources covers the read-only side a user reaches for
// before writing any resource: what locations exist, which images they carry,
// what GPU profiles are on offer — and, once a machine exists, finding it again
// by id and in the list.
//
// The single-server data source carries one field the list cannot (SRV-4:
// vm_tools_installed is populated only by the by-id read), which is why the two
// are separate schemas and why both are checked here.
func TestAccVmwareServer_dataSources(t *testing.T) {
	name := testName("ds")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckVmwareServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckVmwareServersDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccServerDataSourcesConfig(t, name),
				ConfigStateChecks: []statecheck.StateCheck{
					// Catalogs. API-11 moved disk types inside the location, and that
					// is asserted by the configuration rather than here: every server
					// in this suite is sized from `local.disk_type`, which is a
					// one([...]) over the location's disk_types — a location that
					// carried none would fail at plan time.
					acctest.CheckListNotEmpty("data.vcp_vmware_locations.all", "locations"),
					acctest.CheckListNotEmpty("data.vcp_vmware_images.all", "images"),
					// The GPU filter is a documented pair of values; asking for the
					// images that cannot use a GPU must not error, whatever the
					// catalog holds.
					statecheck.ExpectKnownValue("data.vcp_vmware_images.non_gpu",
						tfjsonpath.New("images"), knownvalue.NotNull()),
					// Inventory of the machine just created.
					statecheck.ExpectKnownValue("data.vcp_vmware_server.by_id",
						tfjsonpath.New("name"), knownvalue.StringExact(name)),
					acctest.CheckListNotEmpty("data.vcp_vmware_servers.all", "servers"),
					acctest.CheckListNotEmpty("data.vcp_vmware_networks.all", "networks"),
				},
			},
		},
	})
}

// ============================================================================
// Configurations
// ============================================================================

func testAccServerBasicConfig(t *testing.T, name, computerName string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name,
		fmt.Sprintf("  computer_name    = %q\n", computerName))
}

// testAccServerResizedConfig doubles the CPU and RAM and grows the disk by one
// step of the disk type — a size the platform is guaranteed to accept, unlike a
// round number of our choosing.
func testAccServerResizedConfig(t *testing.T, name, computerName string) string {
	t.Helper()
	return catalogConfig(t) + fmt.Sprintf(`
resource "vcp_vmware_server" "test" {
  location_id      = %[1]s
  name             = %[2]q
  computer_name    = %[3]q
  image_id         = %[4]s
  cpu              = 2
  ram_mb           = local.ram_mb * 2
  system_disk_mb   = local.system_disk_mb + local.disk_type.step_mb
  system_disk_type = local.disk_type.title
}
`, vmwareLocationID(t), name, computerName, vmwareImageID(t))
}

// testAccServerOtherImageConfig points the server at a different image from the
// catalog — whichever one is not the configured default.
func testAccServerOtherImageConfig(t *testing.T, name, computerName string) string {
	t.Helper()
	return catalogConfig(t) + fmt.Sprintf(`
locals {
  other_image = [for i in data.vcp_vmware_images.all.images : i if i.id != %[4]s && !i.is_gpu_only][0]
}

resource "vcp_vmware_server" "test" {
  location_id      = %[1]s
  name             = %[2]q
  computer_name    = %[3]q
  image_id         = local.other_image.id
  cpu              = 1
  ram_mb           = max(1024, local.other_image.min_ram_mb)
  system_disk_mb   = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.other_image.hdd_gb * 1024)
  system_disk_type = local.disk_type.title
}
`, vmwareLocationID(t), name, computerName, vmwareImageID(t))
}

// testAccServerNestedHypervisorConfig orders the machine with nested
// virtualization in the requested state. The two states are one configuration
// with a single value changed: switching the attribute is an in-place edit, and a
// configuration that differed in anything else would let a replacement pass for
// one.
func testAccServerNestedHypervisorConfig(t *testing.T, name string, enabled bool) string {
	t.Helper()
	return catalogConfig(t) + fmt.Sprintf(`
resource "vcp_vmware_server" "test" {
  location_id       = %[1]s
  name              = %[2]q
  image_id          = %[3]s
  cpu               = 1
  ram_mb            = local.ram_mb
  system_disk_mb    = local.system_disk_mb
  system_disk_type  = local.disk_type.title
  nested_hypervisor = %[4]t
}
`, vmwareLocationID(t), name, vmwareImageID(t), enabled)
}

func testAccServerDataSourcesConfig(t *testing.T, name string) string {
	t.Helper()
	return catalogConfig(t) + serverConfig(t, "test", name, "") + fmt.Sprintf(`
data "vcp_vmware_images" "non_gpu" {
  location_id = %[1]s
  gpu         = "unsupported"
}

data "vcp_vmware_gpu_models" "all" {
  location_id = %[1]s
}

data "vcp_vmware_server" "by_id" {
  id = vcp_vmware_server.test.id
}

data "vcp_vmware_servers" "all" {
  location_id = %[1]s
}

data "vcp_vmware_networks" "all" {
  location_id = %[1]s
}
`, vmwareLocationID(t))
}
