package agent

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHITLStore_CreateAndGet(t *testing.T) {
	s := NewHITLStore()
	defer s.Stop()

	r := s.Create("thread-1", HITLInput, "what's your name?", nil)
	if r == nil {
		t.Fatal("Create returned nil")
	}
	if !strings.HasPrefix(r.ID, "hitl_") {
		t.Errorf("id %q should start with hitl_", r.ID)
	}
	if r.ThreadID != "thread-1" {
		t.Errorf("ThreadID = %q, want thread-1", r.ThreadID)
	}
	if r.Status() != HITLPending {
		t.Errorf("initial status = %s, want pending", r.Status())
	}
	if r.IsTerminal() {
		t.Errorf("new request should not be terminal")
	}

	got := s.Get(r.ID)
	if got != r {
		t.Errorf("Get returned different pointer")
	}
	if s.Get("hitl_missing") != nil {
		t.Errorf("Get on unknown ID should return nil")
	}
}

func TestHITLRequest_ResolveUnblocksDone(t *testing.T) {
	s := NewHITLStore()
	defer s.Stop()
	r := s.Create("t1", HITLApproval, "delete the table?", []string{"Approve", "Deny"})

	// Confirm Done is open.
	select {
	case <-r.Done:
		t.Fatal("Done closed before Resolve")
	default:
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		r.Resolve(HITLDenied, "Deny", nil)
	}()

	select {
	case <-r.Done:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Done did not close within 500ms after Resolve")
	}

	if r.Status() != HITLDenied {
		t.Errorf("status = %s, want denied", r.Status())
	}
	if r.Response() != "Deny" {
		t.Errorf("response = %q, want Deny", r.Response())
	}
	if !r.IsTerminal() {
		t.Errorf("request should be terminal after Resolve")
	}
}

func TestHITLRequest_ResolveOnlyFirstWins(t *testing.T) {
	s := NewHITLStore()
	defer s.Stop()
	r := s.Create("t1", HITLInput, "say something", nil)

	if !r.Resolve(HITLAnswered, "first", nil) {
		t.Error("first Resolve should return true")
	}
	if r.Resolve(HITLAnswered, "second", nil) {
		t.Error("second Resolve should return false")
	}
	if r.Resolve(HITLCancelled, "", nil) {
		t.Error("cancellation after answer should return false")
	}
	if r.Response() != "first" {
		t.Errorf("response = %q, want first (subsequent Resolve must not overwrite)", r.Response())
	}
}

func TestHITLRequest_ResolvePendingRejected(t *testing.T) {
	s := NewHITLStore()
	defer s.Stop()
	r := s.Create("t1", HITLInput, "ask", nil)
	if r.Resolve(HITLPending, "x", nil) {
		t.Error("Resolve(HITLPending) must return false — Pending is not terminal")
	}
	if r.IsTerminal() {
		t.Error("request should still be pending after rejected Resolve")
	}
}

func TestHITLStore_ListByThreadScoped(t *testing.T) {
	s := NewHITLStore()
	defer s.Stop()

	a1 := s.Create("A", HITLInput, "p1", nil)
	time.Sleep(2 * time.Millisecond)
	a2 := s.Create("A", HITLInput, "p2", nil)
	s.Create("B", HITLInput, "p3", nil)

	listA := s.ListByThread("A")
	if len(listA) != 2 {
		t.Fatalf("len(ListByThread(A)) = %d, want 2", len(listA))
	}
	if listA[0].ID != a2.ID {
		t.Errorf("newest first violated: got %s, want %s", listA[0].ID, a2.ID)
	}
	if listA[1].ID != a1.ID {
		t.Errorf("oldest last violated: got %s, want %s", listA[1].ID, a1.ID)
	}
}

func TestHITLStore_ConcurrentResolveSafe(t *testing.T) {
	// Race-detector check: many goroutines racing to Resolve, only one wins.
	s := NewHITLStore()
	defer s.Stop()
	r := s.Create("t1", HITLInput, "race", nil)

	var wg sync.WaitGroup
	wins := make(chan bool, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wins <- r.Resolve(HITLAnswered, "x", nil)
		}(i)
	}
	wg.Wait()
	close(wins)

	count := 0
	for w := range wins {
		if w {
			count++
		}
	}
	if count != 1 {
		t.Errorf("exactly one Resolve must win, got %d winners", count)
	}
	if r.Status() != HITLAnswered {
		t.Errorf("status = %s, want answered", r.Status())
	}
}

func TestHITLStore_EvictsTerminalAfterTTL(t *testing.T) {
	s := &HITLStore{
		requests: make(map[string]*HITLRequest),
		ttl:      10 * time.Millisecond,
		stop:     make(chan struct{}),
	}
	defer s.Stop()

	pending := s.Create("t1", HITLInput, "still waiting", nil)
	answered := s.Create("t1", HITLInput, "already answered", nil)
	answered.Resolve(HITLAnswered, "yes", nil)

	// Force backdated update so eviction can trigger.
	answered.mu.Lock()
	answered.updatedAt = time.Now().Add(-1 * time.Hour)
	answered.mu.Unlock()

	s.evict()

	if s.Get(answered.ID) != nil {
		t.Error("terminal request older than TTL should have been evicted")
	}
	if s.Get(pending.ID) == nil {
		t.Error("pending request must never be evicted")
	}
}
