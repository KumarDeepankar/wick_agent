package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Writer sends Server-Sent Events to an http.ResponseWriter.
type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewWriter creates a new SSE writer. Returns nil if the ResponseWriter
// doesn't support http.Flusher.
func NewWriter(w http.ResponseWriter) *Writer {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx
	flusher.Flush()

	return &Writer{w: w, flusher: flusher}
}

// SendEvent writes a named SSE event with JSON data.
func (s *Writer) SendEvent(event string, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal SSE data: %w", err)
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, jsonData)
	s.flusher.Flush()
	return nil
}

// SendData writes an unnamed SSE event (event type = "message") with JSON data.
func (s *Writer) SendData(data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal SSE data: %w", err)
	}
	fmt.Fprintf(s.w, "data: %s\n\n", jsonData)
	s.flusher.Flush()
	return nil
}

// SendComment writes an SSE comment (for keep-alive pings).
func (s *Writer) SendComment(text string) {
	fmt.Fprintf(s.w, ": %s\n\n", text)
	s.flusher.Flush()
}
