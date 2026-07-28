package dns

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

func intPtr(i int) *int { return &i }

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"example.com":      "example.com.",
		"example.com.":     "example.com.",
		"":                 "",
		"www.a.b":          "www.a.b.",
		"WWW.Example.COM":  "www.example.com.", // API lowercases names (verified live)
		"WWW.Example.COM.": "www.example.com.",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The semantic key of a plan value and of the equivalent API record must match
// even when the user omits the trailing dot on hostname fields (canonicalization).
func TestSemKeyMatchesAcrossTrailingDot(t *testing.T) {
	cases := []struct {
		name   string
		rtype  string
		value  dnsValueModel
		record entities.DNSRecord
	}{
		{
			name:   "A",
			rtype:  "A",
			value:  dnsValueModel{IP: types.StringValue("10.0.0.1")},
			record: entities.DNSRecord{Type: entities.RecordTypeA, IP: "10.0.0.1"},
		},
		{
			name:   "MX trailing dot",
			rtype:  "MX",
			value:  dnsValueModel{Priority: types.Int64Value(10), MailHost: types.StringValue("mail.example.com")},
			record: entities.DNSRecord{Type: entities.RecordTypeMX, Priority: intPtr(10), MailHost: "mail.example.com."},
		},
		{
			name:   "MX mixed case host",
			rtype:  "MX",
			value:  dnsValueModel{Priority: types.Int64Value(10), MailHost: types.StringValue("MAIL.Example.com")},
			record: entities.DNSRecord{Type: entities.RecordTypeMX, Priority: intPtr(10), MailHost: "mail.example.com."},
		},
		{
			name:   "CNAME trailing dot",
			rtype:  "CNAME",
			value:  dnsValueModel{CanonicalName: types.StringValue("www.example.com")},
			record: entities.DNSRecord{Type: entities.RecordTypeCNAME, CanonicalName: "www.example.com."},
		},
		{
			name:   "SRV target trailing dot",
			rtype:  "SRV",
			value:  dnsValueModel{Priority: types.Int64Value(10), Weight: types.Int64Value(60), Port: types.Int64Value(5060), Target: types.StringValue("srv.example.com")},
			record: entities.DNSRecord{Type: entities.RecordTypeSRV, Priority: intPtr(10), Weight: intPtr(60), Port: intPtr(5060), Target: "srv.example.com."},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vk := valueSemKey(c.rtype, c.value)
			rk := recordSemKey(c.rtype, c.record)
			if vk != rk {
				t.Errorf("keys differ: value %q vs record %q", vk, rk)
			}
			if vk == "" {
				t.Errorf("empty semantic key for %s", c.rtype)
			}
		})
	}
}

func TestFilterRRset(t *testing.T) {
	records := []entities.DNSRecord{
		{ID: 1, Name: "example.com.", Type: entities.RecordTypeNS, TTL: "1h", NameServerHost: "ns1."},
		{ID: 2, Name: "www.example.com.", Type: entities.RecordTypeA, TTL: "2h", IP: "10.0.0.1"},
		{ID: 3, Name: "www.example.com.", Type: entities.RecordTypeA, TTL: "2h", IP: "10.0.0.2"},
		{ID: 4, Name: "www.example.com.", Type: entities.RecordTypeAAAA, TTL: "5m", IP: "::1"},
	}

	// name given without the trailing dot must still match (normalized).
	rs, found := filterRRset(records, "www.example.com", "A")
	if !found {
		t.Fatal("expected to find the www A rrset")
	}
	if len(rs.records) != 2 {
		t.Errorf("expected 2 A records, got %d", len(rs.records))
	}
	if rs.ttl != "2h" {
		t.Errorf("expected shared ttl 2h, got %q", rs.ttl)
	}

	if _, found := filterRRset(records, "missing.example.com.", "A"); found {
		t.Error("did not expect to find a missing rrset")
	}
}

func TestGroupRRsets(t *testing.T) {
	records := []entities.DNSRecord{
		{ID: 2, Name: "www.example.com.", Type: entities.RecordTypeA, TTL: "1h", IP: "10.0.0.1"},
		{ID: 3, Name: "www.example.com.", Type: entities.RecordTypeA, TTL: "1h", IP: "10.0.0.2"},
		{ID: 1, Name: "example.com.", Type: entities.RecordTypeNS, TTL: "1h", NameServerHost: "ns1."},
	}
	list := groupRRsets(records)
	elems := list.Elements()
	if len(elems) != 2 {
		t.Fatalf("expected 2 record sets, got %d", len(elems))
	}
	// deterministic ordering: by name then type → "example.com." NS before "www.example.com." A.
	first := elems[0].(types.Object).Attributes()
	if first["name"].(types.String).ValueString() != "example.com." || first["type"].(types.String).ValueString() != "NS" {
		t.Errorf("unexpected first group: %v / %v", first["name"], first["type"])
	}
	second := elems[1].(types.Object).Attributes()
	vals := second["values"].(types.Set)
	if len(vals.Elements()) != 2 {
		t.Errorf("expected 2 values in the www A set, got %d", len(vals.Elements()))
	}
}

func TestDiffRRset(t *testing.T) {
	current := []entities.DNSRecord{
		{ID: 1, Type: entities.RecordTypeA, IP: "10.0.0.1"},
		{ID: 2, Type: entities.RecordTypeA, IP: "10.0.0.2"},
	}
	plan := []dnsValueModel{
		{IP: types.StringValue("10.0.0.2")}, // kept
		{IP: types.StringValue("10.0.0.3")}, // added
	}
	toCreate, toDelete := diffRRset("A", plan, current)
	if len(toCreate) != 1 || toCreate[0].IP.ValueString() != "10.0.0.3" {
		t.Errorf("toCreate = %+v, want [10.0.0.3]", toCreate)
	}
	if len(toDelete) != 1 || toDelete[0] != 1 {
		t.Errorf("toDelete = %v, want [1]", toDelete)
	}

	// No change → nothing to do.
	same := []dnsValueModel{{IP: types.StringValue("10.0.0.1")}, {IP: types.StringValue("10.0.0.2")}}
	c, d := diffRRset("A", same, current)
	if len(c) != 0 || len(d) != 0 {
		t.Errorf("expected no diff, got create=%v delete=%v", c, d)
	}
}

func TestNameInZone(t *testing.T) {
	ok := []struct{ name, zone string }{
		{"example.com.", "example.com"},
		{"www.example.com", "example.com."},
		{"_sip._tcp.example.com.", "example.com."},
	}
	for _, c := range ok {
		if !nameInZone(c.name, c.zone) {
			t.Errorf("nameInZone(%q,%q) = false, want true", c.name, c.zone)
		}
	}
	bad := []struct{ name, zone string }{
		{"www.other.com.", "example.com."},
		{"notexample.com.", "example.com."}, // suffix trick must be rejected
	}
	for _, c := range bad {
		if nameInZone(c.name, c.zone) {
			t.Errorf("nameInZone(%q,%q) = true, want false", c.name, c.zone)
		}
	}
}

func TestNormalizeTXT(t *testing.T) {
	cases := map[string]string{
		`"v=spf1 -all"`: "v=spf1 -all",
		"v=spf1 -all":   "v=spf1 -all",
		`""`:            "",
	}
	for in, want := range cases {
		if got := normalizeTXT(in); got != want {
			t.Errorf("normalizeTXT(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildCreateReq(t *testing.T) {
	r := &recordSetResource{}

	t.Run("A", func(t *testing.T) {
		plan := recordSetModel{Name: NewFQDNValue("www.example.com."), Type: types.StringValue("A"), TTL: types.StringValue("1h")}
		req := r.buildCreateReq(plan, dnsValueModel{IP: types.StringValue("10.0.0.1")})
		if req.IP != "10.0.0.1" || req.Type != entities.RecordTypeA || req.TTL != "1h" {
			t.Errorf("unexpected A request: %+v", req)
		}
	})

	t.Run("MX normalizes mail_host", func(t *testing.T) {
		plan := recordSetModel{Name: NewFQDNValue("example.com."), Type: types.StringValue("MX"), TTL: types.StringValue("1h")}
		req := r.buildCreateReq(plan, dnsValueModel{Priority: types.Int64Value(10), MailHost: types.StringValue("mail.example.com")})
		if req.MailHost != "mail.example.com." {
			t.Errorf("mail_host not normalized: %q", req.MailHost)
		}
		if req.Priority == nil || *req.Priority != 10 {
			t.Errorf("priority not set: %v", req.Priority)
		}
	})

	t.Run("SRV sends the full DNS-standard name (SDK derives service/protocol)", func(t *testing.T) {
		plan := recordSetModel{
			Name: NewFQDNValue("_sip._tcp.example.com."), Type: types.StringValue("SRV"), TTL: types.StringValue("30m"),
		}
		req := r.buildCreateReq(plan, dnsValueModel{Priority: types.Int64Value(10), Weight: types.Int64Value(60), Port: types.Int64Value(5060), Target: types.StringValue("srv.example.com")})
		if req.Name != "_sip._tcp.example.com." {
			t.Errorf("expected the full SRV name, got %q", req.Name)
		}
		if req.Service != "" || req.Protocol != nil {
			t.Errorf("service/protocol derivation now lives in the SDK: %q / %v", req.Service, req.Protocol)
		}
		if req.Target != "srv.example.com." || req.Weight == nil || req.Port == nil {
			t.Errorf("srv rdata not set: %+v", req)
		}
	})

	t.Run("omitted ttl is not sent", func(t *testing.T) {
		plan := recordSetModel{Name: NewFQDNValue("x.example.com."), Type: types.StringValue("A"), TTL: types.StringNull()}
		req := r.buildCreateReq(plan, dnsValueModel{IP: types.StringValue("10.0.0.1")})
		if req.TTL != "" {
			t.Errorf("expected empty ttl, got %q", req.TTL)
		}
	})
}

func TestBuildUpdateReq(t *testing.T) {
	r := &recordSetResource{}

	t.Run("CNAME single-value replacement", func(t *testing.T) {
		plan := recordSetModel{Name: NewFQDNValue("alias.example.com."), Type: types.StringValue("CNAME")}
		req := r.buildUpdateReq(plan, dnsValueModel{CanonicalName: types.StringValue("web.example.com")}, "6h")
		if req.CanonicalName != "web.example.com." || req.TTL != "6h" || req.Name != "alias.example.com." {
			t.Errorf("unexpected CNAME update request: %+v", req)
		}
	})

	t.Run("SRV replacement sends the full name (SDK normalizes)", func(t *testing.T) {
		plan := recordSetModel{Name: NewFQDNValue("_sip._tcp.example.com."), Type: types.StringValue("SRV")}
		req := r.buildUpdateReq(plan, dnsValueModel{
			Priority: types.Int64Value(10), Weight: types.Int64Value(60), Port: types.Int64Value(5060),
			Target: types.StringValue("srv.example.com."),
		}, "30m")
		if req.Name != "_sip._tcp.example.com." || req.TTL != "30m" {
			t.Errorf("SRV update must send the full name (SDK strips the prefix): %+v", req)
		}
	})
}

func TestParseSRVName(t *testing.T) {
	svc, proto, base, ok := parseSRVName("_sip._tcp.example.com.")
	if !ok || svc != "sip" || proto != "TCP" || base != "example.com." {
		t.Errorf("parseSRVName full: got (%q,%q,%q,%v)", svc, proto, base, ok)
	}
	// lowercase protocol label is upper-cased for the SDK; trailing dot optional.
	if svc, proto, base, ok := parseSRVName("_ldap._udp.corp.example.com"); !ok || proto != "UDP" || base != "corp.example.com." {
		t.Errorf("parseSRVName udp: got (%q,%q,%q,%v)", svc, proto, base, ok)
	}
	// Not an SRV-shaped name.
	if _, _, _, ok := parseSRVName("www.example.com."); ok {
		t.Error("parseSRVName should reject a non-SRV name")
	}
}

func TestRecordToUpdateReq(t *testing.T) {
	rec := entities.DNSRecord{Name: "www.example.com.", Type: entities.RecordTypeA, TTL: "1h", IP: "10.0.0.1"}
	req := recordToUpdateReq(rec, "6h")
	if req.TTL != "6h" {
		t.Errorf("ttl not updated: %q", req.TTL)
	}
	if req.IP != "10.0.0.1" || req.Type != entities.RecordTypeA || req.Name != "www.example.com." {
		t.Errorf("rdata not preserved: %+v", req)
	}

	// SRV keeps the full name — the SDK normalizes it to the base form on PUT.
	srv := entities.DNSRecord{Name: "_sip._tcp.example.com.", Type: entities.RecordTypeSRV, TTL: "30m", Target: "t.example.com.", Service: "sip"}
	sreq := recordToUpdateReq(srv, "1h")
	if sreq.Name != "_sip._tcp.example.com." || sreq.TTL != "1h" {
		t.Errorf("SRV update req should keep the full name, got %+v", sreq)
	}
}

// TestFQDNSemanticEquals: names are equal if their canonical forms are equal —
// a user literal ("WWW.Example.com") must not produce a diff against
// the API's canonical form ("www.example.com.").
func TestFQDNSemanticEquals(t *testing.T) {
	ctx := context.Background()
	equal := [][2]string{
		{"example.com", "example.com."},
		{"WWW.Example.com", "www.example.com."},
		{"_sip._tcp.Example.com.", "_sip._tcp.example.com."},
	}
	for _, c := range equal {
		ok, diags := NewFQDNValue(c[0]).StringSemanticEquals(ctx, NewFQDNValue(c[1]))
		if diags.HasError() || !ok {
			t.Errorf("FQDN %q and %q must be semantically equal", c[0], c[1])
		}
	}
	notEqual := [][2]string{
		{"www.example.com.", "web.example.com."},
		{"example.com.", "example.org."},
	}
	for _, c := range notEqual {
		ok, _ := NewFQDNValue(c[0]).StringSemanticEquals(ctx, NewFQDNValue(c[1]))
		if ok {
			t.Errorf("FQDN %q and %q must NOT be semantically equal", c[0], c[1])
		}
	}
	// null/unknown never equal.
	if ok, _ := (FQDNValue{StringValue: types.StringNull()}).StringSemanticEquals(ctx, NewFQDNValue("a.")); ok {
		t.Error("null must not be semantically equal to a value")
	}
}
