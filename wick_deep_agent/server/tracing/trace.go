package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"wick_go/agent"
)

// Span represents a single timed operation within a trace.
type Span struct {
	Name       string         `json:"name"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time"`
	DurationMs float64        `json:"duration_ms"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Trace collects all spans for a single invoke/stream request.
// Implements agent.TraceRecorder.
type Trace struct {
	mu         sync.Mutex     `json:"-"`
	TraceID    string         `json:"trace_id"`
	AgentID    string         `json:"agent_id"`
	ThreadID   string         `json:"thread_id"`
	Model      string         `json:"model"`
	Method     string         `json:"method"` // "invoke" or "stream"
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time"`
	DurationMs float64        `json:"duration_ms"`
	Spans      []Span         `json:"spans"`
	Input      map[string]any `json:"input,omitempty"`
	Output     map[string]any `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// Compile-time check that *Trace implements agent.TraceRecorder.
var _ agent.TraceRecorder = (*Trace)(nil)

// NewTrace creates a new trace for an invoke/stream request.
func NewTrace(agentID, threadID, model, method string, messageCount int) *Trace {
	return &Trace{
		TraceID:   generateID(),
		AgentID:   agentID,
		ThreadID:  threadID,
		Model:     model,
		Method:    method,
		StartTime: time.Now(),
		Spans:     []Span{},
		Input:     map[string]any{"message_count": messageCount},
	}
}

// SpanRecorder is a convenience builder returned by StartSpan.
// Implements agent.SpanHandle.
type SpanRecorder struct {
	trace *Trace
	span  Span
}

// Compile-time check that *SpanRecorder implements agent.SpanHandle.
var _ agent.SpanHandle = (*SpanRecorder)(nil)

// StartSpan begins recording a timed span (satisfies agent.TraceRecorder).
func (t *Trace) StartSpan(name string) agent.SpanHandle {
	return &SpanRecorder{
		trace: t,
		span:  Span{Name: name, StartTime: time.Now(), Metadata: map[string]any{}},
	}
}

// RecordEvent records an instantaneous event (satisfies agent.TraceRecorder).
func (t *Trace) RecordEvent(name string, metadata map[string]any) {
	now := time.Now()
	t.addSpan(Span{
		Name:      name,
		StartTime: now,
		EndTime:   now,
		Metadata:  metadata,
	})
}

// Set adds a metadata key-value pair (satisfies agent.SpanHandle).
func (sr *SpanRecorder) Set(key string, value any) agent.SpanHandle {
	sr.span.Metadata[key] = value
	return sr
}

// End finalizes the span and appends it to the trace (satisfies agent.SpanHandle).
func (sr *SpanRecorder) End() {
	sr.span.EndTime = time.Now()
	sr.span.DurationMs = float64(sr.span.EndTime.Sub(sr.span.StartTime)) / float64(time.Millisecond)
	sr.trace.addSpan(sr.span)
}

func (t *Trace) addSpan(s Span) {
	t.mu.Lock()
	t.Spans = append(t.Spans, s)
	t.mu.Unlock()
}

// Finish finalizes the trace with an optional error.
func (t *Trace) Finish(err error) {
	t.EndTime = time.Now()
	t.DurationMs = float64(t.EndTime.Sub(t.StartTime)) / float64(time.Millisecond)
	if err != nil {
		t.Error = err.Error()
	}
}

// --- Store ----------------------------------------------------------------

// Store holds recent traces in memory with bounded capacity.
type Store struct {
	mu     sync.RWMutex
	traces map[string]*Trace
	order  []string // FIFO order for eviction
	max    int
}

// NewStore creates a store that retains up to maxSize traces.
func NewStore(maxSize int) *Store {
	return &Store{
		traces: make(map[string]*Trace),
		order:  make([]string, 0, maxSize),
		max:    maxSize,
	}
}

// Put stores a trace, evicting the oldest if at capacity.
func (s *Store) Put(t *Trace) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.order) >= s.max {
		oldest := s.order[0]
		delete(s.traces, oldest)
		s.order = s.order[1:]
	}
	s.traces[t.TraceID] = t
	s.order = append(s.order, t.TraceID)
}

// Get returns a trace by ID, or nil if not found.
func (s *Store) Get(traceID string) *Trace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.traces[traceID]
}

// List returns the most recent traces, up to limit.
func (s *Store) List(limit int) []*Trace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := len(s.order)
	if limit > n {
		limit = n
	}
	result := make([]*Trace, limit)
	for i := 0; i < limit; i++ {
		result[i] = s.traces[s.order[n-1-i]]
	}
	return result
}

// --- Context helpers ------------------------------------------------------
// Delegates to agent.WithTraceRecorder / agent.TraceFromContext to use a
// single context key, avoiding cross-package key mismatch.

// WithTrace stores the trace in context via agent.WithTraceRecorder.
func WithTrace(ctx context.Context, t *Trace) context.Context {
	return agent.WithTraceRecorder(ctx, t)
}

// FromContext extracts the concrete *Trace from context.
func FromContext(ctx context.Context) *Trace {
	tr := agent.TraceFromContext(ctx)
	if tr == nil {
		return nil
	}
	t, _ := tr.(*Trace)
	return t
}

// --- ID generation --------------------------------------------------------

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
