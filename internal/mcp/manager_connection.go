package mcp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
