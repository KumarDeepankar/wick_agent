package main

import (
	"fmt"
	"log"
	"sync"
)

// Registry maps tool names to their owning downstream client.
type Registry struct {
	mu       sync.RWMutex
	toolMap  map[string]*DownstreamClient // toolName -> client
	allTools []Tool                       // aggregated tool list
}

func NewRegistry() *Registry {
	return &Registry{
		toolMap: make(map[string]*DownstreamClient),
	}
}

// DiscoverAll connects to each downstream, lists tools, and populates the registry.
func (r *Registry) DiscoverAll(clients []*DownstreamClient) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.toolMap = make(map[string]*DownstreamClient)
	r.allTools = nil

	for _, c := range clients {
		log.Printf("Connecting to downstream %q at %s", c.Name, c.URL)

		if err := c.Connect(); err != nil {
			return fmt.Errorf("connect %s: %w", c.Name, err)
		}

		tools, err := c.ListTools()
		if err != nil {
			return fmt.Errorf("discover %s: %w", c.Name, err)
		}

		for _, t := range tools {
			if existing, ok := r.toolMap[t.Name]; ok {
				log.Printf("WARNING: tool %q from %s shadows tool from %s", t.Name, c.Name, existing.Name)
			}
			r.toolMap[t.Name] = c
			r.allTools = append(r.allTools, t)
			log.Printf("  discovered tool %q from %s", t.Name, c.Name)
		}
	}

	log.Printf("Registry ready: %d tools from %d downstreams", len(r.allTools), len(clients))
	return nil
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
