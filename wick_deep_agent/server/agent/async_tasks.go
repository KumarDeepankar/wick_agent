package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// AsyncTaskStatus represents the lifecycle state of an async sub-agent task.
type AsyncTaskStatus string

const (
	AsyncTaskRunning   AsyncTaskStatus = "running"
	AsyncTaskDone      AsyncTaskStatus = "done"
	AsyncTaskError     AsyncTaskStatus = "error"
	AsyncTaskCancelled AsyncTaskStatus = "cancelled"
)

// asyncTaskTTL controls how long completed tasks linger in the store before
// the evictor removes them. Running tasks are never evicted.
const asyncTaskTTL = 1 * time.Hour

// updatesMailboxSize bounds how many pending update_async_task messages can
// queue before a subsequent update_async_task call reports the task as busy.
const updatesMailboxSize = 8

// AsyncTask represents a detached sub-agent run. It is created by
// start_async_task and survives the tool call that spawned it — the supervisor
// agent continues while the task runs in its own goroutine with its own
// context.
type AsyncTask struct {
	ID        string
	ThreadID  string // parent thread — scopes list_async_tasks
	AgentName string
	Task      string // the original task description
	Updates   chan string
	Cancel    context.CancelFunc
	Done      chan struct{} // closed when the goroutine exits

	CreatedAt time.Time

	mu        sync.RWMutex
	status    AsyncTaskStatus
	output    string // accumulates during run; final content on done
	errMsg    string
	updatedAt time.Time
}

// Status returns the current status (thread-safe).
func (t *AsyncTask) Status() AsyncTaskStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

// Output returns the current accumulated output (thread-safe).
func (t *AsyncTask) Output() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.output
}

// Error returns the error message if any (thread-safe).
func (t *AsyncTask) Error() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.errMsg
}

// UpdatedAt returns the last-updated timestamp (thread-safe).
func (t *AsyncTask) UpdatedAt() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.updatedAt
}

// AppendOutput appends streamed content to the task's output buffer.
func (t *AsyncTask) AppendOutput(s string) {
	if s == "" {
		return
	}
	t.mu.Lock()
	t.output += s
	t.updatedAt = time.Now()
	t.mu.Unlock()
}

// SetStatus updates the status (thread-safe).
func (t *AsyncTask) SetStatus(s AsyncTaskStatus) {
	t.mu.Lock()
	t.status = s
	t.updatedAt = time.Now()
	t.mu.Unlock()
}

// Finish marks the task as finished with a terminal status. Subsequent calls
// are no-ops. The Done channel is closed exactly once.
func (t *AsyncTask) Finish(status AsyncTaskStatus, finalOutput, errMsg string) {
	t.mu.Lock()
	// Only the first terminal transition wins.
	if t.status != AsyncTaskRunning {
		t.mu.Unlock()
		return
	}
	t.status = status
	if finalOutput != "" {
		t.output = finalOutput
	}
	t.errMsg = errMsg
	t.updatedAt = time.Now()
	t.mu.Unlock()

	// Close Done exactly once.
	select {
	case <-t.Done:
		// already closed
	default:
		close(t.Done)
	}
}

// IsTerminal reports whether the task has reached a terminal state.
func (t *AsyncTask) IsTerminal() bool {
	s := t.Status()
	return s == AsyncTaskDone || s == AsyncTaskError || s == AsyncTaskCancelled
}

// AsyncTaskStore is an in-memory registry of detached sub-agent tasks with
// TTL-based eviction of terminal tasks. Running tasks are retained until they
// finish. Follows the pattern of ThreadStore.
type AsyncTaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*AsyncTask
	ttl   time.Duration
	stop  chan struct{}
}

// GlobalAsyncTaskStore is the shared store used by SubAgentHook.
var GlobalAsyncTaskStore = NewAsyncTaskStore()

// NewAsyncTaskStore creates a new store and starts the background evictor.
func NewAsyncTaskStore() *AsyncTaskStore {
	s := &AsyncTaskStore{
		tasks: make(map[string]*AsyncTask),
		ttl:   asyncTaskTTL,
		stop:  make(chan struct{}),
	}
	go s.evictLoop()
	return s
}

// Create registers a new running task and returns it. The caller receives a
// task whose Cancel is a no-op until they call SetCancel — this lets the
// caller construct the detached context after registration.
func (s *AsyncTaskStore) Create(threadID, agentName, task string) *AsyncTask {
	now := time.Now()
	t := &AsyncTask{
		ID:        newAsyncTaskID(),
		ThreadID:  threadID,
		AgentName: agentName,
		Task:      task,
		Updates:   make(chan string, updatesMailboxSize),
		Cancel:    func() {}, // replaced by SetCancel
		Done:      make(chan struct{}),
		CreatedAt: now,
		status:    AsyncTaskRunning,
		updatedAt: now,
	}
	s.mu.Lock()
	s.tasks[t.ID] = t
	s.mu.Unlock()
	return t
}

// SetCancel installs the cancel function for a task. Called once after the
// caller has built the detached context.
func (s *AsyncTaskStore) SetCancel(taskID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[taskID]; ok {
		t.Cancel = cancel
	}
}

// Get returns the task with the given ID, or nil.
func (s *AsyncTaskStore) Get(taskID string) *AsyncTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tasks[taskID]
}

// ListByThread returns all tasks belonging to the given thread, newest first.
func (s *AsyncTaskStore) ListByThread(threadID string) []*AsyncTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*AsyncTask, 0)
	for _, t := range s.tasks {
		if t.ThreadID == threadID {
			out = append(out, t)
		}
	}
	// newest first
	sortByCreatedDesc(out)
	return out
}

// Delete removes a task regardless of state. Only intended for tests and
// manual cleanup; the evictor handles routine cleanup.
func (s *AsyncTaskStore) Delete(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, taskID)
}

// Len returns the number of tasks currently in the store.
func (s *AsyncTaskStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tasks)
}

// Stop halts the background evictor. Intended for tests.
func (s *AsyncTaskStore) Stop() {
	select {
	case <-s.stop:
		// already stopped
	default:
		close(s.stop)
	}
}

// evictLoop runs every 5 minutes and removes terminal tasks older than TTL.
func (s *AsyncTaskStore) evictLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evict()
		case <-s.stop:
			return
		}
	}
}

func (s *AsyncTaskStore) evict() {
	cutoff := time.Now().Add(-s.ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, t := range s.tasks {
		if t.IsTerminal() && t.UpdatedAt().Before(cutoff) {
			delete(s.tasks, id)
		}
	}
}

// newAsyncTaskID generates a task ID of the form "task_<12hex>".
func newAsyncTaskID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "task_" + hex.EncodeToString(b[:])
}

// sortByCreatedDesc sorts tasks newest-first in place. Small N, insertion sort.
func sortByCreatedDesc(ts []*AsyncTask) {
	for i := 1; i < len(ts); i++ {
		j := i
		for j > 0 && ts[j].CreatedAt.After(ts[j-1].CreatedAt) {
			ts[j], ts[j-1] = ts[j-1], ts[j]
			j--
		}
	}
}
