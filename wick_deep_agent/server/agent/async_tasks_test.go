package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAsyncTaskStore_CreateAndGet(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	at := s.Create("thread-1", "summarizer", "summarize the doc")
	if at == nil {
		t.Fatal("Create returned nil")
	}
	if !strings.HasPrefix(at.ID, "task_") {
		t.Errorf("task ID %q should start with task_", at.ID)
	}
	if at.ThreadID != "thread-1" {
		t.Errorf("ThreadID = %q, want thread-1", at.ThreadID)
	}
	if at.Status() != AsyncTaskRunning {
		t.Errorf("initial status = %s, want running", at.Status())
	}

	got := s.Get(at.ID)
	if got != at {
		t.Errorf("Get returned different task pointer")
	}

	missing := s.Get("task_doesnotexist")
	if missing != nil {
		t.Errorf("Get on unknown ID returned %v, want nil", missing)
	}
}

func TestAsyncTaskStore_ListByThreadScoped(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	a1 := s.Create("thread-A", "agent1", "task A1")
	time.Sleep(2 * time.Millisecond) // ensure distinguishable CreatedAt
	a2 := s.Create("thread-A", "agent2", "task A2")
	s.Create("thread-B", "agent1", "task B1")

	listA := s.ListByThread("thread-A")
	if len(listA) != 2 {
		t.Fatalf("ListByThread(A) len = %d, want 2", len(listA))
	}
	// newest first
	if listA[0].ID != a2.ID {
		t.Errorf("first entry = %s, want %s (newer)", listA[0].ID, a2.ID)
	}
	if listA[1].ID != a1.ID {
		t.Errorf("second entry = %s, want %s (older)", listA[1].ID, a1.ID)
	}

	listB := s.ListByThread("thread-B")
	if len(listB) != 1 {
		t.Errorf("ListByThread(B) len = %d, want 1", len(listB))
	}

	// Thread C has nothing
	if got := s.ListByThread("thread-C"); len(got) != 0 {
		t.Errorf("ListByThread(C) len = %d, want 0", len(got))
	}
}

func TestAsyncTask_AppendOutputAccumulates(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	at := s.Create("t", "name", "task")
	at.AppendOutput("hello ")
	at.AppendOutput("world")
	at.AppendOutput("") // no-op
	if got := at.Output(); got != "hello world" {
		t.Errorf("Output = %q, want %q", got, "hello world")
	}
}

func TestAsyncTask_FinishIsIdempotentAndClosesDone(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	at := s.Create("t", "name", "task")

	// Done is open while running
	select {
	case <-at.Done:
		t.Fatal("Done should be open before Finish")
	default:
	}

	at.Finish(AsyncTaskDone, "final output", "")
	if at.Status() != AsyncTaskDone {
		t.Errorf("status after Finish = %s, want done", at.Status())
	}
	if at.Output() != "final output" {
		t.Errorf("output after Finish = %q, want 'final output'", at.Output())
	}
	select {
	case <-at.Done:
		// ok
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Done was not closed after Finish")
	}

	// Second Finish must not transition status or panic on close
	at.Finish(AsyncTaskError, "", "second call")
	if at.Status() != AsyncTaskDone {
		t.Errorf("status after second Finish = %s, want still done", at.Status())
	}
	if at.Error() != "" {
		t.Errorf("error after second Finish = %q, want empty (idempotent)", at.Error())
	}
}

func TestAsyncTask_IsTerminal(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	cases := []struct {
		finishStatus AsyncTaskStatus
		wantTerminal bool
	}{
		{AsyncTaskDone, true},
		{AsyncTaskError, true},
		{AsyncTaskCancelled, true},
	}
	for _, c := range cases {
		at := s.Create("t", "n", "tk")
		if at.IsTerminal() {
			t.Errorf("fresh task IsTerminal = true, want false")
		}
		at.Finish(c.finishStatus, "", "")
		if at.IsTerminal() != c.wantTerminal {
			t.Errorf("after Finish(%s), IsTerminal = %v, want %v", c.finishStatus, at.IsTerminal(), c.wantTerminal)
		}
	}
}

func TestAsyncTaskStore_Delete(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	at := s.Create("t", "n", "tk")
	if s.Len() != 1 {
		t.Fatalf("Len after Create = %d, want 1", s.Len())
	}
	s.Delete(at.ID)
	if s.Len() != 0 {
		t.Errorf("Len after Delete = %d, want 0", s.Len())
	}
	if s.Get(at.ID) != nil {
		t.Errorf("Get after Delete should return nil")
	}
}

func TestAsyncTaskStore_SetCancelIsInvoked(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	at := s.Create("t", "n", "tk")
	_, cancel := context.WithCancel(context.Background())
	called := false
	wrapped := func() {
		called = true
		cancel()
	}
	s.SetCancel(at.ID, wrapped)

	// Simulate cancel_async_task calling Cancel()
	at.Cancel()
	if !called {
		t.Error("wrapped cancel was not invoked")
	}
}

func TestAsyncTaskStore_EvictRemovesTerminalTasksOnly(t *testing.T) {
	s := &AsyncTaskStore{
		tasks: make(map[string]*AsyncTask),
		ttl:   1 * time.Millisecond,
		stop:  make(chan struct{}),
	}
	// Note: intentionally NOT starting evictLoop — drive evict() manually.

	running := s.Create("t", "n", "still running")
	finished := s.Create("t", "n", "completed")
	finished.Finish(AsyncTaskDone, "done output", "")

	// Ensure updatedAt on 'finished' is older than TTL
	time.Sleep(5 * time.Millisecond)

	s.evict()

	if s.Get(finished.ID) != nil {
		t.Errorf("finished task should have been evicted")
	}
	if s.Get(running.ID) == nil {
		t.Errorf("running task must NOT be evicted")
	}
}

func TestAsyncTask_UpdatesMailboxRespectsCapacity(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	at := s.Create("t", "n", "tk")

	// Fill the mailbox up to capacity without blocking.
	accepted := 0
	for i := 0; i < updatesMailboxSize+2; i++ {
		select {
		case at.Updates <- "msg":
			accepted++
		default:
			// full
		}
	}
	if accepted != updatesMailboxSize {
		t.Errorf("accepted updates = %d, want %d (mailbox size)", accepted, updatesMailboxSize)
	}
}

func TestAsyncTaskStore_ConcurrentCreateIsSafe(t *testing.T) {
	s := NewAsyncTaskStore()
	defer s.Stop()

	var wg sync.WaitGroup
	const n = 64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Create("thread-X", "agent", "t")
		}()
	}
	wg.Wait()
	if got := len(s.ListByThread("thread-X")); got != n {
		t.Errorf("concurrent creates: got %d tasks, want %d", got, n)
	}
}
