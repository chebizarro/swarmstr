package mcp

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	params = ToolInputSchemaToMap(tool.InputSchema)

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

// ToolInputSchemaToMap converts an MCP tool's InputSchema to a map[string]any
// suitable for metiq's ToolParameters.
func ToolInputSchemaToMap(schema any) map[string]any {
	if schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
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

func cloneMCPTool(tool *mcp.Tool) *mcp.Tool {
	if tool == nil {
		return nil
	}
	cp := *tool
	cp.InputSchema = cloneMCPJSONValue(tool.InputSchema)
	return &cp
}

func cloneMCPJSONValue(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}
