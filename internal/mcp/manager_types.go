package mcp

// ServerConfig defines configuration for a single MCP server.
type ServerConfig struct {
	// Enabled indicates whether this MCP server is active.
	Enabled bool `json:"enabled"`
	// Command is the executable to run (for stdio transport).
	Command string `json:"command,omitempty"`
	// Args are the arguments to pass to the command.
	Args []string `json:"args,omitempty"`
	// Env are environment variables to set for the server process (stdio only).
	Env map[string]string `json:"env,omitempty"`
	// Type is "stdio", "sse", or "http". Auto-detected if empty.
	Type string `json:"type,omitempty"`
	// URL is used for SSE/HTTP transport.
	URL string `json:"url,omitempty"`
	// Headers are HTTP headers to send with requests (SSE/HTTP only).
	Headers map[string]string `json:"headers,omitempty"`
	// OAuth config describes optional remote OAuth acquisition/refresh behavior.
	OAuth *OAuthConfig `json:"oauth,omitempty"`
}

// Config defines configuration for all MCP servers.
// Config defines configuration for all MCP servers.
type Config struct {
	Enabled         bool                            `json:"enabled"`
	Policy          Policy                          `json:"policy,omitempty"`
	Servers         map[string]ResolvedServerConfig `json:"servers,omitempty"`
	DisabledServers map[string]ResolvedServerConfig `json:"disabled_servers,omitempty"`
	FilteredServers map[string]FilteredServer       `json:"filtered_servers,omitempty"`
	Suppressed      []SuppressedServer              `json:"suppressed,omitempty"`
}

// ConnectionState is the manager's runtime state for a configured MCP server.
// ConnectionState is the manager's runtime state for a configured MCP server.
type ConnectionState string

const (
	ConnectionStatePending   ConnectionState = "pending"
	ConnectionStateConnected ConnectionState = "connected"
	ConnectionStateFailed    ConnectionState = "failed"
	ConnectionStateNeedsAuth ConnectionState = "needs-auth"
	ConnectionStateDisabled  ConnectionState = "disabled"
)

// CapabilitySnapshot captures the runtime capabilities currently discovered for
// a connected MCP server.
// CapabilitySnapshot captures the runtime capabilities currently discovered for
// a connected MCP server.
type CapabilitySnapshot struct {
	Tools     bool `json:"tools,omitempty"`
	Resources bool `json:"resources,omitempty"`
	Prompts   bool `json:"prompts,omitempty"`
	Logging   bool `json:"logging,omitempty"`
}

// ServerInfoSnapshot is the normalized runtime server identity reported by the
// MCP initialize handshake.
// ServerInfoSnapshot is the normalized runtime server identity reported by the
// MCP initialize handshake.
type ServerInfoSnapshot struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// ServerStateSnapshot is the runtime-visible state for one configured MCP
// server.
// ServerStateSnapshot is the runtime-visible state for one configured MCP
// server.
type ServerStateSnapshot struct {
	Name              string              `json:"name"`
	State             ConnectionState     `json:"state"`
	Enabled           bool                `json:"enabled"`
	Source            ConfigSource        `json:"source,omitempty"`
	Precedence        int                 `json:"precedence,omitempty"`
	Signature         string              `json:"signature,omitempty"`
	Transport         string              `json:"transport,omitempty"`
	Command           string              `json:"command,omitempty"`
	URL               string              `json:"url,omitempty"`
	Capabilities      CapabilitySnapshot  `json:"capabilities,omitempty"`
	ServerInfo        *ServerInfoSnapshot `json:"server_info,omitempty"`
	Instructions      string              `json:"instructions,omitempty"`
	ToolCount         int                 `json:"tool_count,omitempty"`
	LastError         string              `json:"last_error,omitempty"`
	ReconnectAttempts int                 `json:"reconnect_attempts,omitempty"`
	LastAttemptAtMS   int64               `json:"last_attempt_at_ms,omitempty"`
	LastConnectedAtMS int64               `json:"last_connected_at_ms,omitempty"`
	LastFailedAtMS    int64               `json:"last_failed_at_ms,omitempty"`
	UpdatedAtMS       int64               `json:"updated_at_ms,omitempty"`
}

// ManagerSnapshot is the current runtime-visible MCP inventory.
// ManagerSnapshot is the current runtime-visible MCP inventory.
type ManagerSnapshot struct {
	Enabled    bool                  `json:"enabled"`
	Servers    []ServerStateSnapshot `json:"servers,omitempty"`
	Suppressed []SuppressedServer    `json:"suppressed,omitempty"`
}

// StateChange is emitted whenever the manager mutates a server's runtime state.
// StateChange is emitted whenever the manager mutates a server's runtime state.
type StateChange struct {
	Server        ServerStateSnapshot `json:"server"`
	PreviousState ConnectionState     `json:"previous_state,omitempty"`
	Reason        string              `json:"reason,omitempty"`
	Removed       bool                `json:"removed,omitempty"`
}

// StateObserver receives lifecycle transitions from the manager.
// StateObserver receives lifecycle transitions from the manager.
type StateObserver func(StateChange)

// ServerConnection represents a live connection to an MCP server.
