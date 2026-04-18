package hooks

import (
	"context"
	"strings"
	"testing"

	"wick_server/agent"
)

func TestDrainUpdates_Empty(t *testing.T) {
	at := agent.GlobalAsyncTaskStore.Create("t", "n", "task")
	defer agent.GlobalAsyncTaskStore.Delete(at.ID)

	got := drainUpdates(at)
	if len(got) != 0 {
		t.Errorf("drainUpdates on empty mailbox = %v, want empty", got)
	}
}

func TestDrainUpdates_ReturnsAllQueued(t *testing.T) {
	at := agent.GlobalAsyncTaskStore.Create("t", "n", "task")
	defer agent.GlobalAsyncTaskStore.Delete(at.ID)

	at.Updates <- "first"
	at.Updates <- "second"
	at.Updates <- "third"

	got := drainUpdates(at)
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("drainUpdates len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("drainUpdates[%d] = %q, want %q", i, got[i], w)
		}
	}

	// Mailbox must be empty after drain
	if rem := drainUpdates(at); len(rem) != 0 {
		t.Errorf("mailbox not empty after drain: %v", rem)
	}
}

func TestSafeSendEvent_NilChannelIsNoOp(t *testing.T) {
	// Must not panic.
	safeSendEvent(nil, agent.StreamEvent{Event: "test"})
}

func TestSafeSendEvent_ClosedChannelIsRecovered(t *testing.T) {
	ch := make(chan agent.StreamEvent, 1)
	close(ch)
	// Sending on a closed channel panics; safeSendEvent must recover.
	safeSendEvent(ch, agent.StreamEvent{Event: "test"})
}

func TestSafeSendEvent_FullChannelDrops(t *testing.T) {
	ch := make(chan agent.StreamEvent, 1)
	ch <- agent.StreamEvent{Event: "prior"}

	// Mailbox is full — must not block and must not panic.
	done := make(chan struct{})
	go func() {
		safeSendEvent(ch, agent.StreamEvent{Event: "dropped"})
		close(done)
	}()
	select {
	case <-done:
		// ok
	default:
		// If we got here immediately, the goroutine hasn't scheduled yet. Give it a moment.
	}
	<-done

	// Channel should still contain only the prior event.
	got := <-ch
	if got.Event != "prior" {
		t.Errorf("expected prior event, got %q (dropped event leaked in)", got.Event)
	}
	select {
	case extra := <-ch:
		t.Errorf("channel has extra event %+v; send should have been dropped", extra)
	default:
		// ok
	}
}

func TestSafeSendEvent_DeliversOnHealthyChannel(t *testing.T) {
	ch := make(chan agent.StreamEvent, 1)
	safeSendEvent(ch, agent.StreamEvent{Event: "delivered", TaskID: "task_abc"})

	got := <-ch
	if got.Event != "delivered" || got.TaskID != "task_abc" {
		t.Errorf("got event=%+v, want Event=delivered TaskID=task_abc", got)
	}
}

func TestSubAgentCfg_SyncEnabledDefaultTrue(t *testing.T) {
	sa := agent.SubAgentCfg{Name: "x"}
	if !sa.SyncEnabled() {
		t.Error("SubAgentCfg with no flags should default to SyncEnabled=true")
	}
	if sa.AsyncEnabled() {
		t.Error("SubAgentCfg with no flags should not be AsyncEnabled")
	}
}

func TestSubAgentCfg_AsyncOnly(t *testing.T) {
	sa := agent.SubAgentCfg{Name: "x", Async: true}
	if sa.SyncEnabled() {
		t.Error("Async-only sub-agent must not be SyncEnabled")
	}
	if !sa.AsyncEnabled() {
		t.Error("Async=true must report AsyncEnabled")
	}
}

func TestSubAgentCfg_BothModes(t *testing.T) {
	sa := agent.SubAgentCfg{Name: "x", Sync: true, Async: true}
	if !sa.SyncEnabled() || !sa.AsyncEnabled() {
		t.Errorf("Sync+Async should enable both modes (got sync=%v async=%v)", sa.SyncEnabled(), sa.AsyncEnabled())
	}
}

func TestModifyRequest_InjectsGuidanceWhenAsyncPresent(t *testing.T) {
	h := NewSubAgentHook(
		[]agent.SubAgentCfg{
			{Name: "fast", Sync: true},
			{Name: "slow", Async: true},
		},
		&agent.AgentConfig{}, nil, nil,
	)

	base := "You are a helpful assistant."
	got, _, err := h.ModifyRequest(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("ModifyRequest: %v", err)
	}
	if !strings.HasPrefix(got, base) {
		t.Errorf("prefix changed; original prompt must be preserved")
	}
	if !strings.Contains(got, "Async sub-agent coordination") {
		t.Errorf("expected guidance header in injected prompt, got:\n%s", got)
	}
	if !strings.Contains(got, "start_async_task") {
		t.Errorf("guidance should reference start_async_task")
	}
}

func TestModifyRequest_NoInjectionWhenAllSync(t *testing.T) {
	h := NewSubAgentHook(
		[]agent.SubAgentCfg{
			{Name: "a", Sync: true},
			{Name: "b"}, // defaults to sync via SyncEnabled()
		},
		&agent.AgentConfig{}, nil, nil,
	)

	base := "You are a helpful assistant."
	got, _, err := h.ModifyRequest(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("ModifyRequest: %v", err)
	}
	if got != base {
		t.Errorf("sync-only hook mutated prompt; got:\n%s", got)
	}
}

func TestModifyRequest_NoInjectionWhenNoSubAgents(t *testing.T) {
	h := NewSubAgentHook(nil, &agent.AgentConfig{}, nil, nil)

	base := "prompt"
	got, _, err := h.ModifyRequest(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("ModifyRequest: %v", err)
	}
	if got != base {
		t.Errorf("expected prompt unchanged, got %q", got)
	}
}

func TestModifyRequest_InjectsLiveTaskStatus(t *testing.T) {
	h := NewSubAgentHook(
		[]agent.SubAgentCfg{{Name: "worker", Async: true}},
		&agent.AgentConfig{}, nil, nil,
	)

	// Point the hook at a dedicated store so we don't pollute the global one.
	store := agent.NewAsyncTaskStore()
	defer store.Stop()
	h.taskStore = store

	threadID := "thread-status-test"
	running := store.Create(threadID, "worker", "long job")
	done := store.Create(threadID, "worker", "short job")
	done.Finish(agent.AsyncTaskDone, "final", "")

	// Task for another thread — must NOT appear in this thread's status.
	otherRunning := store.Create("thread-other", "worker", "someone else")

	state := &agent.AgentState{ThreadID: threadID}
	ctx := agent.WithState(context.Background(), state)

	got, _, err := h.ModifyRequest(ctx, "You are helpful.", nil)
	if err != nil {
		t.Fatalf("ModifyRequest: %v", err)
	}

	if !strings.Contains(got, "## Current background task status") {
		t.Errorf("expected status header; got:\n%s", got)
	}
	if !strings.Contains(got, running.ID) {
		t.Errorf("expected running task ID in status; got:\n%s", got)
	}
	if !strings.Contains(got, done.ID) {
		t.Errorf("expected finished task ID in status; got:\n%s", got)
	}
	if strings.Contains(got, otherRunning.ID) {
		t.Errorf("other-thread task leaked into status block")
	}
	if !strings.Contains(got, "Running:") {
		t.Errorf("expected 'Running:' bucket header")
	}
	if !strings.Contains(got, "Recently finished:") {
		t.Errorf("expected 'Recently finished:' bucket header")
	}
}

func TestModifyRequest_NoStatusWhenNoTasks(t *testing.T) {
	h := NewSubAgentHook(
		[]agent.SubAgentCfg{{Name: "worker", Async: true}},
		&agent.AgentConfig{}, nil, nil,
	)
	store := agent.NewAsyncTaskStore()
	defer store.Stop()
	h.taskStore = store

	state := &agent.AgentState{ThreadID: "empty-thread"}
	ctx := agent.WithState(context.Background(), state)

	got, _, err := h.ModifyRequest(ctx, "You are helpful.", nil)
	if err != nil {
		t.Fatalf("ModifyRequest: %v", err)
	}

	// Guidance should still be present (hook is async-capable), but the status
	// block itself (identified by its markdown heading) must be absent because
	// no tasks exist for this thread. Note the guidance text references the
	// phrase "Current background task status" in prose — we check for the
	// actual heading marker to avoid a false positive.
	if !strings.Contains(got, "Async sub-agent coordination") {
		t.Errorf("expected guidance header when async sub-agents exist")
	}
	if strings.Contains(got, "## Current background task status") {
		t.Errorf("status block should not appear when no tasks exist; got:\n%s", got)
	}
	if strings.Contains(got, "Running:\n-") || strings.Contains(got, "Recently finished:\n-") {
		t.Errorf("status bucket headers should not appear when no tasks exist")
	}
}
