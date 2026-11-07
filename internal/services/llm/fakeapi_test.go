package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rsschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// A fake LLM API, and the scaffolding that drives a resource against it.
//
// It covers what an acceptance test reaches only by breaking a live stand on
// purpose: a connection that already exists, a secret that could not be taken
// after the issue, a group disabled out of band, and two applies racing on one
// group. The fake is faithful to the protocol — paths, envelopes, task ids and
// the task's resources[] — because that is what the SDK reads; answers leave a
// field out rather than send it empty, as the API does.

// ============================================================================
// The fake
// ============================================================================

type fakeGroup struct {
	name   string
	state  entities.LLMGroupState
	models []entities.LLMModelID
	tags   []string
}

type fakeKey struct {
	group  int
	name   string
	state  entities.LLMAccessKeyState
	locks  *entities.LLMAccessKeyLocks
	tags   []string
	secret string
}

type fakeAPI struct {
	*httptest.Server

	mu sync.Mutex

	projectID  int
	connection entities.LLMConnection

	groups map[int]*fakeGroup
	keys   map[int]*fakeKey

	// tasks holds the resources each finished task reports, by task id.
	tasks map[string][]entities.TaskResource

	// raw holds a literal answer by path (without /api/v1/), for the catalog
	// and package reads: the wire form a data source has to map.
	raw map[string]string

	nextID   int
	nextTask int

	// requests records every call as "METHOD /path" and bodies its body, so a
	// test asserts what the provider sent rather than what it ended up with.
	requests []string
	bodies   []string

	// failNext makes the next matching calls fail; the key is "METHOD suffix"
	// and the value the status and how many times.
	failNext map[string]failure

	// onMutation, when set, runs (outside the lock) for every mutating call
	// under llm/groups/{g}/, before it is answered.
	onMutation func(group int)
}

type failure struct {
	status, times int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{
		projectID:  4242,
		connection: entities.LLMConnection{State: entities.LLMConnectionStateNotConnected},
		groups:     map[int]*fakeGroup{},
		keys:       map[int]*fakeKey{},
		tasks:      map[string][]entities.TaskResource{},
		raw:        map[string]string{},
		failNext:   map[string]failure{},
		nextID:     700,
	}
	api.Server = httptest.NewServer(http.HandlerFunc(api.route))
	t.Cleanup(api.Close)
	return api
}

// client builds an SDK client pointed at the fake. Every task the fake starts
// is already finished, so the waits return on the first poll.
func (a *fakeAPI) client(t *testing.T) *sdk.CloudClient {
	t.Helper()
	config, err := sdk.NewConfig("test-token", a.URL,
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

func (a *fakeAPI) addGroup(id int, name string, state entities.LLMGroupState, models ...entities.LLMModelID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.groups[id] = &fakeGroup{name: name, state: state, models: models}
}

func (a *fakeAPI) addKey(id, group int, name string, locks *entities.LLMAccessKeyLocks) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[id] = &fakeKey{group: group, name: name, state: entities.LLMAccessKeyStateActive, locks: locks}
}

func (a *fakeAPI) setRaw(path, body string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.raw[path] = body
}

func (a *fakeAPI) failOn(key string, status, times int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failNext[key] = failure{status: status, times: times}
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

// mutations returns the non-GET calls, in order.
func (a *fakeAPI) mutations() []string {
	var out []string
	for _, call := range a.calls() {
		if !strings.HasPrefix(call, http.MethodGet+" ") {
			out = append(out, call)
		}
	}
	return out
}

// bodyOf returns the body of the only call matching the substring.
func (a *fakeAPI) bodyOf(t *testing.T, substr string) string {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	found := ""
	n := 0
	for i, call := range a.requests {
		if strings.Contains(call, substr) {
			found = a.bodies[i]
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d calls match %q, want 1: %v", n, substr, a.requests)
	}
	return found
}

var (
	groupPath     = regexp.MustCompile(`^llm/groups/(\d+)(?:/(models|tags|disable))?$`)
	keysPath      = regexp.MustCompile(`^llm/groups/(\d+)/access-keys(?:/(\d+))?(?:/(secret|lock|unlock|tags))?$`)
	anyGroupPath  = regexp.MustCompile(`^llm/groups/(\d+)/`)
	taskPathRegex = regexp.MustCompile(`^tasks/(.+)$`)
)

func (a *fakeAPI) route(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/")

	a.mu.Lock()
	a.requests = append(a.requests, r.Method+" "+r.URL.Path)
	a.bodies = append(a.bodies, string(body))
	var refuse *failure
	for key, f := range a.failNext {
		method, suffix, _ := strings.Cut(key, " ")
		if r.Method == method && strings.HasSuffix(r.URL.Path, suffix) && f.times > 0 {
			f.times--
			a.failNext[key] = f
			refuse = &failure{status: f.status}
		}
	}
	hook := a.onMutation
	a.mu.Unlock()

	if m := anyGroupPath.FindStringSubmatch(p); m != nil && r.Method != http.MethodGet && hook != nil {
		group, _ := strconv.Atoi(m[1])
		hook(group)
	}

	if refuse != nil {
		a.fail(w, refuse.status, "refused by the test")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if raw, ok := a.raw[p]; ok && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, raw)
		return
	}

	switch {
	case p == "project" && r.Method == http.MethodGet:
		a.writeJSON(w, map[string]any{"project": map[string]any{"id": a.projectID, "state": "active"}})
	case p == "llm/connection":
		a.handleConnection(w, r, body)
	case p == "llm/groups" && r.Method == http.MethodPost:
		var req entities.CreateLLMGroupRequest
		_ = json.Unmarshal(body, &req)
		id := a.newID()
		a.groups[id] = &fakeGroup{name: req.Name, state: entities.LLMGroupStateActive}
		a.writeTask(w, entities.TaskResource{Type: entities.TaskResourceLLMGroup, ID: strconv.Itoa(id)})
	case keysPath.MatchString(p):
		a.handleKeys(w, r, keysPath.FindStringSubmatch(p), body)
	case groupPath.MatchString(p):
		a.handleGroup(w, r, groupPath.FindStringSubmatch(p), body)
	case taskPathRegex.MatchString(p):
		id := taskPathRegex.FindStringSubmatch(p)[1]
		resources, ok := a.tasks[id]
		if !ok {
			a.fail(w, http.StatusNotFound, "no such task")
			return
		}
		a.writeJSON(w, map[string]any{"task": entities.TaskResponse{
			ID: id, Type: "llm", IsCompleted: entities.TaskStateCompleted, Resources: resources,
		}})
	default:
		a.fail(w, http.StatusNotFound, "no route "+r.Method+" "+p)
	}
}

func (a *fakeAPI) handleConnection(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method == http.MethodGet {
		a.writeJSON(w, map[string]any{"connection": a.connection})
		return
	}
	var req entities.ConnectLLMRequest
	_ = json.Unmarshal(body, &req)
	a.connection = entities.LLMConnection{
		State: entities.LLMConnectionStateConnected, GroupName: &req.GroupName, FirstKeyName: &req.FirstKeyName,
	}
	a.writeTask(w)
}

func (a *fakeAPI) handleGroup(w http.ResponseWriter, r *http.Request, m []string, body []byte) {
	id, _ := strconv.Atoi(m[1])
	group, ok := a.groups[id]
	if !ok {
		a.fail(w, http.StatusNotFound, "no such group")
		return
	}
	switch {
	case m[2] == "" && r.Method == http.MethodGet:
		gid := entities.LLMGroupID(id)
		state := group.state
		details := entities.LLMGroupDetails{
			LLMGroup: entities.LLMGroup{
				ID: &gid, Name: group.name, State: &state, Tags: group.tags, CreatedAt: "2026-11-01T00:00:00Z",
			},
			DeploymentState: entities.LLMGroupDeploymentStateDeployed,
			UpdatedAt:       "2026-11-01T00:00:00Z",
		}
		for _, model := range group.models {
			details.Models = append(details.Models, entities.LLMGroupModel{
				ModelID: model, TechTitle: "model-" + strconv.Itoa(int(model)), Name: "Model",
			})
		}
		a.writeJSON(w, map[string]any{"group": details})
	case m[2] == "models" && r.Method == http.MethodPut:
		var req entities.SetLLMGroupModelsRequest
		_ = json.Unmarshal(body, &req)
		group.models = req.ModelIDs
		a.writeTask(w)
	case m[2] == "tags" && r.Method == http.MethodPut:
		var req entities.SetLLMTagsRequest
		_ = json.Unmarshal(body, &req)
		group.tags = req.Tags
		w.WriteHeader(http.StatusNoContent)
	case m[2] == "disable" && r.Method == http.MethodPost:
		group.state = entities.LLMGroupStateDisabled
		a.writeTask(w)
	default:
		a.fail(w, http.StatusNotFound, "no route")
	}
}

func (a *fakeAPI) handleKeys(w http.ResponseWriter, r *http.Request, m []string, body []byte) {
	groupID, _ := strconv.Atoi(m[1])
	if _, ok := a.groups[groupID]; !ok {
		a.fail(w, http.StatusNotFound, "no such group")
		return
	}

	if m[2] == "" {
		if r.Method != http.MethodPost {
			a.fail(w, http.StatusNotFound, "no route")
			return
		}
		var req entities.IssueLLMAccessKeyRequest
		_ = json.Unmarshal(body, &req)
		id := a.newID()
		a.keys[id] = &fakeKey{
			group: groupID, name: req.Name, state: entities.LLMAccessKeyStateActive,
			locks: &entities.LLMAccessKeyLocks{}, secret: "sk-" + strconv.Itoa(id),
		}
		a.writeTask(w, entities.TaskResource{Type: entities.TaskResourceLLMAccessKey, ID: strconv.Itoa(id)})
		return
	}

	id, _ := strconv.Atoi(m[2])
	key, ok := a.keys[id]
	if !ok || key.group != groupID {
		a.fail(w, http.StatusNotFound, "no such key")
		return
	}
	switch {
	case m[3] == "" && r.Method == http.MethodGet:
		kid, state := entities.LLMAccessKeyID(id), key.state
		a.writeJSON(w, map[string]any{"access_key": entities.LLMAccessKey{
			ID: &kid, GroupID: entities.LLMGroupID(groupID), Name: key.name, State: &state,
			Locks: key.locks, Tags: key.tags, CreatedAt: "2026-11-01T00:00:00Z",
		}})
	case m[3] == "" && r.Method == http.MethodDelete:
		key.state = entities.LLMAccessKeyStateRemoved
		a.writeTask(w)
	case m[3] == "secret" && r.Method == http.MethodPost:
		if key.secret == "" {
			a.fail(w, http.StatusBadRequest, "AccessKeySecretAlreadyRevealed")
			return
		}
		a.writeJSON(w, map[string]any{"secret": key.secret})
		key.secret = ""
	case m[3] == "lock" && r.Method == http.MethodPost:
		var req entities.LockLLMAccessKeyRequest
		_ = json.Unmarshal(body, &req)
		if key.locks.ByClient {
			a.fail(w, http.StatusBadRequest, "AccessKeyIsAlreadyLockedByClient")
			return
		}
		key.locks = &entities.LLMAccessKeyLocks{ByClient: true}
		if req.Reason != "" {
			key.locks.ClientReason = &req.Reason
		}
		a.writeTask(w)
	case m[3] == "unlock" && r.Method == http.MethodPost:
		key.locks = &entities.LLMAccessKeyLocks{}
		a.writeTask(w)
	case m[3] == "tags" && r.Method == http.MethodPut:
		var req entities.SetLLMTagsRequest
		_ = json.Unmarshal(body, &req)
		key.tags = req.Tags
		w.WriteHeader(http.StatusNoContent)
	default:
		a.fail(w, http.StatusNotFound, "no route")
	}
}

// newID must be called with a.mu held.
func (a *fakeAPI) newID() int {
	a.nextID++
	return a.nextID
}

// writeTask answers with a new, already finished task reporting the resources.
// It must be called with a.mu held.
func (a *fakeAPI) writeTask(w http.ResponseWriter, resources ...entities.TaskResource) {
	a.nextTask++
	id := "llm" + strconv.Itoa(a.nextTask)
	a.tasks[id] = resources
	a.writeJSON(w, map[string]any{"task_id": id})
}

func (a *fakeAPI) writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (a *fakeAPI) fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]any{{"code": -1, "message": message}},
	})
}

// ============================================================================
// Driving a resource
// ============================================================================

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

func resourceSchema(t *testing.T, res resource.Resource) rsschema.Schema {
	t.Helper()
	resp := resource.SchemaResponse{}
	res.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("reading the schema of %T: %v", res, resp.Diagnostics)
	}
	return resp.Schema
}

func emptyValue(ctx context.Context, s rsschema.Schema) tftypes.Value {
	return tftypes.NewValue(s.Type().TerraformType(ctx), nil)
}

func planOf(t *testing.T, s rsschema.Schema, model any) tfsdk.Plan {
	t.Helper()
	ctx := context.Background()
	plan := tfsdk.Plan{Schema: s, Raw: emptyValue(ctx, s)}
	if diags := plan.Set(ctx, model); diags.HasError() {
		t.Fatalf("building the plan: %v", diags)
	}
	return plan
}

// planForCreate is planOf with every null Computed attribute unknown, as the
// framework hands a Create: a resource that never resolves one would pass with
// a null.
func planForCreate(t *testing.T, s rsschema.Schema, model any) tfsdk.Plan {
	t.Helper()
	plan := planOf(t, s, model)
	attributes := map[string]tftypes.Value{}
	if err := plan.Raw.As(&attributes); err != nil {
		t.Fatalf("reading the plan back: %v", err)
	}
	for name, attribute := range s.Attributes {
		if !attribute.IsComputed() || !attributes[name].IsNull() {
			continue
		}
		attributes[name] = tftypes.NewValue(attributes[name].Type(), tftypes.UnknownValue)
	}
	plan.Raw = tftypes.NewValue(plan.Raw.Type(), attributes)
	return plan
}

// assertNoUnknowns fails on any value left unresolved: Terraform refuses such
// a state with "inconsistent result after apply" once the object exists.
func assertNoUnknowns(t *testing.T, state tfsdk.State) {
	t.Helper()
	err := tftypes.Walk(state.Raw, func(path *tftypes.AttributePath, value tftypes.Value) (bool, error) {
		if !value.IsKnown() {
			t.Errorf("%s is still unknown after the apply", path)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("walking the state: %v", err)
	}
}

func configOf(t *testing.T, s rsschema.Schema, model any) tfsdk.Config {
	t.Helper()
	return tfsdk.Config{Schema: s, Raw: stateOf(t, s, model).Raw}
}

func stateOf(t *testing.T, s rsschema.Schema, model any) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	state := tfsdk.State{Schema: s, Raw: emptyValue(ctx, s)}
	if diags := state.Set(ctx, model); diags.HasError() {
		t.Fatalf("building the state: %v", diags)
	}
	return state
}

func emptyState(s rsschema.Schema) tfsdk.State {
	return tfsdk.State{Schema: s, Raw: emptyValue(context.Background(), s)}
}

func readModel(t *testing.T, state tfsdk.State, into any) {
	t.Helper()
	if state.Raw.IsNull() {
		t.Fatal("the resource left no state behind")
	}
	if diags := state.Get(context.Background(), into); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
}

// readDataSource drives a data source Read against the fake, with the config
// given as a model (nil for a data source without arguments), and returns the
// state it wrote into out.
func readDataSource(t *testing.T, api *fakeAPI, ds datasource.DataSource, config, out any) {
	t.Helper()
	ctx := context.Background()

	withConfigure, ok := ds.(datasource.DataSourceWithConfigure)
	if !ok {
		t.Fatalf("%T cannot be configured", ds)
	}
	configureResp := datasource.ConfigureResponse{}
	withConfigure.Configure(ctx, datasource.ConfigureRequest{ProviderData: api.client(t)}, &configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("configuring %T: %v", ds, configureResp.Diagnostics)
	}

	schemaResp := datasource.SchemaResponse{}
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	empty := tftypes.NewValue(s.Type().TerraformType(ctx), nil)

	req := datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: empty}}
	if config != nil {
		state := tfsdk.State{Schema: s, Raw: empty}
		if diags := state.Set(ctx, config); diags.HasError() {
			t.Fatalf("building the config: %v", diags)
		}
		req.Config.Raw = state.Raw
	}

	resp := datasource.ReadResponse{State: tfsdk.State{Schema: s, Raw: empty}}
	ds.Read(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("reading %T: %v", ds, resp.Diagnostics)
	}
	if diags := resp.State.Get(ctx, out); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
}

// hasAttributeError reports whether the diagnostic points at the attribute.
func hasAttributeError(d diag.Diagnostic, p path.Path) bool {
	withPath, ok := d.(diag.DiagnosticWithPath)
	return ok && withPath.Path().Equal(p)
}
