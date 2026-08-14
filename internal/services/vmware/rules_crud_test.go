package vmware

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// The rule resources driven end to end against the fake API: what they send,
// what they record, and what they do when a call fails half-way. These are the
// paths an acceptance test cannot reach cheaply — provoking a failure in the
// middle of a rule set means breaking a live edge on purpose.

// ============================================================================
// Edge NAT — the reconciler
// ============================================================================

func natRuleModel(ruleType, protocol, originalIP, originalPort, translatedIP string) edgeNATRuleModel {
	rule := edgeNATRuleModel{
		Type:           types.StringValue(ruleType),
		Protocol:       types.StringValue(protocol),
		Description:    types.StringNull(),
		OriginalIP:     types.StringNull(),
		OriginalPort:   types.StringNull(),
		TranslatedIP:   types.StringNull(),
		TranslatedPort: types.StringNull(),
		Enabled:        types.BoolNull(),
	}
	if originalIP != "" {
		rule.OriginalIP = types.StringValue(originalIP)
	}
	if originalPort != "" {
		rule.OriginalPort = types.StringValue(originalPort)
	}
	if translatedIP != "" {
		rule.TranslatedIP = types.StringValue(translatedIP)
	}
	return rule
}

func dnatRule(port, target string) edgeNATRuleModel {
	return natRuleModel(entities.VmwareEdgeNATTypeDNAT, "tcp", "", port, target)
}

// createNAT runs a Create and returns the state it left behind.
func createNAT(t *testing.T, api *fakeAPI, networkID int64, rules []edgeNATRuleModel) (edgeNATModel, resource.CreateResponse) {
	t.Helper()
	res := &edgeNATResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	plan := edgeNATModel{ID: types.Int64Unknown(), NetworkID: types.Int64Value(networkID), Rules: rules}
	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)

	var out edgeNATModel
	if !resp.State.Raw.IsNull() {
		readModel(t, resp.State, &out)
	}
	return out, resp
}

// updateNAT runs an Update from prior state to a new rule list.
func updateNAT(t *testing.T, api *fakeAPI, networkID int64,
	prior, planned []edgeNATRuleModel) (edgeNATModel, resource.UpdateResponse) {
	t.Helper()
	res := &edgeNATResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	state := edgeNATModel{ID: types.Int64Value(networkID), NetworkID: types.Int64Value(networkID), Rules: prior}
	plan := edgeNATModel{ID: types.Int64Value(networkID), NetworkID: types.Int64Value(networkID), Rules: planned}

	resp := resource.UpdateResponse{State: emptyState(s)}
	res.Update(context.Background(), resource.UpdateRequest{
		Plan: planOf(t, s, plan), State: stateOf(t, s, state),
	}, &resp)

	var out edgeNATModel
	if !resp.State.Raw.IsNull() {
		readModel(t, resp.State, &out)
	}
	return out, resp
}

// A first apply creates every rule, and NET-4 lands where it should: the DNAT
// rule's original address is the edge's, not anything the configuration said,
// and it is recorded rather than left null.
func TestEdgeNATCreate(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	state, resp := createNAT(t, api, 2479, []edgeNATRuleModel{
		dnatRule("443", "10.0.0.10"),
		natRuleModel(entities.VmwareEdgeNATTypeSNAT, "any", "10.0.0.0/24", "", ""),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	if len(state.Rules) != 2 {
		t.Fatalf("state holds %d rules, want 2", len(state.Rules))
	}
	if got := state.Rules[0].OriginalIP.ValueString(); got != "203.0.113.5" {
		t.Errorf("original_ip = %q, want the edge address the platform substituted (NET-4)", got)
	}
	if !state.Rules[0].Enabled.ValueBool() {
		t.Error("NET-2: a rule is created passing traffic unless asked otherwise")
	}
	if state.ID.ValueInt64() != 2479 {
		t.Errorf("id = %v, want the network id", state.ID)
	}
	// Two rules, two creates — and no deletes on a first apply.
	if got := api.countCalls("POST /api/v1/vmware/networks/2479/edge/nat"); got != 2 {
		t.Errorf("made %d create calls, want 2", got)
	}
	if got := api.countCalls("DELETE"); got != 0 {
		t.Errorf("made %d delete calls on a first apply, want none", got)
	}
}

// Growing a set overwrites what is there and creates the rest; shrinking deletes
// the tail. This is the reconciler's whole contract, checked against what the
// API ends up holding rather than against the provider's own state.
func TestEdgeNATGrowAndShrink(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	first, resp := createNAT(t, api, 2479, []edgeNATRuleModel{dnatRule("443", "10.0.0.10")})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}
	ruleID := *api.natRules[2479][0].ID

	// Grown to three: the first is overwritten in place, two are created.
	grown, updateResp := updateNAT(t, api, 2479, first.Rules, []edgeNATRuleModel{
		dnatRule("443", "10.0.0.10"),
		dnatRule("8080", "10.0.0.11"),
		dnatRule("8443", "10.0.0.12"),
	})
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}
	if len(api.natRules[2479]) != 3 {
		t.Fatalf("the edge holds %d rules, want 3", len(api.natRules[2479]))
	}
	if got := *api.natRules[2479][0].ID; got != ruleID {
		t.Errorf("the first rule is now id %d, was %d — it should have been overwritten, not replaced", got, ruleID)
	}

	// Back to one: the two extra rules are deleted, not left behind.
	_, shrinkResp := updateNAT(t, api, 2479, grown.Rules, []edgeNATRuleModel{dnatRule("443", "10.0.0.10")})
	if shrinkResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", shrinkResp.Diagnostics)
	}
	if len(api.natRules[2479]) != 1 {
		t.Fatalf("the edge holds %d rules, want 1", len(api.natRules[2479]))
	}
	if got := *api.natRules[2479][0].ID; got != ruleID {
		t.Errorf("the surviving rule is id %d, want the original %d", got, ruleID)
	}
}

// The rule set is applied one call at a time, so a failure lands in the middle of
// it. What state holds afterwards is the whole question: the rules already
// written have to be in it, or the next apply writes them a second time.
func TestEdgeNATCreatePartialFailureRecordsWhatWasApplied(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	// Two rules go through; the third is refused.
	api.failAfterN("POST /edge/nat", 2)

	res := &edgeNATResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	plan := edgeNATModel{
		ID:        types.Int64Unknown(),
		NetworkID: types.Int64Value(2479),
		Rules: []edgeNATRuleModel{
			dnatRule("443", "10.0.0.10"),
			dnatRule("8080", "10.0.0.11"),
			dnatRule("8443", "10.0.0.12"),
		},
	}

	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("a refused rule must fail the apply")
	}
	// The message has to name the rule the apply stopped at — "something went
	// wrong" leaves the user to find out which of the three it was.
	if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "rules[2]") {
		t.Errorf("the error should name the rule it stopped at, got: %s", detail)
	}

	var state edgeNATModel
	if resp.State.Raw.IsNull() {
		t.Fatal("a half-applied change must still leave state behind, or the rules already written are lost")
	}
	readModel(t, resp.State, &state)

	if len(state.Rules) != 2 {
		t.Errorf("state holds %d rules, want the 2 that were applied", len(state.Rules))
	}
	if len(api.natRules[2479]) != 2 {
		t.Errorf("the edge holds %d rules, want 2", len(api.natRules[2479]))
	}
}

// A network deleted elsewhere takes its edge with it. Read has to drop the
// resource rather than fail the refresh, so the next plan offers to build it
// again.
func TestEdgeNATReadDropsResourceWhenNetworkIsGone(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	state, resp := createNAT(t, api, 2479, []edgeNATRuleModel{dnatRule("443", "10.0.0.10")})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	api.mu.Lock()
	delete(api.networks, 2479)
	delete(api.natRules, 2479)
	api.mu.Unlock()

	res := &edgeNATResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	readResp := resource.ReadResponse{State: stateOf(t, s, state)}
	res.Read(context.Background(), resource.ReadRequest{State: stateOf(t, s, state)}, &readResp)

	if readResp.Diagnostics.HasError() {
		t.Fatalf("a vanished network must not fail the refresh: %v", readResp.Diagnostics)
	}
	if !readResp.State.Raw.IsNull() {
		t.Error("the resource must be dropped from state when its network is gone")
	}
}

// A rule set is owned whole, so a rule added in the panel has to disappear from
// the edge on the next apply — there is no merge.
func TestEdgeNATUpdateRemovesRulesAddedElsewhere(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	state, resp := createNAT(t, api, 2479, []edgeNATRuleModel{dnatRule("443", "10.0.0.10")})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	// Somebody adds a rule from the panel.
	api.mu.Lock()
	api.nextID++
	strayID := api.nextID
	stray := "dnat"
	api.natRules[2479] = append(api.natRules[2479], entities.VmwareEdgeNATRule{ID: &strayID, Type: &stray})
	api.mu.Unlock()

	_, updateResp := updateNAT(t, api, 2479, state.Rules, []edgeNATRuleModel{dnatRule("443", "10.0.0.10")})
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}
	if len(api.natRules[2479]) != 1 {
		t.Errorf("the edge holds %d rules, want the 1 the configuration declares", len(api.natRules[2479]))
	}
}

// Destroying the resource clears the edge — the whole set is its own.
func TestEdgeNATDeleteClearsTheEdge(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	state, resp := createNAT(t, api, 2479, []edgeNATRuleModel{
		dnatRule("443", "10.0.0.10"),
		dnatRule("8080", "10.0.0.11"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	res := &edgeNATResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	deleteResp := resource.DeleteResponse{State: stateOf(t, s, state)}
	res.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, s, state)}, &deleteResp)

	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", deleteResp.Diagnostics)
	}
	if len(api.natRules[2479]) != 0 {
		t.Errorf("the edge still holds %d rules after the resource was destroyed", len(api.natRules[2479]))
	}
}

// NAT belongs to a network that has an edge. The provider reads the network
// first so the refusal names the type, instead of letting the API answer
// something about an object the user never mentioned.
func TestEdgeNATRefusesNetworkWithoutAnEdge(t *testing.T) {
	api := newFakeAPI(t)
	api.addIsolatedNetwork(2480, "private")

	_, resp := createNAT(t, api, 2480, []edgeNATRuleModel{dnatRule("443", "10.0.0.10")})

	if !resp.Diagnostics.HasError() {
		t.Fatal("an isolated network has no edge to put NAT rules on")
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(detail, "isolated") || !strings.Contains(detail, "routed") {
		t.Errorf("the message should name the network's type and the one it needs, got: %s", detail)
	}
	// And nothing was attempted on the edge.
	if got := api.countCalls("edge/nat"); got != 0 {
		t.Errorf("made %d edge calls before refusing, want none", got)
	}
}

// ============================================================================
// Edge firewall
// ============================================================================

func edgeFirewallRule(name, action, protocol, destinationPort string) edgeFirewallRuleModel {
	return edgeFirewallRuleModel{
		Name:            types.StringValue(name),
		Action:          types.StringValue(action),
		Protocol:        types.StringValue(protocol),
		Source:          types.StringNull(),
		SourcePort:      types.StringNull(),
		Destination:     types.StringNull(),
		DestinationPort: types.StringValue(destinationPort),
	}
}

func TestEdgeFirewallCreateAndDelete(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	res := &edgeFirewallResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	plan := edgeFirewallModel{
		ID:            types.Int64Unknown(),
		NetworkID:     types.Int64Value(2479),
		DefaultAction: types.StringValue(entities.VmwareEdgeFirewallActionDeny),
		Rules: []edgeFirewallRuleModel{
			edgeFirewallRule("allow-https", entities.VmwareEdgeFirewallActionAllow, "tcp", "443"),
		},
	}

	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	var state edgeFirewallModel
	readModel(t, resp.State, &state)

	if state.DefaultAction.ValueString() != entities.VmwareEdgeFirewallActionDeny {
		t.Errorf("default_action = %q, want deny", state.DefaultAction.ValueString())
	}
	if len(state.Rules) != 1 || state.Rules[0].Name.ValueString() != "allow-https" {
		t.Fatalf("state holds %+v, want the one configured rule", state.Rules)
	}
	// Creating the resource switches the firewall on — its existence is what says so.
	if api.firewalls[2479].Enabled == nil || !*api.firewalls[2479].Enabled {
		t.Error("the firewall must be on after the resource is created")
	}

	// Destroying it switches the firewall off and clears the rules.
	deleteResp := resource.DeleteResponse{State: stateOf(t, s, state)}
	res.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, s, state)}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", deleteResp.Diagnostics)
	}
	if api.firewalls[2479].Enabled == nil || *api.firewalls[2479].Enabled {
		t.Error("the firewall must be off after the resource is destroyed")
	}
	if len(api.firewalls[2479].Rules) != 0 {
		t.Errorf("the edge still holds %d rules after the resource was destroyed", len(api.firewalls[2479].Rules))
	}
}

// NET-7: the backend forces a rule enabled and derives its description from the
// name, whatever the request said. The model has no such fields, so what matters
// is that reading them back changes nothing the user wrote.
func TestEdgeFirewallIgnoresServerDerivedFields(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	res := &edgeFirewallResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	rule := edgeFirewallRule("allow-https", entities.VmwareEdgeFirewallActionAllow, "tcp", "443")
	plan := edgeFirewallModel{
		ID: types.Int64Unknown(), NetworkID: types.Int64Value(2479),
		DefaultAction: types.StringValue(entities.VmwareEdgeFirewallActionDeny),
		Rules:         []edgeFirewallRuleModel{rule},
	}

	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	var state edgeFirewallModel
	readModel(t, resp.State, &state)

	// The attributes the configuration left out came back as the platform stored
	// them; the ones it set are unchanged.
	if state.Rules[0].Name.ValueString() != "allow-https" {
		t.Errorf("name = %q, want the configured one", state.Rules[0].Name.ValueString())
	}
	if state.Rules[0].DestinationPort.ValueString() != "443" {
		t.Errorf("destination_port = %q, want 443", state.Rules[0].DestinationPort.ValueString())
	}
}

// ============================================================================
// Server firewall
// ============================================================================

func TestServerFirewallCreateReadDelete(t *testing.T) {
	api := newFakeAPI(t)
	api.addServer(5678, "web-01")

	res := &serverFirewallResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	rule := serverFirewallRuleModel{
		Name:             types.StringValue("ssh-office"),
		TrafficDirection: types.StringValue(entities.VmwareTrafficDirectionIncoming),
		Action:           types.StringValue(entities.VmwareFirewallActionAllow),
		Protocol:         types.StringValue("tcp"),
		Source:           types.StringValue("203.0.113.0/24"),
		SourcePort:       types.StringNull(),
		Destination:      types.StringNull(),
		DestinationPort:  types.StringValue("22"),
	}
	plan := serverFirewallModel{
		ID: types.Int64Unknown(), ServerID: types.Int64Value(5678),
		Rules: []serverFirewallRuleModel{rule},
	}

	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}

	var state serverFirewallModel
	readModel(t, resp.State, &state)
	if len(state.Rules) != 1 {
		t.Fatalf("state holds %d rules, want 1", len(state.Rules))
	}
	// An attribute the rule left out must stay absent rather than turn into an
	// empty string, which the API would store as a literal value.
	if !state.Rules[0].SourcePort.IsNull() {
		t.Errorf("source_port = %q, want null", state.Rules[0].SourcePort.ValueString())
	}
	if got := api.serverFirewalls[5678]; len(got) != 1 || got[0].Name != "ssh-office" {
		t.Errorf("the server holds %+v, want the one configured rule", got)
	}

	// SRV-2: a server with no rules answers with an empty set, so a 404 means the
	// server itself is gone — and then the resource leaves state.
	api.mu.Lock()
	delete(api.servers, 5678)
	api.mu.Unlock()

	readResp := resource.ReadResponse{State: stateOf(t, s, state)}
	res.Read(context.Background(), resource.ReadRequest{State: stateOf(t, s, state)}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("a vanished server must not fail the refresh: %v", readResp.Diagnostics)
	}
	if !readResp.State.Raw.IsNull() {
		t.Error("the resource must be dropped from state when its server is gone")
	}
}

// An empty rule list is a configuration a user is expected to write, and the one
// most likely to be mistaken for "nothing to do".
func TestServerFirewallAppliesAnEmptySet(t *testing.T) {
	api := newFakeAPI(t)
	api.addServer(5678, "web-01")
	api.serverFirewalls[5678] = []entities.VmwareServerFirewallRule{{
		Name: "stray", TrafficDirection: entities.VmwareTrafficDirectionIncoming,
		Action: entities.VmwareFirewallActionAllow, Protocol: "tcp",
	}}

	res := &serverFirewallResource{}
	configure(t, res, api.client(t))
	s := resourceSchema(t, res)

	plan := serverFirewallModel{
		ID: types.Int64Unknown(), ServerID: types.Int64Value(5678),
		Rules: []serverFirewallRuleModel{},
	}

	resp := resource.CreateResponse{State: emptyState(s)}
	res.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, s, plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", resp.Diagnostics)
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("an empty rule set is still a resource — it must leave state behind")
	}

	var state serverFirewallModel
	readModel(t, resp.State, &state)
	if state.Rules == nil {
		t.Error("an empty set must be an empty list in state, never null")
	}
	if len(api.serverFirewalls[5678]) != 0 {
		t.Errorf("the server still holds %d rules", len(api.serverFirewalls[5678]))
	}
	// Adopting a server that already had rules is worth saying out loud.
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Error("replacing rules that were already there should warn")
	}
}

// The reconciler starts by reading what the edge already holds — that read is
// what every later decision is made from. If it fails, nothing may be written:
// applying a list against an unknown starting point would overwrite rules
// chosen by position that was never established.
func TestEdgeNATCreateWritesNothingWhenTheFirstReadFails(t *testing.T) {
	api := newFakeAPI(t)
	api.addRoutedNetwork(2479, "app")

	// Both reads of the rule set fail: the courtesy one before the write, and the
	// one the reconciler depends on.
	api.failOn("GET /edge/nat", 2)

	_, resp := createNAT(t, api, 2479, []edgeNATRuleModel{dnatRule("443", "10.0.0.10")})

	if !resp.Diagnostics.HasError() {
		t.Fatal("a failed read of the current rules must fail the apply")
	}
	if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "none were applied") {
		t.Errorf("the error should say that nothing was written, got: %s", detail)
	}
	if got := api.countCalls("POST /api/v1/vmware/networks/2479/edge/nat"); got != 0 {
		t.Errorf("wrote %d rule(s) without knowing what was there, want none", got)
	}
}
