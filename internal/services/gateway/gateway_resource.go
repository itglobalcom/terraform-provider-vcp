package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                = &gatewayResource{}
	_ resource.ResourceWithConfigure   = &gatewayResource{}
	_ resource.ResourceWithImportState = &gatewayResource{}
)

func NewGatewayResource() resource.Resource {
	return &gatewayResource{}
}

type gatewayResource struct {
	client *sdk.CloudClient
}

func (r *gatewayResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway"
}

func (r *gatewayResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an edge gateway providing external connectivity to isolated networks.\n\n" +
			"The external (WAN) interface is intrinsic to the gateway: `bandwidth_mbps` is a gateway-wide " +
			"property that must be set at creation. The gateway itself has no network arguments — attach " +
			"isolated networks with the `vcp_gateway_network_attachment` resource.\n\n" +
			"The rest of the gateway configuration lives in its own resources, each owning the whole list it " +
			"manages: `vcp_gateway_nat` for NAT rules and `vcp_gateway_firewall` for firewall rules. Both need " +
			"the gateway's external address, which this resource exposes as `public_ip`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Gateway ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"location_id": schema.StringAttribute{
				MarkdownDescription: "Location ID where the gateway is created. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Gateway name.",
				Required:            true,
			},
			"bandwidth_mbps": schema.Int64Attribute{
				MarkdownDescription: "Uplink bandwidth in Mbps for the external (WAN) interface (must be > 0). Gateway-wide.",
				Required:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"tags": schema.SetAttribute{
				MarkdownDescription: "Set of tags associated with the gateway. If omitted, all existing tags are removed.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default: setdefault.StaticValue(
					types.SetValueMust(types.StringType, []attr.Value{}),
				),
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},

			"public_ip": schema.StringAttribute{
				MarkdownDescription: "Address of the external (WAN) interface. Every NAT rule has to name it — " +
					"`destination` for `DNAT`, `translated` for `SNAT`/`BINAT` (see `vcp_gateway_nat`).",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},

			// Computed (changes on update → no UseStateForUnknown).
			"state":      schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway state (New, Active, Busy, Blocked)."},
			"powered_on": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the gateway is powered on."},
			"created": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *gatewayResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *gatewayResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The API requires a gateway to be created attached to at least one network.
	// We satisfy this transparently: create a throwaway "bootstrap" network, create
	// the gateway on it, then detach and delete that network right away. The gateway
	// resource therefore exposes no network arguments — real networks are attached via
	// vcp_gateway_network_attachment. When the API drops the create-time requirement,
	// this whole dance can be removed without any change to user configuration.
	bootstrapName := fmt.Sprintf("vcp-gw-bootstrap-%s", bootstrapNetworkSuffix())
	tflog.Info(ctx, "Creating throwaway bootstrap network for gateway", map[string]any{"name": bootstrapName})
	bootstrapNet, err := r.client.CreateNetworkAndWait(ctx, &entities.CreateNetworkRequest{
		Name:       bootstrapName,
		LocationID: plan.LocationID.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error Creating Gateway Bootstrap Network",
			fmt.Sprintf("Could not create the temporary network required to bootstrap the gateway: %s", err.Error()))
		return
	}

	createReq := &entities.CreateGatewayRequest{
		LocationID:    plan.LocationID.ValueString(),
		Name:          plan.Name.ValueString(),
		BandwidthMbps: int(plan.BandwidthMbps.ValueInt64()),
		NetworkIDs:    []string{bootstrapNet.ID},
	}
	tflog.Info(ctx, "Creating gateway", map[string]any{"name": createReq.Name, "location_id": createReq.LocationID})
	gw, err := r.client.CreateGatewayAndWait(ctx, createReq)
	if err != nil {
		// Clean up the bootstrap network so a failed create doesn't leak it.
		if delErr := r.client.DeleteNetwork(ctx, bootstrapNet.ID); delErr != nil {
			tflog.Warn(ctx, "Failed to clean up bootstrap network after gateway creation error",
				map[string]any{"network_id": bootstrapNet.ID, "error": delErr.Error()})
		}
		resp.Diagnostics.AddError("Error Creating Gateway",
			fmt.Sprintf("Could not create gateway '%s': %s", plan.Name.ValueString(), err.Error()))
		return
	}
	id := gw.ID

	// Detach the bootstrap network. The create response should carry its NIC; if
	// not, re-read the gateway to locate it. From this point on the gateway
	// exists and is billable, so no failure path may return without recording
	// it in state — an unrecorded gateway would be leaked and re-created by the
	// next apply.
	nic, ok := findBootstrapNIC(gw, bootstrapNet.ID)
	var nicLookupErr error
	if !ok {
		fresh, gerr := r.client.GetGateway(ctx, id)
		if gerr != nil {
			nicLookupErr = gerr
		} else {
			nic, ok = findBootstrapNIC(fresh, bootstrapNet.ID)
		}
	}
	switch {
	case ok:
		if err := r.client.DisconnectNetworkAndWait(ctx, id, nic.ID); err != nil {
			// A detach failure usually means the gateway is wedged Busy (a known
			// backend behavior after NIC operations), so a rollback delete would
			// almost certainly fail too and leak the gateway invisibly. Record it
			// in state and fail the apply instead: Terraform marks it tainted and
			// the next apply replaces it cleanly.
			r.persistCreateState(ctx, resp, gw, plan)
			resp.Diagnostics.AddError("Error Detaching Gateway Bootstrap Network",
				fmt.Sprintf("Gateway %s was created but its temporary bootstrap network %s (%s) could not be detached: %s\n\n"+
					"The gateway was recorded in state and marked tainted — run terraform apply again to replace it. "+
					"If the bootstrap network is left behind afterwards, delete it manually.",
					id, bootstrapName, bootstrapNet.ID, err.Error()))
			return
		}
	case nicLookupErr != nil:
		// Cannot tell whether the bootstrap network is still attached; treat it
		// like a detach failure so nothing is silently leaked.
		r.persistCreateState(ctx, resp, gw, plan)
		resp.Diagnostics.AddError("Error Locating Gateway Bootstrap NIC",
			fmt.Sprintf("Gateway %s was created but reading it back to detach the bootstrap network %s (%s) failed: %s\n\n"+
				"The gateway was recorded in state and marked tainted — run terraform apply again to replace it. "+
				"If the bootstrap network is left behind afterwards, delete it manually.",
				id, bootstrapName, bootstrapNet.ID, nicLookupErr.Error()))
		return
	default:
		// The NIC is genuinely absent — nothing to detach.
	}
	if err := r.client.DeleteNetwork(ctx, bootstrapNet.ID); err != nil {
		// Non-fatal: the network is already detached — this only leaves an empty orphan.
		tflog.Warn(ctx, "Bootstrap network detached but could not be deleted (left orphaned)",
			map[string]any{"network_id": bootstrapNet.ID, "error": err.Error()})
	}

	// Tags.
	tagsRefreshed := true // whether the final read below reflects the tags added here
	if !plan.Tags.IsNull() && len(plan.Tags.Elements()) > 0 {
		var tags []string
		tagDiags := plan.Tags.ElementsAs(ctx, &tags, false)
		// Error without return: the gateway already exists, and the final read +
		// State.Set below must still run so Terraform records it instead of
		// orphaning it.
		resp.Diagnostics.Append(tagDiags...)
		if !tagDiags.HasError() {
			for _, tag := range tags {
				if err := r.client.CreateGatewayTag(ctx, id, &entities.CreateGatewayTagRequest{Value: tag}); err != nil {
					resp.Diagnostics.AddError("Error Adding Gateway Tag",
						fmt.Sprintf("Could not add tag %q to gateway %s: %s", tag, id, err.Error()))
				}
			}
		}
	}

	// Final read for fresh scalar state (after detach and any tag changes).
	if fresh, err := r.client.GetGateway(ctx, id); err != nil {
		// The gateway is fully created; a failed refresh must not orphan it from
		// state. Fall back to the create-time snapshot — the next refresh reconciles.
		tagsRefreshed = false
		resp.Diagnostics.AddWarning("Gateway Created But Refresh Failed",
			fmt.Sprintf("Gateway %s was created, but reading it back failed: %s. "+
				"State was saved from the create response and will be refreshed on the next plan.", id, err.Error()))
	} else {
		gw = fresh
	}

	state := mapGatewayScalars(gw)
	// bandwidth_mbps — user-owned: echoed from plan.
	state.BandwidthMbps = plan.BandwidthMbps
	if !tagsRefreshed {
		// The tag calls above succeeded (any failure added an error diagnostic,
		// which fails the apply anyway), so the plan's tags are authoritative.
		state.Tags = plan.Tags
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// persistCreateState records a just-created gateway in state from the best
// snapshot available, so that a failure later in Create taints the resource
// instead of orphaning it.
func (r *gatewayResource) persistCreateState(ctx context.Context, resp *resource.CreateResponse, gw *entities.Gateway, plan gatewayModel) {
	state := mapGatewayScalars(gw)
	state.BandwidthMbps = plan.BandwidthMbps
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *gatewayResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior gatewayModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gw, err := r.client.GetGateway(ctx, prior.ID.ValueString())
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Gateway",
			fmt.Sprintf("Could not read gateway %s: %s", prior.ID.ValueString(), err.Error()))
		return
	}

	state := mapGatewayScalars(gw)
	// bandwidth — from the WAN NIC (drift detection + population after import).
	state.BandwidthMbps = wanBandwidth(gw, prior.BandwidthMbps)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *gatewayResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state gatewayModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	defer locks.Gateway(id)()

	// Name.
	if !plan.Name.Equal(state.Name) {
		if _, err := r.client.UpdateGateway(ctx, id, &entities.UpdateGatewayRequest{Name: plan.Name.ValueString()}); err != nil {
			resp.Diagnostics.AddError("Error Updating Gateway Name",
				fmt.Sprintf("Could not update the name of gateway %s: %s", id, err.Error()))
			return
		}
	}

	// Bandwidth (gateway-wide).
	if !plan.BandwidthMbps.Equal(state.BandwidthMbps) {
		if _, err := r.client.UpdateGatewayBandwidthAndWait(ctx, id, &entities.UpdateGatewayBandwidthRequest{
			BandwidthMbps: int(plan.BandwidthMbps.ValueInt64()),
		}); err != nil {
			resp.Diagnostics.AddError("Error Updating Gateway Bandwidth",
				fmt.Sprintf("Could not update the bandwidth of gateway %s: %s", id, err.Error()))
			return
		}
	}

	// Tags.
	if !plan.Tags.Equal(state.Tags) {
		var planTags, stateTags []string
		if !plan.Tags.IsNull() {
			resp.Diagnostics.Append(plan.Tags.ElementsAs(ctx, &planTags, false)...)
		}
		if !state.Tags.IsNull() {
			resp.Diagnostics.Append(state.Tags.ElementsAs(ctx, &stateTags, false)...)
		}
		if resp.Diagnostics.HasError() {
			return
		}
		for _, tag := range planTags {
			if !slices.Contains(stateTags, tag) {
				if err := r.client.CreateGatewayTag(ctx, id, &entities.CreateGatewayTagRequest{Value: tag}); err != nil {
					resp.Diagnostics.AddError("Error Adding Gateway Tag", fmt.Sprintf("tag %s: %s", tag, err.Error()))
					return
				}
			}
		}
		for _, tag := range stateTags {
			if !slices.Contains(planTags, tag) {
				if err := r.client.DeleteGatewayTag(ctx, id, tag); err != nil {
					resp.Diagnostics.AddError("Error Removing Gateway Tag", fmt.Sprintf("tag %s: %s", tag, err.Error()))
					return
				}
			}
		}
	}

	gw, err := r.client.GetGateway(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error Refreshing Gateway After Update", err.Error())
		return
	}
	newState := mapGatewayScalars(gw)
	newState.BandwidthMbps = plan.BandwidthMbps
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *gatewayResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.client.DeleteGateway(ctx, id); err != nil {
		// A gateway that is already gone is reported as not-found by the SDK,
		// even though the API answers HTTP 500 in that case.
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Gateway already deleted, treating as success", map[string]any{"id": id})
			return
		}
		resp.Diagnostics.AddError("Error Deleting Gateway",
			fmt.Sprintf("Could not delete gateway %s: %s", id, err.Error()))
	}
}

// ImportState imports the gateway by id. The gateway model holds no network
// state (networks are managed by vcp_gateway_network_attachment), so nothing
// needs manual reconciliation after import.
func (r *gatewayResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// findBootstrapNIC returns the gateway NIC attached to the given network, if present.
func findBootstrapNIC(gw *entities.Gateway, networkID string) (entities.GatewayNIC, bool) {
	for _, nic := range gw.NICs {
		if nic.NetworkID == networkID {
			return nic, true
		}
	}
	return entities.GatewayNIC{}, false
}

// bootstrapNetworkSuffix returns a short random hex suffix used to name the
// throwaway bootstrap network so concurrent creates don't collide.
func bootstrapNetworkSuffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "tmp"
	}
	return hex.EncodeToString(b)
}
