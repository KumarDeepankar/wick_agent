package agent

import "context"

// TraceRecorder allows the agent loop to record spans without importing
// the tracing package (avoids circular dependency).
type TraceRecorder interface {
	// StartSpan begins a timed span; call End() on the returned handle.
	StartSpan(name string) SpanHandle
	// RecordEvent records an instantaneous (zero-duration) event.
	RecordEvent(name string, metadata map[string]any)
}

// SpanHandle is a timed span that accumulates metadata.
type SpanHandle interface {
	Set(key string, value any) SpanHandle
	End()
}

type traceRecorderKey struct{}

// WithTraceRecorder stores a TraceRecorder in the context.
func WithTraceRecorder(ctx context.Context, tr TraceRecorder) context.Context {
	return context.WithValue(ctx, traceRecorderKey{}, tr)
}

// TraceFromContext extracts the TraceRecorder, or nil.
func TraceFromContext(ctx context.Context) TraceRecorder {
	tr, _ := ctx.Value(traceRecorderKey{}).(TraceRecorder)
	return tr
}
