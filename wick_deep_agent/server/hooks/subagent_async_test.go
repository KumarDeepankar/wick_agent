package hooks

import (
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
