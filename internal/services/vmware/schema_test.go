package vmware

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rsschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// TestResourceSchemas ensures every VMware resource schema is internally
// consistent (no conflicting Required/Computed, valid nested attributes, etc.).
func TestResourceSchemas(t *testing.T) {
	resources := map[string]func() resource.Resource{
		"vcp_vmware_network": NewNetworkResource,
		"vcp_vmware_server":  NewServerResource,
	}
	for name, ctor := range resources {
		t.Run(name, func(t *testing.T) {
			var resp resource.SchemaResponse
			ctor().Schema(context.Background(), resource.SchemaRequest{}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("%s schema diagnostics: %v", name, resp.Diagnostics)
			}
			if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
				t.Fatalf("%s schema validation: %v", name, diags)
			}
		})
	}
}

// TestDataSourceSchemas ensures every VMware data-source schema is consistent.
func TestDataSourceSchemas(t *testing.T) {
	dataSources := map[string]func() datasource.DataSource{
		"vcp_vmware_locations":  NewLocationsDataSource,
		"vcp_vmware_images":     NewImagesDataSource,
		"vcp_vmware_gpu_models": NewGpuModelsDataSource,
		"vcp_vmware_server":     NewServerDataSource,
		"vcp_vmware_servers":    NewServersDataSource,
		"vcp_vmware_network":    NewNetworkDataSource,
		"vcp_vmware_networks":   NewNetworksDataSource,
	}
	for name, ctor := range dataSources {
		t.Run(name, func(t *testing.T) {
			var resp datasource.SchemaResponse
			ctor().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("%s schema diagnostics: %v", name, resp.Diagnostics)
			}
			if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
				t.Fatalf("%s schema validation: %v", name, diags)
			}
		})
	}
}

// Compile-time guard: the schema packages are referenced so a future refactor
// that drops an import here is caught.
var (
	_ = rsschema.Schema{}
	_ = dsschema.Schema{}
)
