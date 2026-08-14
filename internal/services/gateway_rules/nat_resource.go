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
	_ resource.Resource                   = &natResource{}
	_ resource.ResourceWithConfigure      = &natResource{}
	_ resource.ResourceWithImportState    = &natResource{}
	_ resource.ResourceWithValidateConfig = &natResource{}
)

func NewNATResource() resource.Resource { return &natResource{} }

type natResource struct {
	client *sdk.CloudClient
}

func (r *natResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_nat"
}

func (r *natResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the NAT rules of an edge gateway.\n\n" +
			"The API replaces the whole list on every change, so this resource owns **all** NAT rules of the " +
			"gateway: declare at most ONE `vcp_gateway_nat` per gateway, and expect rules created in the panel " +
			"to be replaced on the first apply.\n\n" +
			"~> Every rule has to name the gateway's external address — `destination` for `DNAT`, `translated` " +
			"for `SNAT` and `BINAT`. The API accepts no other value there; use `vcp_gateway.<name>.public_ip`.\n\n" +
			"~> Destroying this resource clears the gateway's NAT rules: it owns the whole list, so removing it " +
			"returns the gateway to the empty rule set a new gateway starts with. Note that this also removes " +
			"whatever those rules provided — a `SNAT` rule is what gives the private network outbound access. " +
			"To stop managing the rules without changing them, drop the resource from state " +
			"(`terraform state rm vcp_gateway_nat.<name>`) instead of destroying it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `gateway_id` — one rule set per gateway.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"gateway_id": schema.StringAttribute{
				MarkdownDescription: "ID of the gateway whose NAT rules are managed. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"rules": schema.ListNestedAttribute{
				MarkdownDescription: "The complete list of NAT rules. The API stores and returns them in exactly " +
					"this order; which rule wins when several of them match a packet is not specified by the API " +
					"documentation. An empty list removes every NAT rule from the gateway.",
				Required: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							MarkdownDescription: "Rule type: `DNAT` (inbound, publishes a service), `SNAT` (outbound) or " +
								"`BINAT` (1:1 translation).",
							Required:   true,
							Validators: []validator.String{stringvalidator.OneOf(natTypes...)},
						},
						"protocol": schema.StringAttribute{
							MarkdownDescription: "Protocol: `TCP`, `UDP`, `ICMP` or `IP` (any protocol). " +
								"With `ICMP` and `IP` both ports must be `0`.",
							Required:   true,
							Validators: []validator.String{stringvalidator.OneOf(protocols...)},
						},
						"source": schema.StringAttribute{
							MarkdownDescription: "Source in CIDR notation (`0.0.0.0/0` means any). For `DNAT` this restricts " +
								"who may reach the published service; for `SNAT`/`BINAT` it is the private address being translated.",
							Required:   true,
							Validators: []validator.String{cidrIPv4()},
						},
						"destination": schema.StringAttribute{
							MarkdownDescription: "Destination in CIDR notation. For `DNAT` it must be the gateway's external " +
								"address (`\"${vcp_gateway.example.public_ip}/32\"`); for `SNAT`/`BINAT` it is where the traffic goes " +
								"(usually `0.0.0.0/0`).",
							Required:   true,
							Validators: []validator.String{cidrIPv4()},
						},
						"destination_port": schema.Int64Attribute{
							MarkdownDescription: "Destination port, `0` (the default) means any port. Must be `0` for " +
								"`ICMP`, `IP` and `BINAT`.",
							Optional:   true,
							Computed:   true,
							Default:    int64default.StaticInt64(0),
							Validators: []validator.Int64{int64validator.Between(0, 65535)},
						},
						"translated": schema.StringAttribute{
							MarkdownDescription: "Address the traffic is translated to, **without** a mask. For `DNAT` it is the " +
								"private address of the target server; for `SNAT`/`BINAT` it must be the gateway's external address " +
								"(`vcp_gateway.example.public_ip`).",
							Required:   true,
							Validators: []validator.String{ipv4Address()},
						},
						"translated_port": schema.Int64Attribute{
							MarkdownDescription: "Port after translation, `0` (the default) means any port. Must be `0` for " +
								"`ICMP`, `IP` and `BINAT`.",
							Optional:   true,
							Computed:   true,
							Default:    int64default.StaticInt64(0),
							Validators: []validator.Int64{int64validator.Between(0, 65535)},
						},
					},
				},
			},
		},
	}
}

func (r *natResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *natResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	rules, ok := configRules[natRuleModel](ctx, req.Config)
	if !ok {
		return
	}
	validateNATRules(rules, &resp.Diagnostics)
}

func (r *natResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan natModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := plan.GatewayID.ValueString()
	defer locks.Gateway(gwID)()

	// Adopting a gateway that already has rules replaces them — the API cannot
	// merge. Say so instead of silently overwriting someone else's rules.
	if existing, err := r.client.GetNATRules(ctx, gwID); err != nil {
		tflog.Warn(ctx, "Could not read existing NAT rules before applying", map[string]any{
			"gateway_id": gwID, "error": err.Error(),
		})
	} else if len(existing) > 0 {
		resp.Diagnostics.AddWarning("Existing NAT Rules Replaced",
			fmt.Sprintf("Gateway %s already had %d NAT rule(s); they have been replaced by this resource. "+
				"To keep managing pre-existing rules instead, run terraform import vcp_gateway_nat.<name> %s.",
				gwID, len(existing), gwID))
	}

	if !r.apply(ctx, gwID, plan.Rules, &resp.Diagnostics) {
		return
	}
	r.persist(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *natResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state natModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := state.GatewayID.ValueString()
	rules, err := r.client.GetNATRules(ctx, gwID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Gateway NAT Rules",
			fmt.Sprintf("Could not read the NAT rules of gateway %s: %s", gwID, err.Error()))
		return
	}

	state.ID = types.StringValue(gwID)
	state.Rules = flattenNATRules(rules)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *natResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan natModel
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
func (r *natResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state natModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gwID := state.GatewayID.ValueString()
	defer locks.Gateway(gwID)()

	// Nothing to do if the gateway is gone (it took its rules with it) or if the
	// list is already empty — an empty PUT would only spend a backend task.
	existing, err := r.client.GetNATRules(ctx, gwID)
	switch {
	case err != nil && sdk.IsNotFound(err):
		tflog.Info(ctx, "Gateway already deleted, no NAT rules to clear", map[string]any{"gateway_id": gwID})
		return
	case err == nil && len(existing) == 0:
		tflog.Info(ctx, "Gateway has no NAT rules, nothing to clear", map[string]any{"gateway_id": gwID})
		return
	}

	tflog.Info(ctx, "Clearing gateway NAT rules", map[string]any{"gateway_id": gwID, "rules": len(state.Rules)})
	r.apply(ctx, gwID, nil, &resp.Diagnostics)
}

// ImportState takes the gateway id; Read fills in the rules.
func (r *natResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("gateway_id"), req, resp)
}

// apply PUTs the whole rule list and waits for the backend task. Returns false
// (with a diagnostic) if nothing was applied.
//
// Retrying happens one level down: the SDK already re-sends the (idempotent) list
// once if the backend task fails, and its HTTP layer spends about two minutes on
// "gateway is busy" before giving up. Repeating that here would only make a
// failing apply hang for ten minutes instead of telling the user what to do.
func (r *natResource) apply(ctx context.Context, gwID string, rules []natRuleModel, diags *diag.Diagnostics) bool {
	payload := expandNATRules(rules)
	tflog.Info(ctx, "Applying gateway NAT rules", map[string]any{"gateway_id": gwID, "rules": len(payload)})

	err := r.client.UpdateNATRulesAndWait(ctx, gwID, &entities.UpdateNATRulesRequest{NATRules: payload})
	if err == nil {
		return true
	}

	diags.AddError("Error Applying Gateway NAT Rules",
		fmt.Sprintf("Could not apply %d NAT rule(s) to gateway %s: %s\n\n%s",
			len(payload), gwID, err.Error(), applyFailureHint(err)))
	return false
}

// persist reads the rules back and records them in state. If the read fails
// after a successful write, the plan is recorded and an error is added: the
// rules are applied, so state must not stay empty, and the error taints the
// resource so the next apply refreshes it.
func (r *natResource) persist(ctx context.Context, plan natModel, state *tfsdk.State, diags *diag.Diagnostics) {
	gwID := plan.GatewayID.ValueString()
	out := plan
	out.ID = types.StringValue(gwID)

	fresh, err := r.client.GetNATRules(ctx, gwID)
	if err != nil {
		diags.Append(state.Set(ctx, out)...)
		diags.AddError("Error Reading Gateway NAT Rules After Apply",
			fmt.Sprintf("The NAT rules of gateway %s were applied, but reading them back failed: %s\n\n"+
				"They were recorded in state from the plan and the resource was marked tainted — "+
				"run terraform apply again to refresh it.", gwID, err.Error()))
		return
	}
	out.Rules = flattenNATRules(fresh)
	diags.Append(state.Set(ctx, out)...)
}
