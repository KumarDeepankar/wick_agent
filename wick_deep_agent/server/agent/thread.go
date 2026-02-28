package agent

import (
	"sync"
	"time"
)

const defaultThreadTTL = 1 * time.Hour

// threadEntry wraps an AgentState with a last-accessed timestamp for TTL eviction.
type threadEntry struct {
	state      *AgentState
	lastAccess time.Time
}

// ThreadStore is an in-memory thread state checkpointer with TTL-based eviction.
type ThreadStore struct {
	mu      sync.RWMutex
	threads map[string]*threadEntry
	ttl     time.Duration
	stop    chan struct{}
}

// GlobalThreadStore is the shared thread store for all agents.
var GlobalThreadStore = NewThreadStore()

// NewThreadStore creates a new thread store with default TTL and starts eviction.
func NewThreadStore() *ThreadStore {
	ts := &ThreadStore{
		threads: make(map[string]*threadEntry),
		ttl:     defaultThreadTTL,
		stop:    make(chan struct{}),
	}
	go ts.evictLoop()
	return ts
}

// LoadOrCreate returns the thread state for a given ID, creating if needed.
func (ts *ThreadStore) LoadOrCreate(threadID string) *AgentState {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if entry, ok := ts.threads[threadID]; ok {
		entry.lastAccess = time.Now()
		return entry.state
	}

	state := &AgentState{
		ThreadID: threadID,
		Messages: []Message{},
		Files:    make(map[string]string),
	}
	ts.threads[threadID] = &threadEntry{state: state, lastAccess: time.Now()}
	return state
}

// Save persists the thread state and refreshes its TTL.
func (ts *ThreadStore) Save(threadID string, state *AgentState) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.threads[threadID] = &threadEntry{state: state, lastAccess: time.Now()}
}

// Get returns the thread state or nil.
func (ts *ThreadStore) Get(threadID string) *AgentState {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if entry, ok := ts.threads[threadID]; ok {
		return entry.state
	}
	return nil
}

// Delete removes a thread.
func (ts *ThreadStore) Delete(threadID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.threads, threadID)
}

// Len returns the number of stored threads.
func (ts *ThreadStore) Len() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.threads)
}

// evictLoop runs every 5 minutes and removes threads that haven't been accessed
// within the TTL window.
func (ts *ThreadStore) evictLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ts.evict()
		case <-ts.stop:
			return
		}
	}
}

func (ts *ThreadStore) evict() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	cutoff := time.Now().Add(-ts.ttl)
	for id, entry := range ts.threads {
		if entry.lastAccess.Before(cutoff) {
			delete(ts.threads, id)
		}
	}
}
