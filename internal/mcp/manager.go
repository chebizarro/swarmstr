package mcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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
// SetStateObserver installs a lifecycle observer for state transitions.
func (m *Manager) SetStateObserver(observer StateObserver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observe = observer
}

// SetStateObserverAndSnapshot installs a lifecycle observer and returns the
// current server-state snapshot from the same critical section so callers can
// emit an initial inventory without racing a concurrent state transition.
// SetStateObserverAndSnapshot installs a lifecycle observer and returns the
// current server-state snapshot from the same critical section so callers can
// emit an initial inventory without racing a concurrent state transition.
func (m *Manager) SetStateObserverAndSnapshot(observer StateObserver) []ServerStateSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observe = observer
	return m.snapshotServerStatesLocked()
}

// SetConnectFunc overrides the server connector used for future connects.
// Passing nil restores the default connector.
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
// SetRemoteAuthHeaderProvider installs a callback that can supply dynamic
// headers, such as OAuth bearer tokens, for remote SSE/HTTP servers.
func (m *Manager) SetRemoteAuthHeaderProvider(provider RemoteAuthHeaderProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authHeader = provider
}

// LoadFromConfig loads and connects all configured MCP servers from
// configuration.
// LoadFromConfig loads and connects all configured MCP servers from
// configuration.
func (m *Manager) LoadFromConfig(ctx context.Context, cfg Config) error {
	return m.ApplyConfig(ctx, cfg)
}

// ApplyConfig reconciles the manager's runtime inventory with the supplied MCP
// config. Existing connections are reused when the launch signature is stable;
// changed or newly-enabled servers reconnect immediately.
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
// ReconnectServer reconnects a configured MCP server and refreshes its
// capability/tool snapshot.
func (m *Manager) ReconnectServer(ctx context.Context, name string) error {
	return m.reconnectServer(ctx, strings.TrimSpace(name), "manual.reconnect")
}

// RefreshServer refreshes the discovered capabilities/tools for the currently
// connected session without re-reading config.
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
