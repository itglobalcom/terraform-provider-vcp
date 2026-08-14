package acctest

import (
	"log"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"

	"github.com/itglobalcom/terraform-provider-vcp/internal/provider"
)

const TestProviderVersion = "test"

// ProtoV6ProviderFactories - shared across all tests
var ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"vcp": providerserver.NewProtocol6WithError(provider.New(TestProviderVersion)()),
}

// PreCheck checks the environment variables
func PreCheck(t *testing.T) {
	if v := os.Getenv("VCP_API_TOKEN"); v == "" {
		t.Fatal("VCP_API_TOKEN must be set for acceptance tests")
	}

	if v := os.Getenv("VCP_API_URL"); v == "" {
		t.Fatal("VCP_API_URL must be set for acceptance tests")
	}

	if v := os.Getenv("VCP_LOCATION_ID"); v == "" {
		t.Fatal("VCP_LOCATION_ID must be set for acceptance tests")
	}
}

// PreCheckServer checks the environment variables required by tests that
// create servers (everything PreCheck requires, plus the OS image).
func PreCheckServer(t *testing.T) {
	PreCheck(t)

	if v := os.Getenv("VCP_IMAGE_ID"); v == "" {
		t.Fatal("VCP_IMAGE_ID must be set for acceptance tests that create servers")
	}
}

// PreCheckVmware checks the environment variables required by the VMware Cloud
// acceptance tests. VMware is a separate service with its own catalog, so it has
// its own location variable rather than reusing VCP_LOCATION_ID.
func PreCheckVmware(t *testing.T) {
	if v := os.Getenv("VCP_API_TOKEN"); v == "" {
		t.Fatal("VCP_API_TOKEN must be set for acceptance tests")
	}
	if v := os.Getenv("VCP_API_URL"); v == "" {
		t.Fatal("VCP_API_URL must be set for acceptance tests")
	}
	if v := os.Getenv("VCP_VMWARE_LOCATION_ID"); v == "" {
		t.Fatal("VCP_VMWARE_LOCATION_ID must be set for VMware acceptance tests")
	}
}

// PreCheckVmwareServer checks everything PreCheckVmware requires, plus the OS
// image the tests that create servers order from.
func PreCheckVmwareServer(t *testing.T) {
	PreCheckVmware(t)

	if v := os.Getenv("VCP_VMWARE_IMAGE_ID"); v == "" {
		t.Fatal("VCP_VMWARE_IMAGE_ID must be set for VMware acceptance tests that create servers")
	}
}

// RandomString generates a random string
func RandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

var (
	testClient     *sdk.CloudClient
	testClientOnce sync.Once
)

// GetTestClient returns a singleton client for tests
func GetTestClient() *sdk.CloudClient {
	testClientOnce.Do(func() {

		logger := log.New(os.Stdout, "[vstack-cloud-panel-sdk] ", log.LstdFlags)

		config, err := sdk.NewConfig(
			os.Getenv("VCP_API_TOKEN"),
			os.Getenv("VCP_API_URL"),
			sdk.WithTimeout(60*time.Second),
			sdk.WithPollingInterval(10*time.Second),
			// Match the provider client: fail fast once a task exceeds the
			// expected 5-minute completion window.
			sdk.WithPollingTimeout(5*time.Minute),
			sdk.WithLogger(logger),
			sdk.WithLogLevel(sdk.Info),
		)
		if err != nil {
			panic("failed to create test config: " + err.Error())
		}

		client, err := sdk.NewClient(config)
		if err != nil {
			panic("failed to create test client: " + err.Error())
		}

		testClient = client
	})

	return testClient
}
