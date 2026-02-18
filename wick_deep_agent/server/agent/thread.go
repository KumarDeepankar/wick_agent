package agent

import "sync"

// ThreadStore is an in-memory thread state checkpointer.
type ThreadStore struct {
	mu      sync.RWMutex
	threads map[string]*AgentState
}

// GlobalThreadStore is the shared thread store for all agents.
var GlobalThreadStore = NewThreadStore()

// NewThreadStore creates a new thread store.
func NewThreadStore() *ThreadStore {
	return &ThreadStore{
		threads: make(map[string]*AgentState),
	}
}

// LoadOrCreate returns the thread state for a given ID, creating if needed.
func (ts *ThreadStore) LoadOrCreate(threadID string) *AgentState {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if state, ok := ts.threads[threadID]; ok {
		return state
	}

	state := &AgentState{
		ThreadID: threadID,
		Messages: []Message{},
		Files:    make(map[string]string),
	}
	ts.threads[threadID] = state
	return state
}

// Save persists the thread state.
func (ts *ThreadStore) Save(threadID string, state *AgentState) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.threads[threadID] = state
}

// Get returns the thread state or nil.
func (ts *ThreadStore) Get(threadID string) *AgentState {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.threads[threadID]
}

// Delete removes a thread.
func (ts *ThreadStore) Delete(threadID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.threads, threadID)
}
