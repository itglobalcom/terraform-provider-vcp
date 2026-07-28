package dns

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var ttlValues = []string{"1s", "5s", "30s", "1m", "5m", "10m", "15m", "30m", "1h", "2h", "6h", "12h", "1d"}

var (
	_ resource.Resource                   = &recordSetResource{}
	_ resource.ResourceWithConfigure      = &recordSetResource{}
	_ resource.ResourceWithImportState    = &recordSetResource{}
	_ resource.ResourceWithValidateConfig = &recordSetResource{}
)

// NewRecordSetResource is a helper function to simplify the provider implementation.
func NewRecordSetResource() resource.Resource {
	return &recordSetResource{}
}

type recordSetResource struct {
	client *sdk.CloudClient
}

func (r *recordSetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_record_set"
}

func (r *recordSetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	forceNewStr := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a DNS record set (RRset) — all records sharing one `name` and `type`. " +
			"Per DNS/PowerDNS semantics the `ttl` is shared across the whole set; the individual values are the " +
			"rdata. Changing `domain`, `name` or `type` forces a new record set.\n\n" +
			"`name` must be a full FQDN inside the zone (either the zone itself or a subdomain of `domain`); " +
			"relative names and `@` are not supported. Names are canonicalized to a lowercase, trailing-dot FQDN. " +
			"For `SRV`, encode the service and protocol in the name as `_service._proto.zone.` " +
			"(e.g. `_sip._tcp.example.com.`) — the DNS-standard form.\n\n" +
			"**Note:** each `(domain, name, type)` must be managed by exactly one `vcp_dns_record_set`; creating " +
			"a set that already exists (e.g. made outside Terraform) fails with an import hint. `ttl` is sticky: " +
			"once set it is not reset to the API default by removing it from the config. `TXT` values are stored " +
			"in presentation form (wrapped in quotes) by the backend; very long (>255-byte) TXT strings may not " +
			"round-trip cleanly.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Record set identifier (`domain/name/type`).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"domain": schema.StringAttribute{
				MarkdownDescription: "Zone this record set belongs to, e.g. `example.com.`. Case and the trailing " +
					"dot are not significant. Changing this forces a new resource.",
				CustomType:    FQDNType{},
				Required:      true,
				PlanModifiers: forceNewStr,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Record set name (FQDN), e.g. `www.example.com.`. Case and the trailing dot " +
					"are not significant. Changing this forces a new resource.",
				CustomType:    FQDNType{},
				Required:      true,
				PlanModifiers: forceNewStr,
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Record type. Changing this forces a new resource.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(recordTypes...)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ttl": schema.StringAttribute{
				MarkdownDescription: "TTL shared by the whole record set. One of: " + strings.Join(ttlValues, ", ") + ". " +
					"If omitted, the API default is used.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.String{stringvalidator.OneOf(ttlValues...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"values": schema.SetNestedAttribute{
				MarkdownDescription: "Values (rdata) of the record set. At least one is required. Fields used depend on `type`.",
				Required:            true,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"ip":               schema.StringAttribute{Optional: true, MarkdownDescription: "IPv4/IPv6 address (A/AAAA)."},
						"mail_host":        schema.StringAttribute{Optional: true, MarkdownDescription: "Mail host (MX)."},
						"priority":         schema.Int64Attribute{Optional: true, MarkdownDescription: "Priority (MX/SRV), 0-65535.", Validators: []validator.Int64{int64validator.Between(0, 65535)}},
						"canonical_name":   schema.StringAttribute{Optional: true, MarkdownDescription: "Canonical name (CNAME)."},
						"name_server_host": schema.StringAttribute{Optional: true, MarkdownDescription: "Name server host (NS)."},
						"text":             schema.StringAttribute{Optional: true, MarkdownDescription: "Text (TXT)."},
						"weight":           schema.Int64Attribute{Optional: true, MarkdownDescription: "Weight (SRV), 0-65535.", Validators: []validator.Int64{int64validator.Between(0, 65535)}},
						"port":             schema.Int64Attribute{Optional: true, MarkdownDescription: "Port (SRV), 0-65535.", Validators: []validator.Int64{int64validator.Between(0, 65535)}},
						"target":           schema.StringAttribute{Optional: true, MarkdownDescription: "Target (SRV)."},
					},
				},
			},
		},
	}
}

func (r *recordSetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData),
		)
		return
	}
	r.client = client
}

// ValidateConfig gives plan-time errors for per-type required fields.
func (r *recordSetResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg recordSetModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Type.IsNull() || cfg.Type.IsUnknown() {
		return
	}
	rtype := cfg.Type.ValueString()

	// name must be a full FQDN within its zone.
	if !cfg.Domain.IsNull() && !cfg.Domain.IsUnknown() && !cfg.Name.IsNull() && !cfg.Name.IsUnknown() {
		if !nameInZone(cfg.Name.ValueString(), cfg.Domain.ValueString()) {
			resp.Diagnostics.AddError(
				"Record name outside its zone",
				fmt.Sprintf("`name` %q must equal `domain` %q or be a subdomain of it (use a full FQDN).",
					normalizeName(cfg.Name.ValueString()), normalizeName(cfg.Domain.ValueString())),
			)
		}
	}

	// SRV encodes service+protocol in the name: _service._proto.zone.
	if rtype == "SRV" && !cfg.Name.IsNull() && !cfg.Name.IsUnknown() {
		if _, _, _, ok := parseSRVName(cfg.Name.ValueString()); !ok {
			resp.Diagnostics.AddError(
				"Invalid SRV name",
				"SRV `name` must be of the form _service._proto.zone. (e.g. _sip._tcp.example.com.).",
			)
		}
	}

	if cfg.Values.IsNull() || cfg.Values.IsUnknown() {
		return
	}
	var values []dnsValueModel
	resp.Diagnostics.Append(cfg.Values.ElementsAs(ctx, &values, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if rtype == "CNAME" && len(values) > 1 {
		resp.Diagnostics.AddError("Invalid CNAME record set", "a CNAME record set must contain exactly one value.")
	}

	allowed := allowedValueFields[rtype]
	for _, v := range values {
		// Missing required rdata for the type.
		var missing []string
		for _, f := range allowed {
			if v.fieldIsNull(f) {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			resp.Diagnostics.AddError(
				"Missing rdata for record type",
				fmt.Sprintf("%s record set values require %s.", rtype, strings.Join(missing, ", ")),
			)
			return
		}

		// Stray fields from another type: silently dropping them would make the config lie.
		for _, f := range valueFieldNames {
			if !slices.Contains(allowed, f) && !v.fieldIsNull(f) {
				resp.Diagnostics.AddError(
					"Field not applicable to record type",
					fmt.Sprintf("`%s` is not used by %s record sets (allowed fields: %s).",
						f, rtype, strings.Join(allowed, ", ")),
				)
				return
			}
		}

		// UX warnings for likely mistakes (non-blocking).
		for _, f := range hostnameValueFields {
			if slices.Contains(allowed, f) && !v.fieldIsNull(f) {
				h := strings.TrimSuffix(v.fieldString(f), ".")
				if h != "" && !strings.Contains(h, ".") {
					resp.Diagnostics.AddWarning(
						"Hostname looks relative",
						fmt.Sprintf("`%s = %q` will be treated as the absolute name %q, not as a name inside the zone. "+
							"Use a full FQDN (e.g. %q).", f, v.fieldString(f), h+".", h+".<zone>."),
					)
				}
			}
		}
		if rtype == "TXT" && !v.Text.IsNull() {
			t := v.Text.ValueString()
			if len(t) >= 2 && strings.HasPrefix(t, `"`) && strings.HasSuffix(t, `"`) {
				resp.Diagnostics.AddWarning(
					"TXT value is pre-quoted",
					"The backend stores TXT values in presentation form (quoted) itself — do not wrap `text` in quotes, or the value will round-trip inconsistently.",
				)
			}
		}
	}

	// Two textually different but semantically equal values (e.g. a TXT with
	// and without the surrounding quotes, or hosts differing only by case or
	// trailing dot) are distinct Set elements but the same record to the
	// backend — creating both would duplicate or fail mid-apply.
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		unknownField := false
		for _, f := range allowed {
			if v.fieldIsUnknown(f) {
				unknownField = true
				break
			}
		}
		if unknownField {
			continue // comparable only at apply time
		}
		key := valueSemKey(rtype, v)
		if seen[key] {
			resp.Diagnostics.AddError(
				"Duplicate record value",
				fmt.Sprintf("Two `values` entries are semantically identical (%s); remove one of them.", key),
			)
			return
		}
		seen[key] = true
	}
}

// recordTypes are the record set types supported by the backend.
var recordTypes = []string{"A", "AAAA", "MX", "CNAME", "NS", "TXT", "SRV"}

// Allowed value fields by record type. The order is used in error messages.
var allowedValueFields = map[string][]string{
	"A":     {"ip"},
	"AAAA":  {"ip"},
	"MX":    {"mail_host", "priority"},
	"CNAME": {"canonical_name"},
	"NS":    {"name_server_host"},
	"TXT":   {"text"},
	"SRV":   {"priority", "weight", "port", "target"},
}

var valueFieldNames = []string{"ip", "mail_host", "priority", "canonical_name", "name_server_host", "text", "weight", "port", "target"}

// hostnameValueFields — rdata fields containing hostnames (for warning about relative names).
var hostnameValueFields = []string{"mail_host", "canonical_name", "name_server_host", "target"}

func (r *recordSetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan recordSetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var values []dnsValueModel
	resp.Diagnostics.Append(plan.Values.ElementsAs(ctx, &values, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	domain := normalizeName(plan.Domain.ValueString())
	name := normalizeName(plan.Name.ValueString())
	rtype := plan.Type.ValueString()

	// Pre-flight: refuse to adopt an already-existing record set (created outside
	// Terraform or by another resource) — otherwise we would silently merge with it
	// and fail with a confusing "inconsistent result". Mirrors AWS's default behavior.
	if existing, err := r.client.GetDomainRecords(ctx, domain); err == nil {
		if _, found := filterRRset(existing, name, rtype); found {
			resp.Diagnostics.AddError(
				"DNS Record Set Already Exists",
				fmt.Sprintf("A %s record set named %q already exists in zone %q. "+
					"Import it instead: terraform import <address> '%s/%s/%s'. "+
					"(If a previous apply just failed, its records may still be deleting — retry shortly.)",
					rtype, name, domain, domain, name, rtype),
			)
			return
		}
	}

	var createdIDs []int
	for _, v := range values {
		rec, err := r.client.CreateDomainRecordAndWait(ctx, domain, r.buildCreateReq(plan, v))
		if err != nil {
			// Best-effort rollback: remove records already created for this set so a
			// failed apply doesn't leak orphans that later become duplicates.
			for _, id := range createdIDs {
				if delErr := r.client.DeleteDomainRecord(ctx, domain, id); delErr != nil {
					tflog.Warn(ctx, "rollback delete failed", map[string]any{"id": id, "error": delErr.Error()})
				}
			}
			resp.Diagnostics.AddError(
				"Error Creating DNS Record",
				fmt.Sprintf("Could not create %s record in %s: %s", plan.Type.ValueString(), domain, err.Error()),
			)
			return
		}
		createdIDs = append(createdIDs, rec.ID)
	}

	state, notFound, diags := r.buildState(ctx, plan, values)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if notFound {
		resp.Diagnostics.AddError("Record Set Not Found After Create", "the record set could not be read back after creation.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *recordSetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior recordSetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var priorValues []dnsValueModel
	resp.Diagnostics.Append(prior.Values.ElementsAs(ctx, &priorValues, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, notFound, diags := r.buildState(ctx, prior, priorValues)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if notFound {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *recordSetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state recordSetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var planValues []dnsValueModel
	resp.Diagnostics.Append(plan.Values.ElementsAs(ctx, &planValues, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	domain := normalizeName(plan.Domain.ValueString())
	name := normalizeName(plan.Name.ValueString())
	rtype := plan.Type.ValueString()

	current, err := r.client.GetDomainRecords(ctx, domain)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Record Set For Update", err.Error())
		return
	}
	rs, _ := filterRRset(current, name, rtype)
	toCreate, toDeleteIDs := diffRRset(rtype, planValues, rs.records)

	// Single-value replacement → one atomic PUT on the existing record instead of
	// create+delete. This is mandatory for CNAME: the API rejects a second CNAME for the same name
	// (-5542 Record already exists), so create-before-delete is not possible there;
	// for other types this is simply faster and avoids a transitional window with two responses.
	if len(toCreate) == 1 && len(toDeleteIDs) == 1 {
		ttl := rs.ttl
		if !plan.TTL.IsNull() && !plan.TTL.IsUnknown() {
			ttl = plan.TTL.ValueString()
		}
		if _, err := r.client.UpdateDomainRecordAndWait(ctx, domain, toDeleteIDs[0], r.buildUpdateReq(plan, toCreate[0], ttl)); err != nil {
			resp.Diagnostics.AddError("Error Replacing DNS Record Value", err.Error())
			return
		}
		toCreate, toDeleteIDs = nil, nil
	}

	// Add values that are not present yet.
	for _, v := range toCreate {
		if _, err := r.client.CreateDomainRecordAndWait(ctx, domain, r.buildCreateReq(plan, v)); err != nil {
			resp.Diagnostics.AddError("Error Adding DNS Record", err.Error())
			return
		}
	}

	// Remove values no longer desired.
	for _, id := range toDeleteIDs {
		if err := r.client.DeleteDomainRecord(ctx, domain, id); err != nil {
			resp.Diagnostics.AddError("Error Removing DNS Record", fmt.Sprintf("record %d: %s", id, err.Error()))
			return
		}
	}
	if len(toDeleteIDs) > 0 {
		if err := r.waitRecordsGone(ctx, domain, toDeleteIDs); err != nil {
			resp.Diagnostics.AddError("Error Waiting For DNS Record Removal", err.Error())
			return
		}
	}

	// TTL: if explicitly set and the set doesn't reflect it yet, update one record
	// (PowerDNS propagates the TTL to the whole RRset).
	if !plan.TTL.IsNull() && !plan.TTL.IsUnknown() {
		refetched, err := r.client.GetDomainRecords(ctx, domain)
		if err != nil {
			resp.Diagnostics.AddError("Error Reading Record Set For TTL Update", err.Error())
			return
		}
		cur, found := filterRRset(refetched, name, rtype)
		if found && cur.ttl != plan.TTL.ValueString() {
			one := cur.records[0]
			if _, err := r.client.UpdateDomainRecordAndWait(ctx, domain, one.ID, recordToUpdateReq(one, plan.TTL.ValueString())); err != nil {
				resp.Diagnostics.AddError("Error Updating DNS Record Set TTL", err.Error())
				return
			}
		}
	}

	newState, notFound, diags := r.buildState(ctx, plan, planValues)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if notFound {
		resp.Diagnostics.AddError("Record Set Not Found After Update", "the record set could not be read back after update.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *recordSetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state recordSetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	domain := normalizeName(state.Domain.ValueString())
	name := normalizeName(state.Name.ValueString())
	rtype := state.Type.ValueString()

	records, err := r.client.GetDomainRecords(ctx, domain)
	if err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Reading Record Set For Delete", err.Error())
		return
	}
	rs, found := filterRRset(records, name, rtype)
	if !found {
		return
	}

	var ids []int
	for _, rec := range rs.records {
		if err := r.client.DeleteDomainRecord(ctx, domain, rec.ID); err != nil && !sdk.IsNotFound(err) {
			resp.Diagnostics.AddError("Error Deleting DNS Record", fmt.Sprintf("record %d: %s", rec.ID, err.Error()))
			return
		}
		ids = append(ids, rec.ID)
	}
	if err := r.waitRecordsGone(ctx, domain, ids); err != nil {
		resp.Diagnostics.AddError("Error Waiting For DNS Record Set Deletion", err.Error())
	}
}

func (r *recordSetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected import ID in the form 'domain/name/type', got: %q", req.ID),
		)
		return
	}
	domain := normalizeName(parts[0])
	name := normalizeName(parts[1])
	// Normalize the type: a lowercase or unknown type would pass import and
	// then surface as a confusing "Cannot import non-existent remote object".
	rtype := strings.ToUpper(parts[2])
	if !slices.Contains(recordTypes, rtype) {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Unsupported record type %q; expected one of: %s.", parts[2], strings.Join(recordTypes, ", ")),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), fmt.Sprintf("%s/%s/%s", domain, name, rtype))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), domain)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("type"), rtype)...)
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildState reads the RRset back from the API and builds the resource model,
// preserving the caller's value strings when semantically equal (avoids diffs
// from API canonicalization, e.g. trailing dots).
func (r *recordSetResource) buildState(ctx context.Context, base recordSetModel, priorValues []dnsValueModel) (recordSetModel, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	domain := normalizeName(base.Domain.ValueString())
	name := normalizeName(base.Name.ValueString())
	rtype := base.Type.ValueString()

	records, err := r.client.GetDomainRecords(ctx, domain)
	if err != nil {
		if sdk.IsNotFound(err) {
			return recordSetModel{}, true, diags
		}
		diags.AddError("Error Reading DNS Record Set", fmt.Sprintf("Could not read records for %s: %s", domain, err.Error()))
		return recordSetModel{}, false, diags
	}
	rs, found := filterRRset(records, name, rtype)
	if !found {
		return recordSetModel{}, true, diags
	}

	// Preserve prior value objects when the semantic key matches.
	priorByKey := make(map[string]attr.Value, len(priorValues))
	for _, v := range priorValues {
		priorByKey[valueSemKey(rtype, v)] = valueModelToObject(v)
	}
	vals := make([]attr.Value, 0, len(rs.records))
	for _, rec := range rs.records {
		if obj, ok := priorByKey[recordSemKey(rtype, rec)]; ok {
			vals = append(vals, obj)
		} else {
			vals = append(vals, recordToValueObject(rec))
		}
	}

	state := recordSetModel{
		ID:     types.StringValue(fmt.Sprintf("%s/%s/%s", domain, name, rtype)),
		Domain: NewFQDNValue(domain),
		Name:   NewFQDNValue(name),
		Type:   types.StringValue(rtype),
		TTL:    types.StringValue(rs.ttl),
		Values: types.SetValueMust(dnsValueObjectType, vals),
	}
	return state, false, diags
}

func (r *recordSetResource) buildCreateReq(plan recordSetModel, v dnsValueModel) *entities.CreateRecordRequest {
	rtype := plan.Type.ValueString()
	req := &entities.CreateRecordRequest{
		Name: normalizeName(plan.Name.ValueString()),
		Type: entities.RecordType(rtype),
	}
	if !plan.TTL.IsNull() && !plan.TTL.IsUnknown() {
		req.TTL = entities.TTL(plan.TTL.ValueString())
	}

	// Set only the rdata fields relevant to the type — never forward stray attributes
	// (e.g. a mistaken mail_host on an A value).
	switch rtype {
	case "A", "AAAA":
		req.IP = v.IP.ValueString()
	case "MX":
		req.MailHost = normalizeName(v.MailHost.ValueString())
		if !v.Priority.IsNull() {
			p := int(v.Priority.ValueInt64())
			req.Priority = &p
		}
	case "CNAME":
		req.CanonicalName = normalizeName(v.CanonicalName.ValueString())
	case "NS":
		req.NameServerHost = normalizeName(v.NameServerHost.ValueString())
	case "TXT":
		req.Text = v.Text.ValueString()
	case "SRV":
		// service/protocol are encoded in the DNS-standard full name
		// (_service._proto.zone.) — the SDK derives them and sends the base name.
		if !v.Priority.IsNull() {
			p := int(v.Priority.ValueInt64())
			req.Priority = &p
		}
		if !v.Weight.IsNull() {
			w := int(v.Weight.ValueInt64())
			req.Weight = &w
		}
		if !v.Port.IsNull() {
			p := int(v.Port.ValueInt64())
			req.Port = &p
		}
		req.Target = normalizeName(v.Target.ValueString())
	}
	return req
}

// buildUpdateReq builds a PUT request to replace the rdata of one record in the set. It reuses
// buildCreateReq so that all type-specific logic (including the base name for SRV) lives in one place.
func (r *recordSetResource) buildUpdateReq(plan recordSetModel, v dnsValueModel, ttl string) *entities.UpdateRecordRequest {
	c := r.buildCreateReq(plan, v)
	return &entities.UpdateRecordRequest{
		Name:           c.Name,
		Type:           c.Type,
		TTL:            entities.TTL(ttl),
		IP:             c.IP,
		MailHost:       c.MailHost,
		Priority:       c.Priority,
		CanonicalName:  c.CanonicalName,
		NameServerHost: c.NameServerHost,
		Text:           c.Text,
		Protocol:       c.Protocol,
		Service:        c.Service,
		Weight:         c.Weight,
		Port:           c.Port,
		Target:         c.Target,
	}
}

// waitRecordsGone polls until none of the given record IDs remain (delete is async).
func (r *recordSetResource) waitRecordsGone(ctx context.Context, domain string, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	want := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		records, err := r.client.GetDomainRecords(ctx, domain)
		if err != nil && !sdk.IsNotFound(err) {
			// Transient list error: an empty result would be a false "all gone" —
			// skip this round and retry on the next tick.
			tflog.Warn(ctx, "poll records error", map[string]any{"error": err.Error()})
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
			continue
		}
		remaining := false
		for _, rec := range records {
			if _, ok := want[rec.ID]; ok {
				remaining = true
				break
			}
		}
		if !remaining {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// recordToUpdateReq builds a PUT request from a record that was read (used for
// propagating the TTL to the set). The SDK itself normalizes the full SRV name to the base name.
func recordToUpdateReq(r entities.DNSRecord, ttl string) *entities.UpdateRecordRequest {
	return &entities.UpdateRecordRequest{
		Name:           r.Name,
		Type:           r.Type,
		TTL:            entities.TTL(ttl),
		IP:             r.IP,
		MailHost:       r.MailHost,
		Priority:       r.Priority,
		CanonicalName:  r.CanonicalName,
		NameServerHost: r.NameServerHost,
		Text:           r.Text,
		Protocol:       r.Protocol,
		Service:        r.Service,
		Weight:         r.Weight,
		Port:           r.Port,
		Target:         r.Target,
	}
}

func valueModelToObject(v dnsValueModel) attr.Value {
	return types.ObjectValueMust(dnsValueAttrTypes, map[string]attr.Value{
		"ip":               v.IP,
		"mail_host":        v.MailHost,
		"priority":         v.Priority,
		"canonical_name":   v.CanonicalName,
		"name_server_host": v.NameServerHost,
		"text":             v.Text,
		"weight":           v.Weight,
		"port":             v.Port,
		"target":           v.Target,
	})
}
