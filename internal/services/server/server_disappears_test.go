package vstack_server_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// TestAccServer_disappears deletes the server out-of-band and checks the
// provider detects it is gone (Read → RemoveResource) and plans to recreate it.
func TestAccServer_disappears(t *testing.T) {
	serverName := "test-acc-srv-dis-" + acctest.RandomString(6)
	locationID := os.Getenv("VCP_LOCATION_ID")
	imageID := os.Getenv("VCP_IMAGE_ID")
	config := testAccServerConfig_basic(serverName, locationID, imageID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckServer(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServerDestroy,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { deleteServerOutOfBand(t, serverName) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// deleteServerOutOfBand removes the server with the given name directly via
// the API and waits until it is actually gone (deletion is asynchronous).
func deleteServerOutOfBand(t *testing.T, name string) {
	client := acctest.GetTestClient()
	ctx := context.Background()

	servers, err := client.GetServerList(ctx)
	if err != nil {
		t.Fatalf("out-of-band server list failed: %v", err)
	}

	var serverID string
	for _, server := range servers {
		if server.Name == name {
			serverID = server.ID
			break
		}
	}
	if serverID == "" {
		t.Fatalf("server %s not found to delete out-of-band", name)
	}

	if err := client.DeleteServer(ctx, serverID); err != nil {
		t.Fatalf("out-of-band server delete failed: %v", err)
	}

	// 5 minutes is the agreed ceiling for backend tasks.
	for i := 0; i < 60; i++ {
		_, err := client.GetServer(ctx, serverID)
		if sdk.IsNotFound(err) {
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatal("out-of-band deleted server did not disappear in time")
}
