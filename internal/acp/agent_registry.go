package acp

import (
	"fmt"
	"strings"
	"sync"
)

// ── MCP server config ───────────────────────────────────────────────────────

// MCPServerConfig describes an MCP server that should be made available
// to an ACP agent session.
type MCPServerConfig struct {
	// Name identifies the MCP server (e.g. "filesystem", "github").
	Name string `json:"name"`
	// Command is the executable to run (e.g. "npx", "uvx").
	Command string `json:"command"`
	// Args are command-line arguments.
	Args []string `json:"args,omitempty"`
	// Env are environment variables to pass to the server.
	Env map[string]string `json:"env,omitempty"`
}

// ── Agent entry ─────────────────────────────────────────────────────────────

// AgentEntry describes a registered ACP agent with its execution configuration.
// This is separate from PeerRegistry which tracks remote Nostr peer agents by
// pubkey; AgentEntry maps logical agent names to local execution config.
type AgentEntry struct {
	// ID is the logical agent identifier (normalized lowercase).
	ID string `json:"id"`
	// Command is the executable to launch the agent.
	Command string `json:"command"`
	// Args are command-line arguments for the agent.
	Args []string `json:"args,omitempty"`
	// Env are environment variables for the agent process.
	Env map[string]string `json:"env,omitempty"`
	// MCPServers lists MCP servers available to this agent.
	MCPServers []MCPServerConfig `json:"mcp_servers,omitempty"`
	// Tags holds arbitrary key/value metadata (capabilities, region, etc.).
	Tags map[string]string `json:"tags,omitempty"`
	// MaxSpawnDepth optionally overrides the manager's spawn depth for this agent.
	MaxSpawnDepth int `json:"max_spawn_depth,omitempty"`
	// MaxChildren optionally overrides the manager's direct child limit for this agent.
	MaxChildren int `json:"max_children,omitempty"`
}

// ── Agent registry ──────────────────────────────────────────────────────────

// AgentRegistry maps logical agent names to their execution configuration.
// All methods are goroutine-safe.
type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]AgentEntry // key: normalized id
}

// NewAgentRegistry creates an empty AgentRegistry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{agents: make(map[string]AgentEntry)}
}

// normalizeAgentID lowercases and trims an agent ID.
func normalizeAgentID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// Register adds or replaces an agent entry. Returns an error if ID or Command
// is empty.
func (r *AgentRegistry) Register(e AgentEntry) error {
	id := normalizeAgentID(e.ID)
	if id == "" {
		return fmt.Errorf("agent id required")
	}
	if strings.TrimSpace(e.Command) == "" {
		return fmt.Errorf("agent command required for %q", id)
	}
	e.ID = id
	r.mu.Lock()
	r.agents[id] = e
	r.mu.Unlock()
	return nil
}

// Unregister removes an agent by ID.
func (r *AgentRegistry) Unregister(id string) {
	r.mu.Lock()
	delete(r.agents, normalizeAgentID(id))
	r.mu.Unlock()
}

// Resolve returns the entry for a registered agent. ID is normalized on lookup.
func (r *AgentRegistry) Resolve(id string) (AgentEntry, bool) {
	r.mu.RLock()
	e, ok := r.agents[normalizeAgentID(id)]
	r.mu.RUnlock()
	return e, ok
}

// List returns all registered agents.
func (r *AgentRegistry) List() []AgentEntry {
	r.mu.RLock()
	out := make([]AgentEntry, 0, len(r.agents))
	for _, e := range r.agents {
		out = append(out, e)
	}
	r.mu.RUnlock()
	return out
}

// Count returns the number of registered agents.
func (r *AgentRegistry) Count() int {
	r.mu.RLock()
	n := len(r.agents)
	r.mu.RUnlock()
	return n
}
