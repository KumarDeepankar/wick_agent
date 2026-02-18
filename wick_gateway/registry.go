package main

import (
	"log"
	"sync"
	"time"
)

// Registry maps tool names to their owning downstream client.
type Registry struct {
	mu       sync.RWMutex
	toolMap  map[string]*DownstreamClient // toolName -> client
	allTools []Tool                       // aggregated tool list
	clients  []*DownstreamClient          // all registered downstreams

	stopHealth chan struct{}

	// OnChange is called (without mu held) whenever the tool list changes.
	OnChange func()
}

// notifyChange fires the OnChange callback if set.
func (r *Registry) notifyChange() {
	if r.OnChange != nil {
		r.OnChange()
	}
}

func NewRegistry() *Registry {
	return &Registry{
		toolMap: make(map[string]*DownstreamClient),
	}
}

// DiscoverAll connects to each downstream, lists tools, and populates the registry.
// Failures are logged as warnings — the gateway starts regardless.
func (r *Registry) DiscoverAll(clients []*DownstreamClient) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.toolMap = make(map[string]*DownstreamClient)
	r.allTools = nil
	r.clients = clients

	for _, c := range clients {
		r.connectAndDiscover(c)
	}

	log.Printf("Registry ready: %d tools from %d downstreams", len(r.allTools), len(clients))
}

// connectAndDiscover attempts to connect + list tools for a single downstream.
// Must be called with r.mu held for writing.
func (r *Registry) connectAndDiscover(c *DownstreamClient) {
	log.Printf("Connecting to downstream %q at %s", c.Name, c.URL)

	if err := c.Connect(); err != nil {
		log.Printf("WARNING: failed to connect to %s: %v (will retry)", c.Name, err)
		c.SetHealth(false, err.Error(), 0)
		return
	}

	tools, err := c.ListTools()
	if err != nil {
		log.Printf("WARNING: failed to discover tools from %s: %v (will retry)", c.Name, err)
		c.SetHealth(false, err.Error(), 0)
		return
	}

	for _, t := range tools {
		if existing, ok := r.toolMap[t.Name]; ok {
			log.Printf("WARNING: tool %q from %s shadows tool from %s", t.Name, c.Name, existing.Name)
		}
		r.toolMap[t.Name] = c
		r.allTools = append(r.allTools, t)
		log.Printf("  discovered tool %q from %s", t.Name, c.Name)
	}

	c.SetHealth(true, "", len(tools))
}

// AddDownstream registers a new downstream, attempts connect + discovery, and adds its tools.
func (r *Registry) AddDownstream(name, url string) *DownstreamClient {
	r.mu.Lock()

	// Check if already exists.
	for _, c := range r.clients {
		if c.Name == name {
			log.Printf("Downstream %q already exists, skipping add", name)
			r.mu.Unlock()
			return c
		}
	}

	c := NewDownstreamClient(name, url)
	r.clients = append(r.clients, c)
	toolsBefore := len(r.allTools)
	r.connectAndDiscover(c)
	changed := len(r.allTools) != toolsBefore
	r.mu.Unlock()

	if changed {
		r.notifyChange()
	}
	return c
}

// RemoveDownstream removes a downstream and all its tools from the registry.
func (r *Registry) RemoveDownstream(name string) bool {
	r.mu.Lock()

	var target *DownstreamClient
	remaining := make([]*DownstreamClient, 0, len(r.clients))
	for _, c := range r.clients {
		if c.Name == name {
			target = c
		} else {
			remaining = append(remaining, c)
		}
	}

	if target == nil {
		r.mu.Unlock()
		return false
	}

	r.clients = remaining

	// Remove all tools owned by this downstream.
	newToolMap := make(map[string]*DownstreamClient)
	var newTools []Tool
	for _, t := range r.allTools {
		if r.toolMap[t.Name] != target {
			newToolMap[t.Name] = r.toolMap[t.Name]
			newTools = append(newTools, t)
		}
	}
	r.toolMap = newToolMap
	r.allTools = newTools

	// Close the session in the background.
	go func() {
		if err := target.Close(); err != nil {
			log.Printf("Error closing downstream %s: %v", name, err)
		}
	}()

	log.Printf("Removed downstream %q", name)
	r.mu.Unlock()

	r.notifyChange()
	return true
}

// StartHealthLoop starts a background goroutine that periodically checks downstream health.
// Disconnected downstreams are retried; connected ones are pinged.
func (r *Registry) StartHealthLoop(interval time.Duration) {
	r.stopHealth = make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.healthCheck()
			case <-r.stopHealth:
				return
			}
		}
	}()
}

// StopHealthLoop stops the background health check goroutine.
func (r *Registry) StopHealthLoop() {
	if r.stopHealth != nil {
		close(r.stopHealth)
	}
}

func (r *Registry) healthCheck() {
	r.mu.Lock()

	changed := false
	for _, c := range r.clients {
		status := c.Status()
		if !status.Connected {
			// Retry disconnected downstream.
			log.Printf("Retrying connection to %s", c.Name)
			toolsBefore := len(r.allTools)
			r.connectAndDiscover(c)
			if len(r.allTools) != toolsBefore {
				changed = true
			}
		} else {
			// Ping connected downstream to verify it's still alive.
			_, _, err := c.call("ping", nil)
			if err != nil {
				log.Printf("WARNING: downstream %s failed ping: %v", c.Name, err)
				c.SetHealth(false, err.Error(), 0)
				// Remove its tools from the registry.
				toolsBefore := len(r.allTools)
				r.removeToolsFor(c)
				if len(r.allTools) != toolsBefore {
					changed = true
				}
			} else {
				c.SetHealth(true, "", status.ToolCount)
			}
		}
	}

	r.mu.Unlock()

	if changed {
		r.notifyChange()
	}
}

// removeToolsFor removes all tools belonging to a specific downstream.
// Must be called with r.mu held for writing.
func (r *Registry) removeToolsFor(c *DownstreamClient) {
	newToolMap := make(map[string]*DownstreamClient)
	var newTools []Tool
	for _, t := range r.allTools {
		if r.toolMap[t.Name] != c {
			newToolMap[t.Name] = r.toolMap[t.Name]
			newTools = append(newTools, t)
		}
	}
	r.toolMap = newToolMap
	r.allTools = newTools
}

// AllDownstreams returns the status of every registered downstream.
func (r *Registry) AllDownstreams() []DownstreamStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	statuses := make([]DownstreamStatus, len(r.clients))
	for i, c := range r.clients {
		statuses[i] = c.Status()
	}
	return statuses
}

// Clients returns all registered downstream clients.
func (r *Registry) Clients() []*DownstreamClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*DownstreamClient, len(r.clients))
	copy(result, r.clients)
	return result
}

// Lookup returns the downstream client that owns the given tool, or nil.
func (r *Registry) Lookup(toolName string) *DownstreamClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.toolMap[toolName]
}

// AllTools returns the merged list of tools from all downstreams.
func (r *Registry) AllTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.allTools
}
