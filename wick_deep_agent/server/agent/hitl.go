package agent

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// HITLKind distinguishes free-form input from a constrained option choice.
type HITLKind string

const (
	HITLInput    HITLKind = "input"
	HITLApproval HITLKind = "approval"
)

// HITLStatus is the lifecycle of a single human-in-the-loop request.
type HITLStatus string

const (
	HITLPending   HITLStatus = "pending"
	HITLAnswered  HITLStatus = "answered"
	HITLDenied    HITLStatus = "denied"
	HITLCancelled HITLStatus = "cancelled"
	HITLTimedOut  HITLStatus = "timeout"
)

// hitlRequestTTL controls how long terminal requests linger before eviction.
// Pending requests are never evicted.
const hitlRequestTTL = 1 * time.Hour

// HITLRequest is a single pending question/approval directed at the user.
// It mirrors AsyncTask in shape: a Done channel that closes on terminal
// transition, with the response payload accessible via getters.
type HITLRequest struct {
	ID        string
	ThreadID  string // scopes ListByThread and HTTP routing
	Kind      HITLKind
	Prompt    string
	Options   []string // populated for HITLApproval
	CreatedAt time.Time
	Done      chan struct{} // closed when Resolve fires (exactly once)

	mu        sync.RWMutex
	status    HITLStatus
	response  string
	metadata  map[string]any
	updatedAt time.Time
}

func (r *HITLRequest) Status() HITLStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *HITLRequest) Response() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.response
}

func (r *HITLRequest) UpdatedAt() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.updatedAt
}

func (r *HITLRequest) Metadata() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.metadata == nil {
		return nil
	}
	out := make(map[string]any, len(r.metadata))
	for k, v := range r.metadata {
		out[k] = v
	}
	return out
}

// IsTerminal reports whether the request has been resolved one way or another.
func (r *HITLRequest) IsTerminal() bool {
	return r.Status() != HITLPending
}

// Resolve transitions the request to a terminal status and unblocks any
// waiter on Done. Only the first call wins; subsequent calls are no-ops and
// return false.
func (r *HITLRequest) Resolve(status HITLStatus, response string, metadata map[string]any) bool {
	if status == HITLPending {
		return false
	}
	r.mu.Lock()
	if r.status != HITLPending {
		r.mu.Unlock()
		return false
	}
	r.status = status
	r.response = response
	if metadata != nil {
		r.metadata = metadata
	}
	r.updatedAt = time.Now()
	r.mu.Unlock()

	select {
	case <-r.Done:
		// already closed (shouldn't happen — first writer above)
	default:
		close(r.Done)
	}
	return true
}

// HITLStore is the in-memory registry of HITL requests with TTL eviction of
// terminal entries. Same shape as AsyncTaskStore.
type HITLStore struct {
	mu       sync.RWMutex
	requests map[string]*HITLRequest
	ttl      time.Duration
	stop     chan struct{}
}

// GlobalHITLStore is the shared store used by HITLHook and the HTTP handler.
var GlobalHITLStore = NewHITLStore()

// NewHITLStore creates a new store and starts the background evictor.
func NewHITLStore() *HITLStore {
	s := &HITLStore{
		requests: make(map[string]*HITLRequest),
		ttl:      hitlRequestTTL,
		stop:     make(chan struct{}),
	}
	go s.evictLoop()
	return s
}

// Create registers a new pending request and returns it.
func (s *HITLStore) Create(threadID string, kind HITLKind, prompt string, options []string) *HITLRequest {
	now := time.Now()
	r := &HITLRequest{
		ID:        newHITLID(),
		ThreadID:  threadID,
		Kind:      kind,
		Prompt:    prompt,
		Options:   options,
		CreatedAt: now,
		Done:      make(chan struct{}),
		status:    HITLPending,
		updatedAt: now,
	}
	s.mu.Lock()
	s.requests[r.ID] = r
	s.mu.Unlock()
	return r
}

// Get returns the request with the given ID, or nil.
func (s *HITLStore) Get(id string) *HITLRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requests[id]
}

// ListByThread returns all requests for the given thread, newest first.
func (s *HITLStore) ListByThread(threadID string) []*HITLRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*HITLRequest, 0)
	for _, r := range s.requests {
		if r.ThreadID == threadID {
			out = append(out, r)
		}
	}
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j].CreatedAt.After(out[j-1].CreatedAt) {
			out[j], out[j-1] = out[j-1], out[j]
			j--
		}
	}
	return out
}

// Delete removes a request regardless of state. For tests / manual cleanup.
func (s *HITLStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.requests, id)
}

// Len returns the number of requests currently in the store.
func (s *HITLStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.requests)
}

// Stop halts the background evictor.
func (s *HITLStore) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

func (s *HITLStore) evictLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.evict()
		case <-s.stop:
			return
		}
	}
}

func (s *HITLStore) evict() {
	cutoff := time.Now().Add(-s.ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.requests {
		if r.IsTerminal() && r.UpdatedAt().Before(cutoff) {
			delete(s.requests, id)
		}
	}
}

func newHITLID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "hitl_" + hex.EncodeToString(b[:])
}
