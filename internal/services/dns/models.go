package dns

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ─────────────────────────────────────────────────────────────────────────────
// Models
// ─────────────────────────────────────────────────────────────────────────────

// domainResourceModel — model for the vcp_dns_domain resource. Name is FQDNValue:
// comparison is case-insensitive and ignores the trailing dot (see fqdn_type.go).
type domainResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        FQDNValue    `tfsdk:"name"`
	IsDelegated types.Bool   `tfsdk:"is_delegated"`
}

// recordSetModel — model for the vcp_dns_record_set resource (one RRset name+type).
// For SRV, service/protocol are not separate fields: they are encoded in name (_service._proto.zone),
// as required by DNS and the API (see parseSRVName). Domain/Name are FQDNValue (semantic equality).
type recordSetModel struct {
	ID     types.String `tfsdk:"id"`
	Domain FQDNValue    `tfsdk:"domain"`
	Name   FQDNValue    `tfsdk:"name"`
	Type   types.String `tfsdk:"type"`
	TTL    types.String `tfsdk:"ttl"`
	Values types.Set    `tfsdk:"values"`
}

// dnsValueModel — one element of the values set (rdata of a single record).
type dnsValueModel struct {
	IP             types.String `tfsdk:"ip"`
	MailHost       types.String `tfsdk:"mail_host"`
	Priority       types.Int64  `tfsdk:"priority"`
	CanonicalName  types.String `tfsdk:"canonical_name"`
	NameServerHost types.String `tfsdk:"name_server_host"`
	Text           types.String `tfsdk:"text"`
	Weight         types.Int64  `tfsdk:"weight"`
	Port           types.Int64  `tfsdk:"port"`
	Target         types.String `tfsdk:"target"`
}

// fieldIsNull reports whether the value's field is set, given its schema name.
func (v dnsValueModel) fieldIsNull(field string) bool {
	switch field {
	case "ip":
		return v.IP.IsNull()
	case "mail_host":
		return v.MailHost.IsNull()
	case "priority":
		return v.Priority.IsNull()
	case "canonical_name":
		return v.CanonicalName.IsNull()
	case "name_server_host":
		return v.NameServerHost.IsNull()
	case "text":
		return v.Text.IsNull()
	case "weight":
		return v.Weight.IsNull()
	case "port":
		return v.Port.IsNull()
	case "target":
		return v.Target.IsNull()
	}
	return true
}

// fieldIsUnknown reports whether the value's field is unknown, given its schema name.
func (v dnsValueModel) fieldIsUnknown(field string) bool {
	switch field {
	case "ip":
		return v.IP.IsUnknown()
	case "mail_host":
		return v.MailHost.IsUnknown()
	case "priority":
		return v.Priority.IsUnknown()
	case "canonical_name":
		return v.CanonicalName.IsUnknown()
	case "name_server_host":
		return v.NameServerHost.IsUnknown()
	case "text":
		return v.Text.IsUnknown()
	case "weight":
		return v.Weight.IsUnknown()
	case "port":
		return v.Port.IsUnknown()
	case "target":
		return v.Target.IsUnknown()
	}
	return false
}

// fieldString returns the value's string field by its schema name ("" for non-string fields).
func (v dnsValueModel) fieldString(field string) string {
	switch field {
	case "ip":
		return v.IP.ValueString()
	case "mail_host":
		return v.MailHost.ValueString()
	case "canonical_name":
		return v.CanonicalName.ValueString()
	case "name_server_host":
		return v.NameServerHost.ValueString()
	case "text":
		return v.Text.ValueString()
	case "target":
		return v.Target.ValueString()
	}
	return ""
}

// domainDataModel — model for the vcp_dns_domain data source (with nested record_sets).
type domainDataModel struct {
	Name        types.String `tfsdk:"name"`
	IsDelegated types.Bool   `tfsdk:"is_delegated"`
	RecordSets  types.List   `tfsdk:"record_sets"`
}

// domainsDataModel — model for the vcp_dns_domains data source.
type domainsDataModel struct {
	Domains []domainDataModel `tfsdk:"domains"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Object types for computed nested attributes
// ─────────────────────────────────────────────────────────────────────────────

// dnsValueAttrTypes describes one element of the values set.
var dnsValueAttrTypes = map[string]attr.Type{
	"ip":               types.StringType,
	"mail_host":        types.StringType,
	"priority":         types.Int64Type,
	"canonical_name":   types.StringType,
	"name_server_host": types.StringType,
	"text":             types.StringType,
	"weight":           types.Int64Type,
	"port":             types.Int64Type,
	"target":           types.StringType,
}

var dnsValueObjectType = types.ObjectType{AttrTypes: dnsValueAttrTypes}

// dnsRecordSetAttrTypes describes one RRset in the data source.
var dnsRecordSetAttrTypes = map[string]attr.Type{
	"name":     types.StringType,
	"type":     types.StringType,
	"ttl":      types.StringType,
	"service":  types.StringType,
	"protocol": types.StringType,
	"values":   types.SetType{ElemType: dnsValueObjectType},
}

var dnsRecordSetObjectType = types.ObjectType{AttrTypes: dnsRecordSetAttrTypes}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// normalizeName converts a name to the API's canonical form: lowercase + trailing
// dot (verified live: the API stores names in lowercase; DNS names are case-insensitive).
func normalizeName(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToLower(s)
	if !strings.HasSuffix(s, ".") {
		return s + "."
	}
	return s
}

// normalizeIP converts an IP to its canonical form (important for IPv6, which the API/PowerDNS
// may return in a different representation — e.g. compressed). Invalid strings
// are returned as is.
func normalizeIP(s string) string {
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	return s
}

// normalizeTXT strips a single pair of surrounding double quotes: the API stores TXT in
// presentation form ("...") — we strip them so the user's value and the API's
// value compare semantically equal.
func normalizeTXT(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}
	return s
}

// parseSRVName splits an SRV name of the form _service._proto.zone. into its components.
// The API/PowerDNS itself adds the _service._proto prefix to the base name, so
// on creation we send just the base name (zone.) plus service/protocol.
func parseSRVName(name string) (service, protocol, base string, ok bool) {
	n := strings.TrimSuffix(name, ".")
	labels := strings.SplitN(n, ".", 3)
	if len(labels) < 3 || !strings.HasPrefix(labels[0], "_") || !strings.HasPrefix(labels[1], "_") {
		return "", "", "", false
	}
	service = strings.TrimPrefix(labels[0], "_")
	protocol = strings.ToUpper(strings.TrimPrefix(labels[1], "_"))
	base = normalizeName(labels[2])
	return service, protocol, base, service != "" && protocol != "" && base != ""
}

func strOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func intPtrOrNull(p *int) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*p))
}

// recordToValueObject builds a value object from an API record.
func recordToValueObject(r entities.DNSRecord) attr.Value {
	return types.ObjectValueMust(dnsValueAttrTypes, map[string]attr.Value{
		"ip":               strOrNull(r.IP),
		"mail_host":        strOrNull(r.MailHost),
		"priority":         intPtrOrNull(r.Priority),
		"canonical_name":   strOrNull(r.CanonicalName),
		"name_server_host": strOrNull(r.NameServerHost),
		"text":             strOrNull(r.Text),
		"weight":           intPtrOrNull(r.Weight),
		"port":             intPtrOrNull(r.Port),
		"target":           strOrNull(r.Target),
	})
}

// recordSemKey — the semantic key of an API record's rdata (for comparing sets).
// Hostnames are normalized to trailing-dot so that 'x' and 'x.' are considered equal.
func recordSemKey(rtype string, r entities.DNSRecord) string {
	switch rtype {
	case "A", "AAAA":
		return "ip=" + normalizeIP(r.IP)
	case "CNAME":
		return "cname=" + normalizeName(r.CanonicalName)
	case "NS":
		return "ns=" + normalizeName(r.NameServerHost)
	case "TXT":
		return "txt=" + normalizeTXT(r.Text)
	case "MX":
		return fmt.Sprintf("mx=%d/%s", derefInt(r.Priority), normalizeName(r.MailHost))
	case "SRV":
		return fmt.Sprintf("srv=%d/%d/%d/%s", derefInt(r.Priority), derefInt(r.Weight), derefInt(r.Port), normalizeName(r.Target))
	}
	return ""
}

// valueSemKey — the semantic key of rdata from a value model.
func valueSemKey(rtype string, v dnsValueModel) string {
	switch rtype {
	case "A", "AAAA":
		return "ip=" + normalizeIP(v.IP.ValueString())
	case "CNAME":
		return "cname=" + normalizeName(v.CanonicalName.ValueString())
	case "NS":
		return "ns=" + normalizeName(v.NameServerHost.ValueString())
	case "TXT":
		return "txt=" + normalizeTXT(v.Text.ValueString())
	case "MX":
		return fmt.Sprintf("mx=%d/%s", v.Priority.ValueInt64(), normalizeName(v.MailHost.ValueString()))
	case "SRV":
		return fmt.Sprintf("srv=%d/%d/%d/%s", v.Priority.ValueInt64(), v.Weight.ValueInt64(), v.Port.ValueInt64(), normalizeName(v.Target.ValueString()))
	}
	return ""
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// rrset — records of one set (name+type), collected from the API's flat list.
type rrset struct {
	name     string
	rtype    string
	ttl      string
	service  string
	protocol string
	records  []entities.DNSRecord // with individual ids (internal bookkeeping)
}

// filterRRset selects the records of a set (name, type) from a flat list.
func filterRRset(records []entities.DNSRecord, name, rtype string) (rrset, bool) {
	want := normalizeName(name)
	out := rrset{name: want, rtype: rtype}
	found := false
	for _, r := range records {
		if normalizeName(r.Name) != want || string(r.Type) != rtype {
			continue
		}
		found = true
		out.ttl = string(r.TTL) // shared across the set (PowerDNS RRset semantics)
		out.records = append(out.records, r)
	}
	return out, found
}

// nameInZone checks that the record name is an FQDN within its zone (equal to the zone or a subdomain of it).
func nameInZone(name, domain string) bool {
	n := normalizeName(name)
	d := normalizeName(domain)
	return n == d || strings.HasSuffix(n, "."+d)
}

// diffRRset computes which plan values need to be created and which current records
// (by id) need to be deleted, matching by the semantic key of rdata. A pure function — testable without a network.
func diffRRset(rtype string, planValues []dnsValueModel, current []entities.DNSRecord) (toCreate []dnsValueModel, toDeleteIDs []int) {
	currentByKey := make(map[string]entities.DNSRecord, len(current))
	for _, rec := range current {
		currentByKey[recordSemKey(rtype, rec)] = rec
	}
	desired := make(map[string]struct{}, len(planValues))
	for _, v := range planValues {
		k := valueSemKey(rtype, v)
		desired[k] = struct{}{}
		if _, ok := currentByKey[k]; !ok {
			toCreate = append(toCreate, v)
		}
	}
	for k, rec := range currentByKey {
		if _, ok := desired[k]; !ok {
			toDeleteIDs = append(toDeleteIDs, rec.ID)
		}
	}
	return toCreate, toDeleteIDs
}

// valuesSet builds a types.Set of values from the set's records.
func valuesSet(rs rrset) types.Set {
	vals := make([]attr.Value, 0, len(rs.records))
	for _, r := range rs.records {
		vals = append(vals, recordToValueObject(r))
	}
	return types.SetValueMust(dnsValueObjectType, vals)
}

// groupRRsets groups a flat list of records by (name, type) — for the data source.
// The order is deterministic (by name, then type).
func groupRRsets(records []entities.DNSRecord) types.List {
	type key struct{ name, rtype string }
	order := make([]key, 0)
	groups := make(map[key]*rrset)
	for _, r := range records {
		k := key{normalizeName(r.Name), string(r.Type)}
		g, ok := groups[k]
		if !ok {
			g = &rrset{name: k.name, rtype: k.rtype}
			groups[k] = g
			order = append(order, k)
		}
		g.ttl = string(r.TTL)
		if r.Service != "" {
			g.service = r.Service
		}
		if r.Protocol != nil {
			g.protocol = string(*r.Protocol)
		}
		g.records = append(g.records, r)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].name != order[j].name {
			return order[i].name < order[j].name
		}
		return order[i].rtype < order[j].rtype
	})

	elems := make([]attr.Value, 0, len(order))
	for _, k := range order {
		g := groups[k]
		obj := types.ObjectValueMust(dnsRecordSetAttrTypes, map[string]attr.Value{
			"name":     types.StringValue(g.name),
			"type":     types.StringValue(g.rtype),
			"ttl":      types.StringValue(g.ttl),
			"service":  strOrNull(g.service),
			"protocol": strOrNull(g.protocol),
			"values":   valuesSet(*g),
		})
		elems = append(elems, obj)
	}
	return types.ListValueMust(dnsRecordSetObjectType, elems)
}

// mapDomainToDataModel populates the data source model from an API domain.
func mapDomainToDataModel(d *entities.Domain) domainDataModel {
	return domainDataModel{
		Name:        types.StringValue(normalizeName(d.Name)),
		IsDelegated: types.BoolValue(d.IsDelegated),
		RecordSets:  groupRRsets(d.Records),
	}
}

// Canonicalization of names to the API form (lowercase + trailing dot) is NOT done by a plan
// modifier (Terraform core forbids the provider from changing a value set in the
// config: "planned value does not match config value"), but by the custom
// FQDNType type with semantic equality — see fqdn_type.go. Names always go
// through normalizeName for the API and for internal comparisons.
