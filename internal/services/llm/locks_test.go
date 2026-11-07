package llm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// Terraform applies independent resources in parallel, and the platform
// serializes changes to a group: several keys of one group in one apply race
// each other unless the provider holds the group for the whole operation,
// task wait included. Keys of different groups must still run side by side.
func TestLLMGroupMutationsAreSequentialPerGroup(t *testing.T) {
	api := newFakeAPI(t)
	api.addGroup(10, "team", entities.LLMGroupStateActive, 3)
	api.addGroup(20, "other", entities.LLMGroupStateActive)
	api.addKey(690, 10, "old-a", &entities.LLMAccessKeyLocks{})
	api.addKey(691, 10, "old-b", &entities.LLMAccessKeyLocks{})

	var (
		mu         sync.Mutex
		inFlight   = map[int]int{}
		maxFlight  = map[int]int{}
		overlapped bool
	)
	api.onMutation = func(group int) {
		mu.Lock()
		inFlight[group]++
		if inFlight[group] > maxFlight[group] {
			maxFlight[group] = inFlight[group]
		}
		mu.Unlock()

		if group == 20 {
			// Wait for group 10 to be busy: under one lock for every group this
			// never happens.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				mu.Lock()
				busy := inFlight[10] > 0
				mu.Unlock()
				if busy {
					overlapped = true
					break
				}
				time.Sleep(time.Millisecond)
			}
		} else {
			time.Sleep(20 * time.Millisecond)
		}

		mu.Lock()
		inFlight[group]--
		mu.Unlock()
	}

	client := api.client(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	run := func(op func()) {
		wg.Add(1)
		go func() { defer wg.Done(); op() }()
	}

	keys := NewAccessKeyResource()
	configure(t, keys, client)
	keySchema := resourceSchema(t, keys)
	for _, name := range []string{"a", "b", "c"} {
		plan := planForCreate(t, keySchema, keyPlan(10, name))
		run(func() {
			resp := resource.CreateResponse{State: emptyState(keySchema)}
			keys.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		})
	}
	{
		prior := keyAt(10, 690, false, types.StringNull())
		plan := prior
		plan.Tags = stringSet([]string{"x"})
		req := resource.UpdateRequest{Plan: planOf(t, keySchema, plan), State: stateOf(t, keySchema, prior)}
		run(func() {
			resp := resource.UpdateResponse{State: emptyState(keySchema)}
			keys.Update(ctx, req, &resp)
		})
	}
	{
		state := stateOf(t, keySchema, keyAt(10, 691, false, types.StringNull()))
		run(func() {
			resp := resource.DeleteResponse{State: state}
			keys.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
		})
	}

	groups := NewGroupResource()
	configure(t, groups, client)
	groupSchema := resourceSchema(t, groups)
	{
		prior := groupAt(10, "team", int64Set(3), stringSet(nil))
		plan := groupAt(10, "team", int64Set(3, 4), stringSet(nil))
		req := resource.UpdateRequest{Plan: planOf(t, groupSchema, plan), State: stateOf(t, groupSchema, prior)}
		run(func() {
			resp := resource.UpdateResponse{State: emptyState(groupSchema)}
			groups.Update(ctx, req, &resp)
		})
	}

	otherPlan := planForCreate(t, keySchema, keyPlan(20, "z"))
	run(func() {
		resp := resource.CreateResponse{State: emptyState(keySchema)}
		keys.Create(ctx, resource.CreateRequest{Plan: otherPlan}, &resp)
	})

	wg.Wait()

	if maxFlight[10] != 1 {
		t.Errorf("up to %d changes of group 10 ran at once, want 1", maxFlight[10])
	}
	if !overlapped {
		t.Error("a key of group 20 waited for group 10: the lock is not per group")
	}
	// Every operation really reached the API.
	if got := api.countCalls("POST /api/v1/llm/groups/10/access-keys"); got < 3 {
		t.Errorf("issued %d keys in group 10, want 3: %v", got, api.calls())
	}
	if api.countCalls("PUT /api/v1/llm/groups/10/access-keys/690/tags") != 1 ||
		api.countCalls("DELETE /api/v1/llm/groups/10/access-keys/691") != 1 ||
		api.countCalls("PUT /api/v1/llm/groups/10/models") != 1 {
		t.Errorf("not every change of group 10 reached the API: %v", api.calls())
	}
}
