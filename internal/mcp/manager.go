// Package mcp provides MCP (Model Context Protocol) client support.
//
// It manages connections to external MCP servers (stdio or HTTP/SSE transport),
// discovers their tools, and adapts them into swarmstr's ToolFunc/ToolDefinition
// system so they can be used by the agent runtime.
package mcp

import (
	"crypto/sha1"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

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
}

// Config defines configuration for all MCP servers.
type Config struct {
	Enabled bool                    `json:"enabled"`
	Servers map[string]ServerConfig `json:"servers,omitempty"`
}

// ServerConnection represents a connection to an MCP server.
type ServerConnection struct {
	Name    string
	Client  *mcp.Client
	Session *mcp.ClientSession
	Tools   []*mcp.Tool
}

// Manager manages multiple MCP server connections.
type Manager struct {
	servers map[string]*ServerConnection
	mu      sync.RWMutex
	closed  atomic.Bool
	wg      sync.WaitGroup // tracks in-flight CallTool calls
}

// NewManager creates a new MCP manager.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*ServerConnection),
	}
}

// LoadFromConfig loads and connects all enabled MCP servers from configuration.
func (m *Manager) LoadFromConfig(ctx context.Context, cfg Config) error {
	if !cfg.Enabled {
		return nil
	}
	if len(cfg.Servers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(cfg.Servers))
	enabledCount := 0

	for name, serverCfg := range cfg.Servers {
		if !serverCfg.Enabled {
			continue
		}
		enabledCount++
		wg.Add(1)
		go func(name string, serverCfg ServerConfig) {
			defer wg.Done()
			if err := m.ConnectServer(ctx, name, serverCfg); err != nil {
				log.Printf("[mcp] failed to connect to server %s: %v", name, err)
				errs <- fmt.Errorf("server %s: %w", name, err)
			}
		}(name, serverCfg)
	}

	wg.Wait()
	close(errs)

	var allErrors []error
	for err := range errs {
		allErrors = append(allErrors, err)
	}

	connectedCount := len(m.GetServers())
	if enabledCount > 0 && connectedCount == 0 {
		return errors.Join(allErrors...)
	}

	if connectedCount > 0 {
		log.Printf("[mcp] connected to %d/%d servers", connectedCount, enabledCount)
	}

	return nil
}

// ConnectServer connects to a single MCP server.
func (m *Manager) ConnectServer(ctx context.Context, name string, cfg ServerConfig) error {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "swarmstr",
		Version: "1.0.0",
	}, nil)

	// Build transport based on configuration.
	var transport mcp.Transport
	transportType := cfg.Type

	// Auto-detect transport type.
	if transportType == "" {
		if cfg.URL != "" {
			transportType = "sse"
		} else if cfg.Command != "" {
			transportType = "stdio"
		} else {
			return fmt.Errorf("either URL or command must be provided")
		}
	}

	switch transportType {
	case "sse", "http":
		if cfg.URL == "" {
			return fmt.Errorf("URL is required for SSE/HTTP transport")
		}
		st := &mcp.StreamableClientTransport{
			Endpoint: cfg.URL,
		}
		if len(cfg.Headers) > 0 {
			st.HTTPClient = &http.Client{
				Transport: &headerTransport{
					base:    http.DefaultTransport,
					headers: cfg.Headers,
				},
			}
		}
		transport = st

	case "stdio":
		if cfg.Command == "" {
			return fmt.Errorf("command is required for stdio transport")
		}
		cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
		// Build env: inherit parent + overlay config env.
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
		transport = &mcp.CommandTransport{Command: cmd}

	default:
		return fmt.Errorf("unsupported transport type: %s (supported: stdio, sse, http)", transportType)
	}

	// Connect to server.
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// List available tools if supported.
	var tools []*mcp.Tool
	initResult := session.InitializeResult()
	if initResult != nil && initResult.Capabilities.Tools != nil {
		for tool, err := range session.Tools(ctx, nil) {
			if err != nil {
				log.Printf("[mcp] error listing tool from %s: %v", name, err)
				continue
			}
			tools = append(tools, tool)
		}
		log.Printf("[mcp] server %s: %d tools available", name, len(tools))
	}

	// Store connection.
	m.mu.Lock()
	m.servers[name] = &ServerConnection{
		Name:    name,
		Client:  client,
		Session: session,
		Tools:   tools,
	}
	m.mu.Unlock()

	return nil
}

// GetServers returns all connected servers.
func (m *Manager) GetServers() map[string]*ServerConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*ServerConnection, len(m.servers))
	for k, v := range m.servers {
		result[k] = v
	}
	return result
}

// CallTool calls a tool on a specific server.
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (*mcp.CallToolResult, error) {
	if m.closed.Load() {
		return nil, fmt.Errorf("manager is closed")
	}

	m.mu.RLock()
	if m.closed.Load() {
		m.mu.RUnlock()
		return nil, fmt.Errorf("manager is closed")
	}
	conn, ok := m.servers[serverName]
	if ok {
		m.wg.Add(1)
	}
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("server %s not found", serverName)
	}
	defer m.wg.Done()

	result, err := conn.Session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call tool: %w", err)
	}

	return result, nil
}

// GetAllTools returns all tools from all connected servers, keyed by server name.
func (m *Manager) GetAllTools() map[string][]*mcp.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string][]*mcp.Tool)
	for name, conn := range m.servers {
		if len(conn.Tools) > 0 {
			result[name] = conn.Tools
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
	for name, conn := range m.servers {
		if err := conn.Session.Close(); err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", name, err))
		}
	}
	m.servers = make(map[string]*ServerConnection)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
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

// ParseMCPConfig extracts MCP configuration from the extra config map.
// Expected format:
//
//	extra:
//	  mcp:
//	    enabled: true
//	    servers:
//	      myserver:
//	        enabled: true
//	        command: "npx"
//	        args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
//	      remote:
//	        enabled: true
//	        url: "https://mcp.example.com/sse"
//	        headers:
//	          Authorization: "Bearer tok"
func ParseMCPConfig(extra map[string]any) Config {
	var cfg Config
	mcpRaw, ok := extra["mcp"]
	if !ok {
		return cfg
	}
	mcpMap, ok := mcpRaw.(map[string]any)
	if !ok {
		return cfg
	}

	if enabled, ok := mcpMap["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}

	serversRaw, ok := mcpMap["servers"]
	if !ok {
		return cfg
	}
	serversMap, ok := serversRaw.(map[string]any)
	if !ok {
		return cfg
	}

	cfg.Servers = make(map[string]ServerConfig, len(serversMap))
	for name, raw := range serversMap {
		sm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var sc ServerConfig
		if v, ok := sm["enabled"].(bool); ok {
			sc.Enabled = v
		}
		if v, ok := sm["command"].(string); ok {
			sc.Command = v
		}
		if v, ok := sm["type"].(string); ok {
			sc.Type = v
		}
		if v, ok := sm["url"].(string); ok {
			sc.URL = v
		}
		// Parse args.
		if argsRaw, ok := sm["args"]; ok {
			if argsSlice, ok := argsRaw.([]any); ok {
				for _, a := range argsSlice {
					if s, ok := a.(string); ok {
						sc.Args = append(sc.Args, s)
					}
				}
			}
		}
		// Parse env.
		if envRaw, ok := sm["env"].(map[string]any); ok {
			sc.Env = make(map[string]string, len(envRaw))
			for k, v := range envRaw {
				if s, ok := v.(string); ok {
					sc.Env[k] = s
				}
			}
		}
		// Parse headers.
		if headersRaw, ok := sm["headers"].(map[string]any); ok {
			sc.Headers = make(map[string]string, len(headersRaw))
			for k, v := range headersRaw {
				if s, ok := v.(string); ok {
					sc.Headers[k] = s
				}
			}
		}
		cfg.Servers[name] = sc
	}

	return cfg
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

// MCPToolToToolDef converts an MCP Tool into a swarmstr ToolDefinition and ToolFunc.
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
// suitable for swarmstr's ToolParameters.
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
