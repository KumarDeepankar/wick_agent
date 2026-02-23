package handlers

import "sync"

// EventBus is a simple pub/sub for broadcasting config-change events.
type EventBus struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

// NewEventBus creates a new event bus.
func NewEventBus() *EventBus {
	return &EventBus{
		clients: make(map[chan string]struct{}),
	}
}

// Subscribe returns a channel that receives broadcast events.
func (eb *EventBus) Subscribe() chan string {
	ch := make(chan string, 16)
	eb.mu.Lock()
	eb.clients[ch] = struct{}{}
	eb.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (eb *EventBus) Unsubscribe(ch chan string) {
	eb.mu.Lock()
	delete(eb.clients, ch)
	eb.mu.Unlock()
}

// Broadcast sends an event to all subscribers.
func (eb *EventBus) Broadcast(event string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	for ch := range eb.clients {
		select {
		case ch <- event:
		default:
			// Drop if buffer full
		}
	}
}
