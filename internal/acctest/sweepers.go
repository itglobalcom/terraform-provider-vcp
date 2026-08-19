package acctest

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// sweepNetworks cleans up test networks
func SweepNetworks(region string) error {
	log.Printf("[INFO] Starting sweep of VStack networks")

	// Use the client from acctest
	client := GetTestClient()

	ctx := context.Background()

	// Get the list of all networks
	networks, err := client.GetNetworkList(ctx)
	if err != nil {
		return fmt.Errorf("error listing networks: %w", err)
	}

	if len(networks) == 0 {
		log.Print("[DEBUG] No VStack networks to sweep")
		return nil
	}

	log.Printf("[INFO] Found %d networks, checking for test networks", len(networks))

	// Filter first so progress counters reflect test networks only.
	var testNetworks []*entities.Network
	for _, network := range networks {
		if !isTestResourceName(network.Name) {
			log.Printf("[DEBUG] Skipping network: %s (not a test network)", network.Name)
			continue
		}
		testNetworks = append(testNetworks, network)
	}

	var sweeperErrors []error
	sweeperCount := len(testNetworks)

	for i, network := range testNetworks {
		log.Printf("[INFO] [%d/%d] Deleting test network: %s (ID: %s)",
			i+1, len(testNetworks), network.Name, network.ID)

		// The server/gateway sweepers this one depends on only *start* the
		// deletions (the SDK's DeleteServer/DeleteGateway do not wait), so a
		// network can still report attachments here. Wait for the in-flight
		// deletions to release it before deleting; attachments that never
		// clear (e.g. non-test resources) surface as a delete error below.
		if len(network.ServerIDs) > 0 || len(network.GatewayIDs) > 0 {
			log.Printf("[INFO] Network %s still has %d server(s) / %d gateway(s) attached; waiting for in-flight deletions",
				network.ID, len(network.ServerIDs), len(network.GatewayIDs))
			if err := waitNetworkDetached(ctx, client, network.ID, 5*time.Minute); err != nil {
				log.Printf("[WARN] Network %s is still attached after waiting: %v (delete will likely fail)",
					network.ID, err)
			}
		}

		// Delete the network
		err := client.DeleteNetwork(ctx, network.ID)
		if err != nil {
			sweeperError := fmt.Errorf("error deleting network %s (%s): %w",
				network.Name, network.ID, err)
			log.Printf("[ERROR] %s", sweeperError)
			sweeperErrors = append(sweeperErrors, sweeperError)
			continue
		}

		log.Printf("[INFO] ✓ Successfully deleted network: %s (ID: %s)", network.Name, network.ID)
	}

	// Final statistics
	if sweeperCount > 0 {
		log.Printf("[INFO] Sweep summary: attempted to delete %d test networks", sweeperCount)
		if len(sweeperErrors) > 0 {
			log.Printf("[WARN] %d networks failed to delete", len(sweeperErrors))
		} else {
			log.Printf("[INFO] All test networks deleted successfully")
		}
	} else {
		log.Print("[INFO] No test networks found to sweep")
	}

	if len(sweeperErrors) > 0 {
		return fmt.Errorf("encountered %d errors during network sweep: %v",
			len(sweeperErrors), sweeperErrors)
	}

	log.Printf("[INFO] Network sweep completed successfully")
	return nil
}

// SweepServers deletes test servers (matched by the test-* name prefix).
func SweepServers(region string) error {
	log.Printf("[INFO] Starting sweep of VStack servers")

	client := GetTestClient()
	ctx := context.Background()

	servers, err := client.GetServerList(ctx)
	if err != nil {
		return fmt.Errorf("error listing servers: %w", err)
	}

	if len(servers) == 0 {
		log.Print("[DEBUG] No VStack servers to sweep")
		return nil
	}

	log.Printf("[INFO] Found %d servers, checking for test servers", len(servers))

	var sweeperErrors []error
	sweeperCount := 0

	for _, server := range servers {
		if !isTestResourceName(server.Name) {
			log.Printf("[DEBUG] Skipping server: %s (not a test server)", server.Name)
			continue
		}

		sweeperCount++
		log.Printf("[INFO] Deleting test server: %s (ID: %s, state: %s)",
			server.Name, server.ID, server.State)

		if err := client.DeleteServer(ctx, server.ID); err != nil {
			sweeperError := fmt.Errorf("error deleting server %s (%s): %w",
				server.Name, server.ID, err)
			log.Printf("[ERROR] %s", sweeperError)
			sweeperErrors = append(sweeperErrors, sweeperError)
			continue
		}

		log.Printf("[INFO] ✓ Successfully deleted server: %s (ID: %s)", server.Name, server.ID)
	}

	if sweeperCount == 0 {
		log.Print("[INFO] No test servers found to sweep")
	}

	if len(sweeperErrors) > 0 {
		return fmt.Errorf("encountered %d errors during server sweep: %v",
			len(sweeperErrors), sweeperErrors)
	}

	log.Printf("[INFO] Server sweep completed successfully")
	return nil
}

// SweepSSHKeys deletes test SSH keys (matched by the test-* name prefix).
func SweepSSHKeys(region string) error {
	log.Printf("[INFO] Starting sweep of VStack SSH keys")

	client := GetTestClient()
	ctx := context.Background()

	keys, err := client.GetSSHKeyList(ctx)
	if err != nil {
		return fmt.Errorf("error listing SSH keys: %w", err)
	}

	if len(keys) == 0 {
		log.Print("[DEBUG] No VStack SSH keys to sweep")
		return nil
	}

	log.Printf("[INFO] Found %d SSH keys, checking for test keys", len(keys))

	var sweeperErrors []error
	sweeperCount := 0

	for _, key := range keys {
		if !isTestResourceName(key.Name) {
			log.Printf("[DEBUG] Skipping SSH key: %s (not a test key)", key.Name)
			continue
		}

		sweeperCount++
		log.Printf("[INFO] Deleting test SSH key: %s (ID: %d)", key.Name, key.ID)

		if err := client.DeleteSSHKey(ctx, key.ID); err != nil {
			sweeperError := fmt.Errorf("error deleting SSH key %s (%d): %w", key.Name, key.ID, err)
			log.Printf("[ERROR] %s", sweeperError)
			sweeperErrors = append(sweeperErrors, sweeperError)
			continue
		}

		log.Printf("[INFO] ✓ Successfully deleted SSH key: %s (ID: %d)", key.Name, key.ID)
	}

	if sweeperCount == 0 {
		log.Print("[INFO] No test SSH keys found to sweep")
	}

	if len(sweeperErrors) > 0 {
		return fmt.Errorf("encountered %d errors during SSH key sweep: %v",
			len(sweeperErrors), sweeperErrors)
	}

	log.Printf("[INFO] SSH key sweep completed successfully")
	return nil
}

// SweepAffinityGroups deletes test affinity groups (matched by the test-* name prefix).
func SweepAffinityGroups(region string) error {
	log.Printf("[INFO] Starting sweep of VStack affinity groups")

	client := GetTestClient()
	ctx := context.Background()

	groups, err := client.GetAffinityGroupList(ctx)
	if err != nil {
		return fmt.Errorf("error listing affinity groups: %w", err)
	}

	if len(groups) == 0 {
		log.Print("[DEBUG] No VStack affinity groups to sweep")
		return nil
	}

	log.Printf("[INFO] Found %d affinity groups, checking for test groups", len(groups))

	var sweeperErrors []error
	sweeperCount := 0

	for _, group := range groups {
		if !isTestResourceName(group.Name) {
			log.Printf("[DEBUG] Skipping affinity group: %s (not a test group)", group.Name)
			continue
		}

		sweeperCount++
		log.Printf("[INFO] Deleting test affinity group: %s (ID: %s)", group.Name, group.ID)

		if err := client.DeleteAffinityGroup(ctx, group.ID); err != nil {
			sweeperError := fmt.Errorf("error deleting affinity group %s (%s): %w", group.Name, group.ID, err)
			log.Printf("[ERROR] %s", sweeperError)
			sweeperErrors = append(sweeperErrors, sweeperError)
			continue
		}

		log.Printf("[INFO] ✓ Successfully deleted affinity group: %s (ID: %s)", group.Name, group.ID)
	}

	if sweeperCount == 0 {
		log.Print("[INFO] No test affinity groups found to sweep")
	}

	if len(sweeperErrors) > 0 {
		return fmt.Errorf("encountered %d errors during affinity group sweep: %v",
			len(sweeperErrors), sweeperErrors)
	}

	log.Printf("[INFO] Affinity group sweep completed successfully")
	return nil
}

// SweepGateways deletes test gateways (matched by the test-* name prefix).
func SweepGateways(region string) error {
	log.Printf("[INFO] Starting sweep of VStack gateways")

	client := GetTestClient()
	ctx := context.Background()

	gateways, err := client.GetGatewayList(ctx)
	if err != nil {
		return fmt.Errorf("error listing gateways: %w", err)
	}

	if len(gateways) == 0 {
		log.Print("[DEBUG] No VStack gateways to sweep")
		return nil
	}

	log.Printf("[INFO] Found %d gateways, checking for test gateways", len(gateways))

	var sweeperErrors []error
	sweeperCount := 0

	for _, gw := range gateways {
		if !isTestResourceName(gw.Name) {
			log.Printf("[DEBUG] Skipping gateway: %s (not a test gateway)", gw.Name)
			continue
		}

		sweeperCount++
		log.Printf("[INFO] Deleting test gateway: %s (ID: %s, state: %s)", gw.Name, gw.ID, gw.State)

		if err := client.DeleteGateway(ctx, gw.ID); err != nil {
			sweeperError := fmt.Errorf("error deleting gateway %s (%s): %w", gw.Name, gw.ID, err)
			log.Printf("[ERROR] %s", sweeperError)
			sweeperErrors = append(sweeperErrors, sweeperError)
			continue
		}

		log.Printf("[INFO] ✓ Successfully deleted gateway: %s (ID: %s)", gw.Name, gw.ID)
	}

	if sweeperCount == 0 {
		log.Print("[INFO] No test gateways found to sweep")
	}

	if len(sweeperErrors) > 0 {
		return fmt.Errorf("encountered %d errors during gateway sweep: %v", len(sweeperErrors), sweeperErrors)
	}

	log.Printf("[INFO] Gateway sweep completed successfully")
	return nil
}

// SweepDomains deletes test DNS zones (matched by the test-* name prefix). Deleting a
// zone cascades to remove its records, so there are no dependencies.
func SweepDomains(region string) error {
	log.Printf("[INFO] Starting sweep of VStack DNS domains")

	client := GetTestClient()
	ctx := context.Background()

	domains, err := client.GetDomains(ctx)
	if err != nil {
		return fmt.Errorf("error listing DNS domains: %w", err)
	}

	if len(domains) == 0 {
		log.Print("[DEBUG] No VStack DNS domains to sweep")
		return nil
	}

	log.Printf("[INFO] Found %d DNS domains, checking for test domains", len(domains))

	var sweeperErrors []error
	sweeperCount := 0

	for _, d := range domains {
		if !isTestResourceName(d.Name) {
			log.Printf("[DEBUG] Skipping DNS domain: %s (not a test domain)", d.Name)
			continue
		}

		sweeperCount++
		log.Printf("[INFO] Deleting test DNS domain: %s", d.Name)

		if err := client.DeleteDomain(ctx, d.Name); err != nil {
			sweeperError := fmt.Errorf("error deleting DNS domain %s: %w", d.Name, err)
			log.Printf("[ERROR] %s", sweeperError)
			sweeperErrors = append(sweeperErrors, sweeperError)
			continue
		}

		log.Printf("[INFO] ✓ Successfully deleted DNS domain: %s", d.Name)
	}

	if sweeperCount == 0 {
		log.Print("[INFO] No test DNS domains found to sweep")
	}

	if len(sweeperErrors) > 0 {
		return fmt.Errorf("encountered %d errors during DNS domain sweep: %v", len(sweeperErrors), sweeperErrors)
	}

	log.Printf("[INFO] DNS domain sweep completed successfully")
	return nil
}

// SweepVmwareServers deletes test VMware servers (matched by the test-* name
// prefix). Deletion is asynchronous and the task finishes before the object
// disappears, so this waits: the network sweeper runs afterwards and a server
// that is still around holds its networks.
func SweepVmwareServers(region string) error {
	log.Printf("[INFO] Starting sweep of VMware servers")

	client := GetTestClient()
	ctx := context.Background()

	servers, err := client.GetVmwareServerList(ctx, nil)
	if err != nil {
		return fmt.Errorf("error listing VMware servers: %w", err)
	}

	var testServers []*entities.VmwareServer
	for _, server := range servers {
		if !isTestResourceName(server.Name) {
			log.Printf("[DEBUG] Skipping VMware server: %s (not a test server)", server.Name)
			continue
		}
		testServers = append(testServers, server)
	}

	if len(testServers) == 0 {
		log.Print("[INFO] No test VMware servers found to sweep")
		return nil
	}

	var sweeperErrors []error
	for i, server := range testServers {
		log.Printf("[INFO] [%d/%d] Deleting test VMware server: %s (ID: %d, state: %s)",
			i+1, len(testServers), server.Name, server.ID, server.State)

		if err := client.DeleteVmwareServerAndWait(ctx, server.ID); err != nil && !sdk.IsNotFound(err) {
			sweeperError := fmt.Errorf("error deleting VMware server %s (%d): %w", server.Name, server.ID, err)
			log.Printf("[ERROR] %s", sweeperError)
			sweeperErrors = append(sweeperErrors, sweeperError)
			continue
		}

		log.Printf("[INFO] ✓ Successfully deleted VMware server: %s (ID: %d)", server.Name, server.ID)
	}

	if len(sweeperErrors) > 0 {
		return fmt.Errorf("encountered %d errors during VMware server sweep: %v", len(sweeperErrors), sweeperErrors)
	}

	log.Printf("[INFO] VMware server sweep completed successfully")
	return nil
}

// SweepVmwareNetworks deletes test VMware networks (matched by the test-* name
// prefix). Runs after the server sweeper: a network with an interface on it
// cannot be deleted (-19511).
func SweepVmwareNetworks(region string) error {
	log.Printf("[INFO] Starting sweep of VMware networks")

	client := GetTestClient()
	ctx := context.Background()

	networks, err := client.GetVmwareNetworkList(ctx, nil)
	if err != nil {
		return fmt.Errorf("error listing VMware networks: %w", err)
	}

	var testNetworks []*entities.VmwareNetwork
	for _, network := range networks {
		if !isTestResourceName(network.Name) {
			log.Printf("[DEBUG] Skipping VMware network: %s (not a test network)", network.Name)
			continue
		}
		testNetworks = append(testNetworks, network)
	}

	if len(testNetworks) == 0 {
		log.Print("[INFO] No test VMware networks found to sweep")
		return nil
	}

	var sweeperErrors []error
	for i, network := range testNetworks {
		log.Printf("[INFO] [%d/%d] Deleting test VMware network: %s (ID: %d, NICs: %d)",
			i+1, len(testNetworks), network.Name, network.ID, network.NICsCount)

		if err := client.DeleteVmwareNetworkAndWait(ctx, network.ID); err != nil && !sdk.IsNotFound(err) {
			sweeperError := fmt.Errorf("error deleting VMware network %s (%d): %w", network.Name, network.ID, err)
			log.Printf("[ERROR] %s", sweeperError)
			sweeperErrors = append(sweeperErrors, sweeperError)
			continue
		}

		log.Printf("[INFO] ✓ Successfully deleted VMware network: %s (ID: %d)", network.Name, network.ID)
	}

	if len(sweeperErrors) > 0 {
		return fmt.Errorf("encountered %d errors during VMware network sweep: %v", len(sweeperErrors), sweeperErrors)
	}

	log.Printf("[INFO] VMware network sweep completed successfully")
	return nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// waitNetworkDetached polls the network until it reports no attached servers
// or gateways, or the timeout elapses. Used by SweepNetworks because the
// upstream sweepers only start server/gateway deletions asynchronously.
func waitNetworkDetached(ctx context.Context, client *sdk.CloudClient, networkID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		network, err := client.GetNetwork(ctx, networkID)
		switch {
		case sdk.IsNotFound(err):
			return nil // network vanished — nothing left to wait for
		case err == nil && len(network.ServerIDs) == 0 && len(network.GatewayIDs) == 0:
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("network %s still has attachments after %v: %w", networkID, timeout, ctx.Err())
		case <-time.After(10 * time.Second):
		}
	}
}

// isTestResourceName checks whether a resource name is a test name (by prefix).
// Deliberately narrow: a bare "test-" prefix would also match real resources
// in a shared account, so every acceptance test must name its resources with
// one of these prefixes to be swept.
func isTestResourceName(name string) bool {
	testPrefixes := []string{
		"test-acc-",
		"tf-acc-",
	}

	nameLower := strings.ToLower(name)
	for _, prefix := range testPrefixes {
		if strings.HasPrefix(nameLower, prefix) {
			return true
		}
	}

	return false
}
