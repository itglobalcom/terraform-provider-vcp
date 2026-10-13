package server_backup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// fakeBackupAPI answers the backup service of server "s1": the state reads
// return in turn (the last one repeats), the enabling request is refused or
// answered with task "t1", and task "t1" finishes Failed.
type fakeBackupAPI struct {
	mu        sync.Mutex
	reads     []entities.ServerBackup
	refuse    bool
	requests  []string
	readCount int
}

func (a *fakeBackupAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/servers/s1/backup":
		i := min(a.readCount, len(a.reads)-1)
		a.readCount++
		_ = json.NewEncoder(w).Encode(a.reads[i])
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/servers/s1/backup":
		if a.refuse {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"backup service is already enabled"}`))
			return
		}
		_, _ = w.Write([]byte(`{"task_id":"t1"}`))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/t1":
		_, _ = w.Write([]byte(`{"task":{"id":"t1","is_completed":"Failed"}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *fakeBackupAPI) posted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, req := range a.requests {
		if req == "POST /api/v1/servers/s1/backup" {
			return true
		}
	}
	return false
}

func runCreate(t *testing.T, api *fakeBackupAPI) resource.CreateResponse {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	config, err := sdk.NewConfig("test-token", server.URL,
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

	ctx := context.Background()
	s := backupSchema(t)
	plan := tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := plan.Set(ctx, backupModel{
		ID:       types.StringUnknown(),
		ServerID: types.StringValue("s1"),
		Hour:     types.Int64Value(2),
		Minute:   types.Int64Value(0),
		Daily:    &dailyRuleModel{Keep: types.Int64Value(3), BackupStorageID: types.Int64Value(7)},
	}); diags.HasError() {
		t.Fatalf("building the plan: %v", diags)
	}

	resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	r := &backupResource{client: client}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	return resp
}

var enabledDaily = entities.ServerBackup{Enabled: true, Schedule: &entities.BackupSchedule{
	Hour:  2,
	Daily: &entities.BackupRule{Keep: 3, BackupStorageID: 7},
}}

// TestCreate_waitFailsServiceEnabled: the enabling request went through and
// only its wait failed, so the enabled service is recorded as tainted.
func TestCreate_waitFailsServiceEnabled(t *testing.T) {
	api := &fakeBackupAPI{reads: []entities.ServerBackup{{Enabled: false}, enabledDaily}}
	resp := runCreate(t, api)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Create reported no error for a failed wait")
	}
	if resp.Diagnostics.WarningsCount() != 1 ||
		resp.Diagnostics.Warnings()[0].Summary() != "Backup Recorded Despite Error" {
		t.Errorf("warnings = %v, want one \"Backup Recorded Despite Error\"", resp.Diagnostics.Warnings())
	}
	var got backupModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state: %v", diags)
	}
	if got.ServerID.ValueString() != "s1" || got.Hour.ValueInt64() != 2 || got.Daily == nil ||
		got.Daily.Keep.ValueInt64() != 3 {
		t.Errorf("state = %+v, want the enabled schedule of s1", got)
	}
}

// TestCreate_waitFailsServiceDisabled: the service is not on after the failed
// wait, so nothing is recorded.
func TestCreate_waitFailsServiceDisabled(t *testing.T) {
	api := &fakeBackupAPI{reads: []entities.ServerBackup{{Enabled: false}}}
	resp := runCreate(t, api)

	if api.readCount != 2 {
		t.Fatalf("state reads = %d, want 2: before the request and after the failed wait", api.readCount)
	}
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create reported no error for a failed wait")
	}
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("warnings = %v, want none", resp.Diagnostics.Warnings())
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("state = %v, want none", resp.State.Raw)
	}
}

// TestCreate_refusedOnEnabledService: a service enabled before the apply is
// not the resource's, so the refused request records nothing.
func TestCreate_refusedOnEnabledService(t *testing.T) {
	api := &fakeBackupAPI{reads: []entities.ServerBackup{enabledDaily}, refuse: true}
	resp := runCreate(t, api)

	if !api.posted() {
		t.Fatal("Create did not send the enabling request")
	}
	if !resp.Diagnostics.HasError() {
		t.Fatal("Create reported no error for a refused request")
	}
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("warnings = %v, want none", resp.Diagnostics.Warnings())
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("state = %v, want none", resp.State.Raw)
	}
}
