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
	_ resource.Resource                   = &serverFirewallResource{}
	_ resource.ResourceWithConfigure      = &serverFirewallResource{}
	_ resource.ResourceWithImportState    = &serverFirewallResource{}
	_ resource.ResourceWithValidateConfig = &serverFirewallResource{}
)

func NewServerFirewallResource() resource.Resource { return &serverFirewallResource{} }

type serverFirewallResource struct {
	client *sdk.CloudClient
}

func (r *serverFirewallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_server_firewall"
}

func (r *serverFirewallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the firewall rules of a VMware Cloud server.\n\n" +
			"The API replaces the whole list on every change, so this resource owns **all** firewall rules of the " +
			"server: declare at most ONE `vcp_vmware_server_firewall` per server, and expect rules created in the " +
			"panel to be replaced on the first apply.\n\n" +
			"~> This is the firewall of the machine itself, and it has no default action — traffic no rule matches " +
			"is passed. It is independent of `vcp_vmware_edge_firewall`, which filters at the edge of the network.\n\n" +
			"~> Destroying this resource clears the server's firewall rules, which leaves the server unfiltered. To " +
			"stop managing the rules without changing them, drop the resource from state " +
			"(`terraform state rm vcp_vmware_server_firewall.<name>`) instead of destroying it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "Same as `server_id` — one rule set per server.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"server_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the server whose firewall rules are managed. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"rules": schema.ListNestedAttribute{
				MarkdownDescription: "The complete list of firewall rules, in the order they are applied. An empty " +
					"list removes every rule and leaves the server unfiltered.",
				Required: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							MarkdownDescription: "Name of the rule. The API identifies a rule by it, so it has to be " +
								"unique within the server.",
							Required:   true,
							Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
						},
						"traffic_direction": schema.StringAttribute{
							MarkdownDescription: "`incoming` filters traffic entering the server, `outgoing` traffic leaving it.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(serverDirections...)},
						},
						"action": schema.StringAttribute{
							MarkdownDescription: "`allow` passes matching packets, `deny` drops them.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(serverFirewallActions...)},
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
								"comma-separated list. Defaults to `any`.",
							Optional:   true,
							Computed:   true,
							Validators: []validator.String{vmwareRulePort()},
						},
						"destination": schema.StringAttribute{
							MarkdownDescription: "Destination, in the same forms as `source`. Defaults to `any`.",
							Optional:            true,
							Computed:            true,
							Validators:          []validator.String{vmwareRuleAddress()},
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

func (r *serverFirewallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig reports a duplicate rule name at plan time: the API rejects it
// with a bare -2002 that names neither the field nor the value (SRV-1).
func (r *serverFirewallResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	rules, ok := configRules[serverFirewallRuleModel](ctx, req.Config)
	if !ok {
		return
	}
	validateServerFirewallRules(rules, &resp.Diagnostics)
}

func (r *serverFirewallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverFirewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(plan.ServerID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	// Adopting a server that already has rules replaces them — the API cannot
	// merge. Say so instead of silently overwriting someone else's rules.
	if existing, err := r.client.GetVmwareServerFirewall(ctx, serverID); err != nil {
		tflog.Warn(ctx, "Could not read existing server firewall rules before applying", map[string]any{
			"server_id": serverID, "error": err.Error(),
		})
	} else if len(existing) > 0 {
		resp.Diagnostics.AddWarning("Existing Server Firewall Rules Replaced",
			fmt.Sprintf("Server %d already had %d firewall rule(s); they have been replaced by this resource. "+
				"To keep managing pre-existing rules instead, run "+
				"terraform import vcp_vmware_server_firewall.<name> %d.", serverID, len(existing), serverID))
	}

	r.applyAndPersist(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *serverFirewallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverFirewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(state.ServerID.ValueInt64())
	rules, err := r.client.GetVmwareServerFirewall(ctx, serverID)
	if err != nil {
		// SRV-2: a server without rules answers with an empty set, so a 404 now
		// means only one thing — the server is gone.
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading VMware Server Firewall Rules",
			fmt.Sprintf("Could not read the firewall rules of server %d: %s", serverID, err.Error()))
		return
	}

	state.ID = types.Int64Value(int64(serverID))
	state.Rules = flattenServerFirewallRules(rules)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *serverFirewallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serverFirewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	defer locks.VmwareServer(int(plan.ServerID.ValueInt64()))()
	r.applyAndPersist(ctx, plan, &resp.State, &resp.Diagnostics)
}

// Delete clears the rule set. The resource owns the whole list, so destroying it
// puts the server back to the empty list a new server starts with.
func (r *serverFirewallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverFirewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := int(state.ServerID.ValueInt64())
	defer locks.VmwareServer(serverID)()

	// Nothing to do if the server is gone (it took its rules with it) or if the
	// list is already empty — an empty PUT would only spend a backend task.
	existing, err := r.client.GetVmwareServerFirewall(ctx, serverID)
	switch {
	case err != nil && sdk.IsNotFound(err):
		tflog.Info(ctx, "VMware server already deleted, no firewall rules to clear",
			map[string]any{"server_id": serverID})
		return
	case err == nil && len(existing) == 0:
		tflog.Info(ctx, "VMware server has no firewall rules, nothing to clear",
			map[string]any{"server_id": serverID})
		return
	}

	tflog.Info(ctx, "Clearing VMware server firewall rules",
		map[string]any{"server_id": serverID, "rules": len(state.Rules)})
	r.apply(ctx, serverID, nil, &resp.Diagnostics)
}

// ImportState takes the server id; Read fills in the rules.
func (r *serverFirewallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	serverID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || serverID <= 0 {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected the numeric id of the server whose firewall rules are imported, got: %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), serverID)...)
}

// apply PUTs the whole rule list and waits for the backend task, returning the
// rules the API reports afterwards. The second result is what tells success from
// failure — a cleared rule set legitimately reads back as no rules at all.
func (r *serverFirewallResource) apply(ctx context.Context, serverID int, rules []serverFirewallRuleModel,
	diags *diag.Diagnostics) ([]entities.VmwareServerFirewallRule, bool) {
	payload := expandServerFirewallRules(rules)
	tflog.Info(ctx, "Applying VMware server firewall rules",
		map[string]any{"server_id": serverID, "rules": len(payload)})

	applied, err := r.client.UpdateVmwareServerFirewallAndWait(ctx, serverID,
		&entities.VmwareUpdateServerFirewallRequest{Rules: payload})
	if err == nil {
		return applied, true
	}

	diags.AddError("Error Applying VMware Server Firewall Rules",
		fmt.Sprintf("Could not apply %d firewall rule(s) to server %d: %s\n\n%s",
			len(payload), serverID, err.Error(), applyFailureHint(err, "server firewall")))
	return nil, false
}

// applyAndPersist writes the plan and records what the API reports back. The
// applied rules are what goes into state: attributes a rule left out (source,
// ports) come back filled in, and storing the plan instead would leave them
// unknown.
func (r *serverFirewallResource) applyAndPersist(ctx context.Context, plan serverFirewallModel,
	state *tfsdk.State, diags *diag.Diagnostics) {
	serverID := int(plan.ServerID.ValueInt64())

	applied, ok := r.apply(ctx, serverID, plan.Rules, diags)
	if !ok {
		return
	}

	out := plan
	out.ID = types.Int64Value(int64(serverID))
	out.Rules = mergeServerFirewallRules(plan.Rules, flattenServerFirewallRules(applied))
	diags.Append(state.Set(ctx, out)...)
}
