// Package vmware_test holds the acceptance tests of the VMware Cloud resources.
// They drive the provider the way a user does — through real configurations
// applied against a live API — and they are skipped unless TF_ACC is set.
//
// Environment: VCP_API_URL, VCP_API_TOKEN, VCP_VMWARE_LOCATION_ID and (for the
// tests that order a machine) VCP_VMWARE_IMAGE_ID. See .env.example.
//
// Every resource is named "test-acc-…" so `make sweep` can clean up after a run
// that failed half-way.
package vmware_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// Environment
// ============================================================================

func vmwareLocationID(t *testing.T) string {
	t.Helper()
	return os.Getenv("VCP_VMWARE_LOCATION_ID")
}

func vmwareImageID(t *testing.T) string {
	t.Helper()
	return os.Getenv("VCP_VMWARE_IMAGE_ID")
}

// testName builds a sweepable name with a random suffix, so two runs against the
// same account never collide.
func testName(kind string) string {
	return "test-acc-vmw-" + kind + "-" + acctest.RandomString(6)
}

// ============================================================================
// Configuration fragments
// ============================================================================

// catalogConfig sizes a server the way the documentation tells a user to: read
// the catalog, take a disk type the location actually offers, and respect the
// image's own minimums. Hard-coding a size would make the suite fail on any
// stand whose catalog differs, and it would leave the three metadata data
// sources without a single acceptance test.
func catalogConfig(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = %[1]s
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == %[1]s])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == %[2]s])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}
`, vmwareLocationID(t), vmwareImageID(t))
}

// serverConfig declares one server sized from the catalog. Extra attributes are
// appended verbatim so a test can add what it is about without a builder per
// case.
func serverConfig(t *testing.T, resourceName, serverName, extra string) string {
	t.Helper()
	return fmt.Sprintf(`
resource "vcp_vmware_server" %[1]q {
  location_id      = %[2]s
  name             = %[3]q
  image_id         = %[4]s
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
%[5]s}
`, resourceName, vmwareLocationID(t), serverName, vmwareImageID(t), extra)
}

// networkConfig declares one network. address is ignored for a public network,
// which takes capacity instead.
func networkConfig(t *testing.T, resourceName, netName, netType, body string) string {
	t.Helper()
	return fmt.Sprintf(`
resource "vcp_vmware_network" %[1]q {
  type        = %[2]q
  location_id = %[3]s
  name        = %[4]q
%[5]s}
`, resourceName, netType, vmwareLocationID(t), netName, body)
}

// routedNetworkConfig is the network the edge tests need: only a routed network
// carries an edge of its own.
func routedNetworkConfig(t *testing.T, name, address string) string {
	t.Helper()
	return networkConfig(t, "test", name, "routed", fmt.Sprintf(`  address        = %q
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = %d
`, address, testAccNetworkBandwidth))
}

// testAccNetworkBandwidth is the bandwidth the routed/public test networks ask
// for. It is also the edge's bandwidth — the two are one field (NET-8).
const testAccNetworkBandwidth = 20

// ============================================================================
// State helpers
// ============================================================================

// captureAttr stores a string attribute of a resource for use in a later step:
// PreConfig runs before the state of the step is available, so anything an
// out-of-band call needs has to be picked up in the step before it.
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
	value := res.AttributeValues[c.attribute]
	switch v := value.(type) {
	case string:
		if v == "" {
			resp.Error = fmt.Errorf("attribute %q of %s is empty", c.attribute, c.address)
			return
		}
		*c.dst = v
	case json.Number: // the test framework decodes the state with UseJSONNumber
		*c.dst = v.String()
	case float64: // a plain decoder would give float64 instead
		*c.dst = strconv.FormatInt(int64(v), 10)
	default:
		resp.Error = fmt.Errorf("attribute %q of %s is neither a string nor a number: %v", c.attribute, c.address, value)
	}
}

// intAttr reads a numeric attribute out of the Terraform state.
func intAttr(s *terraform.State, resourceName, attribute string) (int, error) {
	rs, ok := s.RootModule().Resources[resourceName]
	if !ok {
		return 0, fmt.Errorf("resource not found: %s", resourceName)
	}
	raw, ok := rs.Primary.Attributes[attribute]
	if !ok || raw == "" {
		return 0, fmt.Errorf("attribute %q is not set on %s", attribute, resourceName)
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("attribute %q of %s is not numeric (%q): %w", attribute, resourceName, raw, err)
	}
	return value, nil
}

// ============================================================================
// Out-of-band API access
// ============================================================================
//
// The provider owns whole rule sets, so the interesting failure mode is a change
// made outside Terraform. These helpers make those changes with the same SDK the
// provider uses, which is as close to "somebody edited it in the panel" as a
// test can get.

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	v, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("expected a numeric id, got %q: %v", s, err)
	}
	return v
}

// setEdgeFirewallOutOfBand replaces the edge firewall rule set behind
// Terraform's back.
func setEdgeFirewallOutOfBand(t *testing.T, networkID string, rules []entities.VmwareUpdateEdgeFirewallRule) {
	t.Helper()
	enabled := true
	action := entities.VmwareEdgeFirewallActionDeny
	_, err := acctest.GetTestClient().UpdateVmwareEdgeFirewallAndWait(context.Background(), mustAtoi(t, networkID),
		&entities.VmwareUpdateEdgeFirewallRequest{Enabled: &enabled, DefaultAction: &action, Rules: rules})
	if err != nil {
		t.Fatalf("out-of-band edge firewall update failed: %v", err)
	}
}

// addEdgeNATRuleOutOfBand creates one NAT rule behind Terraform's back.
func addEdgeNATRuleOutOfBand(t *testing.T, networkID string, req *entities.VmwareUpsertNATRuleRequest) {
	t.Helper()
	if _, err := acctest.GetTestClient().UpsertVmwareEdgeNATRuleAndWait(context.Background(),
		mustAtoi(t, networkID), req); err != nil {
		t.Fatalf("out-of-band NAT rule creation failed: %v", err)
	}
}

// setServerFirewallOutOfBand replaces a server's firewall rule set behind
// Terraform's back.
func setServerFirewallOutOfBand(t *testing.T, serverID string, rules []entities.VmwareServerFirewallRule) {
	t.Helper()
	_, err := acctest.GetTestClient().UpdateVmwareServerFirewallAndWait(context.Background(), mustAtoi(t, serverID),
		&entities.VmwareUpdateServerFirewallRequest{Rules: rules})
	if err != nil {
		t.Fatalf("out-of-band server firewall update failed: %v", err)
	}
}

// setNestedHypervisorOutOfBand switches nested virtualization on a server behind
// Terraform's back and waits for the saga.
func setNestedHypervisorOutOfBand(t *testing.T, serverID string, enabled bool) {
	t.Helper()
	var err error
	if enabled {
		_, err = acctest.GetTestClient().EnableVmwareServerNestedHypervisorAndWait(
			context.Background(), mustAtoi(t, serverID))
	} else {
		_, err = acctest.GetTestClient().DisableVmwareServerNestedHypervisorAndWait(
			context.Background(), mustAtoi(t, serverID))
	}
	if err != nil {
		t.Fatalf("out-of-band nested hypervisor switch failed: %v", err)
	}
}

// deleteNetworkOutOfBand removes a network directly through the API and waits
// until it is really gone — the delete task completes before the object does.
func deleteNetworkOutOfBand(t *testing.T, networkID string) {
	t.Helper()
	if err := acctest.GetTestClient().DeleteVmwareNetworkAndWait(context.Background(),
		mustAtoi(t, networkID)); err != nil && !sdk.IsNotFound(err) {
		t.Fatalf("out-of-band network delete failed: %v", err)
	}
}

// deleteServerOutOfBand removes a server directly through the API and waits for
// it to disappear.
func deleteServerOutOfBand(t *testing.T, serverID string) {
	t.Helper()
	if err := acctest.GetTestClient().DeleteVmwareServerAndWait(context.Background(),
		mustAtoi(t, serverID)); err != nil && !sdk.IsNotFound(err) {
		t.Fatalf("out-of-band server delete failed: %v", err)
	}
}

// deleteNICOutOfBand detaches an interface directly through the API.
func deleteNICOutOfBand(t *testing.T, serverID, nicID string) {
	t.Helper()
	if err := acctest.GetTestClient().DeleteVmwareNICAndWait(context.Background(),
		mustAtoi(t, serverID), mustAtoi(t, nicID)); err != nil && !sdk.IsNotFound(err) {
		t.Fatalf("out-of-band NIC delete failed: %v", err)
	}
}

// ============================================================================
// API-side assertions
// ============================================================================
//
// State says what the provider recorded; these say what the platform actually
// holds. A resource that owns a whole rule set has to be checked both ways —
// state alone would pass even if the provider never wrote anything.

// checkNATRuleCount asserts, straight from the API, how many NAT rules an edge
// has. Takes a pointer because the network id is captured in an earlier step.
func checkNATRuleCount(networkID *string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		nat, err := acctest.GetTestClient().GetVmwareEdgeNAT(context.Background(), mustAtoiErr(*networkID))
		if err != nil {
			return fmt.Errorf("reading edge NAT rules of network %s: %w", *networkID, err)
		}
		if len(nat.Rules) != want {
			return fmt.Errorf("network %s has %d NAT rule(s), want %d", *networkID, len(nat.Rules), want)
		}
		return nil
	}
}

// checkEdgeFirewallRuleCount asserts the edge firewall rule count from the API.
//
// The network id comes from the state of the step being checked, not from a
// variable a ConfigStateChecks entry fills in: those run after Check, so on the
// first step such a variable is still empty and on later ones it holds the
// previous step's value.
func checkEdgeFirewallRuleCount(networkResource string, want int) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		networkID, err := intAttr(state, networkResource, "id")
		if err != nil {
			return err
		}
		firewall, err := acctest.GetTestClient().GetVmwareEdgeFirewall(context.Background(), networkID)
		if err != nil {
			return fmt.Errorf("reading edge firewall of network %d: %w", networkID, err)
		}
		if len(firewall.Rules) != want {
			return fmt.Errorf("network %d has %d firewall rule(s), want %d", networkID, len(firewall.Rules), want)
		}
		return nil
	}
}

// checkEdgeFirewallEnabled asserts whether the edge firewall is switched on.
// Destroying the resource clears its rules and leaves the firewall on: an edge
// backed by NSX-T refuses to be switched off at all.
func checkEdgeFirewallEnabled(networkResource string, want bool) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		networkID, err := intAttr(state, networkResource, "id")
		if err != nil {
			return err
		}
		firewall, err := acctest.GetTestClient().GetVmwareEdgeFirewall(context.Background(), networkID)
		if err != nil {
			return fmt.Errorf("reading edge firewall of network %d: %w", networkID, err)
		}
		got := firewall.Enabled != nil && *firewall.Enabled
		if got != want {
			return fmt.Errorf("edge firewall of network %d is enabled=%v, want %v", networkID, got, want)
		}
		return nil
	}
}

// checkServerFirewallRuleCount asserts the server firewall rule count from the API.
func checkServerFirewallRuleCount(serverID *string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		rules, err := acctest.GetTestClient().GetVmwareServerFirewall(context.Background(), mustAtoiErr(*serverID))
		if err != nil {
			return fmt.Errorf("reading firewall rules of server %s: %w", *serverID, err)
		}
		if len(rules) != want {
			return fmt.Errorf("server %s has %d firewall rule(s), want %d", *serverID, len(rules), want)
		}
		return nil
	}
}

// checkServerNestedHypervisor asserts, straight from the API, what the machine
// itself reports — not what the resource recorded in state.
func checkServerNestedHypervisor(resourceName string, want bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		serverID, err := intAttr(s, resourceName, "id")
		if err != nil {
			return err
		}
		server, err := acctest.GetTestClient().GetVmwareServer(context.Background(), serverID)
		if err != nil {
			return fmt.Errorf("reading server %d: %w", serverID, err)
		}
		if server.NestedHypervisor != want {
			return fmt.Errorf("server %d reports nested_hypervisor=%v, want %v",
				serverID, server.NestedHypervisor, want)
		}
		return nil
	}
}

// checkServerNICCount asserts how many interfaces a server has, which is how the
// attachment and public-interface resources are verified against the platform
// rather than against their own state.
func checkServerNICCount(resourceName string, want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		serverID, err := intAttr(s, resourceName, "id")
		if err != nil {
			return err
		}
		nics, err := acctest.GetTestClient().GetVmwareServerNICs(context.Background(), serverID)
		if err != nil {
			return fmt.Errorf("reading interfaces of server %d: %w", serverID, err)
		}
		if len(nics) != want {
			return fmt.Errorf("server %d has %d interface(s), want %d", serverID, len(nics), want)
		}
		return nil
	}
}

// mustAtoiErr converts a captured id, returning 0 for anything unparseable —
// the caller then fails on the API error, which names the id.
func mustAtoiErr(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}

// waitForBackend pauses between steps that hit the same object. The API
// serializes changes per object and answers -4000 to a second one; the SDK
// retries, but a short pause keeps a chain of applies out of that path
// altogether.
func waitForBackend() { time.Sleep(5 * time.Second) }

// regexpAny matches any diagnostic. Used where the test asserts *that* a
// configuration is refused, without pinning the wording — the message is
// expected to improve (a provider-side check replacing a generic API error), and
// a test that fails when it does would be a test against progress.
func regexpAny() *regexp.Regexp { return regexp.MustCompile(`(?s).`) }
