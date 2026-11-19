package vstack_server_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

func testAccServerCloneConfig_source(sourceName, locationID, imageID string) string {
	return fmt.Sprintf(`
resource "vcp_server" "source" {
  name        = %q
  location_id = %q
  image_id    = %q
  cpu         = 1
  ram_mb      = 2048

  volumes = [
    {
      number  = 0
      name    = "boot"
      size_mb = 30720
    },
    {
      number  = 1
      name    = "data"
      size_mb = 10240
    },
  ]
}
`, sourceName, locationID, imageID)
}

// testAccServerCloneConfig_clone declares the source and a clone of it; extra
// goes into the clone's body as is.
func testAccServerCloneConfig_clone(sourceName, cloneName, locationID, imageID, extra string) string {
	return testAccServerCloneConfig_source(sourceName, locationID, imageID) + fmt.Sprintf(`
resource "vcp_server" "clone" {
  name                 = %q
  clone_from_server_id = vcp_server.source.id
%s
}
`, cloneName, extra)
}

// TestAccServerClone_lifecycle walks the life of a clone: created from a
// stopped source with nothing but a name, it carries the source's size and
// volumes (asked of the API, not only of state), plans empty straight after —
// a perpetual diff on the Optional + Computed arguments it never declared would
// show here — is resized in place like any server, and imports with
// clone_from_server_id null. Terraform destroys it before its source, which the
// platform refuses to delete while a clone lives.
func TestAccServerClone_lifecycle(t *testing.T) {
	sourceName := "test-acc-clone-src-" + acctest.RandomString(6)
	cloneName := "test-acc-clone-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_CLONE_LOCATION_ID")
	imageID := os.Getenv("VCP_CLONE_IMAGE_ID")
	clone := "vcp_server.clone"

	cloneConfig := testAccServerCloneConfig_clone(sourceName, cloneName, locationID, imageID, "")
	resized := testAccServerCloneConfig_clone(sourceName, cloneName, locationID, imageID, `
  cpu    = 2
  ram_mb = 4096
`)
	cloneIDSame := statecheck.CompareValue(compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServerClone(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			{Config: testAccServerCloneConfig_source(sourceName, locationID, imageID)},
			{
				// The platform clones only a stopped server.
				PreConfig: func() { powerOffServerOutOfBand(t, sourceName) },
				Config:    cloneConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					checkServerExists{resourceAddress: clone},
					statecheck.CompareValuePairs(clone, tfjsonpath.New("clone_from_server_id"),
						"vcp_server.source", tfjsonpath.New("id"), compare.ValuesSame()),
					statecheck.CompareValuePairs(clone, tfjsonpath.New("location_id"),
						"vcp_server.source", tfjsonpath.New("location_id"), compare.ValuesSame()),
					statecheck.ExpectKnownValue(clone, tfjsonpath.New("cpu"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(clone, tfjsonpath.New("ram_mb"), knownvalue.Int64Exact(2048)),
					statecheck.ExpectKnownValue(clone, tfjsonpath.New("volumes"), knownvalue.ListSizeExact(2)),
					checkServerVolumeCount{resourceAddress: clone, expectedCount: 2},
					checkServerVolumeByNumber{resourceAddress: clone, volumeNumber: 0, expectedName: "boot", expectedSizeMB: 30720},
					checkServerVolumeByNumber{resourceAddress: clone, volumeNumber: 1, expectedName: "data", expectedSizeMB: 10240},
					cloneIDSame.AddStateValue(clone, tfjsonpath.New("id")),
				},
			},
			{
				Config: cloneConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: resized,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(clone, plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(clone, tfjsonpath.New("cpu"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(clone, tfjsonpath.New("ram_mb"), knownvalue.Int64Exact(4096)),
					cloneIDSame.AddStateValue(clone, tfjsonpath.New("id")),
				},
			},
			{
				Config:            resized,
				ResourceName:      clone,
				ImportState:       true,
				ImportStateVerify: true,
				// volumes: ImportStateVerify compares lists positionally, see
				// TestAccServer_import; clone_from_server_id is never reported by
				// the API and is checked to be null below.
				ImportStateVerifyIgnore: []string{"password", "login", "volumes", "clone_from_server_id"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("expected exactly 1 imported state, got %d", len(states))
					}
					if v := states[0].Attributes["clone_from_server_id"]; v != "" {
						return fmt.Errorf("imported clone_from_server_id = %q, want null", v)
					}
					return nil
				},
			},
		},
	})
}

// TestAccServerClone_refusesOrderArguments: an order argument beside
// clone_from_server_id fails the plan, before anything is sent — otherwise the
// platform would clone the source and silently drop the argument. The plan
// needs no clone-capable stand: nothing is created.
func TestAccServerClone_refusesOrderArguments(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "vcp_server" "clone" {
  name                 = "test-acc-clone-refused"
  clone_from_server_id = "not-created"
  cpu                  = 2
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Argument Not Accepted By A Clone`),
			},
		},
	})
}

// powerOffServerOutOfBand stops the server with the given name through the API
// and waits for the stop to finish.
func powerOffServerOutOfBand(t *testing.T, name string) {
	client := acctest.GetTestClient()
	ctx := context.Background()

	servers, err := client.GetServerList(ctx)
	if err != nil {
		t.Fatalf("out-of-band server list failed: %v", err)
	}
	for _, server := range servers {
		if server.Name == name {
			if _, err := client.PowerOffServerAndWait(ctx, server.ID); err != nil {
				t.Fatalf("out-of-band power off of %s failed: %v", name, err)
			}
			return
		}
	}
	t.Fatalf("server %s not found to power off", name)
}
