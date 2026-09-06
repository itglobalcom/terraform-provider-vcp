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
	_ resource.Resource                   = &edgeFirewallResource{}
	_ resource.ResourceWithConfigure      = &edgeFirewallResource{}
	_ resource.ResourceWithImportState    = &edgeFirewallResource{}
	_ resource.ResourceWithValidateConfig = &edgeFirewallResource{}
)

func NewEdgeFirewallResource() resource.Resource { return &edgeFirewallResource{} }

type edgeFirewallResource struct {
	client *sdk.CloudClient
}

func (r *edgeFirewallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_edge_firewall"
}

func (r *edgeFirewallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the firewall of the edge in front of a VMware Cloud network.\n\n" +
			"The API replaces the whole list on every change, so this resource owns **all** firewall rules of the " +
			"network's edge: declare at most ONE `vcp_vmware_edge_firewall` per network, and expect rules created in " +
			"the panel to be replaced on the first apply.\n\n" +
			"~> The firewall has no `enabled` switch here — it is on for as long as this resource exists. Creating it " +
			"turns the firewall on, destroying it turns it off and clears the rules. To stop managing the rules " +
			"without changing them, drop the resource from state " +
			"(`terraform state rm vcp_vmware_edge_firewall.<name>`) instead of destroying it.\n\n" +
			"~> `default_action` is what applies to traffic no rule matches: `deny` makes the list a whitelist, " +
			"`allow` makes it a blacklist.\n\n" +
			"~> A rule is always applied enabled, and its description is derived by the platform from `name`. The API " +
			"accepts `enabled` and `description` on a rule and then ignores both (defect NET-7), so this resource " +
			"does not offer fields that would promise a round trip the platform does not perform.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "Same as `network_id` — one firewall per network.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"network_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the network whose edge firewall is managed. Only a `routed` or `public` " +
					"network has an edge. Changing this forces a new resource.",
				Required:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"default_action": schema.StringAttribute{
				MarkdownDescription: "What happens to traffic no rule matches: `deny` or `allow`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(edgeFirewallActions...)},
			},
			"rules": schema.ListNestedAttribute{
				MarkdownDescription: "The complete list of firewall rules, in the order they are applied. An empty " +
					"list leaves the firewall on with `default_action` as its only behaviour.",
				Required: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							MarkdownDescription: "Name of the rule. The platform also derives the rule's description from it.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
						},
						"action": schema.StringAttribute{
							MarkdownDescription: "`allow` passes matching packets, `deny` drops them.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(edgeFirewallActions...)},
						},
						"protocol": schema.StringAttribute{
							MarkdownDescription: "Protocol: `tcp`, `udp`, `icmp` or `any`.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(ruleProtocols...)},
						},
						"source": schema.StringAttribute{
							MarkdownDescription: "Source: `any`, an address (`10.0.0.5`), a network (`10.0.0.0/24`) or " +
								"a range (`10.0.0.5-10.0.0.9`). Defaults to `any`.",
							Optional:   true,
							Computed:   true,
							Validators: []validator.String{vmwareRuleAddress()},
						},
						"source_port": schema.StringAttribute{
							MarkdownDescription: "Source port: `any`, a port (`443`), a range (`1000-2000`) or a " +
								"comma-separated list. Defaults to `any`. An edge backed by NSX-T accepts " +
								"only `any` here, whatever the protocol; one backed by NSX-V accepts a value " +
								"for every protocol except `icmp` and `any`.",
							Optional:   true,
							Computed:   true,
							Validators: []validator.String{vmwareRulePort()},
						},
						"destination": schema.StringAttribute{
							MarkdownDescription: "Destination, in the same forms as `source`. For traffic published " +
								"with a DNAT rule this is already the *private* address of the server. Defaults to `any`.",
							Optional:   true,
							Computed:   true,
							Validators: []validator.String{vmwareRuleAddress()},
						},
						"destination_port": schema.StringAttribute{
							MarkdownDescription: "Destination port, in the same forms as `source_port`. Defaults to `any`.",
							Optional:            true,
							Computed:            true,
							Validators:          []validator.String{vmwareRulePort()},
						},
					},
				},
			},
		},
	}
}

func (r *edgeFirewallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *edgeFirewallResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	rules, ok := configRules[edgeFirewallRuleModel](ctx, req.Config)
	if !ok {
		return
	}
	validateEdgeFirewallRules(rules, &resp.Diagnostics)
}

func (r *edgeFirewallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan edgeFirewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(plan.NetworkID.ValueInt64())
	if !checkEdgeCapableNetwork(ctx, r.client, networkID, "the edge firewall",
		[]string{networkTypeRouted, networkTypePublic}, &resp.Diagnostics) {
		return
	}

	defer locks.VmwareNetwork(networkID)()

	// Adopting an edge that already has rules replaces them — the API cannot merge.
	// Say so instead of silently overwriting someone else's rules.
	if existing, err := r.client.GetVmwareEdgeFirewall(ctx, networkID); err != nil {
		tflog.Warn(ctx, "Could not read existing edge firewall rules before applying", map[string]any{
			"network_id": networkID, "error": err.Error(),
		})
	} else if len(existing.Rules) > 0 {
		resp.Diagnostics.AddWarning("Existing Edge Firewall Rules Replaced",
			fmt.Sprintf("The edge of network %d already had %d firewall rule(s); they have been replaced by this "+
				"resource. To keep managing pre-existing rules instead, run "+
				"terraform import vcp_vmware_edge_firewall.<name> %d.", networkID, len(existing.Rules), networkID))
	}

	r.applyAndPersist(ctx, plan, true, &resp.State, &resp.Diagnostics)
}

func (r *edgeFirewallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state edgeFirewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(state.NetworkID.ValueInt64())
	firewall, err := r.client.GetVmwareEdgeFirewall(ctx, networkID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading VMware Edge Firewall",
			fmt.Sprintf("Could not read the edge firewall of network %d: %s", networkID, err.Error()))
		return
	}

	// The firewall has no `enabled` attribute — existence of the resource is what
	// says it should be on. A firewall switched off elsewhere is therefore drift
	// Terraform cannot see in a plan, so report it here, where the read happens.
	if firewall.Enabled != nil && !*firewall.Enabled {
		resp.Diagnostics.AddWarning("Edge Firewall Disabled Outside Terraform",
			fmt.Sprintf("The edge firewall of network %d is switched off, but its rules are still managed here. "+
				"Terraform cannot see this as a change because the resource has no enabled attribute — run "+
				"terraform apply -replace=vcp_vmware_edge_firewall.<name> to switch it back on.", networkID))
	}

	state.ID = types.Int64Value(int64(networkID))
	state.DefaultAction = fromStringPtr(firewall.DefaultAction)
	state.Rules = flattenEdgeFirewallRules(firewall.Rules)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *edgeFirewallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan edgeFirewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	defer locks.VmwareNetwork(int(plan.NetworkID.ValueInt64()))()
	r.applyAndPersist(ctx, plan, true, &resp.State, &resp.Diagnostics)
}

// Delete switches the firewall off and clears its rules. The resource owns the
// whole set, so destroying it returns the edge to the state a new network starts
// in — one step, like any other resource. Keeping the rules while giving up
// management is what `terraform state rm` is for.
func (r *edgeFirewallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state edgeFirewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(state.NetworkID.ValueInt64())
	defer locks.VmwareNetwork(networkID)()

	// Nothing to do if the network (and with it the edge) is already gone.
	if _, err := r.client.GetVmwareEdgeFirewall(ctx, networkID); err != nil && sdk.IsNotFound(err) {
		tflog.Info(ctx, "VMware network already deleted, no edge firewall to clear",
			map[string]any{"network_id": networkID})
		return
	}

	// The rules go, the firewall stays on: an edge backed by NSX-T refuses to be
	// switched off at all ("The firewall cannot be disabled"), which used to make
	// destroy impossible there. Leaving it on is also the safer of the two — the
	// edge keeps filtering by its default action instead of passing everything.
	tflog.Info(ctx, "Clearing the rules of the VMware edge firewall",
		map[string]any{"network_id": networkID, "rules": len(state.Rules)})
	r.apply(ctx, networkID, nil, nil, true, &resp.Diagnostics)
}

// ImportState takes the network id; Read fills in default_action and the rules.
func (r *edgeFirewallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	networkID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || networkID <= 0 {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected the numeric id of the network whose edge firewall is imported, got: %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("network_id"), networkID)...)
}

// apply PUTs the whole rule set and waits for the backend task, returning the
// configuration the API reports afterwards (nil with a diagnostic on failure).
//
// Retrying happens one level down: the SDK's HTTP layer already re-sends a
// request the API rejected as a competitive change. Repeating that here would
// only make a failing apply hang instead of telling the user what to do.
func (r *edgeFirewallResource) apply(ctx context.Context, networkID int, defaultAction *string,
	rules []edgeFirewallRuleModel, enabled bool, diags *diag.Diagnostics) *entities.VmwareEdgeFirewall {
	payload := expandEdgeFirewallRules(rules)
	tflog.Info(ctx, "Applying VMware edge firewall rules", map[string]any{
		"network_id": networkID, "rules": len(payload), "enabled": enabled,
	})

	firewall, err := r.client.UpdateVmwareEdgeFirewallAndWait(ctx, networkID, &entities.VmwareUpdateEdgeFirewallRequest{
		Enabled:       &enabled,
		DefaultAction: defaultAction,
		Rules:         payload,
	})
	if err == nil {
		return firewall
	}

	diags.AddError("Error Applying VMware Edge Firewall Rules",
		fmt.Sprintf("Could not apply %d firewall rule(s) to the edge of network %d: %s\n\n%s",
			len(payload), networkID, err.Error(), applyFailureHint(err, "edge firewall")))
	return nil
}

// applyAndPersist writes the plan and records what the API reports back. The
// applied configuration is what goes into state — Optional+Computed attributes a
// rule left out (source, ports) come back filled in, and storing the plan instead
// would leave them unknown.
func (r *edgeFirewallResource) applyAndPersist(ctx context.Context, plan edgeFirewallModel, enabled bool,
	state *tfsdk.State, diags *diag.Diagnostics) {
	networkID := int(plan.NetworkID.ValueInt64())
	defaultAction := plan.DefaultAction.ValueString()

	firewall := r.apply(ctx, networkID, &defaultAction, plan.Rules, enabled, diags)
	if firewall == nil {
		return
	}

	out := plan
	out.ID = types.Int64Value(int64(networkID))
	out.DefaultAction = preferPlanned(fromStringPtr(firewall.DefaultAction), plan.DefaultAction)
	out.Rules = mergeEdgeFirewallRules(plan.Rules, flattenEdgeFirewallRules(firewall.Rules))
	diags.Append(state.Set(ctx, out)...)
}
