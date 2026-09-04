package vmware_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// The acceptance configurations are assembled from strings, and a stray brace or
// a fragment glued on in the wrong place is only found when a live run reaches
// that step — after minutes of provisioning, and only for whoever has the
// credentials to run it.
//
// These tests parse every configuration the suite can produce, which costs
// nothing and runs in the ordinary `go test`. They check the grammar, not the
// meaning: whether a resource takes an attribute is the schema's business, and
// whether the platform accepts the value is the acceptance run's.

// configFixture is one configuration the suite applies, with a note saying what
// it is for. The note is what makes the generated dump readable — see
// TestDumpAcceptanceConfigs.
type configFixture struct {
	Label  string
	Note   string
	Config string
}

// configFixtures returns every configuration the acceptance tests build, in
// reading order. Placeholders stand in for the environment, so the fixtures are
// the same shape a real run assembles.
func configFixtures(t *testing.T) []configFixture {
	t.Helper()

	// The builders read the environment; a run without credentials would splice
	// empty ids into the HCL and fail for the wrong reason.
	t.Setenv("VCP_VMWARE_LOCATION_ID", "5")
	t.Setenv("VCP_VMWARE_IMAGE_ID", "42")

	const (
		name    = "test-acc-vmw-syntax"
		address = "10.240.1.0"
	)

	return []configFixture{
		// ---------- Fragments every other configuration is built on ----------
		{"fragment/catalog", "Reads the catalog and derives the sizing every server configuration below uses. " +
			"A location that offered no system disk type would fail here, at plan time.",
			catalogConfig(t)},
		{"fragment/server", "The catalog fragment plus one server sized from it.",
			catalogConfig(t) + serverConfig(t, "test", name, "  computer_name = \"host01\"\n")},
		{"fragment/routedNetwork", "The network the edge tests need — only a routed network carries an edge.",
			routedNetworkConfig(t, name, address)},

		// ---------- Networks ----------
		{"network/isolated", "TestAccVmwareNetwork_isolated, _forcesReplacement, _disappears.",
			testAccNetworkIsolatedConfig(t, name, address)},
		{"network/withDataSource", "TestAccVmwareNetwork_isolated — the same network read back through the by-id data source.",
			testAccNetworkWithDataSourceConfig(t, name, address)},
		{"network/routedBandwidth", "TestAccVmwareNetwork_routed — the bandwidth raised in place (NET-8: this is also the edge's bandwidth).",
			testAccNetworkRoutedBandwidthConfig(t, name, address, 30)},
		{"network/public", "TestAccVmwareNetwork_public — ordered by capacity rather than by address range.",
			testAccNetworkPublicConfig(t, name, "1")},

		// ---------- Servers ----------
		{"server/basic", "TestAccVmwareServer_basic, _computerNameIsCaseInsensitive, _updateInPlace, _disappears.",
			testAccServerBasicConfig(t, name, "host01")},
		{"server/resized", "TestAccVmwareServer_updateInPlace — twice the CPU and RAM, one disk step bigger, renamed.",
			testAccServerResizedConfig(t, name, "host01")},
		{"server/otherImage", "TestAccVmwareServer_imageForcesReplacement — a different image from the catalog.",
			testAccServerOtherImageConfig(t, name, "host01")},
		{"server/dataSources", "TestAccVmwareServer_dataSources — every VMware data source at once.",
			testAccServerDataSourcesConfig(t, name)},
		{"server/byName", "TestAccVmwareServer_dataSourceByName — looking a machine up by the name on the screen.",
			testAccServerByNameConfig(t, name)},
		{"server/bandwidth", "TestAccVmwareServer_bandwidthInPlace — the bandwidth of the interface the machine is born with.",
			testAccServerBandwidthConfig(t, name, 10)},

		// ---------- Disks ----------
		{"volumes/one", "TestAccVmwareServerVolumes_lifecycle — one data disk beside the boot disk.",
			testAccServerVolumesConfig(t, name, testAccVolumeData)},
		{"volumes/grown", "TestAccVmwareServerVolumes_lifecycle — the same disk renamed and grown by one step of its type.",
			testAccServerVolumesConfig(t, name, testAccVolumeDataGrown)},
		{"volumes/two", "TestAccVmwareServerVolumes_lifecycle — a second disk alongside the first.",
			testAccServerVolumesConfig(t, name, testAccVolumeDataGrown+testAccVolumeLogs)},

		// ---------- Copy ----------
		{"copy/basic", "TestAccVmwareServerCopy_lifecycle — a machine and a copy of it, which states only where it comes from and what it is called.",
			testAccServerCopyConfig(t, name)},
		{"copy/resized", "TestAccVmwareServerCopy_lifecycle — the same copy resized in place, which is what a copy is once it exists.",
			testAccServerCopyResizedConfig(t, name)},
		{"copy/withImage", "TestAccVmwareServerCopy_refusesOrderArguments — a copy and an image at once, refused at plan time.",
			testAccServerCopyWithImageConfig(t, name)},

		// ---------- Snapshot ----------
		{"snapshot/one", "TestAccVmwareServerSnapshot_lifecycle, _disappears — the single snapshot a VMware server can hold.",
			testAccServerSnapshotConfig(t, name, "before-upgrade")},
		{"snapshot/two", "TestAccVmwareServerSnapshot_secondIsRefused — a second snapshot of one machine, which the platform allows no server to hold.",
			testAccServerSnapshotPairConfig(t, name)},

		// ---------- Interfaces ----------
		{"nic/attachmentBase", "TestAccVmwareServerNetworkAttachment_basic (last step) — server and network with no attachment between them.",
			testAccAttachmentBaseConfig(t, name, address)},
		{"nic/attachmentStatic", "TestAccVmwareServerNetworkAttachment_basic, _ipForcesReplacement, _disappears.",
			testAccAttachmentConfig(t, name, address, `  ip = "10.240.1.10"`)},
		{"nic/attachmentNoIP", "The same attachment with the address left to the platform.",
			testAccAttachmentConfig(t, name, address, "")},
		{"nic/attachmentDHCP", "TestAccVmwareServerNetworkAttachment_dhcp — a network that hands out addresses.",
			testAccAttachmentDHCPConfig(t, name, address)},
		{"nic/publicInterface", "TestAccVmwareServerPublicInterface_basic — a second public address.",
			testAccPublicInterfaceConfig(t, name, 10)},
		{"nic/parallel", "TestAccVmwareServerNICs_parallel — a private attachment and a public interface on one machine in one apply (locks.VmwareServer).",
			testAccNICsParallelConfig(t, name, address)},

		// ---------- Rule sets ----------
		{"rules/edgeFirewallAllFields", "TestAccVmwareEdgeFirewall_basic — every attribute a rule has.",
			testAccEdgeFirewallConfig(t, name, "10.233.1.0", "deny", testAccEdgeFirewallRulesAllFields)},
		{"rules/edgeFirewallShort", "TestAccVmwareEdgeFirewall_drift, _adoptsExistingRules.",
			testAccEdgeFirewallConfig(t, name, address, "allow", testAccEdgeFirewallRulesShort)},
		{"rules/edgeFirewallEmpty", "TestAccVmwareEdgeFirewall_basic — an empty list: the firewall stays on and default_action becomes its whole behaviour.",
			testAccEdgeFirewallConfig(t, name, address, "deny", "")},
		{"rules/edgeNATPair", "TestAccVmwareEdgeNAT_basic — one rule of each kind. original_ip is absent on purpose (NET-4).",
			testAccEdgeNATConfig(t, name, "10.233.4.0", testAccEdgeNATRulesPair)},
		{"rules/edgeNATSingle", "TestAccVmwareEdgeNAT_growAndShrink, _drift — the starting point.",
			testAccEdgeNATConfig(t, name, address, testAccEdgeNATRuleDNAT("443", "10.240.1.10"))},
		{"rules/edgeNATThree", "TestAccVmwareEdgeNAT_growAndShrink — grown to three: the first rule is overwritten in place, two are created.",
			testAccEdgeNATConfig(t, name, address,
				testAccEdgeNATRuleDNAT("443", "10.240.1.10")+
					testAccEdgeNATRuleDNAT("8080", "10.240.1.11")+
					testAccEdgeNATRuleDNAT("8443", "10.240.1.12"))},
		{"rules/edgeNATEmpty", "An empty NAT list — every rule removed from the edge.",
			testAccEdgeNATConfig(t, name, address, "")},
		{"rules/edgeParallel", "TestAccVmwareEdgeRules_parallel — firewall and NAT of one network in one apply (locks.VmwareNetwork).",
			testAccEdgeRulesParallelConfig(t, name, "10.233.9.0")},
		{"rules/serverFirewallFull", "TestAccVmwareServerFirewall_basic — both directions, a closing deny rule.",
			testAccServerFirewallConfig(t, name, testAccServerFirewallRulesFull)},
		{"rules/serverFirewallOne", "TestAccVmwareServerFirewall_basic (update), _drift.",
			testAccServerFirewallConfig(t, name, testAccServerFirewallRulesShort)},
		{"rules/serverFirewallNone", "An empty server firewall — every rule removed, the machine left unfiltered.",
			testAccServerFirewallConfig(t, name, "")},

		// ---------- VPN ----------
		{"vpn/tunnel", "TestAccVmwareEdgeVPNTunnel_basic — a site-to-site tunnel on the edge of a routed network.",
			testAccVPNTunnelConfig(t, name, "10.234.1.0", "aes256", "dh14", 1500, true)},
		{"vpn/tunnelDisabled", "TestAccVmwareEdgeVPNTunnel_basic — the same tunnel with a smaller MTU, switched off.",
			testAccVPNTunnelConfig(t, name, "10.234.1.0", "aes256", "dh14", 1400, false)},
	}
}

func TestAccConfigsAreValidHCL(t *testing.T) {
	for _, fixture := range configFixtures(t) {
		t.Run(fixture.Label, func(t *testing.T) {
			_, diags := hclsyntax.ParseConfig([]byte(fixture.Config), fixture.Label+".tf", hcl.InitialPos)
			if diags.HasErrors() {
				t.Fatalf("%s produced invalid HCL: %v\n\n%s", fixture.Label, diags, fixture.Config)
			}
		})
	}
}

// TestAccConfigsDeclareTheirDependencies catches the mistake string concatenation
// makes easy: a fragment that references `local.…` or a resource without the
// piece that defines it being glued on in front of it.
func TestAccConfigsDeclareTheirDependencies(t *testing.T) {
	for _, fixture := range configFixtures(t) {
		// Fragments are meant to be combined, so they are exempt from the check
		// that a configuration stands on its own.
		if strings.HasPrefix(fixture.Label, "fragment/") && fixture.Label != "fragment/server" {
			continue
		}
		t.Run(fixture.Label, func(t *testing.T) {
			if strings.Contains(fixture.Config, "local.") && !strings.Contains(fixture.Config, "locals {") {
				t.Error("references a local without a locals block — the catalog fragment is missing in front of it")
			}
			for _, ref := range []string{
				"vcp_vmware_server.test.id",
				"vcp_vmware_network.test.id",
			} {
				declaration := "resource \"" + strings.TrimSuffix(ref, ".test.id") + "\" \"test\""
				if strings.Contains(fixture.Config, ref) && !strings.Contains(fixture.Config, declaration) {
					t.Errorf("references %s without declaring it", ref)
				}
			}
		})
	}
}

// TestDumpAcceptanceConfigs writes every configuration the suite applies to a
// single readable document, so the whole set can be reviewed without reading Go.
//
// It is generated rather than hand-written precisely so it cannot drift from the
// tests. Regenerate with:
//
//	VMWARE_CONFIG_DUMP=$PWD/vmware_acceptance_configs.md go test ./internal/services/vmware -run TestDumpAcceptanceConfigs
//
// The path is resolved from the package directory, so pass an absolute one.
func TestDumpAcceptanceConfigs(t *testing.T) {
	path := os.Getenv("VMWARE_CONFIG_DUMP")
	if path == "" {
		t.Skip("set VMWARE_CONFIG_DUMP=<file> to write the acceptance configurations out")
	}

	fixtures := configFixtures(t)

	var doc strings.Builder
	doc.WriteString(dumpHeader)

	doc.WriteString("## Оглавление\n\n")
	for _, fixture := range fixtures {
		fmt.Fprintf(&doc, "- [`%s`](#%s) — %s\n", fixture.Label, anchor(fixture.Label), fixture.Note)
	}
	doc.WriteString("\n---\n\n")

	for _, fixture := range fixtures {
		// Concatenating fragments leaves the alignment uneven; the reader gets the
		// layout `terraform fmt` would produce.
		formatted := hclwrite.Format([]byte(strings.TrimLeft(fixture.Config, "\n")))
		fmt.Fprintf(&doc, "## %s\n\n%s\n\n```hcl\n%s```\n\n", fixture.Label, fixture.Note, formatted)
	}

	if err := os.WriteFile(path, []byte(doc.String()), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	t.Logf("wrote %d configurations to %s", len(fixtures), path)
}

// anchor turns a fixture label into the id GitHub gives its heading.
func anchor(label string) string {
	return strings.ToLower(strings.ReplaceAll(label, "/", ""))
}

const dumpHeader = `# VMware: конфигурации приёмочных тестов

Сгенерировано из ` + "`internal/services/vmware/config_syntax_test.go`" + `. Не редактировать руками —
перегенерировать:

` + "```sh" + `
VMWARE_CONFIG_DUMP=$PWD/vmware_acceptance_configs.md go test ./internal/services/vmware -run TestDumpAcceptanceConfigs
` + "```" + `

Это ровно тот HCL, который приёмочные тесты применяют к живому API, с двумя
подстановками вместо переменных окружения: ` + "`location_id = 5`" + ` (` + "`VCP_VMWARE_LOCATION_ID`" + `)
и ` + "`image_id = 42`" + ` (` + "`VCP_VMWARE_IMAGE_ID`" + `). Имена ресурсов в реальном прогоне
получают случайный суффикс — ` + "`test-acc-vmw-<вид>-<6 символов>`" + ` — чтобы два прогона в
одном проекте не сталкивались, а ` + "`make sweep`" + ` мог убрать за упавшим.

Размеры машин нигде не захардкожены: они выводятся из каталога через
` + "`vcp_vmware_locations`" + ` и ` + "`vcp_vmware_images`" + `, как это делал бы пользователь. Поэтому
набор идёт на любом стенде, а заодно каждый серверный тест проверяет справочники.

Каждая конфигурация ниже — самостоятельный ` + "`.tf`" + `: её можно скопировать целиком,
подставить свои ` + "`location_id`" + `/` + "`image_id`" + ` и применить.

`
