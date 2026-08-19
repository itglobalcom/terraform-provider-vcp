package acctest

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestMain enables the sweeper mechanism: `go test ./internal/acctest -sweep=all`
// (or `make sweep`) runs the sweepers registered below. Without TestMain
// the -sweep flag is parsed but the sweepers are not executed.
func TestMain(m *testing.M) {
	resource.TestMain(m)
}

func init() {
	// Sweep servers first — networks/keys/groups can be attached to servers,
	// so the other sweepers have Dependencies: ["vcp_server"] set, and the
	// framework will run that dependency first.
	resource.AddTestSweepers("vcp_server", &resource.Sweeper{
		Name: "vcp_server",
		F:    SweepServers,
	})

	resource.AddTestSweepers("vcp_gateway", &resource.Sweeper{
		Name: "vcp_gateway",
		F:    SweepGateways,
	})

	// A network cannot be deleted while servers or a gateway are attached to it — sweep those first.
	resource.AddTestSweepers("vcp_network", &resource.Sweeper{
		Name:         "vcp_network",
		Dependencies: []string{"vcp_server", "vcp_gateway"},
		F:            SweepNetworks,
	})

	resource.AddTestSweepers("vcp_ssh_key", &resource.Sweeper{
		Name:         "vcp_ssh_key",
		Dependencies: []string{"vcp_server"},
		F:            SweepSSHKeys,
	})

	resource.AddTestSweepers("vcp_affinity_group", &resource.Sweeper{
		Name:         "vcp_affinity_group",
		Dependencies: []string{"vcp_server"},
		F:            SweepAffinityGroups,
	})

	// VMware is a separate service with its own objects: its servers and networks
	// are not in the vStack lists, so they need sweepers of their own.
	resource.AddTestSweepers("vcp_vmware_server", &resource.Sweeper{
		Name: "vcp_vmware_server",
		F:    SweepVmwareServers,
	})

	// A VMware network cannot be deleted while an interface is on it (-19511).
	resource.AddTestSweepers("vcp_vmware_network", &resource.Sweeper{
		Name:         "vcp_vmware_network",
		Dependencies: []string{"vcp_vmware_server"},
		F:            SweepVmwareNetworks,
	})

	// DNS zones are independent; deleting a zone cascades to remove its records.
	resource.AddTestSweepers("vcp_dns_domain", &resource.Sweeper{
		Name: "vcp_dns_domain",
		F:    SweepDomains,
	})
}
