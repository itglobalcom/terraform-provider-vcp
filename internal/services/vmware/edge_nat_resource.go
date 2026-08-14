package vmware

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                   = &edgeNATResource{}
	_ resource.ResourceWithConfigure      = &edgeNATResource{}
	_ resource.ResourceWithImportState    = &edgeNATResource{}
	_ resource.ResourceWithValidateConfig = &edgeNATResource{}
)

func NewEdgeNATResource() resource.Resource { return &edgeNATResource{} }

type edgeNATResource struct {
	client *sdk.CloudClient
}

func (r *edgeNATResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_edge_nat"
}

func (r *edgeNATResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the NAT rules of the edge in front of a routed VMware Cloud network.\n\n" +
			"This resource owns **all** NAT rules of the network's edge: declare at most ONE `vcp_vmware_edge_nat` " +
			"per network, and expect rules created in the panel to be replaced on the first apply.\n\n" +
			"~> Unlike the firewall, NAT has no bulk endpoint — the provider applies the list rule by rule and " +
			"matches configuration entries to existing rules by position. A change therefore costs one backend task " +
			"per rule it touches, and a failure half-way through leaves the edge partly updated: the provider then " +
			"records what the API actually holds and names the rule it stopped at, so the next apply carries on.\n\n" +
			"~> The order of the list is not a priority — the API does not let a client order NAT rules — but it is " +
			"what pairs an entry with the rule the platform already holds. Appending is therefore cheap, while " +
			"inserting in the middle rewrites every rule below the insertion point.\n\n" +
			"~> Destroying this resource deletes the edge's NAT rules. Note that this also removes whatever those " +
			"rules provided — an SNAT rule is what gives the network outbound access. To stop managing the rules " +
			"without changing them, drop the resource from state " +
			"(`terraform state rm vcp_vmware_edge_nat.<name>`) instead of destroying it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "Same as `network_id` — one rule set per network.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"network_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the network whose edge NAT rules are managed. NAT applies to `routed` " +
					"networks only. Changing this forces a new resource.",
				Required:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"rules": schema.ListNestedAttribute{
				MarkdownDescription: "The complete list of NAT rules. An empty list removes every NAT rule from the edge.",
				Required:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							MarkdownDescription: "`dnat` translates an inbound connection to an address inside the " +
								"network (publishes a service); `snat` translates traffic leaving the network.",
							Required:   true,
							Validators: []validator.String{stringvalidator.OneOf(edgeNATTypes...)},
						},
						"protocol": schema.StringAttribute{
							MarkdownDescription: "Protocol: `tcp`, `udp`, `icmp` or `any`.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(ruleProtocols...)},
						},
						"description": schema.StringAttribute{
							MarkdownDescription: "Free-text description of the rule.",
							Optional:            true,
							Computed:            true,
						},
						"original_ip": schema.StringAttribute{
							MarkdownDescription: "The address traffic is translated **from**. For `snat` this is the " +
								"private address or subnet being translated and it is required. For `dnat` it must be " +
								"left unset: the platform always translates from the external address of the edge and " +
								"replaces any value sent (API defect NET-4); the address it chose appears here after apply.",
							Optional:   true,
							Computed:   true,
							Validators: []validator.String{vmwareRuleAddress()},
						},
						"original_port": schema.StringAttribute{
							MarkdownDescription: "Port on the original side: `any`, a port (`443`) or a range " +
								"(`1000-2000`). For `dnat` this is the port published to the outside.",
							Optional:   true,
							Computed:   true,
							Validators: []validator.String{vmwareRulePort()},
						},
						"translated_ip": schema.StringAttribute{
							MarkdownDescription: "The address traffic is translated **to**. For `dnat` this is the " +
								"private address of the target server and it is required. For `snat` it is the external " +
								"address of the edge; leave it unset to let the platform fill it in.",
							Optional:   true,
							Computed:   true,
							Validators: []validator.String{vmwareRuleAddress()},
						},
						"translated_port": schema.StringAttribute{
							MarkdownDescription: "Port after translation, in the same forms as `original_port`.",
							Optional:            true,
							Computed:            true,
							Validators:          []validator.String{vmwareRulePort()},
						},
						"enabled": schema.BoolAttribute{
							MarkdownDescription: "Whether the rule passes traffic. Rules are created enabled; set " +
								"`false` to keep a rule in the configuration without applying it.",
							Optional: true,
							Computed: true,
						},
					},
				},
			},
		},
	}
}

func (r *edgeNATResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

// ValidateConfig reports what each rule type needs (and what it must not carry)
// at plan time instead of half-way through a rule-by-rule apply.
func (r *edgeNATResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	rules, ok := configRules[edgeNATRuleModel](ctx, req.Config)
	if !ok {
		return
	}
	validateEdgeNATRules(rules, &resp.Diagnostics)
}

func (r *edgeNATResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan edgeNATModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(plan.NetworkID.ValueInt64())
	if !checkEdgeCapableNetwork(ctx, r.client, networkID, "edge NAT",
		[]string{networkTypeRouted}, &resp.Diagnostics) {
		return
	}

	defer locks.VmwareNetwork(networkID)()

	// Adopting an edge that already has rules rewrites them position by position.
	// Say so instead of silently overwriting someone else's rules.
	if existing, err := r.client.GetVmwareEdgeNAT(ctx, networkID); err != nil {
		tflog.Warn(ctx, "Could not read existing edge NAT rules before applying", map[string]any{
			"network_id": networkID, "error": err.Error(),
		})
	} else if len(existing.Rules) > 0 {
		resp.Diagnostics.AddWarning("Existing Edge NAT Rules Replaced",
			fmt.Sprintf("The edge of network %d already had %d NAT rule(s); they have been replaced by this "+
				"resource. To keep managing pre-existing rules instead, run "+
				"terraform import vcp_vmware_edge_nat.<name> %d.", networkID, len(existing.Rules), networkID))
	}

	r.applyAndPersist(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *edgeNATResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state edgeNATModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(state.NetworkID.ValueInt64())
	nat, err := r.client.GetVmwareEdgeNAT(ctx, networkID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading VMware Edge NAT Rules",
			fmt.Sprintf("Could not read the edge NAT rules of network %d: %s", networkID, err.Error()))
		return
	}

	state.ID = types.Int64Value(int64(networkID))
	state.Rules = flattenEdgeNATRules(nat.Rules)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *edgeNATResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan edgeNATModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	defer locks.VmwareNetwork(int(plan.NetworkID.ValueInt64()))()
	r.applyAndPersist(ctx, plan, &resp.State, &resp.Diagnostics)
}

// Delete removes every NAT rule of the edge — the resource owns the whole set, so
// destroying it returns the edge to the empty list a new network starts with.
func (r *edgeNATResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state edgeNATModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(state.NetworkID.ValueInt64())
	defer locks.VmwareNetwork(networkID)()

	nat, err := r.client.GetVmwareEdgeNAT(ctx, networkID)
	switch {
	case err != nil && sdk.IsNotFound(err):
		tflog.Info(ctx, "VMware network already deleted, no edge NAT rules to clear",
			map[string]any{"network_id": networkID})
		return
	case err != nil:
		resp.Diagnostics.AddError("Error Reading VMware Edge NAT Rules",
			fmt.Sprintf("Could not read the edge NAT rules of network %d before clearing them: %s", networkID, err.Error()))
		return
	case len(nat.Rules) == 0:
		tflog.Info(ctx, "VMware edge has no NAT rules, nothing to clear", map[string]any{"network_id": networkID})
		return
	}

	tflog.Info(ctx, "Clearing VMware edge NAT rules",
		map[string]any{"network_id": networkID, "rules": len(nat.Rules)})
	r.deleteRules(ctx, networkID, natRuleIDs(nat.Rules), &resp.Diagnostics)
}

// ImportState takes the network id; Read fills in the rules.
//
// The rules land in state in the order the API lists them, which is the order the
// configuration has to repeat — an entry is paired with an existing rule by
// position, so a differently ordered configuration rewrites rules instead of
// adopting them.
func (r *edgeNATResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	networkID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || networkID <= 0 {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected the numeric id of the network whose edge NAT rules are imported, got: %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("network_id"), networkID)...)
}

// reconcile brings the edge's rule set to `rules`. The API has no bulk endpoint,
// so the list is applied rule by rule: entry i overwrites the rule the API holds
// at position i, entries beyond that are created, and rules the configuration no
// longer has are deleted.
//
// It returns the rule set the API holds afterwards — also when a step failed, so
// the caller can record what was actually applied instead of leaving state empty.
// The second result reports whether the whole list went through.
func (r *edgeNATResource) reconcile(ctx context.Context, networkID int, rules []edgeNATRuleModel,
	diags *diag.Diagnostics) (*entities.VmwareEdgeNAT, bool) {
	current, err := r.client.GetVmwareEdgeNAT(ctx, networkID)
	if err != nil {
		diags.AddError("Error Reading VMware Edge NAT Rules",
			fmt.Sprintf("Could not read the current edge NAT rules of network %d, so none were applied: %s",
				networkID, err.Error()))
		return nil, false
	}

	// The pairing is worked out once, before any write: an upsert keeps a rule's
	// id, so the positions this snapshot describes stay valid for the whole pass.
	steps := planNATReconcile(natRuleIDs(current.Rules), len(rules))
	latest := current

	tflog.Info(ctx, "Applying VMware edge NAT rules", map[string]any{
		"network_id": networkID, "rules": len(rules), "existing": len(current.Rules),
		"deleting": len(steps.DeleteIDs),
	})

	for i, rule := range rules {
		applied, err := r.client.UpsertVmwareEdgeNATRuleAndWait(ctx, networkID, expandEdgeNATRule(rule, steps.UpsertIDs[i]))
		if err != nil {
			diags.AddError("Error Applying VMware Edge NAT Rules",
				fmt.Sprintf("Could not apply rules[%d] (%s) to the edge of network %d: %s\n\n%s\n\n"+
					"The rules before it were applied and are recorded in state; the resource is marked tainted, so "+
					"the next terraform apply carries on from here.",
					i, rule.Type.ValueString(), networkID, err.Error(), applyFailureHint(err, "edge NAT")))
			return r.refresh(ctx, networkID, latest), false
		}
		latest = applied
	}

	// Rules the configuration dropped. Only non-empty when the API held more rules
	// than the configuration declares, so no rule created above is in this range.
	if len(steps.DeleteIDs) > 0 {
		if !r.deleteRules(ctx, networkID, steps.DeleteIDs, diags) {
			return r.refresh(ctx, networkID, latest), false
		}
		latest = r.refresh(ctx, networkID, latest)
	}

	return latest, true
}

// warnNATOrderMismatch reports a rule set the API listed in an order other than
// the one it was written in.
//
// Pairing a configuration entry with an existing rule by position only works
// while the API keeps the order stable, which it has done in every observation so
// far — but nothing in the contract promises it. If it ever stops, the symptom is
// a plan that never settles: each apply rewrites the rules into the positions the
// previous one moved them out of. Saying so once is far better than leaving the
// user to work that out from a diff that keeps coming back.
func warnNATOrderMismatch(planned, fresh []edgeNATRuleModel, diags *diag.Diagnostics) {
	for i := range planned {
		if i >= len(fresh) {
			return
		}
		// Only attributes the configuration actually stated can be compared: the
		// rest are filled in by the platform.
		if isSet(planned[i].Type) && !planned[i].Type.Equal(fresh[i].Type) {
			natOrderWarning(i, diags)
			return
		}
		if isSet(planned[i].TranslatedIP) && !planned[i].TranslatedIP.Equal(fresh[i].TranslatedIP) {
			natOrderWarning(i, diags)
			return
		}
		if isSet(planned[i].OriginalPort) && !planned[i].OriginalPort.Equal(fresh[i].OriginalPort) {
			natOrderWarning(i, diags)
			return
		}
	}
}

func natOrderWarning(index int, diags *diag.Diagnostics) {
	diags.AddAttributeWarning(path.Root("rules"), "NAT Rules Are Not In The Order They Were Written",
		fmt.Sprintf("The API listed the rules in a different order than this configuration declares, from "+
			"rules[%d] on. Entries are paired with existing rules by position, so the next plan will show a "+
			"difference and the next apply will rewrite the rules to match the configuration again — which may "+
			"repeat. Reorder the configuration to match what the API reports to settle it.", index))
}

// deleteRules removes the given rules, reporting the first failure.
func (r *edgeNATResource) deleteRules(ctx context.Context, networkID int, ids []int, diags *diag.Diagnostics) bool {
	for _, id := range ids {
		if id == 0 {
			// A rule the API listed without an id cannot be addressed. Deleting the
			// others still leaves the edge closer to the configuration than bailing out.
			diags.AddWarning("Edge NAT Rule Cannot Be Removed",
				fmt.Sprintf("The API listed a NAT rule of network %d without an id, so it cannot be deleted. "+
					"Remove it from the panel.", networkID))
			continue
		}
		if err := r.client.DeleteVmwareEdgeNATRuleAndWait(ctx, networkID, id); err != nil {
			if sdk.IsNotFound(err) {
				continue
			}
			diags.AddError("Error Removing VMware Edge NAT Rule",
				fmt.Sprintf("Could not delete NAT rule %d of network %d: %s\n\n%s",
					id, networkID, err.Error(), applyFailureHint(err, "edge NAT")))
			return false
		}
	}
	return true
}

// refresh re-reads the rule set, falling back to the last known one when the read
// fails — it is only used on the error path, where a stale picture is still better
// than an empty one.
func (r *edgeNATResource) refresh(ctx context.Context, networkID int, fallback *entities.VmwareEdgeNAT) *entities.VmwareEdgeNAT {
	nat, err := r.client.GetVmwareEdgeNAT(ctx, networkID)
	if err != nil {
		tflog.Warn(ctx, "Could not re-read edge NAT rules after a failed change", map[string]any{
			"network_id": networkID, "error": err.Error(),
		})
		return fallback
	}
	return nat
}

// applyAndPersist reconciles the rule set and records what the API holds
// afterwards. On a partial failure the applied part is still written to state —
// the error taints the resource, so the next apply finishes the job instead of
// creating the same rules twice.
func (r *edgeNATResource) applyAndPersist(ctx context.Context, plan edgeNATModel, state *tfsdk.State, diags *diag.Diagnostics) {
	networkID := int(plan.NetworkID.ValueInt64())

	nat, _ := r.reconcile(ctx, networkID, plan.Rules, diags)
	if nat == nil {
		return
	}

	fresh := flattenEdgeNATRules(nat.Rules)
	warnNATOrderMismatch(plan.Rules, fresh, diags)

	out := plan
	out.ID = types.Int64Value(int64(networkID))
	out.Rules = mergeEdgeNATRules(plan.Rules, fresh)
	diags.Append(state.Set(ctx, out)...)
}
