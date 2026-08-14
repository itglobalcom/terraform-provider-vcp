package vmware

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                = &edgeVPNTunnelResource{}
	_ resource.ResourceWithConfigure   = &edgeVPNTunnelResource{}
	_ resource.ResourceWithImportState = &edgeVPNTunnelResource{}
)

func NewEdgeVPNTunnelResource() resource.Resource { return &edgeVPNTunnelResource{} }

type edgeVPNTunnelResource struct {
	client *sdk.CloudClient
}

// edgeVPNTunnelModel — model of vcp_vmware_edge_vpn_tunnel.
//
// Unlike the firewall and NAT, a tunnel is a resource of its own rather than an
// entry in a set: it is a named connection with a secret, not a rule in a policy,
// and two tunnels of one network have nothing to do with each other.
type edgeVPNTunnelModel struct {
	ID        types.Int64  `tfsdk:"id"`
	NetworkID types.Int64  `tfsdk:"network_id"`
	Name      types.String `tfsdk:"name"`
	Enabled   types.Bool   `tfsdk:"enabled"`

	PeerEndpoint      types.String `tfsdk:"peer_endpoint"`
	PeerIdentificator types.String `tfsdk:"peer_identificator"`
	PeerNetwork       types.String `tfsdk:"peer_network"`

	SharedKey             types.String `tfsdk:"shared_key"`
	EncryptionType        types.String `tfsdk:"encryption_type"`
	DiffieHellmanGroup    types.String `tfsdk:"diffie_hellman_group"`
	Mtu                   types.Int64  `tfsdk:"mtu"`
	PerfectForwardSecrecy types.Bool   `tfsdk:"perfect_forward_secrecy"`

	// Everything the platform decides about the local side.
	VcloudID        types.String `tfsdk:"vcloud_id"`
	Description     types.String `tfsdk:"description"`
	LocalID         types.String `tfsdk:"local_id"`
	LocalIP         types.String `tfsdk:"local_ip"`
	LocalSubnets    types.List   `tfsdk:"local_subnets"`
	DigestAlgorithm types.String `tfsdk:"digest_algorithm"`
}

func (r *edgeVPNTunnelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vmware_edge_vpn_tunnel"
}

func (r *edgeVPNTunnelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one site-to-site IPsec tunnel on the edge of a VMware Cloud network.\n\n" +
			"A tunnel joins this network to a single subnet on the far side. It is a resource of its own rather " +
			"than an entry in a list — it is a named connection with a secret of its own, and several tunnels of " +
			"one network are independent.\n\n" +
			"~> The local side is not yours to choose: `local_ip`, `local_id`, `local_subnets` and " +
			"`digest_algorithm` are decided by the platform and reported here.\n\n" +
			"~> `shared_key` is never returned by the API. It lives in state, and a key changed in the panel is " +
			"invisible to Terraform — change it here to be sure of it.\n\n" +
			"~> The API answers `encryption_type` and `diffie_hellman_group` in a spelling of its own " +
			"(`aes256` comes back as `AES_256`, `dh14` as `DH14`). The provider compares them ignoring case and " +
			"punctuation, so the configuration keeps the spelling the contract documents.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				MarkdownDescription: "ID of the tunnel.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"network_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the network whose edge carries the tunnel. VPN needs an edge, so this " +
					"is a `routed` or `public` network. Changing this forces a new resource.",
				Required:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the tunnel, unique within the network. Changing this forces a new resource.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the tunnel carries traffic. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"peer_endpoint": schema.StringAttribute{
				MarkdownDescription: "Public IPv4 address of the far side. Not a hostname, and not the external " +
					"address of this edge or the gateway of this network.",
				Required:   true,
				Validators: []validator.String{ipv4Only()},
			},
			"peer_identificator": schema.StringAttribute{
				MarkdownDescription: "IKE identity of the far side, as an IPv4 address — usually the same as `peer_endpoint`.",
				Required:            true,
				Validators:          []validator.String{ipv4Only()},
			},
			"peer_network": schema.StringAttribute{
				MarkdownDescription: "The single subnet reachable through the tunnel, in CIDR notation. One subnet " +
					"per tunnel: declare another tunnel for another subnet.",
				Required:   true,
				Validators: []validator.String{ipv4CIDROnly()},
			},
			"shared_key": schema.StringAttribute{
				MarkdownDescription: "IPsec pre-shared key: 32–128 alphanumeric characters with at least one " +
					"upper-case letter, one lower-case letter and one digit. Tunnels to the same peer must share it.",
				Required:   true,
				Sensitive:  true,
				Validators: []validator.String{vpnSharedKey()},
			},
			"encryption_type": schema.StringAttribute{
				MarkdownDescription: "Encryption: `aes`, `aes256`, `aesgcm` or `tripledes`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(vpnEncryptionTypes...)},
				PlanModifiers:       []planmodifier.String{loosePlanModifier{}},
			},
			"diffie_hellman_group": schema.StringAttribute{
				MarkdownDescription: "Diffie-Hellman group: `dh2`, `dh5`, `dh14`, `dh15` or `dh16`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(vpnDiffieHellmanGroups...)},
				PlanModifiers:       []planmodifier.String{loosePlanModifier{}},
			},
			"mtu": schema.Int64Attribute{
				MarkdownDescription: "MTU of the tunnel, e.g. `1500`.",
				Required:            true,
				Validators:          []validator.Int64{int64validator.Between(68, 9000)},
			},
			"perfect_forward_secrecy": schema.BoolAttribute{
				MarkdownDescription: "Whether to renegotiate the key for every session. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},

			"vcloud_id":        schema.StringAttribute{Computed: true, MarkdownDescription: "Id of the tunnel in vCloud, for reference."},
			"description":      schema.StringAttribute{Computed: true, MarkdownDescription: "Description the platform generated for the tunnel."},
			"local_id":         schema.StringAttribute{Computed: true, MarkdownDescription: "IKE identity of this side, chosen by the platform."},
			"local_ip":         schema.StringAttribute{Computed: true, MarkdownDescription: "External address of this side, chosen by the platform."},
			"digest_algorithm": schema.StringAttribute{Computed: true, MarkdownDescription: "Digest algorithm the platform applies."},
			"local_subnets": schema.ListAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				MarkdownDescription: "Subnets of this side that the tunnel carries, chosen by the platform.",
			},
		},
	}
}

func (r *edgeVPNTunnelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *edgeVPNTunnelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan edgeVPNTunnelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(plan.NetworkID.ValueInt64())
	if !checkEdgeCapableNetwork(ctx, r.client, networkID, "the edge VPN",
		[]string{networkTypeRouted, networkTypePublic}, &resp.Diagnostics) {
		return
	}

	defer locks.VmwareNetwork(networkID)()

	// A name is what identifies a tunnel here, so refuse to write over one that
	// already carries it rather than silently adopting somebody else's tunnel.
	if existing, err := r.findTunnel(ctx, networkID, plan.Name.ValueString()); err == nil && existing != nil {
		resp.Diagnostics.AddError("VPN Tunnel Already Exists",
			fmt.Sprintf("The edge of network %d already has a tunnel named %q (id %d). Import it with "+
				"terraform import vcp_vmware_edge_vpn_tunnel.<name> %d:%d, or choose another name.",
				networkID, plan.Name.ValueString(), derefInt(existing.ID), networkID, derefInt(existing.ID)))
		return
	}

	tflog.Info(ctx, "Creating a VMware edge VPN tunnel",
		map[string]any{"network_id": networkID, "name": plan.Name.ValueString()})

	if _, err := r.client.UpsertVmwareEdgeVPNTunnelAndWait(ctx, networkID, expandVPNTunnel(plan, nil)); err != nil {
		resp.Diagnostics.AddError("Error Creating VMware Edge VPN Tunnel",
			fmt.Sprintf("Could not create tunnel %q on network %d: %s\n\n%s",
				plan.Name.ValueString(), networkID, err.Error(), vpnFailureHint(err)))
		return
	}

	created, err := r.findTunnel(ctx, networkID, plan.Name.ValueString())
	if err != nil || created == nil {
		resp.Diagnostics.AddError("Error Reading Created VMware Edge VPN Tunnel",
			fmt.Sprintf("Tunnel %q was created on network %d but could not be read back. Run terraform apply "+
				"again to record it.", plan.Name.ValueString(), networkID))
		return
	}

	state := plan
	flattenVPNTunnel(&state, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *edgeVPNTunnelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state edgeVPNTunnelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(state.NetworkID.ValueInt64())
	vpn, err := r.client.GetVmwareEdgeVPN(ctx, networkID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading VMware Edge VPN",
			fmt.Sprintf("Could not read the VPN of network %d: %s", networkID, err.Error()))
		return
	}

	tunnel := findTunnelByID(vpn.Tunnels, int(state.ID.ValueInt64()))
	if tunnel == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	flattenVPNTunnel(&state, tunnel)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *edgeVPNTunnelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state edgeVPNTunnelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(plan.NetworkID.ValueInt64())
	tunnelID := int(state.ID.ValueInt64())
	defer locks.VmwareNetwork(networkID)()

	tflog.Info(ctx, "Updating a VMware edge VPN tunnel",
		map[string]any{"network_id": networkID, "tunnel_id": tunnelID})

	if _, err := r.client.UpsertVmwareEdgeVPNTunnelAndWait(ctx, networkID, expandVPNTunnel(plan, &tunnelID)); err != nil {
		resp.Diagnostics.AddError("Error Updating VMware Edge VPN Tunnel",
			fmt.Sprintf("Could not update tunnel %q (id %d) on network %d: %s\n\n%s",
				plan.Name.ValueString(), tunnelID, networkID, err.Error(), vpnFailureHint(err)))
		return
	}

	vpn, err := r.client.GetVmwareEdgeVPN(ctx, networkID)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Updated VMware Edge VPN Tunnel", err.Error())
		return
	}

	newState := plan
	newState.ID = state.ID
	if tunnel := findTunnelByID(vpn.Tunnels, tunnelID); tunnel != nil {
		flattenVPNTunnel(&newState, tunnel)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *edgeVPNTunnelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state edgeVPNTunnelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := int(state.NetworkID.ValueInt64())
	tunnelID := int(state.ID.ValueInt64())
	defer locks.VmwareNetwork(networkID)()

	if err := r.client.DeleteVmwareEdgeVPNTunnelAndWait(ctx, networkID, tunnelID); err != nil {
		if sdk.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error Deleting VMware Edge VPN Tunnel",
			fmt.Sprintf("Could not delete tunnel %d on network %d: %s", tunnelID, networkID, err.Error()))
	}
}

// ImportState parses "network_id:tunnel_id".
//
// The pre-shared key cannot be imported — the API never returns it — so an
// imported tunnel takes the key from the configuration, and the first apply
// rewrites the tunnel with it.
func (r *edgeVPNTunnelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	networkID, tunnelID, ok := parseServerNICImportID(req.ID)
	if !ok {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID of the form 'network_id:tunnel_id' with two positive integers, got: %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("network_id"), networkID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), tunnelID)...)
}

// findTunnel locates a tunnel of the network by name.
func (r *edgeVPNTunnelResource) findTunnel(ctx context.Context, networkID int, name string) (*entities.VmwareEdgeVPNTunnel, error) {
	vpn, err := r.client.GetVmwareEdgeVPN(ctx, networkID)
	if err != nil {
		return nil, err
	}
	for i := range vpn.Tunnels {
		tunnel := &vpn.Tunnels[i]
		if tunnel.Name != nil && *tunnel.Name == name {
			return tunnel, nil
		}
	}
	return nil, nil
}

func findTunnelByID(tunnels []entities.VmwareEdgeVPNTunnel, id int) *entities.VmwareEdgeVPNTunnel {
	for i := range tunnels {
		if tunnels[i].ID != nil && *tunnels[i].ID == id {
			return &tunnels[i]
		}
	}
	return nil
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// expandVPNTunnel builds the upsert request. tunnelID selects an existing tunnel;
// pass nil to create one.
func expandVPNTunnel(m edgeVPNTunnelModel, tunnelID *int) *entities.VmwareUpsertVPNTunnelRequest {
	mtu := int(m.Mtu.ValueInt64())
	return &entities.VmwareUpsertVPNTunnelRequest{
		TunnelID:              tunnelID,
		Name:                  m.Name.ValueString(),
		Enabled:               optionalBool(m.Enabled),
		MTU:                   &mtu,
		EncryptionType:        m.EncryptionType.ValueString(),
		SharedKey:             m.SharedKey.ValueString(),
		PeerNetwork:           m.PeerNetwork.ValueString(),
		PeerEndpoint:          m.PeerEndpoint.ValueString(),
		PeerIdentificator:     m.PeerIdentificator.ValueString(),
		PerfectForwardSecrecy: optionalBool(m.PerfectForwardSecrecy),
		DiffieHellmanGroup:    m.DiffieHellmanGroup.ValueString(),
	}
}

// flattenVPNTunnel copies what the API reports into the model.
//
// NET-5: the read side is not the write side. `peer_network` is written as one
// subnet and read back as a list, and the two enum fields come back in another
// spelling — so the configured values are kept and only what the platform alone
// knows is taken from the response.
func flattenVPNTunnel(m *edgeVPNTunnelModel, tunnel *entities.VmwareEdgeVPNTunnel) {
	if tunnel.ID != nil {
		m.ID = types.Int64Value(int64(*tunnel.ID))
	}
	m.VcloudID = fromStringPtr(tunnel.VcloudID)
	m.Description = fromStringPtr(tunnel.Description)
	m.LocalID = fromStringPtr(tunnel.LocalID)
	m.LocalIP = fromStringPtr(tunnel.LocalIP)
	m.DigestAlgorithm = fromStringPtr(tunnel.DigestAlgorithm)
	m.Enabled = preferPlannedBool(fromBoolPtr(tunnel.Enabled), m.Enabled)

	subnets := make([]attr.Value, 0, len(tunnel.LocalSubnets))
	for _, subnet := range tunnel.LocalSubnets {
		subnets = append(subnets, types.StringValue(subnet))
	}
	m.LocalSubnets = types.ListValueMust(types.StringType, subnets)

	// The two enums round-trip in a different spelling; keep the configured one
	// unless the platform really chose something else.
	m.EncryptionType = keepIfLooselyEqual(m.EncryptionType, fromStringPtr(tunnel.EncryptionType))
	m.DiffieHellmanGroup = keepIfLooselyEqual(m.DiffieHellmanGroup, fromStringPtr(tunnel.DiffieHellmanGroup))

	// PeerSubnets is the list form of the single peer_network that was written.
	if len(tunnel.PeerSubnets) == 1 && isSet(m.PeerNetwork) {
		if !strings.EqualFold(tunnel.PeerSubnets[0], m.PeerNetwork.ValueString()) {
			m.PeerNetwork = types.StringValue(tunnel.PeerSubnets[0])
		}
	}
	if tunnel.PeerEndpoint != nil {
		m.PeerEndpoint = types.StringValue(*tunnel.PeerEndpoint)
	}
	if tunnel.PeerIdentificator != nil {
		m.PeerIdentificator = types.StringValue(*tunnel.PeerIdentificator)
	}
	if tunnel.MTU != nil {
		m.Mtu = types.Int64Value(int64(*tunnel.MTU))
	}
	if tunnel.PerfectForwardSecrecy != nil {
		m.PerfectForwardSecrecy = types.BoolValue(*tunnel.PerfectForwardSecrecy)
	}
}

// vpnFailureHint turns the platform limits behind a rejected tunnel into
// something to act on. The API reports all of them as a bare bad request.
func vpnFailureHint(err error) string {
	if sdk.IsConflict(err) {
		return "The edge is still applying another change. Wait and run terraform apply again."
	}
	return "The platform refuses a tunnel whose peer_endpoint is the edge's own external address or the " +
		"gateway of the network, a second tunnel with the same peer_network, and a tunnel to a peer that " +
		"another tunnel already reaches with a different shared_key. VPN is also unavailable on NSX-T networks. " +
		"Up to 50 tunnels can exist on one network."
}
