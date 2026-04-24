// Package mcp tools provides agent-facing MCP tool implementations.
//
// These tools allow agents to interact with MCP servers at runtime:
//   - mcp_list_servers: List connected MCP servers
//   - mcp_list_resources: List resources from an MCP server
//   - mcp_read_resource: Read a specific resource
//   - mcp_list_tools: List tools from an MCP server
//   - mcp_list_prompts: List prompts from an MCP server
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── Tool Definitions ────────────────────────────────────────────────────────

// MCPToolDefinitions returns tool definitions for agent-facing MCP tools.
// These are registered with the agent tool registry.
func MCPToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "mcp_list_servers",
			Description: "List all connected MCP servers and their capabilities",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "mcp_list_resources",
			Description: "List resources available from an MCP server",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "MCP server name",
					},
				},
				"required": []string{"server"},
			},
		},
		{
			Name:        "mcp_read_resource",
			Description: "Read the contents of a resource from an MCP server",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "MCP server name",
					},
					"uri": map[string]any{
						"type":        "string",
						"description": "Resource URI to read",
					},
				},
				"required": []string{"server", "uri"},
			},
		},
		{
			Name:        "mcp_list_tools",
			Description: "List tools available from an MCP server",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "MCP server name (optional, lists all if omitted)",
					},
				},
			},
		},
		{
			Name:        "mcp_list_prompts",
			Description: "List prompts available from an MCP server",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "MCP server name",
					},
				},
				"required": []string{"server"},
			},
		},
		{
			Name:        "mcp_get_prompt",
			Description: "Get a prompt template from an MCP server with optional arguments",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "MCP server name",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Prompt name",
					},
					"arguments": map[string]any{
						"type":        "object",
						"description": "Optional arguments for the prompt",
					},
				},
				"required": []string{"server", "name"},
			},
		},
	}
}

// ToolDefinition describes an MCP tool for registration.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ─── Tool Handlers ───────────────────────────────────────────────────────────

// MCPTools provides agent-facing MCP tool implementations.
type MCPTools struct {
	manager *Manager
}

// NewMCPTools creates a new MCPTools instance.
func NewMCPTools(manager *Manager) *MCPTools {
	return &MCPTools{manager: manager}
}

// ListServers returns information about all connected MCP servers.
func (t *MCPTools) ListServers(ctx context.Context, args map[string]any) (string, error) {
	snapshot := t.manager.Snapshot()

	type serverInfo struct {
		Name         string             `json:"name"`
		State        string             `json:"state"`
		Transport    string             `json:"transport"`
		Capabilities CapabilitySnapshot `json:"capabilities"`
		ToolCount    int                `json:"tool_count"`
		Instructions string             `json:"instructions,omitempty"`
	}

	servers := make([]serverInfo, 0, len(snapshot.Servers))
	for _, s := range snapshot.Servers {
		if s.State != ConnectionStateConnected {
			continue
		}
		servers = append(servers, serverInfo{
			Name:         s.Name,
			State:        string(s.State),
			Transport:    s.Transport,
			Capabilities: s.Capabilities,
			ToolCount:    s.ToolCount,
			Instructions: s.Instructions,
		})
	}

	result := map[string]any{
		"enabled":      snapshot.Enabled,
		"server_count": len(servers),
		"servers":      servers,
	}

	return jsonString(result)
}

// ListResources lists resources available from an MCP server.
func (t *MCPTools) ListResources(ctx context.Context, args map[string]any) (string, error) {
	serverName, _ := args["server"].(string)
	if serverName == "" {
		return "", fmt.Errorf("server name is required")
	}

	listResult, err := t.manager.ListResources(ctx, serverName)
	if err != nil {
		return "", fmt.Errorf("list resources: %w", err)
	}

	// Convert to simplified format
	type resourceInfo struct {
		URI         string `json:"uri"`
		Name        string `json:"name,omitempty"`
		Description string `json:"description,omitempty"`
		MimeType    string `json:"mime_type,omitempty"`
	}

	resources := make([]resourceInfo, 0)
	if listResult != nil && listResult.Resources != nil {
		for _, r := range listResult.Resources {
			resources = append(resources, resourceInfo{
				URI:         r.URI,
				Name:        r.Name,
				Description: r.Description,
				MimeType:    r.MIMEType,
			})
		}
	}

	result := map[string]any{
		"server":    serverName,
		"count":     len(resources),
		"resources": resources,
	}

	return jsonString(result)
}

// ReadResource reads the contents of a resource from an MCP server.
func (t *MCPTools) ReadResource(ctx context.Context, args map[string]any) (string, error) {
	serverName, _ := args["server"].(string)
	uri, _ := args["uri"].(string)

	if serverName == "" {
		return "", fmt.Errorf("server name is required")
	}
	if uri == "" {
		return "", fmt.Errorf("resource URI is required")
	}

	readResult, err := t.manager.ReadResource(ctx, serverName, uri)
	if err != nil {
		return "", fmt.Errorf("read resource: %w", err)
	}

	// Extract content from result
	var contents []any
	if readResult != nil && readResult.Contents != nil {
		for _, c := range readResult.Contents {
			contents = append(contents, map[string]any{
				"uri":       c.URI,
				"mime_type": c.MIMEType,
				"text":      c.Text,
				"blob":      c.Blob,
			})
		}
	}

	result := map[string]any{
		"server":   serverName,
		"uri":      uri,
		"contents": contents,
	}

	return jsonString(result)
}

// ListTools lists tools available from MCP servers.
func (t *MCPTools) ListTools(ctx context.Context, args map[string]any) (string, error) {
	serverName, _ := args["server"].(string)

	allTools := t.manager.GetAllTools()

	if serverName != "" {
		// List tools from specific server
		tools, ok := allTools[serverName]
		if !ok {
			return "", fmt.Errorf("server %s not found or not connected", serverName)
		}

		toolInfos := convertTools(tools)
		result := map[string]any{
			"server": serverName,
			"count":  len(toolInfos),
			"tools":  toolInfos,
		}
		return jsonString(result)
	}

	// List tools from all servers
	allToolInfos := make([]map[string]any, 0)
	for srvName, tools := range allTools {
		for _, tool := range tools {
			toolInfo := map[string]any{
				"server":      srvName,
				"name":        tool.Name,
				"description": tool.Description,
			}
			if tool.InputSchema != nil {
				toolInfo["input_schema"] = tool.InputSchema
			}
			allToolInfos = append(allToolInfos, toolInfo)
		}
	}

	result := map[string]any{
		"count": len(allToolInfos),
		"tools": allToolInfos,
	}
	return jsonString(result)
}

// ListPrompts lists prompts available from an MCP server.
func (t *MCPTools) ListPrompts(ctx context.Context, args map[string]any) (string, error) {
	serverName, _ := args["server"].(string)
	if serverName == "" {
		return "", fmt.Errorf("server name is required")
	}

	listResult, err := t.manager.ListPrompts(ctx, serverName)
	if err != nil {
		return "", fmt.Errorf("list prompts: %w", err)
	}

	type promptInfo struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}

	prompts := make([]promptInfo, 0)
	if listResult != nil && listResult.Prompts != nil {
		for _, p := range listResult.Prompts {
			prompts = append(prompts, promptInfo{
				Name:        p.Name,
				Description: p.Description,
			})
		}
	}

	result := map[string]any{
		"server":  serverName,
		"count":   len(prompts),
		"prompts": prompts,
	}

	return jsonString(result)
}

// GetPrompt retrieves a prompt template from an MCP server.
func (t *MCPTools) GetPrompt(ctx context.Context, args map[string]any) (string, error) {
	serverName, _ := args["server"].(string)
	promptName, _ := args["name"].(string)
	rawArgs, _ := args["arguments"].(map[string]any)

	if serverName == "" {
		return "", fmt.Errorf("server name is required")
	}
	if promptName == "" {
		return "", fmt.Errorf("prompt name is required")
	}

	// Convert map[string]any to map[string]string
	arguments := make(map[string]string)
	for k, v := range rawArgs {
		if s, ok := v.(string); ok {
			arguments[k] = s
		}
	}

	promptResult, err := t.manager.GetPrompt(ctx, serverName, promptName, arguments)
	if err != nil {
		return "", fmt.Errorf("get prompt: %w", err)
	}

	// Convert messages
	var messages []map[string]any
	if promptResult != nil && promptResult.Messages != nil {
		for _, msg := range promptResult.Messages {
			messages = append(messages, map[string]any{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}

	result := map[string]any{
		"server":      serverName,
		"name":        promptName,
		"description": promptResult.Description,
		"messages":    messages,
	}

	return jsonString(result)
}

// Handle dispatches a tool call to the appropriate handler.
func (t *MCPTools) Handle(ctx context.Context, toolName string, args map[string]any) (string, error) {
	switch toolName {
	case "mcp_list_servers":
		return t.ListServers(ctx, args)
	case "mcp_list_resources":
		return t.ListResources(ctx, args)
	case "mcp_read_resource":
		return t.ReadResource(ctx, args)
	case "mcp_list_tools":
		return t.ListTools(ctx, args)
	case "mcp_list_prompts":
		return t.ListPrompts(ctx, args)
	case "mcp_get_prompt":
		return t.GetPrompt(ctx, args)
	default:
		return "", fmt.Errorf("unknown MCP tool: %s", toolName)
	}
}

// IsMCPTool returns true if the tool name is an MCP agent tool.
func IsMCPTool(toolName string) bool {
	return strings.HasPrefix(toolName, "mcp_")
}

func convertTools(tools []*mcp.Tool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		info := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
		}
		if tool.InputSchema != nil {
			info["input_schema"] = tool.InputSchema
		}
		result = append(result, info)
	}
	return result
}

func jsonString(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
