package gateway_rules

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                   = &firewallResource{}
	_ resource.ResourceWithConfigure      = &firewallResource{}
	_ resource.ResourceWithImportState    = &firewallResource{}
	_ resource.ResourceWithValidateConfig = &firewallResource{}
)

func NewFirewallResource() resource.Resource { return &firewallResource{} }

type firewallResource struct {
	client *sdk.CloudClient
}

func (r *firewallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_firewall"
}

func (r *firewallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the firewall rules of an edge gateway.\n\n" +
			"The API replaces the whole list on every change, so this resource owns **all** firewall rules of the " +
			"gateway: declare at most ONE `vcp_gateway_firewall` per gateway, and expect rules created in the panel " +
			"to be replaced on the first apply.\n\n" +
			"~> **The order of the rules matters, and the last matching rule wins** — the opposite of the usual " +
			"first-match-wins. A catch-all rule therefore belongs at the *top* of the list: put `Deny` first and the " +
			"specific `Allow` rules below it.\n\n" +
			"~> Destroying this resource clears the gateway's firewall rules: it owns the whole list, so removing " +
			"it returns the gateway to the empty rule set a new gateway starts with. To stop managing the rules " +
			"without changing them, drop the resource from state " +
			"(`terraform state rm vcp_gateway_firewall.<name>`) instead of destroying it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `gateway_id` — one rule set per gateway.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"gateway_id": schema.StringAttribute{
				MarkdownDescription: "ID of the gateway whose firewall rules are managed. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"rules": schema.ListNestedAttribute{
				MarkdownDescription: "The complete list of firewall rules. Rules are evaluated in order and **the last " +
					"match wins**, so lower entries take precedence over higher ones. An empty list removes every " +
					"firewall rule from the gateway.",
				Required: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"action": schema.StringAttribute{
							MarkdownDescription: "`Allow` passes matching packets, `Deny` drops them.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(fwActions...)},
						},
						"direction": schema.StringAttribute{
							MarkdownDescription: "`In` for inbound packets, `Out` for outbound ones.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(fwDirections...)},
						},
						"protocol": schema.StringAttribute{
							MarkdownDescription: "Protocol: `TCP`, `UDP`, `ICMP` or `IP` (any protocol). " +
								"With `ICMP` and `IP` both ports must be `0`.",
							Required:   true,
							Validators: []validator.String{stringvalidator.OneOf(protocols...)},
						},
						"source": schema.StringAttribute{
							MarkdownDescription: "Source in CIDR notation, e.g. `10.0.0.5/32`, `10.0.0.0/24` or " +
								"`0.0.0.0/0` (any address).",
							Required:   true,
							Validators: []validator.String{cidrIPv4()},
						},
						"source_port": schema.Int64Attribute{
							MarkdownDescription: "Source port, `0` (the default) means any port. Must be `0` for `ICMP` and `IP`.",
							Optional:            true,
							Computed:            true,
							Default:             int64default.StaticInt64(0),
							Validators:          []validator.Int64{int64validator.Between(0, 65535)},
						},
						"destination": schema.StringAttribute{
							MarkdownDescription: "Destination in CIDR notation. Note that for traffic published with a " +
								"`DNAT` rule the destination is already the *private* address of the server.",
							Required:   true,
							Validators: []validator.String{cidrIPv4()},
						},
						"destination_port": schema.Int64Attribute{
							MarkdownDescription: "Destination port, `0` (the default) means any port. Must be `0` for `ICMP` and `IP`.",
							Optional:            true,
							Computed:            true,
							Default:             int64default.StaticInt64(0),
							Validators:          []validator.Int64{int64validator.Between(0, 65535)},
						},
					},
				},
			},
		},
	}
}

func (r *firewallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig reports the API's cross-field rules (ports that must be "any")
// at plan time instead of half-way through apply.
func (r *firewallResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	rules, ok := configRules[firewallRuleModel](ctx, req.Config)
	if !ok {
		return
	}
	validateFirewallRules(rules, &resp.Diagnostics)
}

func (r *firewallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := plan.GatewayID.ValueString()
	defer locks.Gateway(gwID)()

	// Adopting a gateway that already has rules replaces them — the API cannot
	// merge. Say so instead of silently overwriting someone else's rules.
	if existing, err := r.client.GetFirewallRules(ctx, gwID); err != nil {
		tflog.Warn(ctx, "Could not read existing firewall rules before applying", map[string]any{
			"gateway_id": gwID, "error": err.Error(),
		})
	} else if len(existing) > 0 {
		resp.Diagnostics.AddWarning("Existing Firewall Rules Replaced",
			fmt.Sprintf("Gateway %s already had %d firewall rule(s); they have been replaced by this resource. "+
				"To keep managing pre-existing rules instead, run terraform import vcp_gateway_firewall.<name> %s.",
				gwID, len(existing), gwID))
	}

	if !r.apply(ctx, gwID, plan.Rules, &resp.Diagnostics) {
		return
	}
	r.persist(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *firewallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := state.GatewayID.ValueString()
	rules, err := r.client.GetFirewallRules(ctx, gwID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Gateway Firewall Rules",
			fmt.Sprintf("Could not read the firewall rules of gateway %s: %s", gwID, err.Error()))
		return
	}

	state.ID = types.StringValue(gwID)
	state.Rules = flattenFirewallRules(rules)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *firewallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := plan.GatewayID.ValueString()
	defer locks.Gateway(gwID)()

	if !r.apply(ctx, gwID, plan.Rules, &resp.Diagnostics) {
		return
	}
	r.persist(ctx, plan, &resp.State, &resp.Diagnostics)
}

// Delete clears the rule set. The resource owns the whole list, so destroying it
// puts the gateway back to the empty list a new gateway starts with — one step,
// like any other resource. Keeping the rules while giving up management is what
// `terraform state rm` is for.
func (r *firewallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := state.GatewayID.ValueString()
	defer locks.Gateway(gwID)()

	// Nothing to do if the gateway is gone (it took its rules with it) or if the
	// list is already empty — an empty PUT would only spend a backend task.
	existing, err := r.client.GetFirewallRules(ctx, gwID)
	switch {
	case err != nil && sdk.IsNotFound(err):
		tflog.Info(ctx, "Gateway already deleted, no firewall rules to clear", map[string]any{"gateway_id": gwID})
		return
	case err == nil && len(existing) == 0:
		tflog.Info(ctx, "Gateway has no firewall rules, nothing to clear", map[string]any{"gateway_id": gwID})
		return
	}

	tflog.Info(ctx, "Clearing gateway firewall rules", map[string]any{"gateway_id": gwID, "rules": len(state.Rules)})
	r.apply(ctx, gwID, nil, &resp.Diagnostics)
}

// ImportState takes the gateway id; Read fills in the rules.
func (r *firewallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("gateway_id"), req, resp)
}

// apply PUTs the whole rule list and waits for the backend task. Returns false
// (with a diagnostic) if nothing was applied.
//
// Retrying happens one level down: the SDK already re-sends the (idempotent) list
// once if the backend task fails, and its HTTP layer spends about two minutes on
// "gateway is busy" before giving up. Repeating that here would only make a
// failing apply hang for ten minutes instead of telling the user what to do.
func (r *firewallResource) apply(ctx context.Context, gwID string, rules []firewallRuleModel, diags *diag.Diagnostics) bool {
	payload := expandFirewallRules(rules)
	tflog.Info(ctx, "Applying gateway firewall rules", map[string]any{"gateway_id": gwID, "rules": len(payload)})

	err := r.client.UpdateFirewallRulesAndWait(ctx, gwID, &entities.UpdateFirewallRulesRequest{FirewallRules: payload})
	if err == nil {
		return true
	}

	diags.AddError("Error Applying Gateway Firewall Rules",
		fmt.Sprintf("Could not apply %d firewall rule(s) to gateway %s: %s\n\n%s",
			len(payload), gwID, err.Error(), applyFailureHint(err)))
	return false
}

// persist reads the rules back and records them in state. If the read fails
// after a successful write, the plan is recorded and an error is added: the
// rules are applied, so state must not stay empty, and the error taints the
// resource so the next apply refreshes it.
func (r *firewallResource) persist(ctx context.Context, plan firewallModel, state *tfsdk.State, diags *diag.Diagnostics) {
	gwID := plan.GatewayID.ValueString()
	out := plan
	out.ID = types.StringValue(gwID)

	fresh, err := r.client.GetFirewallRules(ctx, gwID)
	if err != nil {
		diags.Append(state.Set(ctx, out)...)
		diags.AddError("Error Reading Gateway Firewall Rules After Apply",
			fmt.Sprintf("The firewall rules of gateway %s were applied, but reading them back failed: %s\n\n"+
				"They were recorded in state from the plan and the resource was marked tainted — "+
				"run terraform apply again to refresh it.", gwID, err.Error()))
		return
	}
	out.Rules = flattenFirewallRules(fresh)
	diags.Append(state.Set(ctx, out)...)
}
