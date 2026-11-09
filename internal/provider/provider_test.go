package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// provider.go is the only registry: a resource that compiles but is not listed
// there does not exist for Terraform, and nothing else fails. The schemas are
// asked for the way Terraform asks — through the protocol server — so a
// duplicate type name or a schema the framework refuses fails here too.
func TestProviderServesLLMTypes(t *testing.T) {
	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatalf("starting the provider server: %v", err)
	}
	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("asking for the schemas: %v", err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("the provider refused its schemas: %s: %s", d.Summary, d.Detail)
		}
	}

	for _, name := range []string{"vcp_llm_connection", "vcp_llm_group", "vcp_llm_access_key"} {
		if _, ok := resp.ResourceSchemas[name]; !ok {
			t.Errorf("resource %s is not served by the provider", name)
		}
	}
	for _, name := range []string{"vcp_llm_models", "vcp_llm_model", "vcp_llm_package", "vcp_llm_package_plans"} {
		if _, ok := resp.DataSourceSchemas[name]; !ok {
			t.Errorf("data source %s is not served by the provider", name)
		}
	}
}
