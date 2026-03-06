// Package agent – per-agent runtime registry and session router.
package agent

import (
	"sync"
)

// ──────────────────────────────────────────────────────────────────────────────
// AgentRuntimeRegistry
// ──────────────────────────────────────────────────────────────────────────────

// AgentRuntimeRegistry manages per-agent Runtime instances.  The registry is
// goroutine-safe.  A default (fallback) runtime is returned for any agent ID
// that does not have an explicitly registered runtime.
type AgentRuntimeRegistry struct {
	mu    sync.RWMutex
	cache map[string]Runtime
	def   Runtime
}

// NewAgentRuntimeRegistry returns a registry backed by defaultRuntime.
// The default is returned for any agent ID that has no registered runtime.
func NewAgentRuntimeRegistry(defaultRuntime Runtime) *AgentRuntimeRegistry {
	return &AgentRuntimeRegistry{
		cache: make(map[string]Runtime),
		def:   defaultRuntime,
	}
}

// Get returns the Runtime registered for agentID, or the default runtime if
// no explicit registration exists.  "main" (or an empty string) always returns
// the default runtime.
func (r *AgentRuntimeRegistry) Get(agentID string) Runtime {
	if agentID == "" || agentID == "main" {
		return r.def
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rt, ok := r.cache[agentID]; ok {
		return rt
	}
	return r.def
}

// Set registers a Runtime for agentID.  Pass nil to remove the registration.
func (r *AgentRuntimeRegistry) Set(agentID string, rt Runtime) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt == nil {
		delete(r.cache, agentID)
	} else {
		r.cache[agentID] = rt
	}
}

// Remove removes the explicit runtime for agentID; subsequent Get calls return
// the default runtime.
func (r *AgentRuntimeRegistry) Remove(agentID string) {
	r.Set(agentID, nil)
}

// Registered returns all explicitly registered agent IDs (excludes the default).
// The returned slice is not sorted.
func (r *AgentRuntimeRegistry) Registered() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.cache))
	for id := range r.cache {
		ids = append(ids, id)
	}
	return ids
}

// ──────────────────────────────────────────────────────────────────────────────
// AgentSessionRouter
// ──────────────────────────────────────────────────────────────────────────────

// AgentSessionRouter maps session IDs (peer pubkeys for DM, client IDs for WS)
// to agent IDs.  It is goroutine-safe.
type AgentSessionRouter struct {
	mu       sync.RWMutex
	sessions map[string]string // sessionID → agentID
}

// NewAgentSessionRouter returns a ready-to-use AgentSessionRouter.
func NewAgentSessionRouter() *AgentSessionRouter {
	return &AgentSessionRouter{sessions: make(map[string]string)}
}

// Assign routes sessionID to agentID.
func (r *AgentSessionRouter) Assign(sessionID, agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sessionID] = agentID
}

// Unassign removes the explicit routing for sessionID; subsequent Get calls
// return "" so the caller can fall back to the default agent.
func (r *AgentSessionRouter) Unassign(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

// Get returns the agentID assigned to sessionID, or "" if none is assigned.
func (r *AgentSessionRouter) Get(sessionID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[sessionID]
}

// List returns a copy of all current session→agentID assignments.
func (r *AgentSessionRouter) List() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.sessions))
	for k, v := range r.sessions {
		out[k] = v
	}
	return out
}
