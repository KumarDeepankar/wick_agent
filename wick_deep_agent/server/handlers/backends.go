package handlers

import (
	"sync"

	"wick_server/backend"
)

// BackendStore maps "agentID:username" → backend.Backend.
type BackendStore struct {
	mu       sync.RWMutex
	backends map[string]backend.Backend
}

// NewBackendStore creates a new backend store.
func NewBackendStore() *BackendStore {
	return &BackendStore{
		backends: make(map[string]backend.Backend),
	}
}

func backendKey(agentID, username string) string {
	return agentID + ":" + username
}

// Get returns the backend for a given agent/user or nil.
func (bs *BackendStore) Get(agentID, username string) backend.Backend {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.backends[backendKey(agentID, username)]
}

// Set stores a backend for a given agent/user.
func (bs *BackendStore) Set(agentID, username string, b backend.Backend) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.backends[backendKey(agentID, username)] = b
}

// Remove removes and cleans up a backend.
func (bs *BackendStore) Remove(agentID, username string) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	key := backendKey(agentID, username)
	if b, ok := bs.backends[key]; ok {
		// Clean up backends that manage containers
		if cm, ok := b.(backend.ContainerManager); ok {
			cm.CancelLaunch()
			cm.StopContainer()
		}
		delete(bs.backends, key)
	}
}
