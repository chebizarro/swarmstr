// Package mcp provides MCP (Model Context Protocol) client support.
//
// It manages connections to external MCP servers (stdio or HTTP/SSE transport),
// discovers their tools, and adapts them into metiq's ToolFunc/ToolDefinition
// system so they can be used by the agent runtime.
package mcp

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
type Config struct {
	Enabled         bool                            `json:"enabled"`
	Policy          Policy                          `json:"policy,omitempty"`
	Servers         map[string]ResolvedServerConfig `json:"servers,omitempty"`
	DisabledServers map[string]ResolvedServerConfig `json:"disabled_servers,omitempty"`
	FilteredServers map[string]FilteredServer       `json:"filtered_servers,omitempty"`
	Suppressed      []SuppressedServer              `json:"suppressed,omitempty"`
}

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
type CapabilitySnapshot struct {
	Tools     bool `json:"tools,omitempty"`
	Resources bool `json:"resources,omitempty"`
	Prompts   bool `json:"prompts,omitempty"`
	Logging   bool `json:"logging,omitempty"`
}

// ServerInfoSnapshot is the normalized runtime server identity reported by the
// MCP initialize handshake.
type ServerInfoSnapshot struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

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
type ManagerSnapshot struct {
	Enabled    bool                  `json:"enabled"`
	Servers    []ServerStateSnapshot `json:"servers,omitempty"`
	Suppressed []SuppressedServer    `json:"suppressed,omitempty"`
}

// StateChange is emitted whenever the manager mutates a server's runtime state.
type StateChange struct {
	Server        ServerStateSnapshot `json:"server"`
	PreviousState ConnectionState     `json:"previous_state,omitempty"`
	Reason        string              `json:"reason,omitempty"`
	Removed       bool                `json:"removed,omitempty"`
}

// StateObserver receives lifecycle transitions from the manager.
type StateObserver func(StateChange)

// ServerConnection represents a live connection to an MCP server.
type ServerConnection struct {
	Name         string
	Client       *mcp.Client
	Session      *mcp.ClientSession
	Tools        []*mcp.Tool
	Capabilities CapabilitySnapshot
	ServerInfo   *ServerInfoSnapshot
	Instructions string
	// Optional RPC overrides primarily used by tests and synthetic connections.
	CallToolFunc      func(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
	ListResourcesFunc func(context.Context, *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error)
	ReadResourceFunc  func(context.Context, *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error)
	ListPromptsFunc   func(context.Context, *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error)
	GetPromptFunc     func(context.Context, *mcp.GetPromptParams) (*mcp.GetPromptResult, error)
}

func (c *ServerConnection) callTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if c == nil {
		return nil, fmt.Errorf("server is not connected")
	}
	if c.CallToolFunc != nil {
		return c.CallToolFunc(ctx, params)
	}
	if c.Session == nil {
		return nil, fmt.Errorf("server session unavailable")
	}
	return c.Session.CallTool(ctx, params)
}

func (c *ServerConnection) listResources(ctx context.Context, params *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error) {
	if c == nil {
		return nil, fmt.Errorf("server is not connected")
	}
	if c.ListResourcesFunc != nil {
		return c.ListResourcesFunc(ctx, params)
	}
	if c.Session == nil {
		return nil, fmt.Errorf("server session unavailable")
	}
	return c.Session.ListResources(ctx, params)
}

func (c *ServerConnection) readResource(ctx context.Context, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	if c == nil {
		return nil, fmt.Errorf("server is not connected")
	}
	if c.ReadResourceFunc != nil {
		return c.ReadResourceFunc(ctx, params)
	}
	if c.Session == nil {
		return nil, fmt.Errorf("server session unavailable")
	}
	return c.Session.ReadResource(ctx, params)
}

func (c *ServerConnection) listPrompts(ctx context.Context, params *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error) {
	if c == nil {
		return nil, fmt.Errorf("server is not connected")
	}
	if c.ListPromptsFunc != nil {
		return c.ListPromptsFunc(ctx, params)
	}
	if c.Session == nil {
		return nil, fmt.Errorf("server session unavailable")
	}
	return c.Session.ListPrompts(ctx, params)
}

func (c *ServerConnection) getPrompt(ctx context.Context, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	if c == nil {
		return nil, fmt.Errorf("server is not connected")
	}
	if c.GetPromptFunc != nil {
		return c.GetPromptFunc(ctx, params)
	}
	if c.Session == nil {
		return nil, fmt.Errorf("server session unavailable")
	}
	return c.Session.GetPrompt(ctx, params)
}

type serverRecord struct {
	config            ResolvedServerConfig
	connection        *ServerConnection
	state             ConnectionState
	lastError         string
	reconnectAttempts int
	lastAttemptAt     time.Time
	lastConnectedAt   time.Time
	lastFailedAt      time.Time
	updatedAt         time.Time
}

type connectFunc func(context.Context, string, ServerConfig) (*ServerConnection, error)
type RemoteAuthHeaderProvider func(context.Context, string, ServerConfig) (map[string]string, error)

// Manager manages multiple MCP server connections.
type Manager struct {
	servers    map[string]*serverRecord
	suppressed []SuppressedServer
	enabled    bool
	mu         sync.RWMutex
	closed     atomic.Bool
	wg         sync.WaitGroup // tracks in-flight CallTool calls
	observe    StateObserver
	connectFn  connectFunc
	authHeader RemoteAuthHeaderProvider
}

// NewManager creates a new MCP manager.
func NewManager() *Manager {
	m := &Manager{
		servers: make(map[string]*serverRecord),
	}
	m.connectFn = func(ctx context.Context, name string, cfg ServerConfig) (*ServerConnection, error) {
		return m.defaultConnectServer(ctx, name, cfg)
	}
	return m
}

// SetStateObserver installs a lifecycle observer for state transitions.
func (m *Manager) SetStateObserver(observer StateObserver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observe = observer
}

// SetConnectFunc overrides the server connector used for future connects.
// Passing nil restores the default connector.
func (m *Manager) SetConnectFunc(fn func(context.Context, string, ServerConfig) (*ServerConnection, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fn == nil {
		m.connectFn = func(ctx context.Context, name string, cfg ServerConfig) (*ServerConnection, error) {
			return m.defaultConnectServer(ctx, name, cfg)
		}
		return
	}
	m.connectFn = fn
}

// SetRemoteAuthHeaderProvider installs a callback that can supply dynamic
// headers, such as OAuth bearer tokens, for remote SSE/HTTP servers.
func (m *Manager) SetRemoteAuthHeaderProvider(provider RemoteAuthHeaderProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authHeader = provider
}

// LoadFromConfig loads and connects all configured MCP servers from
// configuration.
func (m *Manager) LoadFromConfig(ctx context.Context, cfg Config) error {
	return m.ApplyConfig(ctx, cfg)
}

// ApplyConfig reconciles the manager's runtime inventory with the supplied MCP
// config. Existing connections are reused when the launch signature is stable;
// changed or newly-enabled servers reconnect immediately.
func (m *Manager) ApplyConfig(ctx context.Context, cfg Config) error {
	if m.closed.Load() {
		return fmt.Errorf("manager is closed")
	}

	changes := make([]StateChange, 0)
	closers := make([]*ServerConnection, 0)
	connectTargets := make([]string, 0)
	var allErrors []error

	m.mu.Lock()
	m.enabled = cfg.Enabled
	m.suppressed = append([]SuppressedServer(nil), cfg.Suppressed...)

	if !cfg.Enabled {
		desiredDisabled := make(map[string]ResolvedServerConfig, len(cfg.DisabledServers))
		for name, resolved := range cfg.DisabledServers {
			desiredDisabled[name] = resolved
		}
		for name, record := range m.servers {
			resolved, keep := desiredDisabled[name]
			if !keep {
				if record.connection != nil {
					closers = append(closers, record.connection)
				}
				changes = append(changes, StateChange{
					Server:        record.snapshot(name),
					PreviousState: record.state,
					Reason:        "config.removed",
					Removed:       true,
				})
				delete(m.servers, name)
				continue
			}
			record.config = resolved
			change, closer := m.setStateLocked(name, record, ConnectionStateDisabled, "", time.Time{}, time.Time{}, "config.disabled")
			if closer != nil {
				closers = append(closers, closer)
			}
			if change != nil {
				changes = append(changes, *change)
			}
			delete(desiredDisabled, name)
		}
		for name, resolved := range desiredDisabled {
			record := m.ensureRecordLocked(name, resolved)
			record.config = resolved
			change, closer := m.setStateLocked(name, record, ConnectionStateDisabled, "", time.Time{}, time.Time{}, "config.disabled")
			if closer != nil {
				closers = append(closers, closer)
			}
			if change != nil {
				changes = append(changes, *change)
			}
		}
		m.mu.Unlock()
		closeConnections(closers)
		m.emitStateChanges(changes)
		return nil
	}

	desired := make(map[string]ResolvedServerConfig, len(cfg.Servers)+len(cfg.DisabledServers))
	for name, resolved := range cfg.Servers {
		desired[name] = resolved
	}
	for name, resolved := range cfg.DisabledServers {
		desired[name] = resolved
	}

	for name, record := range m.servers {
		if _, ok := desired[name]; ok {
			continue
		}
		if record.connection != nil {
			closers = append(closers, record.connection)
		}
		change := StateChange{
			Server:        record.snapshot(name),
			PreviousState: record.state,
			Reason:        "config.removed",
			Removed:       true,
		}
		delete(m.servers, name)
		changes = append(changes, change)
	}

	for name, resolved := range cfg.DisabledServers {
		record := m.ensureRecordLocked(name, resolved)
		record.config = resolved
		change, closer := m.setStateLocked(name, record, ConnectionStateDisabled, "", time.Time{}, time.Time{}, "config.disabled")
		if closer != nil {
			closers = append(closers, closer)
		}
		if change != nil {
			changes = append(changes, *change)
		}
	}

	for name, resolved := range cfg.Servers {
		record := m.ensureRecordLocked(name, resolved)
		previousSignature := record.config.Signature
		previousState := record.state
		record.config = resolved
		if record.connection == nil || previousSignature != resolved.Signature || previousState == ConnectionStateDisabled {
			connectTargets = append(connectTargets, name)
		}
	}
	m.mu.Unlock()

	closeConnections(closers)
	m.emitStateChanges(changes)

	for _, name := range connectTargets {
		if err := m.reconnectServer(ctx, name, "config.connect"); err != nil {
			allErrors = append(allErrors, fmt.Errorf("server %s: %w", name, err))
		}
	}

	snapshot := m.Snapshot()
	connectedCount := 0
	configuredEnabled := 0
	for _, server := range snapshot.Servers {
		if !server.Enabled {
			continue
		}
		configuredEnabled++
		if server.State == ConnectionStateConnected {
			connectedCount++
		}
	}
	if connectedCount > 0 {
		log.Printf("[mcp] connected to %d/%d servers", connectedCount, configuredEnabled)
	}
	if configuredEnabled > 0 && connectedCount == 0 && len(allErrors) > 0 {
		return errors.Join(allErrors...)
	}
	return nil
}

// ConnectServer connects to a single MCP server and records it in the manager.
func (m *Manager) ConnectServer(ctx context.Context, name string, cfg ServerConfig) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("server name is required")
	}
	m.mu.Lock()
	record := m.ensureRecordLocked(name, ResolvedServerConfig{
		Name:         name,
		ServerConfig: normalizeServerConfig(cfg),
		Source:       ConfigSource("manual"),
		Precedence:   0,
		Signature:    getServerSignature(cfg),
	})
	record.config.Enabled = true
	m.enabled = true
	m.mu.Unlock()
	return m.reconnectServer(ctx, name, "manual.connect")
}

// ReconnectServer reconnects a configured MCP server and refreshes its
// capability/tool snapshot.
func (m *Manager) ReconnectServer(ctx context.Context, name string) error {
	return m.reconnectServer(ctx, strings.TrimSpace(name), "manual.reconnect")
}

// RefreshServer refreshes the discovered capabilities/tools for the currently
// connected session without re-reading config.
func (m *Manager) RefreshServer(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("server name is required")
	}
	if m.closed.Load() {
		return fmt.Errorf("manager is closed")
	}

	m.mu.RLock()
	record, ok := m.servers[name]
	var current *ServerConnection
	if ok {
		current = record.connection
	}
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server %s not found", name)
	}
	if record.state == ConnectionStateDisabled {
		return fmt.Errorf("server %s is disabled", name)
	}
	if current == nil || current.Session == nil {
		return fmt.Errorf("server %s is not connected", name)
	}

	refreshed, err := refreshConnectedServer(ctx, name, current)
	if err != nil {
		state := classifyConnectError(err)
		m.mu.Lock()
		record = m.ensureRecordLocked(name, record.config)
		change, closer := m.setStateLocked(name, record, state, err.Error(), time.Time{}, time.Now().UTC(), "manual.refresh")
		m.mu.Unlock()
		closeConnections([]*ServerConnection{closer})
		m.emitStateChanges([]StateChange{*change})
		return err
	}

	m.mu.Lock()
	record = m.ensureRecordLocked(name, record.config)
	if record.connection != nil {
		refreshed.Client = record.connection.Client
	}
	old := record.connection
	record.connection = refreshed
	record.lastError = ""
	record.lastConnectedAt = time.Now().UTC()
	record.updatedAt = record.lastConnectedAt
	change := m.transitionLocked(name, record, ConnectionStateConnected, "", "manual.refresh")
	change.Server = record.snapshot(name)
	m.mu.Unlock()

	closeConnections([]*ServerConnection{old})
	m.emitStateChanges([]StateChange{change})
	return nil
}

func (m *Manager) reconnectServer(ctx context.Context, name, reason string) error {
	if name == "" {
		return fmt.Errorf("server name is required")
	}
	if m.closed.Load() {
		return fmt.Errorf("manager is closed")
	}

	m.mu.Lock()
	record, ok := m.servers[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("server %s not found", name)
	}
	if !record.config.Enabled {
		change, closer := m.setStateLocked(name, record, ConnectionStateDisabled, "", time.Time{}, time.Time{}, reason)
		m.mu.Unlock()
		closeConnections([]*ServerConnection{closer})
		m.emitStateChanges([]StateChange{*change})
		return nil
	}
	record.lastAttemptAt = time.Now().UTC()
	record.reconnectAttempts++
	record.updatedAt = record.lastAttemptAt
	pendingChange := m.transitionLocked(name, record, ConnectionStatePending, "", reason)
	pendingChange.Server = record.snapshot(name)
	cfg := record.config.ServerConfig
	m.mu.Unlock()
	m.emitStateChanges([]StateChange{pendingChange})

	connected, err := m.connectFn(ctx, name, cfg)
	if err != nil {
		state := classifyConnectError(err)
		m.mu.Lock()
		record, ok = m.servers[name]
		if !ok {
			m.mu.Unlock()
			return err
		}
		change, closer := m.setStateLocked(name, record, state, err.Error(), time.Time{}, time.Now().UTC(), reason)
		m.mu.Unlock()
		closeConnections([]*ServerConnection{closer})
		m.emitStateChanges([]StateChange{*change})
		return err
	}

	now := time.Now().UTC()
	m.mu.Lock()
	record, ok = m.servers[name]
	if !ok {
		m.mu.Unlock()
		closeConnections([]*ServerConnection{connected})
		return fmt.Errorf("server %s removed during reconnect", name)
	}
	old := record.connection
	record.connection = connected
	record.lastError = ""
	record.lastConnectedAt = now
	record.updatedAt = now
	change := m.transitionLocked(name, record, ConnectionStateConnected, "", reason)
	change.Server = record.snapshot(name)
	m.mu.Unlock()

	closeConnections([]*ServerConnection{old})
	m.emitStateChanges([]StateChange{change})
	return nil
}

func (m *Manager) ensureRecordLocked(name string, resolved ResolvedServerConfig) *serverRecord {
	if m.servers == nil {
		m.servers = make(map[string]*serverRecord)
	}
	if record, ok := m.servers[name]; ok && record != nil {
		return record
	}
	record := &serverRecord{config: resolved}
	if !resolved.Enabled {
		record.state = ConnectionStateDisabled
	}
	m.servers[name] = record
	return record
}

func (m *Manager) transitionLocked(name string, record *serverRecord, next ConnectionState, errText, reason string) StateChange {
	previous := record.state
	record.state = next
	record.lastError = strings.TrimSpace(errText)
	record.updatedAt = time.Now().UTC()
	return StateChange{
		Server:        record.snapshot(name),
		PreviousState: previous,
		Reason:        strings.TrimSpace(reason),
	}
}

func (m *Manager) setStateLocked(name string, record *serverRecord, next ConnectionState, errText string, connectedAt, failedAt time.Time, reason string) (*StateChange, *ServerConnection) {
	if record == nil {
		return nil, nil
	}
	if !connectedAt.IsZero() {
		record.lastConnectedAt = connectedAt.UTC()
	}
	if !failedAt.IsZero() {
		record.lastFailedAt = failedAt.UTC()
	}
	var closer *ServerConnection
	if next == ConnectionStateDisabled || next == ConnectionStateFailed || next == ConnectionStateNeedsAuth {
		closer = record.connection
		record.connection = nil
	}
	change := m.transitionLocked(name, record, next, errText, reason)
	change.Server = record.snapshot(name)
	return &change, closer
}

func (r *serverRecord) snapshot(name string) ServerStateSnapshot {
	snap := ServerStateSnapshot{
		Name:              name,
		State:             r.state,
		Enabled:           r.config.Enabled,
		Source:            r.config.Source,
		Precedence:        r.config.Precedence,
		Signature:         r.config.Signature,
		Transport:         transportTypeForSignature(r.config.ServerConfig),
		Command:           r.config.Command,
		URL:               r.config.URL,
		LastError:         r.lastError,
		ReconnectAttempts: r.reconnectAttempts,
		LastAttemptAtMS:   runtimeMillis(r.lastAttemptAt),
		LastConnectedAtMS: runtimeMillis(r.lastConnectedAt),
		LastFailedAtMS:    runtimeMillis(r.lastFailedAt),
		UpdatedAtMS:       runtimeMillis(r.updatedAt),
	}
	if r.connection != nil {
		snap.ToolCount = len(r.connection.Tools)
		snap.Capabilities = r.connection.Capabilities
		snap.ServerInfo = cloneServerInfo(r.connection.ServerInfo)
		snap.Instructions = r.connection.Instructions
	}
	return snap
}

// Snapshot returns a consistent MCP runtime snapshot ordered by server name.
func (m *Manager) Snapshot() ManagerSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	servers := make([]ServerStateSnapshot, 0, len(m.servers))
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		servers = append(servers, m.servers[name].snapshot(name))
	}
	return ManagerSnapshot{
		Enabled:    m.enabled,
		Servers:    servers,
		Suppressed: append([]SuppressedServer(nil), m.suppressed...),
	}
}

// ListServerStates returns the current runtime-visible state for every tracked
// server.
func (m *Manager) ListServerStates() []ServerStateSnapshot {
	return m.Snapshot().Servers
}

// GetServers returns all connected servers.
func (m *Manager) GetServers() map[string]*ServerConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*ServerConnection, len(m.servers))
	for name, record := range m.servers {
		if record == nil || record.connection == nil || record.state != ConnectionStateConnected {
			continue
		}
		result[name] = record.connection
	}
	return result
}

func (m *Manager) acquireConnection(serverName string) (*ServerConnection, error) {
	if m.closed.Load() {
		return nil, fmt.Errorf("manager is closed")
	}

	m.mu.RLock()
	if m.closed.Load() {
		m.mu.RUnlock()
		return nil, fmt.Errorf("manager is closed")
	}
	record, ok := m.servers[serverName]
	var conn *ServerConnection
	if ok {
		conn = record.connection
	}
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("server %s not found", serverName)
	}
	if record.state != ConnectionStateConnected || conn == nil {
		state := record.state
		m.mu.RUnlock()
		return nil, fmt.Errorf("server %s is %s", serverName, state)
	}
	m.wg.Add(1)
	m.mu.RUnlock()
	return conn, nil
}

// CallTool calls a tool on a specific server.
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (*mcp.CallToolResult, error) {
	conn, err := m.acquireConnection(serverName)
	if err != nil {
		return nil, err
	}
	defer m.wg.Done()

	result, err := conn.callTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call tool: %w", err)
	}

	return result, nil
}

// ListResources lists resources from a specific connected MCP server.
func (m *Manager) ListResources(ctx context.Context, serverName string) (*mcp.ListResourcesResult, error) {
	conn, err := m.acquireConnection(serverName)
	if err != nil {
		return nil, err
	}
	defer m.wg.Done()

	if !conn.Capabilities.Resources {
		return nil, fmt.Errorf("server %s does not support resources", serverName)
	}

	result, err := conn.listResources(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}
	return result, nil
}

// ReadResource reads a specific resource from a connected MCP server.
func (m *Manager) ReadResource(ctx context.Context, serverName, uri string) (*mcp.ReadResourceResult, error) {
	conn, err := m.acquireConnection(serverName)
	if err != nil {
		return nil, err
	}
	defer m.wg.Done()

	if !conn.Capabilities.Resources {
		return nil, fmt.Errorf("server %s does not support resources", serverName)
	}

	result, err := conn.readResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, fmt.Errorf("failed to read resource: %w", err)
	}
	return result, nil
}

// ListPrompts lists prompts from a specific connected MCP server.
func (m *Manager) ListPrompts(ctx context.Context, serverName string) (*mcp.ListPromptsResult, error) {
	conn, err := m.acquireConnection(serverName)
	if err != nil {
		return nil, err
	}
	defer m.wg.Done()

	if !conn.Capabilities.Prompts {
		return nil, fmt.Errorf("server %s does not support prompts", serverName)
	}

	result, err := conn.listPrompts(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}
	return result, nil
}

// GetPrompt fetches a prompt from a connected MCP server, optionally applying prompt arguments.
func (m *Manager) GetPrompt(ctx context.Context, serverName, promptName string, arguments map[string]string) (*mcp.GetPromptResult, error) {
	conn, err := m.acquireConnection(serverName)
	if err != nil {
		return nil, err
	}
	defer m.wg.Done()

	if !conn.Capabilities.Prompts {
		return nil, fmt.Errorf("server %s does not support prompts", serverName)
	}

	result, err := conn.getPrompt(ctx, &mcp.GetPromptParams{
		Name:      promptName,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt: %w", err)
	}
	return result, nil
}

// GetAllTools returns all tools from all connected servers, keyed by server name.
func (m *Manager) GetAllTools() map[string][]*mcp.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string][]*mcp.Tool)
	for name, record := range m.servers {
		if record == nil || record.connection == nil || record.state != ConnectionStateConnected {
			continue
		}
		if len(record.connection.Tools) > 0 {
			result[name] = record.connection.Tools
		}
	}
	return result
}

// Close closes all server connections.
func (m *Manager) Close() error {
	if m.closed.Swap(true) {
		return nil // already closed
	}

	// Wait for in-flight calls to finish.
	m.wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, record := range m.servers {
		if record == nil || record.connection == nil || record.connection.Session == nil {
			continue
		}
		if err := record.connection.Session.Close(); err != nil {
			errs = append(errs, err)
		}
		record.connection = nil
	}
	m.servers = make(map[string]*serverRecord)
	m.suppressed = nil

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (m *Manager) defaultConnectServer(ctx context.Context, name string, cfg ServerConfig) (*ServerConnection, error) {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "metiq",
		Version: "1.0.0",
	}, nil)

	transport, err := m.buildTransport(ctx, name, cfg)
	if err != nil {
		return nil, err
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	connected := &ServerConnection{
		Name:    name,
		Client:  client,
		Session: session,
	}
	refreshed, err := refreshConnectedServer(ctx, name, connected)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return refreshed, nil
}

func refreshConnectedServer(ctx context.Context, name string, conn *ServerConnection) (*ServerConnection, error) {
	if conn == nil || conn.Session == nil {
		return nil, fmt.Errorf("server %s is not connected", name)
	}
	refreshed := *conn
	initResult := conn.Session.InitializeResult()
	refreshed.Capabilities = capabilitySnapshotFromInit(initResult)
	refreshed.ServerInfo = serverInfoSnapshotFromInit(initResult)
	refreshed.Instructions = ""
	if initResult != nil {
		refreshed.Instructions = strings.TrimSpace(initResult.Instructions)
	}

	var tools []*mcp.Tool
	if initResult != nil && initResult.Capabilities != nil && initResult.Capabilities.Tools != nil {
		for tool, err := range conn.Session.Tools(ctx, nil) {
			if err != nil {
				log.Printf("[mcp] error listing tool from %s: %v", name, err)
				continue
			}
			tools = append(tools, tool)
		}
		log.Printf("[mcp] server %s: %d tools available", name, len(tools))
	}
	refreshed.Tools = tools
	return &refreshed, nil
}

func (m *Manager) buildTransport(ctx context.Context, serverName string, cfg ServerConfig) (mcp.Transport, error) {
	// Build transport based on configuration.
	transportType := cfg.Type

	// Auto-detect transport type.
	if transportType == "" {
		if cfg.URL != "" {
			transportType = "sse"
		} else if cfg.Command != "" {
			transportType = "stdio"
		} else {
			return nil, fmt.Errorf("either URL or command must be provided")
		}
	}

	switch transportType {
	case "sse", "http":
		if cfg.URL == "" {
			return nil, fmt.Errorf("URL is required for SSE/HTTP transport")
		}
		headers := trimStringMap(cfg.Headers)
		m.mu.RLock()
		authProvider := m.authHeader
		m.mu.RUnlock()
		if authProvider != nil {
			dynamicHeaders, err := authProvider(ctx, serverName, cfg)
			if err != nil {
				return nil, err
			}
			if len(dynamicHeaders) > 0 {
				if headers == nil {
					headers = map[string]string{}
				}
				for key, value := range dynamicHeaders {
					key = strings.TrimSpace(key)
					value = strings.TrimSpace(value)
					if key == "" || value == "" {
						continue
					}
					headers[key] = value
				}
			}
		}
		st := &mcp.StreamableClientTransport{Endpoint: cfg.URL}
		if len(headers) > 0 {
			st.HTTPClient = &http.Client{
				Transport: &headerTransport{base: http.DefaultTransport, headers: headers},
			}
		}
		return st, nil
	case "stdio":
		if cfg.Command == "" {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
		envMap := make(map[string]string)
		for _, e := range os.Environ() {
			if idx := strings.Index(e, "="); idx > 0 {
				envMap[e[:idx]] = e[idx+1:]
			}
		}
		for k, v := range cfg.Env {
			envMap[k] = v
		}
		env := make([]string, 0, len(envMap))
		for k, v := range envMap {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
		return &mcp.CommandTransport{Command: cmd}, nil
	default:
		return nil, fmt.Errorf("unsupported transport type: %s (supported: stdio, sse, http)", transportType)
	}
}

func capabilitySnapshotFromInit(initResult *mcp.InitializeResult) CapabilitySnapshot {
	if initResult == nil || initResult.Capabilities == nil {
		return CapabilitySnapshot{}
	}
	caps := initResult.Capabilities
	return CapabilitySnapshot{
		Tools:     caps.Tools != nil,
		Resources: caps.Resources != nil,
		Prompts:   caps.Prompts != nil,
		Logging:   caps.Logging != nil,
	}
}

func serverInfoSnapshotFromInit(initResult *mcp.InitializeResult) *ServerInfoSnapshot {
	if initResult == nil || initResult.ServerInfo == nil {
		return nil
	}
	info := initResult.ServerInfo
	return &ServerInfoSnapshot{
		Name:    strings.TrimSpace(info.Name),
		Title:   strings.TrimSpace(info.Title),
		Version: strings.TrimSpace(info.Version),
	}
}

func cloneServerInfo(info *ServerInfoSnapshot) *ServerInfoSnapshot {
	if info == nil {
		return nil
	}
	cp := *info
	return &cp
}

func classifyConnectError(err error) ConnectionState {
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "401"),
		strings.Contains(msg, "403"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "forbidden"),
		strings.Contains(msg, "needs auth"),
		strings.Contains(msg, "need auth"),
		strings.Contains(msg, "auth required"),
		strings.Contains(msg, "authentication"):
		return ConnectionStateNeedsAuth
	default:
		return ConnectionStateFailed
	}
}

func closeConnections(connections []*ServerConnection) {
	for _, conn := range connections {
		if conn == nil || conn.Session == nil {
			continue
		}
		if err := conn.Session.Close(); err != nil {
			log.Printf("[mcp] close session error: %v", err)
		}
	}
}

func (m *Manager) emitStateChanges(changes []StateChange) {
	if len(changes) == 0 {
		return
	}
	m.mu.RLock()
	observer := m.observe
	m.mu.RUnlock()
	if observer == nil {
		return
	}
	for _, change := range changes {
		observer(change)
	}
}

func runtimeMillis(ts time.Time) int64 {
	if ts.IsZero() {
		return 0
	}
	return ts.UnixMilli()
}

// headerTransport is an http.RoundTripper that adds custom headers to requests.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for key, value := range t.headers {
		req.Header.Set(key, value)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// extractContentText extracts text from MCP content array.
func extractContentText(content []mcp.Content) string {
	var parts []string
	for _, c := range content {
		switch v := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, v.Text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[Image: %s]", v.MIMEType))
		default:
			parts = append(parts, fmt.Sprintf("[Content: %T]", v))
		}
	}
	return strings.Join(parts, "\n")
}

// MCPToolToToolDef converts an MCP Tool into a metiq ToolDefinition and ToolFunc.
// The returned name is prefixed with "mcp_{serverName}_{toolName}" and sanitized.
func MCPToolToToolDef(mgr *Manager, serverName string, tool *mcp.Tool) (name string, fn func(context.Context, map[string]any) (string, error), params map[string]any) {
	// Build sanitized name.
	sanitized := sanitize(serverName) + "_" + sanitize(tool.Name)
	name = "mcp_" + sanitized
	if len(name) > 64 {
		suffix := "_" + shortHashHex("mcp|"+serverName+"|"+tool.Name)
		maxPrefix := 64 - len(suffix)
		if maxPrefix < 1 {
			maxPrefix = 1
		}
		if maxPrefix > len(name) {
			maxPrefix = len(name)
		}
		name = name[:maxPrefix] + suffix
	}

	// Build parameters schema.
	params = toolInputSchemaToMap(tool.InputSchema)

	// Build executor.
	fn = func(ctx context.Context, args map[string]any) (string, error) {
		result, err := mgr.CallTool(ctx, serverName, tool.Name, args)
		if err != nil {
			return "", fmt.Errorf("MCP tool %s/%s failed: %w", serverName, tool.Name, err)
		}
		if result == nil {
			return "", fmt.Errorf("MCP tool %s/%s returned nil result", serverName, tool.Name)
		}
		if result.IsError {
			return "", fmt.Errorf("MCP tool error: %s", extractContentText(result.Content))
		}
		return extractContentText(result.Content), nil
	}

	return name, fn, params
}

func shortHashHex(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}

// sanitize normalizes a string for use in tool names.
func sanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prev := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			if !prev {
				b.WriteByte('_')
				prev = true
			}
			continue
		}
		if r == '_' {
			if prev {
				continue
			}
			prev = true
		} else {
			prev = false
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "_")
}

// toolInputSchemaToMap converts an MCP tool's InputSchema to a map[string]any
// suitable for metiq's ToolParameters.
func toolInputSchemaToMap(schema any) map[string]any {
	if schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	// Try direct map.
	if m, ok := schema.(map[string]any); ok {
		return m
	}
	// Try JSON round-trip.
	data, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return result
}
