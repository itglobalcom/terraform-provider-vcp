package vmware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	rsschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// A fake VMware API, and the scaffolding that drives a resource against it.
//
// Everything below runs in `go test`, with no credentials and no cloud. It is
// what covers the halves of a resource an acceptance test cannot reach cheaply:
// the reconciler that walks a NAT rule set one call at a time, what state holds
// when a call fails half-way through, and what a Read does when the object it
// describes has gone. Reaching those on a live stand means paying for a machine
// and then breaking it on purpose.
//
// The fake answers the shapes the contract documents; it is not a second
// implementation of the platform. What it is faithful about is the protocol —
// paths, envelopes, task ids — because that is what the SDK reads.

// ============================================================================
// The fake
// ============================================================================

type fakeAPI struct {
	*httptest.Server

	mu sync.Mutex

	// networks and servers the fake knows about, by id.
	networks map[int]*entities.VmwareNetwork
	servers  map[int]*entities.VmwareServer

	// edge state, by network id.
	firewalls map[int]*entities.VmwareEdgeFirewall
	natRules  map[int][]entities.VmwareEdgeNATRule

	// server firewall rule sets, by server id.
	serverFirewalls map[int][]entities.VmwareServerFirewallRule

	nextID int

	// serverOrders records the create requests the provider sent, decoded. An
	// order option is a *bool or a *int in the SDK, so "not asked for" and
	// "asked for and false" stay distinguishable — which is the whole difference
	// between an Optional+Computed attribute left out and one set to false.
	serverOrders []entities.VmwareCreateServerRequest

	// requests records every call, so a test can assert what the provider did
	// rather than only what it ended up with.
	requests []string

	// failNext makes the next matching call fail, which is how the half-applied
	// paths are reached.
	failNext map[string]int

	// failAfter lets a number of matching calls through and refuses every one
	// after them — a failure in the middle of a rule set, which is where the
	// interesting half of the reconciler lives.
	failAfter map[string]int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()

	api := &fakeAPI{
		networks:        map[int]*entities.VmwareNetwork{},
		servers:         map[int]*entities.VmwareServer{},
		firewalls:       map[int]*entities.VmwareEdgeFirewall{},
		natRules:        map[int][]entities.VmwareEdgeNATRule{},
		serverFirewalls: map[int][]entities.VmwareServerFirewallRule{},
		failNext:        map[string]int{},
		failAfter:       map[string]int{},
		nextID:          600,
	}
	api.Server = httptest.NewServer(http.HandlerFunc(api.route))
	t.Cleanup(api.Close)
	return api
}

// client builds an SDK client pointed at the fake.
func (a *fakeAPI) client(t *testing.T) *sdk.CloudClient {
	t.Helper()
	config, err := sdk.NewConfig("test-token", a.URL,
		// The provider's own waits are irrelevant here: the fake answers at once
		// and every task it starts is already finished.
		sdk.WithPollingInterval(time.Millisecond),
		sdk.WithPollingTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("building the SDK config: %v", err)
	}
	client, err := sdk.NewClient(config)
	if err != nil {
		t.Fatalf("building the SDK client: %v", err)
	}
	return client
}

// addRoutedNetwork registers a routed network, which is what an edge hangs off.
func (a *fakeAPI) addRoutedNetwork(id int, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	enabled, action := true, entities.VmwareEdgeFirewallActionAllow
	a.networks[id] = &entities.VmwareNetwork{
		ID: id, Type: entities.VmwareNetworkTypeRoutedClient, Name: name,
		State: entities.VmwareNetworkStateActive,
	}
	a.firewalls[id] = &entities.VmwareEdgeFirewall{Enabled: &enabled, DefaultAction: &action}
}

func (a *fakeAPI) addIsolatedNetwork(id int, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.networks[id] = &entities.VmwareNetwork{
		ID: id, Type: entities.VmwareNetworkTypePrivateClient, Name: name,
		State: entities.VmwareNetworkStateActive,
	}
}

func (a *fakeAPI) addServer(id int, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.servers[id] = &entities.VmwareServer{ID: id, Name: name, State: entities.VmwareServerStateActive}
	a.serverFirewalls[id] = nil
}

// addServerWithNestedHypervisor registers a server whose nested virtualization is
// in a known state — the starting point of a switch, and the reading a refresh
// has to bring back.
func (a *fakeAPI) addServerWithNestedHypervisor(id int, name string, enabled bool) {
	a.addServer(id, name)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.servers[id].NestedHypervisor = enabled
}

// nestedHypervisorOf reports what the fake holds for a server, which is what the
// platform would answer the next read — state agreeing with itself would pass a
// resource that never called the API at all.
func (a *fakeAPI) nestedHypervisorOf(id int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	server, ok := a.servers[id]
	return ok && server.NestedHypervisor
}

// failOn makes the next `times` matching requests fail with a 400. The key is
// "METHOD /path-suffix".
func (a *fakeAPI) failOn(key string, times int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failNext[key] = times
}

// failAfterN lets n matching requests through and refuses everything after them.
func (a *fakeAPI) failAfterN(key string, n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failAfter[key] = n
}

func (a *fakeAPI) calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.requests...)
}

// countCalls returns how many recorded calls contain the given substring.
func (a *fakeAPI) countCalls(substr string) int {
	n := 0
	for _, call := range a.calls() {
		if strings.Contains(call, substr) {
			n++
		}
	}
	return n
}

var (
	edgeNATPath      = regexp.MustCompile(`^/api/v1/vmware/networks/(\d+)/edge/nat/?(\d*)$`)
	edgeFirewallPath = regexp.MustCompile(`^/api/v1/vmware/networks/(\d+)/edge/firewall$`)
	networkPath      = regexp.MustCompile(`^/api/v1/vmware/networks/(\d+)$`)
	serverFWPath     = regexp.MustCompile(`^/api/v1/vmware/servers/(\d+)/firewall$`)
	taskPath         = regexp.MustCompile(`^/api/v1/tasks/(.+)$`)

	serversPath       = regexp.MustCompile(`^/api/v1/vmware/servers$`)
	serverPath        = regexp.MustCompile(`^/api/v1/vmware/servers/(\d+)$`)
	serverVolumesPath = regexp.MustCompile(`^/api/v1/vmware/servers/(\d+)/volumes$`)
	// The switch is two endpoints rather than one field: the action is in the path,
	// and there is no request body at all.
	serverNestedHypervisorPath = regexp.MustCompile(`^/api/v1/vmware/servers/(\d+)/nested-hypervisor/(enable|disable)$`)
)

func (a *fakeAPI) route(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.requests = append(a.requests, r.Method+" "+r.URL.Path)
	refuse := false
	for key, left := range a.failNext {
		method, suffix, _ := strings.Cut(key, " ")
		if r.Method == method && strings.HasSuffix(r.URL.Path, suffix) && left > 0 {
			a.failNext[key] = left - 1
			refuse = true
		}
	}
	for key, left := range a.failAfter {
		method, suffix, _ := strings.Cut(key, " ")
		if r.Method != method || !strings.HasSuffix(r.URL.Path, suffix) {
			continue
		}
		if left > 0 {
			a.failAfter[key] = left - 1
		} else {
			refuse = true
		}
	}
	a.mu.Unlock()

	if refuse {
		a.fail(w, http.StatusBadRequest, -2002, "the request body is not formatted or not specified")
		return
	}

	switch {
	case taskPath.MatchString(r.URL.Path):
		// Every task the fake starts is already finished — the provider's waiting
		// is the SDK's business and is tested there.
		id := taskPath.FindStringSubmatch(r.URL.Path)[1]
		a.writeJSON(w, map[string]any{"task": entities.VmwareTask{
			ID: id, State: entities.VmwareTaskStateCompleted,
		}})
	case edgeNATPath.MatchString(r.URL.Path):
		a.handleNAT(w, r)
	case edgeFirewallPath.MatchString(r.URL.Path):
		a.handleEdgeFirewall(w, r)
	case serverFWPath.MatchString(r.URL.Path):
		a.handleServerFirewall(w, r)
	case serverNestedHypervisorPath.MatchString(r.URL.Path):
		a.handleServerNestedHypervisor(w, r)
	case serverVolumesPath.MatchString(r.URL.Path):
		a.handleServerVolumes(w, r)
	case serverPath.MatchString(r.URL.Path):
		a.handleServer(w, r)
	case serversPath.MatchString(r.URL.Path):
		a.handleServers(w, r)
	case networkPath.MatchString(r.URL.Path):
		a.handleNetwork(w, r)
	default:
		a.fail(w, http.StatusNotFound, -404, "no such endpoint: "+r.URL.Path)
	}
}

func (a *fakeAPI) handleNetwork(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(networkPath.FindStringSubmatch(r.URL.Path)[1])

	a.mu.Lock()
	network, ok := a.networks[id]
	a.mu.Unlock()
	if !ok {
		a.fail(w, http.StatusNotFound, -404, "network not found")
		return
	}
	a.writeJSON(w, map[string]any{"network": network})
}

func (a *fakeAPI) handleEdgeFirewall(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(edgeFirewallPath.FindStringSubmatch(r.URL.Path)[1])

	a.mu.Lock()
	defer a.mu.Unlock()

	firewall, ok := a.firewalls[id]
	if !ok {
		a.fail(w, http.StatusNotFound, -404, "network not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// NET-11: the firewall answer is wrapped.
		a.writeJSON(w, map[string]any{"firewall": firewall})
	case http.MethodPut:
		var req entities.VmwareUpdateEdgeFirewallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			a.fail(w, http.StatusBadRequest, -2002, "bad body")
			return
		}
		// NET-13: an omitted field leaves the current value alone.
		if req.Enabled != nil {
			firewall.Enabled = req.Enabled
		}
		if req.DefaultAction != nil {
			firewall.DefaultAction = req.DefaultAction
		}
		// NET-7: the backend derives the description from the name and forces the
		// rule enabled, whatever the request said.
		firewall.Rules = nil
		for _, rule := range req.Rules {
			enabled := true
			firewall.Rules = append(firewall.Rules, entities.VmwareEdgeFirewallRule{
				Enabled: &enabled, Description: rule.Name, Name: rule.Name,
				Action: &rule.Action, Protocol: rule.Protocol,
				Source: rule.Source, SourcePort: rule.SourcePort,
				Destination: rule.Destination, DestinationPort: rule.DestinationPort,
			})
		}
		a.writeTask(w, "vmw1001")
	default:
		a.fail(w, http.StatusMethodNotAllowed, -405, "method not allowed")
	}
}

func (a *fakeAPI) handleNAT(w http.ResponseWriter, r *http.Request) {
	match := edgeNATPath.FindStringSubmatch(r.URL.Path)
	networkID, _ := strconv.Atoi(match[1])
	ruleID, _ := strconv.Atoi(match[2])

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.networks[networkID]; !ok {
		a.fail(w, http.StatusNotFound, -404, "network not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// C-10: NAT answers flat, without the single-key envelope.
		rules := a.natRules[networkID]
		if rules == nil {
			rules = []entities.VmwareEdgeNATRule{}
		}
		a.writeJSON(w, entities.VmwareEdgeNAT{Rules: rules})

	case http.MethodPost:
		var req entities.VmwareUpsertNATRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			a.fail(w, http.StatusBadRequest, -2002, "bad body")
			return
		}
		rule := a.natRuleFromRequest(req)

		if req.RuleID != nil {
			for i := range a.natRules[networkID] {
				if a.natRules[networkID][i].ID != nil && *a.natRules[networkID][i].ID == *req.RuleID {
					rule.ID = req.RuleID
					a.natRules[networkID][i] = rule
					a.writeTask(w, "vmw1002")
					return
				}
			}
			a.fail(w, http.StatusNotFound, -404, "no such rule")
			return
		}

		a.nextID++
		id := a.nextID
		rule.ID = &id
		a.natRules[networkID] = append(a.natRules[networkID], rule)
		a.writeTask(w, "vmw1003")

	case http.MethodDelete:
		rules := a.natRules[networkID]
		for i := range rules {
			if rules[i].ID != nil && *rules[i].ID == ruleID {
				a.natRules[networkID] = append(rules[:i:i], rules[i+1:]...)
				a.writeTask(w, "vmw1004")
				return
			}
		}
		a.fail(w, http.StatusNotFound, -404, "no such rule")

	default:
		a.fail(w, http.StatusMethodNotAllowed, -405, "method not allowed")
	}
}

// natRuleFromRequest applies the platform's own substitution: NET-4 replaces the
// original address of a DNAT rule with the edge's external one, whatever the
// request carried. That behaviour is the reason the attribute is Optional +
// Computed, so the fake has to reproduce it or the tests prove nothing.
func (a *fakeAPI) natRuleFromRequest(req entities.VmwareUpsertNATRuleRequest) entities.VmwareEdgeNATRule {
	const edgeExternalIP = "203.0.113.5"

	ruleType := req.Type
	originalIP := req.OriginalIP
	if strings.EqualFold(ruleType, entities.VmwareEdgeNATTypeDNAT) {
		originalIP = edgeExternalIP
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	rule := entities.VmwareEdgeNATRule{
		Type: &ruleType, Protocol: &req.Protocol,
		OriginalIP: &originalIP, TranslatedIP: &req.TranslatedIP,
		Enabled: &enabled,
	}
	if req.Description != "" {
		rule.Description = &req.Description
	}
	if req.OriginalPort != "" {
		rule.OriginalPort = &req.OriginalPort
	}
	if req.TranslatedPort != "" {
		rule.TranslatedPort = &req.TranslatedPort
	}
	return rule
}

func (a *fakeAPI) handleServerFirewall(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(serverFWPath.FindStringSubmatch(r.URL.Path)[1])

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.servers[id]; !ok {
		// SRV-2: a 404 here means "no such server", never "no rules".
		a.fail(w, http.StatusNotFound, -404, "server not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		rules := a.serverFirewalls[id]
		if rules == nil {
			rules = []entities.VmwareServerFirewallRule{}
		}
		a.writeJSON(w, map[string]any{"rules": rules})
	case http.MethodPut:
		var req entities.VmwareUpdateServerFirewallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			a.fail(w, http.StatusBadRequest, -2002, "bad body")
			return
		}
		a.serverFirewalls[id] = req.Rules
		a.writeTask(w, "vmw1005")
	default:
		a.fail(w, http.StatusMethodNotAllowed, -405, "method not allowed")
	}
}

func (a *fakeAPI) handleServer(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(serverPath.FindStringSubmatch(r.URL.Path)[1])

	a.mu.Lock()
	server, ok := a.servers[id]
	a.mu.Unlock()
	if !ok {
		a.fail(w, http.StatusNotFound, -404, "server not found")
		return
	}
	if r.Method != http.MethodGet {
		a.fail(w, http.StatusMethodNotAllowed, -405, "method not allowed")
		return
	}
	a.writeJSON(w, map[string]any{"server": server})
}

// handleServers answers the order. The fake records the request and creates the
// machine the order describes, so a test can check both what was sent and what
// the next read brings back.
func (a *fakeAPI) handleServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.fail(w, http.StatusMethodNotAllowed, -405, "method not allowed")
		return
	}
	var req entities.VmwareCreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.fail(w, http.StatusBadRequest, -2002, "bad body")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.serverOrders = append(a.serverOrders, req)
	a.nextID++
	id := a.nextID
	server := &entities.VmwareServer{
		ID: id, LocationID: req.LocationID, Name: req.Name, ImageID: req.ImageID,
		CPU: req.CPUCount, RamMB: req.RamMB, SystemDiskMB: req.SystemDiskSizeMB,
		State: entities.VmwareServerStateActive, IsPowerOn: true,
	}
	// Omitted means off — the platform's own default, and the reason the attribute
	// can be left out of a configuration at all.
	if req.NestedHypervisor != nil {
		server.NestedHypervisor = *req.NestedHypervisor
	}
	a.servers[id] = server
	a.serverFirewalls[id] = nil
	a.writeJSON(w, entities.VmwareServerOrder{ServerID: id, TaskID: "vmw1006"})
}

// handleServerVolumes answers the volume list every server apply ends with. No
// test here is about data disks, so the machine has none.
func (a *fakeAPI) handleServerVolumes(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(serverVolumesPath.FindStringSubmatch(r.URL.Path)[1])

	a.mu.Lock()
	_, ok := a.servers[id]
	a.mu.Unlock()
	if !ok {
		a.fail(w, http.StatusNotFound, -404, "server not found")
		return
	}
	a.writeJSON(w, map[string]any{"volumes": []entities.VmwareVolume{}})
}

// handleServerNestedHypervisor switches nested virtualization, reproducing the
// one thing about this endpoint the SDK reads: a request that matches the current
// state is answered 200 with a null task id, because there is nothing to do. A
// caller that awaited that as a task would hang on an id that does not exist.
func (a *fakeAPI) handleServerNestedHypervisor(w http.ResponseWriter, r *http.Request) {
	match := serverNestedHypervisorPath.FindStringSubmatch(r.URL.Path)
	id, _ := strconv.Atoi(match[1])
	enable := match[2] == "enable"

	a.mu.Lock()
	defer a.mu.Unlock()

	server, ok := a.servers[id]
	if !ok {
		a.fail(w, http.StatusNotFound, -404, "server not found")
		return
	}
	if r.Method != http.MethodPost {
		a.fail(w, http.StatusMethodNotAllowed, -405, "method not allowed")
		return
	}
	if server.NestedHypervisor == enable {
		a.writeJSON(w, map[string]any{"task_id": nil})
		return
	}
	server.NestedHypervisor = enable
	a.writeTask(w, "vmw1007")
}

func (a *fakeAPI) writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (a *fakeAPI) writeTask(w http.ResponseWriter, taskID string) {
	a.writeJSON(w, map[string]any{"task_id": taskID})
}

func (a *fakeAPI) fail(w http.ResponseWriter, status, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]any{{"code": code, "message": message}},
	})
}

// ============================================================================
// Driving a resource
// ============================================================================

// configure hands the resource its client, the way the provider does.
func configure(t *testing.T, res resource.Resource, client *sdk.CloudClient) {
	t.Helper()
	withConfigure, ok := res.(resource.ResourceWithConfigure)
	if !ok {
		t.Fatalf("%T cannot be configured", res)
	}
	resp := resource.ConfigureResponse{}
	withConfigure.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configuring %T: %v", res, resp.Diagnostics)
	}
}

// resourceSchema returns a resource's schema.
func resourceSchema(t *testing.T, res resource.Resource) rsschema.Schema {
	t.Helper()
	resp := resource.SchemaResponse{}
	res.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("reading the schema of %T: %v", res, resp.Diagnostics)
	}
	return resp.Schema
}

// emptyValue is a null value of the schema's type — the starting point for a
// plan or a state that is about to be filled in from a model.
func emptyValue(ctx context.Context, s rsschema.Schema) tftypes.Value {
	return tftypes.NewValue(s.Type().TerraformType(ctx), nil)
}

// planOf turns a model into the plan the framework would hand a resource.
func planOf(t *testing.T, s rsschema.Schema, model any) tfsdk.Plan {
	t.Helper()
	ctx := context.Background()
	plan := tfsdk.Plan{Schema: s, Raw: emptyValue(ctx, s)}
	if diags := plan.Set(ctx, model); diags.HasError() {
		t.Fatalf("building the plan: %v", diags)
	}
	return plan
}

// configOf turns a model into a configuration, for ValidateConfig. A Config is
// read-only, so the value is built through a State and handed over.
func configOf(t *testing.T, s rsschema.Schema, model any) tfsdk.Config {
	t.Helper()
	return tfsdk.Config{Schema: s, Raw: stateOf(t, s, model).Raw}
}

// stateOf turns a model into prior state.
func stateOf(t *testing.T, s rsschema.Schema, model any) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	state := tfsdk.State{Schema: s, Raw: emptyValue(ctx, s)}
	if diags := state.Set(ctx, model); diags.HasError() {
		t.Fatalf("building the state: %v", diags)
	}
	return state
}

// emptyState is the state a Create writes into.
func emptyState(s rsschema.Schema) tfsdk.State {
	return tfsdk.State{Schema: s, Raw: emptyValue(context.Background(), s)}
}

// readModel pulls a model back out of the state a resource produced.
func readModel(t *testing.T, state tfsdk.State, into any) {
	t.Helper()
	if state.Raw.IsNull() {
		t.Fatal("the resource left no state behind")
	}
	if diags := state.Get(context.Background(), into); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
}
