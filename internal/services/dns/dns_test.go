package dns_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

// TestAccDNSRecordSet_basic covers the record-set lifecycle on A and MX sets:
// create multi-value sets, add a value + change the shared TTL, import both by
// domain/name/type, and an empty plan afterwards.
func TestAccDNSRecordSet_basic(t *testing.T) {
	rsName := "vcp_dns_record_set.a"
	mxName := "vcp_dns_record_set.mx"
	domain := "tf-acc-" + acctest.RandomString(6) + ".com."

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckDNSDomainsDestroyed,
		Steps: []resource.TestStep{
			// 1. Create: A record set (two values, ttl 1h) + MX (two values, priorities).
			{
				Config: testAccDNSConfig(domain, "1h", `{ ip = "10.0.0.1" }, { ip = "10.0.0.2" }`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(rsName, tfjsonpath.New("values"), knownvalue.SetSizeExact(2)),
					statecheck.ExpectKnownValue(rsName, tfjsonpath.New("ttl"), knownvalue.StringExact("1h")),
					statecheck.ExpectKnownValue(rsName, tfjsonpath.New("id"), knownvalue.StringExact(domain+"/www."+domain+"/A")),
					statecheck.ExpectKnownValue(mxName, tfjsonpath.New("values"), knownvalue.SetSizeExact(2)),
				},
			},
			// 2. Update: add a third A value and change the shared TTL to 2h.
			{
				Config: testAccDNSConfig(domain, "2h", `{ ip = "10.0.0.1" }, { ip = "10.0.0.2" }, { ip = "10.0.0.3" }`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(rsName, tfjsonpath.New("values"), knownvalue.SetSizeExact(3)),
					statecheck.ExpectKnownValue(rsName, tfjsonpath.New("ttl"), knownvalue.StringExact("2h")),
				},
			},
			// 3. Import both sets by domain/name/type (verifies canonicalized round-trip).
			{
				ResourceName:      rsName,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      mxName,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// 4. Idempotency — empty plan (no perpetual diff from canonicalization/shared TTL).
			{
				Config:   testAccDNSConfig(domain, "2h", `{ ip = "10.0.0.1" }, { ip = "10.0.0.2" }, { ip = "10.0.0.3" }`),
				PlanOnly: true,
			},
		},
	})
}

func testAccDNSConfig(domain, ttl, values string) string {
	return fmt.Sprintf(`
resource "vcp_dns_domain" "test" {
  name = %q
}

resource "vcp_dns_record_set" "a" {
  domain = vcp_dns_domain.test.name
  name   = "www.%s"
  type   = "A"
  ttl    = %q
  values = [ %s ]
}

resource "vcp_dns_record_set" "mx" {
  domain = vcp_dns_domain.test.name
  name   = %q
  type   = "MX"
  ttl    = "1h"
  values = [
    { priority = 10, mail_host = "mail1.%s" },
    { priority = 20, mail_host = "mail2.%s" },
  ]
}
`, domain, domain, ttl, values, domain, domain, domain)
}

// TestAccDNSRecordSet_disappears deletes the underlying records out-of-band and
// checks the provider detects the record set is gone (Read → RemoveResource) and
// plans to recreate it.
func TestAccDNSRecordSet_disappears(t *testing.T) {
	domain := "tf-acc-dis-" + acctest.RandomString(6) + ".com."
	config := fmt.Sprintf(`
resource "vcp_dns_domain" "test" {
  name = %[1]q
}

resource "vcp_dns_record_set" "a" {
  domain = vcp_dns_domain.test.name
  name   = "www.%[1]s"
  type   = "A"
  ttl    = "1h"
  values = [{ ip = "10.0.0.1" }]
}
`, domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckDNSDomainsDestroyed,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { deleteRecordsOutOfBand(t, domain, "www."+domain, "A") },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// deleteRecordsOutOfBand removes the (name,type) records directly via the API and
// waits until they are actually gone (deletion is asynchronous).
func deleteRecordsOutOfBand(t *testing.T, domain, name, rtype string) {
	client := acctest.GetTestClient()
	ctx := context.Background()

	recs, err := client.GetDomainRecords(ctx, domain)
	if err != nil {
		t.Fatalf("out-of-band read failed: %v", err)
	}
	var ids []int
	for _, r := range recs {
		if r.Name == name && string(r.Type) == rtype {
			if err := client.DeleteDomainRecord(ctx, domain, r.ID); err != nil {
				t.Fatalf("out-of-band delete failed: %v", err)
			}
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatalf("no %s records found for %s to delete out-of-band", rtype, name)
	}

	for i := 0; i < 30; i++ {
		time.Sleep(3 * time.Second)
		recs, err := client.GetDomainRecords(ctx, domain)
		if err != nil {
			// A transient API error must not be mistaken for "records gone".
			continue
		}
		gone := true
		for _, r := range recs {
			for _, id := range ids {
				if r.ID == id {
					gone = false
				}
			}
		}
		if gone {
			return
		}
	}
	t.Fatal("out-of-band deleted records did not disappear in time")
}

// TestAccDNSDomain_disappears deletes the zone out-of-band and checks the
// provider detects it is gone (Read → RemoveResource) and plans to recreate it.
func TestAccDNSDomain_disappears(t *testing.T) {
	domain := "tf-acc-domdis-" + acctest.RandomString(6) + ".com."
	config := fmt.Sprintf(`
resource "vcp_dns_domain" "test" {
  name = %q
}
`, domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckDNSDomainsDestroyed,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { deleteDomainOutOfBand(t, domain) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// deleteDomainOutOfBand removes the zone directly via the API and waits until
// it is actually gone (deletion is asynchronous).
func deleteDomainOutOfBand(t *testing.T, domain string) {
	client := acctest.GetTestClient()
	if err := client.DeleteDomainAndWait(context.Background(), domain); err != nil {
		t.Fatalf("out-of-band domain delete failed: %v", err)
	}
}

// TestAccDNSDomain_basic covers the zone resource on its own: create, the computed
// is_delegated / system NS record sets (via the data source), and import.
func TestAccDNSDomain_basic(t *testing.T) {
	domain := "tf-acc-dom-" + acctest.RandomString(6) + ".com."
	config := fmt.Sprintf(`
resource "vcp_dns_domain" "test" {
  name = %q
}

data "vcp_dns_domain" "test" {
  name       = vcp_dns_domain.test.name
  depends_on = [vcp_dns_domain.test]
}

data "vcp_dns_domains" "all" {
  depends_on = [vcp_dns_domain.test]
}
`, domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckDNSDomainsDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("vcp_dns_domain.test", tfjsonpath.New("id"), knownvalue.StringExact(domain)),
					statecheck.ExpectKnownValue("vcp_dns_domain.test", tfjsonpath.New("is_delegated"), knownvalue.Bool(false)),
					// A freshly created zone ships with system NS record sets.
					statecheck.ExpectKnownValue("data.vcp_dns_domain.test", tfjsonpath.New("record_sets"), knownvalue.ListSizeExact(1)),
					// Plural data source returns at least our zone.
					statecheck.ExpectKnownValue("data.vcp_dns_domains.all", tfjsonpath.New("domains"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      "vcp_dns_domain.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccDNSRecordSet_allTypes exercises every record type in one zone (AAAA, CNAME,
// NS multi-value, TXT, SRV), the data source grouping, idempotency and SRV import.
func TestAccDNSRecordSet_allTypes(t *testing.T) {
	domain := "tf-acc-all-" + acctest.RandomString(6) + ".com."

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckDNSDomainsDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccDNSAllTypesConfig(domain, "30m", "www."+domain, false),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("vcp_dns_record_set.aaaa", tfjsonpath.New("values"), knownvalue.SetSizeExact(1)),
					// Mixed-case config name: state preserves the user's literal (FQDN
					// semantic equality suppresses the diff vs the API's lowercase form).
					statecheck.ExpectKnownValue("vcp_dns_record_set.aaaa", tfjsonpath.New("name"), knownvalue.StringExact("WWW."+domain)),
					statecheck.ExpectKnownValue("vcp_dns_record_set.cname", tfjsonpath.New("values"), knownvalue.SetSizeExact(1)),
					statecheck.ExpectKnownValue("vcp_dns_record_set.ns", tfjsonpath.New("values"), knownvalue.SetSizeExact(2)),
					statecheck.ExpectKnownValue("vcp_dns_record_set.txt", tfjsonpath.New("values"), knownvalue.SetSizeExact(1)),
					statecheck.ExpectKnownValue("vcp_dns_record_set.srv", tfjsonpath.New("values"), knownvalue.SetSizeExact(1)),
					statecheck.ExpectKnownValue("vcp_dns_record_set.srv", tfjsonpath.New("ttl"), knownvalue.StringExact("30m")),
					// system NS + 5 managed sets (aaaa, cname, ns, txt, srv).
					statecheck.ExpectKnownValue("data.vcp_dns_domain.test", tfjsonpath.New("record_sets"), knownvalue.ListSizeExact(6)),
				},
			},
			// Update: change the SRV shared TTL (exercises the SRV PUT base-name fix),
			// add a 3rd NS value (multi-value update), and change the CNAME target —
			// the single-value replacement PUT path (create-before-delete is impossible
			// for CNAME: the API rejects a second CNAME at the same name, -5542).
			{
				Config: testAccDNSAllTypesConfig(domain, "1h", "web."+domain, true),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("vcp_dns_record_set.srv", tfjsonpath.New("ttl"), knownvalue.StringExact("1h")),
					statecheck.ExpectKnownValue("vcp_dns_record_set.srv", tfjsonpath.New("values"), knownvalue.SetSizeExact(1)),
					statecheck.ExpectKnownValue("vcp_dns_record_set.ns", tfjsonpath.New("values"), knownvalue.SetSizeExact(3)),
					statecheck.ExpectKnownValue("vcp_dns_record_set.cname", tfjsonpath.New("values"), knownvalue.SetSizeExact(1)),
				},
			},
			// Idempotency: catches any round-trip diff (IPv6/TXT canonicalization, trailing dots, case).
			{
				Config:   testAccDNSAllTypesConfig(domain, "1h", "web."+domain, true),
				PlanOnly: true,
			},
			// SRV round-trips cleanly (dotted hostnames).
			{
				ResourceName:      "vcp_dns_record_set.srv",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccDNSAllTypesConfig(domain, srvTTL, cnameTarget string, nsThird bool) string {
	nsValues := fmt.Sprintf("{ name_server_host = \"ns1.%[1]s\" },\n    { name_server_host = \"ns2.%[1]s\" },", domain)
	if nsThird {
		nsValues += fmt.Sprintf("\n    { name_server_host = \"ns3.%s\" },", domain)
	}
	return fmt.Sprintf(`
resource "vcp_dns_domain" "test" {
  name = %[1]q
}

resource "vcp_dns_record_set" "aaaa" {
  domain = vcp_dns_domain.test.name
  name   = "WWW.%[1]s" # mixed case on purpose — canonicalized to lowercase
  type   = "AAAA"
  ttl    = "1h"
  values = [{ ip = "2606:2800:220:1::1" }]
}

resource "vcp_dns_record_set" "cname" {
  domain = vcp_dns_domain.test.name
  name   = "alias.%[1]s"
  type   = "CNAME"
  ttl    = "6h"
  values = [{ canonical_name = %[4]q }]
}

resource "vcp_dns_record_set" "ns" {
  domain = vcp_dns_domain.test.name
  name   = "sub.%[1]s"
  type   = "NS"
  ttl    = "1d"
  values = [
    %[3]s
  ]
}

resource "vcp_dns_record_set" "txt" {
  domain = vcp_dns_domain.test.name
  name   = %[1]q
  type   = "TXT"
  ttl    = "1h"
  values = [{ text = "v=spf1 -all" }]
}

resource "vcp_dns_record_set" "srv" {
  domain = vcp_dns_domain.test.name
  name   = "_sip._tcp.%[1]s"
  type   = "SRV"
  ttl    = %[2]q
  values = [{ priority = 10, weight = 60, port = 5060, target = "srv.%[1]s" }]
}

data "vcp_dns_domain" "test" {
  name = vcp_dns_domain.test.name
  depends_on = [
    vcp_dns_record_set.aaaa,
    vcp_dns_record_set.cname,
    vcp_dns_record_set.ns,
    vcp_dns_record_set.txt,
    vcp_dns_record_set.srv,
  ]
}
`, domain, srvTTL, nsValues, cnameTarget)
}

// TestAccDNSRecordSet_conflict: creating a record set that already exists must fail
// with an import hint instead of silently merging with the existing records.
func TestAccDNSRecordSet_conflict(t *testing.T) {
	domain := "tf-acc-con-" + acctest.RandomString(6) + ".com."
	base := fmt.Sprintf(`
resource "vcp_dns_domain" "test" {
  name = %[1]q
}

resource "vcp_dns_record_set" "a" {
  domain = vcp_dns_domain.test.name
  name   = "www.%[1]s"
  type   = "A"
  ttl    = "1h"
  values = [{ ip = "10.0.0.1" }]
}
`, domain)
	dup := base + fmt.Sprintf(`
resource "vcp_dns_record_set" "duplicate" {
  domain = vcp_dns_domain.test.name
  name   = "www.%[1]s"
  type   = "A"
  ttl    = "1h"
  values = [{ ip = "10.0.0.2" }]
}
`, domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckDNSDomainsDestroyed,
		Steps: []resource.TestStep{
			{Config: base},
			{
				Config:      dup,
				ExpectError: regexp.MustCompile(`Already Exists`),
			},
			// Recovery: dropping the duplicate leaves the original intact and idempotent.
			{
				Config:   base,
				PlanOnly: true,
			},
		},
	})
}

// TestAccDNSRecordSet_validation checks plan-time ValidateConfig errors (no API calls).
func TestAccDNSRecordSet_validation(t *testing.T) {
	badConfig := func(body string) string {
		return `resource "vcp_dns_record_set" "bad" {
  domain = "example.com."
  name   = "x.example.com."
` + body + "\n}\n"
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      badConfig(`  type = "CNAME"` + "\n  values = [{ canonical_name = \"a.example.com.\" }, { canonical_name = \"b.example.com.\" }]"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`exactly one value`),
			},
			{
				Config:      badConfig(`  type = "A"` + "\n  values = [{ text = \"nope\" }]"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`require`),
			},
			{
				// SRV name without the _service._proto prefix.
				Config:      badConfig(`  type = "SRV"` + "\n  values = [{ priority = 1, weight = 1, port = 1, target = \"t.example.com.\" }]"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Invalid SRV name`),
			},
			{
				Config:      badConfig(`  type = "MX"` + "\n  values = [{ mail_host = \"m.example.com.\" }]"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`require`),
			},
			{
				// name outside its zone.
				Config: `resource "vcp_dns_record_set" "bad" {
  domain = "example.com."
  name   = "x.other.com."
  type   = "A"
  values = [{ ip = "10.0.0.1" }]
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`outside its zone`),
			},
			{
				// stray field from another type must be rejected, not silently dropped.
				Config:      badConfig(`  type = "A"` + "\n  values = [{ ip = \"10.0.0.1\", mail_host = \"m.example.com.\" }]"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`not applicable|not used by`),
			},
		},
	})
}
