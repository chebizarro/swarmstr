// Package toolbuiltin – ContextVM MCP-over-Nostr tools.
//
// Registers: contextvm_discover, contextvm_tools_list, contextvm_call, contextvm_raw
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
	"time"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
	"swarmstr/internal/contextvm"
)

// ContextVMToolOpts configures ContextVM tools.
type ContextVMToolOpts struct {
	Keyer      nostr.Keyer
	Relays     []string
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
			"server_pubkey": {Type: "string", Description: "Hex pubkey of the ContextVM server."},
			"tool_name":     {Type: "string", Description: "Name of the MCP tool to call."},
			"arguments":     {Type: "string", Description: "JSON object string of tool arguments (e.g. '{\"prompt\":\"a cat\"}')."},
			"relays":        {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Relay URLs. Defaults to configured relays."},
			"encryption":    {Type: "string", Description: "Optional encryption mode for request content: none|nip44|nip04|auto."},
			"timeout_seconds": {Type: "number", Description: "Optional response timeout in seconds (default 60)."},
		},
		Required: []string{"server_pubkey", "tool_name"},
	},
}

var contextVMRawDef = agent.ToolDefinition{
	Name:        "contextvm_raw",
	Description: "Send an arbitrary MCP JSON-RPC message to a ContextVM server over Nostr. Use for advanced operations not covered by contextvm_call (e.g. resources/list, prompts/get).",
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
	pool := nostr.NewPool(NostrToolOpts{Keyer: opts.Keyer}.PoolOptsNIP42())

	resolveKeyer := func(ctx context.Context) (nostr.Keyer, error) {
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

		servers, err := contextvm.DiscoverServers(ctx, pool, relays, limit)
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
		tools2, err := contextvm.ListTools(ctx, pool, ks, relays, serverPubKey, encryption)
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
		timeoutSec := 60
		if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
			timeoutSec = int(v)
		}
		if timeoutSec > 600 {
			timeoutSec = 600
		}
		result, err := contextvm.CallToolWithTimeout(ctx, pool, ks, relays, serverPubKey, toolName, toolArgs, time.Duration(timeoutSec)*time.Second, encryption)
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
		respRaw, err := contextvm.SendRaw(ctx, pool, ks, relays, serverPubKey, msg, encryption)
		if err != nil {
			return "", err
		}
		return string(respRaw), nil
	}, contextVMRawDef)
}
