package provider

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	affinity "github.com/itglobalcom/terraform-provider-vcp/internal/services/affinity_group"
	"github.com/itglobalcom/terraform-provider-vcp/internal/services/dns"
	"github.com/itglobalcom/terraform-provider-vcp/internal/services/gateway"
	gateway_attachment "github.com/itglobalcom/terraform-provider-vcp/internal/services/gateway_network_attachment"
	"github.com/itglobalcom/terraform-provider-vcp/internal/services/isolated_network"
	"github.com/itglobalcom/terraform-provider-vcp/internal/services/metadata"
	vstack_server "github.com/itglobalcom/terraform-provider-vcp/internal/services/server"
	server_attachment "github.com/itglobalcom/terraform-provider-vcp/internal/services/server_network_attachment"
	server_pubif "github.com/itglobalcom/terraform-provider-vcp/internal/services/server_public_interface"
	ssh_key "github.com/itglobalcom/terraform-provider-vcp/internal/services/ssh_key"
	"github.com/itglobalcom/terraform-provider-vcp/internal/services/vmware"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

var (
	_ provider.Provider = &CloudProvider{}
)

// New returns a provider with the given version
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &CloudProvider{version: version}
	}
}

// CloudProvider implements the terraform-plugin-framework provider.Provider interface
type CloudProvider struct {
	version string
}

// CloudProviderModel is the provider configuration structure
type CloudProviderModel struct {
	Host types.String `tfsdk:"host"`
	Key  types.String `tfsdk:"key"`
}

// Metadata returns the provider name and version
func (p *CloudProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "vcp"
	resp.Version = p.version
}

// Schema describes the provider configuration options
func (p *CloudProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The VStack Cloud Panel (`vcp`) provider manages VStack Cloud Panel resources — " +
			"servers, isolated networks, gateways, DNS zones, SSH keys, and affinity groups, " +
			"as well as VMware Cloud servers and networks (`vcp_vmware_*`). " +
			"Configure it with an API endpoint (`host`) and token (`key`), or the `VCP_API_URL` / " +
			"`VCP_API_TOKEN` environment variables.",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "API host URL. Can also be set via the `VCP_API_URL` environment variable.",
			},
			"key": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "API key for authentication. Can also be set via the `VCP_API_TOKEN` environment variable.",
			},
		},
	}
}

// Configure initializes the SDK client for the provider
func (p *CloudProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	// Get data from the provider configuration
	var config CloudProviderModel
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check that the configuration values are not unknown.
	// This prevents an unexpectedly misconfigured client
	// if the values from Terraform are only known after applying another resource
	if config.Host.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("host"),
			"Unknown API Host",
			"The provider cannot create the API client because the host value is unknown. "+
				"Either apply the source of the value first, set the value statically in the configuration, or use the VCP_API_URL environment variable.",
		)
	}

	if config.Key.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("key"),
			"Unknown API Token",
			"The provider cannot create the API client because the key value is unknown. "+
				"Either apply the source of the value first, set the value statically in the configuration, or use the VCP_API_TOKEN environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	host := os.Getenv("VCP_API_URL")
	key := os.Getenv("VCP_API_TOKEN")

	if !config.Host.IsNull() {
		host = config.Host.ValueString()
	}

	if !config.Key.IsNull() {
		key = config.Key.ValueString()
	}

	if host == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("host"),
			"Missing API Host",
			"The provider cannot create the API client because the host value is missing or empty. "+
				"Set the host value in the configuration or use the VCP_API_URL environment variable. "+
				"If the value is already set, make sure it is not empty.",
		)
	}

	if key == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("key"),
			"Missing API Token",
			"The provider cannot create the API client because the key value is missing or empty. "+
				"Set the key value in the configuration or use the VCP_API_TOKEN environment variable. "+
				"If the value is already set, make sure it is not empty.",
		)
	}

	// Validate the host here (not with a schema validator) so the env-var
	// path is covered too. Without this, a value like "panel.example.com"
	// (no scheme) passes Configure and every API call then fails with a
	// low-level HTTP error that never points back at `host`.
	if host != "" {
		if u, err := url.Parse(host); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			resp.Diagnostics.AddAttributeError(
				path.Root("host"),
				"Invalid API Host",
				fmt.Sprintf("The host value %q is not a valid URL. Expected the form https://host[:port], "+
					"e.g. https://api.example.com.", host),
			)
		}
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// SDK log output goes to stderr, never stdout: stdout carries the
	// go-plugin protocol. Error level only — at Info the SDK logs full
	// request bodies (e.g. server init_script), which must not end up in
	// Terraform logs.
	logger := log.New(os.Stderr, "[vstack-cloud-panel-sdk] ", log.LstdFlags)

	// Create SDK configuration
	clientConfig, err := sdk.NewConfig(
		key,
		host,
		sdk.WithTimeout(60*time.Second),
		sdk.WithPollingInterval(10*time.Second),
		// Backend tasks are expected to complete within 5 minutes (the SDK
		// default is 2); past that point we fail fast instead of waiting.
		sdk.WithPollingTimeout(5*time.Minute),
		sdk.WithLogger(logger),
		sdk.WithLogLevel(sdk.Error),
		sdk.WithUserAgent("terraform-provider-vcp/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error initializing client configuration",
			"Failed to create the SDK configuration. "+
				"If the error is unclear, contact the provider developers.\n\n"+
				"SDK Config Error: "+err.Error(),
		)
		return
	}

	// Create SDK client
	client, err := sdk.NewClient(clientConfig)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error creating API client",
			"An unexpected error occurred while creating the API client. "+
				"If the error is unclear, contact the provider developers.\n\n"+
				"SDK Client Error: "+err.Error(),
		)
		return
	}

	// Make the client available to data sources and resources
	resp.DataSourceData = client
	resp.ResourceData = client
}

// Resources returns the list of resource creation functions
func (p *CloudProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		isolated_network.NewNetworkResource,
		ssh_key.NewSSHKeyResource,
		affinity.NewAffinityGroupResource,
		vstack_server.NewServerResource,
		gateway.NewGatewayResource,
		server_pubif.NewResource,
		server_attachment.NewResource,
		gateway_attachment.NewResource,
		dns.NewDomainResource,
		dns.NewRecordSetResource,
		vmware.NewNetworkResource,
		vmware.NewServerResource,
		vmware.NewServerNetworkAttachmentResource,
		vmware.NewServerPublicInterfaceResource,
		vmware.NewServerFirewallResource,
		vmware.NewEdgeFirewallResource,
		vmware.NewEdgeNATResource,
		vmware.NewEdgeVPNTunnelResource,
	}
}

// DataSources returns the list of data source functions
func (p *CloudProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		isolated_network.NewNetworkDataSource,
		isolated_network.NewNetworksDataSource,
		ssh_key.NewSSHKeyDataSource,
		ssh_key.NewSSHKeysDataSource,
		metadata.NewImagesDataSource,
		metadata.NewLocationsDataSource,
		metadata.NewProjectDataSource,
		metadata.NewApplicationsDataSource,
		affinity.NewAffinityGroupDataSource,
		affinity.NewAffinityGroupsDataSource,
		vstack_server.NewServerDataSource,
		vstack_server.NewServersDataSource,
		gateway.NewGatewayDataSource,
		gateway.NewGatewaysDataSource,
		dns.NewDomainDataSource,
		dns.NewDomainsDataSource,
		vmware.NewLocationsDataSource,
		vmware.NewImagesDataSource,
		vmware.NewGpuModelsDataSource,
		vmware.NewServerDataSource,
		vmware.NewServersDataSource,
		vmware.NewNetworkDataSource,
		vmware.NewNetworksDataSource,
	}
}
