// Package toolbuiltin – ContextVM MCP-over-Nostr tools.
//
// Registers: contextvm_discover, contextvm_tools_list, contextvm_call, contextvm_resources_list,
// contextvm_resources_read, contextvm_prompts_list, contextvm_prompts_get, contextvm_raw
//
// ContextVM transports Model Context Protocol (MCP) messages over the Nostr relay network
// using kind 25910 ephemeral events. Clients discover servers via kind 11316 replaceable events.
// All MCP message content is stringified JSON embedded in the event content field.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/contextvm"
	nostruntime "metiq/internal/nostr/runtime"
)

// ContextVMToolOpts configures ContextVM tools.
type ContextVMToolOpts struct {
	HubFunc func() *nostruntime.NostrHub
	Keyer   nostr.Keyer
	Relays  []string
}

// ToolDefinitions for ContextVM tools.
var contextVMDiscoverDef = agent.ToolDefinition{
	Name:        "contextvm_discover",
	Description: "Discover ContextVM MCP servers on the Nostr network. Fetches kind:11316 server announcement events from relays. Returns a list of servers with their pubkeys and capabilities. Use contextvm_tools_list to see what tools a server offers.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"limit":  {Type: "number", Description: "Maximum number of servers to return (default 20)."},
			"relays": {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs to search. Defaults to configured relays."},
		},
	},
}

var contextVMToolsListDef = agent.ToolDefinition{
	Name:        "contextvm_tools_list",
	Description: "List MCP tools available on a ContextVM server. Sends a tools/list JSON-RPC request via kind:25910 event to the server and returns its tool definitions.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server_pubkey": {Type: "string", Description: "Hex pubkey of the ContextVM server."},
			"relays":        {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs. Defaults to configured relays."},
			"encryption":    {Type: "string", Description: "Optional encryption mode for request content: none|nip44|nip04|auto."},
		},
		Required: []string{"server_pubkey"},
	},
}

var contextVMCallDef = agent.ToolDefinition{
	Name:        "contextvm_call",
	Description: "Call an MCP tool on a ContextVM server over Nostr (kind:25910). Use contextvm_tools_list first to discover available tools and their parameter schemas.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server_pubkey":   {Type: "string", Description: "Hex pubkey of the ContextVM server."},
			"tool_name":       {Type: "string", Description: "Name of the MCP tool to call."},
			"arguments":       {Type: "string", Description: "JSON object string of tool arguments (e.g. '{\"prompt\":\"a cat\"}')."},
			"relays":          {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs. Defaults to configured relays."},
			"encryption":      {Type: "string", Description: "Optional encryption mode for request content: none|nip44|nip04|auto."},
			"timeout_seconds": {Type: "number", Description: "Deprecated compatibility field; ignored because completion is driven by the Nostr response event."},
		},
		Required: []string{"server_pubkey", "tool_name"},
	},
}

var contextVMResourcesListDef = agent.ToolDefinition{
	Name:        "contextvm_resources_list",
	Description: "List MCP resources available on a ContextVM server. Sends a resources/list request and returns structured resource metadata.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server_pubkey": {Type: "string", Description: "Hex pubkey of the ContextVM server."},
			"relays":        {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs. Defaults to configured relays."},
			"encryption":    {Type: "string", Description: "Optional encryption mode for request content: none|nip44|nip04|auto."},
		},
		Required: []string{"server_pubkey"},
	},
}

var contextVMResourcesReadDef = agent.ToolDefinition{
	Name:        "contextvm_resources_read",
	Description: "Read a specific MCP resource from a ContextVM server. Sends a resources/read request using the provided resource URI.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server_pubkey": {Type: "string", Description: "Hex pubkey of the ContextVM server."},
			"uri":           {Type: "string", Description: "Resource URI to read."},
			"relays":        {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs. Defaults to configured relays."},
			"encryption":    {Type: "string", Description: "Optional encryption mode for request content: none|nip44|nip04|auto."},
		},
		Required: []string{"server_pubkey", "uri"},
	},
}

var contextVMPromptsListDef = agent.ToolDefinition{
	Name:        "contextvm_prompts_list",
	Description: "List MCP prompts available on a ContextVM server. Sends a prompts/list request and returns structured prompt metadata.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server_pubkey": {Type: "string", Description: "Hex pubkey of the ContextVM server."},
			"relays":        {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs. Defaults to configured relays."},
			"encryption":    {Type: "string", Description: "Optional encryption mode for request content: none|nip44|nip04|auto."},
		},
		Required: []string{"server_pubkey"},
	},
}

var contextVMPromptsGetDef = agent.ToolDefinition{
	Name:        "contextvm_prompts_get",
	Description: "Fetch a named MCP prompt from a ContextVM server. Sends a prompts/get request and optionally passes prompt arguments as JSON.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server_pubkey": {Type: "string", Description: "Hex pubkey of the ContextVM server."},
			"name":          {Type: "string", Description: "Prompt name to fetch."},
			"arguments":     {Type: "string", Description: "Optional JSON object string of prompt arguments."},
			"relays":        {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs. Defaults to configured relays."},
			"encryption":    {Type: "string", Description: "Optional encryption mode for request content: none|nip44|nip04|auto."},
		},
		Required: []string{"server_pubkey", "name"},
	},
}

var contextVMRawDef = agent.ToolDefinition{
	Name:        "contextvm_raw",
	Description: "Send an arbitrary MCP JSON-RPC message to a ContextVM server over Nostr. Use for advanced operations not covered by the typed ContextVM tools.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server_pubkey": {Type: "string", Description: "Hex pubkey of the ContextVM server."},
			"message":       {Type: "string", Description: "Stringified JSON-RPC object (e.g. '{\"jsonrpc\":\"2.0\",\"method\":\"tools/list\",\"id\":1}')."},
			"relays":        {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs. Defaults to configured relays."},
			"encryption":    {Type: "string", Description: "Optional encryption mode for request content: none|nip44|nip04|auto."},
		},
		Required: []string{"server_pubkey", "message"},
	},
}

// RegisterContextVMTools registers ContextVM MCP-over-Nostr tools.
func RegisterContextVMTools(tools *agent.ToolRegistry, opts ContextVMToolOpts) {
	var (
		fallbackPool *nostr.Pool
		poolOnce     sync.Once
	)
	getPool := func() *nostr.Pool {
		if opts.HubFunc != nil {
			if h := opts.HubFunc(); h != nil {
				return h.Pool()
			}
		}
		poolOnce.Do(func() {
			fallbackPool = nostr.NewPool(nostruntime.PoolOptsNIP42(opts.Keyer))
		})
		return fallbackPool
	}

	resolveKeyer := func(ctx context.Context) (nostr.Keyer, error) {
		if opts.HubFunc != nil {
			if h := opts.HubFunc(); h != nil {
				return h.Keyer(), nil
			}
		}
		if opts.Keyer == nil {
			return nil, fmt.Errorf("no signing keyer configured")
		}
		return opts.Keyer, nil
	}

	// contextvm_discover: find ContextVM MCP servers (kind 11316).
	tools.RegisterWithDef("contextvm_discover", func(ctx context.Context, args map[string]any) (string, error) {
		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		servers, err := contextvm.DiscoverServers(ctx, getPool(), relays, limit)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"servers": servers,
			"count":   len(servers),
			"note":    "Use contextvm_tools_list with a server pubkey to see available tools.",
		})
		return string(out), nil
	}, contextVMDiscoverDef)

	// contextvm_tools_list: send a tools/list MCP request to a ContextVM server (kind 25910).
	tools.RegisterWithDef("contextvm_tools_list", func(ctx context.Context, args map[string]any) (string, error) {
		serverPubKey, _ := args["server_pubkey"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if serverPubKey == "" {
			return "", fmt.Errorf("contextvm_tools_list: server_pubkey is required")
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("contextvm_tools_list: %w", err)
		}

		encryption := strings.TrimSpace(argString(args, "encryption"))
		tools2, err := contextvm.ListTools(ctx, getPool(), ks, relays, serverPubKey, encryption)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"server_pubkey": serverPubKey,
			"tools":         tools2,
			"count":         len(tools2),
		})
		return string(out), nil
	}, contextVMToolsListDef)

	// contextvm_call: call an MCP tool on a ContextVM server (kind 25910).
	tools.RegisterWithDef("contextvm_call", func(ctx context.Context, args map[string]any) (string, error) {
		serverPubKey, _ := args["server_pubkey"].(string)
		toolName, _ := args["tool_name"].(string)
		argsStr, _ := args["arguments"].(string) // JSON object string
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if serverPubKey == "" {
			return "", fmt.Errorf("contextvm_call: server_pubkey is required")
		}
		if toolName == "" {
			return "", fmt.Errorf("contextvm_call: tool_name is required")
		}

		var toolArgs map[string]any
		if argsStr != "" {
			if err := json.Unmarshal([]byte(argsStr), &toolArgs); err != nil {
				return "", fmt.Errorf("contextvm_call: parse arguments JSON: %w", err)
			}
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("contextvm_call: %w", err)
		}

		encryption := strings.TrimSpace(argString(args, "encryption"))
		result, err := contextvm.CallTool(ctx, getPool(), ks, relays, serverPubKey, toolName, toolArgs, encryption)
		if err != nil {
			return "", err
		}

		// Flatten the content array into a readable string where possible.
		var textParts []string
		for _, c := range result.Content {
			if c["type"] == "text" {
				if t, ok := c["text"].(string); ok {
					textParts = append(textParts, t)
				}
			}
		}
		if len(textParts) > 0 && !result.IsError {
			return strings.Join(textParts, "\n"), nil
		}

		out, _ := json.Marshal(result)
		return string(out), nil
	}, contextVMCallDef)

	tools.RegisterWithDef("contextvm_resources_list", func(ctx context.Context, args map[string]any) (string, error) {
		serverPubKey, _ := args["server_pubkey"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}
		if serverPubKey == "" {
			return "", fmt.Errorf("contextvm_resources_list: server_pubkey is required")
		}
		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("contextvm_resources_list: %w", err)
		}
		encryption := strings.TrimSpace(argString(args, "encryption"))
		resources, err := contextvm.ListResources(ctx, getPool(), ks, relays, serverPubKey, encryption)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"server_pubkey": serverPubKey, "resources": resources, "count": len(resources)})
		return string(out), nil
	}, contextVMResourcesListDef)

	tools.RegisterWithDef("contextvm_resources_read", func(ctx context.Context, args map[string]any) (string, error) {
		serverPubKey, _ := args["server_pubkey"].(string)
		uri, _ := args["uri"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}
		if serverPubKey == "" {
			return "", fmt.Errorf("contextvm_resources_read: server_pubkey is required")
		}
		uri = strings.TrimSpace(uri)
		if uri == "" {
			return "", fmt.Errorf("contextvm_resources_read: uri is required")
		}
		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("contextvm_resources_read: %w", err)
		}
		encryption := strings.TrimSpace(argString(args, "encryption"))
		result, err := contextvm.ReadResource(ctx, getPool(), ks, relays, serverPubKey, uri, encryption)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	}, contextVMResourcesReadDef)

	tools.RegisterWithDef("contextvm_prompts_list", func(ctx context.Context, args map[string]any) (string, error) {
		serverPubKey, _ := args["server_pubkey"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}
		if serverPubKey == "" {
			return "", fmt.Errorf("contextvm_prompts_list: server_pubkey is required")
		}
		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("contextvm_prompts_list: %w", err)
		}
		encryption := strings.TrimSpace(argString(args, "encryption"))
		prompts, err := contextvm.ListPrompts(ctx, getPool(), ks, relays, serverPubKey, encryption)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"server_pubkey": serverPubKey, "prompts": prompts, "count": len(prompts)})
		return string(out), nil
	}, contextVMPromptsListDef)

	tools.RegisterWithDef("contextvm_prompts_get", func(ctx context.Context, args map[string]any) (string, error) {
		serverPubKey, _ := args["server_pubkey"].(string)
		name, _ := args["name"].(string)
		argsStr, _ := args["arguments"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}
		if serverPubKey == "" {
			return "", fmt.Errorf("contextvm_prompts_get: server_pubkey is required")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return "", fmt.Errorf("contextvm_prompts_get: name is required")
		}
		var promptArgs map[string]any
		if strings.TrimSpace(argsStr) != "" {
			if err := json.Unmarshal([]byte(argsStr), &promptArgs); err != nil {
				return "", fmt.Errorf("contextvm_prompts_get: parse arguments JSON: %w", err)
			}
		}
		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("contextvm_prompts_get: %w", err)
		}
		encryption := strings.TrimSpace(argString(args, "encryption"))
		result, err := contextvm.GetPrompt(ctx, getPool(), ks, relays, serverPubKey, name, promptArgs, encryption)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	}, contextVMPromptsGetDef)

	// contextvm_raw: send an arbitrary MCP JSON-RPC message to a ContextVM server.
	tools.RegisterWithDef("contextvm_raw", func(ctx context.Context, args map[string]any) (string, error) {
		serverPubKey, _ := args["server_pubkey"].(string)
		messageStr, _ := args["message"].(string) // stringified JSON-RPC object
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if serverPubKey == "" {
			return "", fmt.Errorf("contextvm_raw: server_pubkey is required")
		}
		if messageStr == "" {
			return "", fmt.Errorf("contextvm_raw: message (JSON-RPC object) is required")
		}

		var msg map[string]any
		if err := json.Unmarshal([]byte(messageStr), &msg); err != nil {
			return "", fmt.Errorf("contextvm_raw: parse message: %w", err)
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("contextvm_raw: %w", err)
		}

		encryption := strings.TrimSpace(argString(args, "encryption"))
		respRaw, err := contextvm.SendRaw(ctx, getPool(), ks, relays, serverPubKey, msg, encryption)
		if err != nil {
			return "", err
		}
		return string(respRaw), nil
	}, contextVMRawDef)
}
