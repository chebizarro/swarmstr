package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"metiq/internal/agent"
	mcppkg "metiq/internal/mcp"
)

// MCPPromptToolOpts configures built-in tools for external MCP prompts.
type MCPPromptToolOpts struct {
	Manager func() *mcppkg.Manager
}

var MCPPromptsListDef = agent.ToolDefinition{
	Name:        "mcp_prompts_list",
	Description: "List prompts exposed by connected external MCP servers. If server is omitted, aggregates prompts across all connected prompt-capable servers.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server": {Type: "string", Description: "Optional MCP server name to restrict the listing to one server."},
		},
	},
}

var MCPPromptsGetDef = agent.ToolDefinition{
	Name:        "mcp_prompts_get",
	Description: "Fetch a specific prompt from a connected external MCP server by server name and prompt name. Optional prompt arguments may be provided as key/value pairs.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server":    {Type: "string", Description: "MCP server name."},
			"name":      {Type: "string", Description: "Prompt name to fetch."},
			"arguments": {Type: "object", Description: "Optional prompt arguments as key/value pairs."},
		},
		Required: []string{"server", "name"},
	},
}

type externalMCPPrompt struct {
	Server      string                    `json:"server"`
	Name        string                    `json:"name"`
	Title       string                    `json:"title,omitempty"`
	Description string                    `json:"description,omitempty"`
	Arguments   []externalMCPPromptArgDef `json:"arguments,omitempty"`
}

type externalMCPPromptArgDef struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type externalMCPPromptError struct {
	Server string `json:"server"`
	Error  string `json:"error"`
}

// RegisterMCPPromptTools registers stable built-in tools for querying prompts
// exposed by connected external MCP servers.
func RegisterMCPPromptTools(tools *agent.ToolRegistry, opts MCPPromptToolOpts) {
	resolveManager := func() (*mcppkg.Manager, error) {
		if opts.Manager == nil {
			return nil, fmt.Errorf("external MCP manager unavailable")
		}
		mgr := opts.Manager()
		if mgr == nil {
			return nil, fmt.Errorf("external MCP manager unavailable")
		}
		return mgr, nil
	}

	appendPrompts := func(dst []externalMCPPrompt, server string, result *sdkmcp.ListPromptsResult) []externalMCPPrompt {
		if result == nil {
			return dst
		}
		for _, prompt := range result.Prompts {
			if prompt == nil {
				continue
			}
			entry := externalMCPPrompt{
				Server:      server,
				Name:        strings.TrimSpace(prompt.Name),
				Title:       strings.TrimSpace(prompt.Title),
				Description: strings.TrimSpace(prompt.Description),
			}
			for _, arg := range prompt.Arguments {
				if arg == nil {
					continue
				}
				entry.Arguments = append(entry.Arguments, externalMCPPromptArgDef{
					Name:        strings.TrimSpace(arg.Name),
					Title:       strings.TrimSpace(arg.Title),
					Description: strings.TrimSpace(arg.Description),
					Required:    arg.Required,
				})
			}
			dst = append(dst, entry)
		}
		return dst
	}

	tools.RegisterWithDef("mcp_prompts_list", func(ctx context.Context, args map[string]any) (string, error) {
		mgr, err := resolveManager()
		if err != nil {
			return "", fmt.Errorf("mcp_prompts_list: %w", err)
		}
		serverName := strings.TrimSpace(agent.ArgString(args, "server"))
		prompts := make([]externalMCPPrompt, 0)
		errorsOut := make([]externalMCPPromptError, 0)

		if serverName != "" {
			result, err := mgr.ListPrompts(ctx, serverName)
			if err != nil {
				return "", fmt.Errorf("mcp_prompts_list: %w", err)
			}
			prompts = appendPrompts(prompts, serverName, result)
		} else {
			states := mgr.ListServerStates()
			sort.SliceStable(states, func(i, j int) bool { return states[i].Name < states[j].Name })
			for _, state := range states {
				if state.State != mcppkg.ConnectionStateConnected || !state.Capabilities.Prompts {
					continue
				}
				result, err := mgr.ListPrompts(ctx, state.Name)
				if err != nil {
					errorsOut = append(errorsOut, externalMCPPromptError{Server: state.Name, Error: err.Error()})
					continue
				}
				prompts = appendPrompts(prompts, state.Name, result)
			}
		}

		out := map[string]any{
			"prompts": prompts,
			"count":   len(prompts),
		}
		if serverName != "" {
			out["server"] = serverName
		}
		if len(errorsOut) > 0 {
			out["errors"] = errorsOut
		}
		payload, _ := json.Marshal(out)
		return string(payload), nil
	}, MCPPromptsListDef)

	tools.RegisterWithDef("mcp_prompts_get", func(ctx context.Context, args map[string]any) (string, error) {
		mgr, err := resolveManager()
		if err != nil {
			return "", fmt.Errorf("mcp_prompts_get: %w", err)
		}
		serverName := strings.TrimSpace(agent.ArgString(args, "server"))
		if serverName == "" {
			return "", fmt.Errorf("mcp_prompts_get: server is required")
		}
		name := strings.TrimSpace(agent.ArgString(args, "name"))
		if name == "" {
			return "", fmt.Errorf("mcp_prompts_get: name is required")
		}
		promptArgs, err := parseMCPPromptArguments(args["arguments"])
		if err != nil {
			return "", fmt.Errorf("mcp_prompts_get: parse arguments: %w", err)
		}
		result, err := mgr.GetPrompt(ctx, serverName, name, promptArgs)
		if err != nil {
			return "", fmt.Errorf("mcp_prompts_get: %w", err)
		}
		payload, _ := json.Marshal(map[string]any{
			"server":    serverName,
			"name":      name,
			"arguments": promptArgs,
			"description": func() string {
				if result == nil {
					return ""
				}
				return strings.TrimSpace(result.Description)
			}(),
			"messages": func() []*sdkmcp.PromptMessage {
				if result == nil {
					return nil
				}
				return result.Messages
			}(),
			"count": func() int {
				if result == nil {
					return 0
				}
				return len(result.Messages)
			}(),
		})
		return string(payload), nil
	}, MCPPromptsGetDef)
}

func parseMCPPromptArguments(raw any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch value := raw.(type) {
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return nil, nil
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, err
		}
		return parseMCPPromptArguments(decoded)
	case map[string]string:
		out := make(map[string]string, len(value))
		for key, val := range value {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			out[key] = val
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	case map[string]any:
		out := make(map[string]string, len(value))
		for key, val := range value {
			key = strings.TrimSpace(key)
			if key == "" || val == nil {
				continue
			}
			switch v := val.(type) {
			case string:
				out[key] = v
			case fmt.Stringer:
				out[key] = v.String()
			case bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
				out[key] = fmt.Sprint(v)
			case json.Number:
				out[key] = v.String()
			default:
				encoded, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("argument %q: %w", key, err)
				}
				out[key] = string(encoded)
			}
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected object or JSON string, got %T", raw)
	}
}
