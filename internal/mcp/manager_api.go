package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Snapshot returns a consistent MCP runtime snapshot ordered by server name.
func (m *Manager) Snapshot() ManagerSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return ManagerSnapshot{
		Enabled:    m.enabled,
		Servers:    m.snapshotServerStatesLocked(),
		Suppressed: append([]SuppressedServer(nil), m.suppressed...),
	}
}

func (m *Manager) snapshotServerStatesLocked() []ServerStateSnapshot {
	servers := make([]ServerStateSnapshot, 0, len(m.servers))
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		servers = append(servers, m.servers[name].snapshot(name))
	}
	return servers
}

// ListServerStates returns the current runtime-visible state for every tracked
// server.
// ListServerStates returns the current runtime-visible state for every tracked
// server.
func (m *Manager) ListServerStates() []ServerStateSnapshot {
	return m.Snapshot().Servers
}

// GetServers returns all connected servers.
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
