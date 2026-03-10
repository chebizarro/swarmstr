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

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
	"swarmstr/internal/contextvm"
)

// ContextVMToolOpts configures ContextVM tools.
type ContextVMToolOpts struct {
	Keyer      nostr.Keyer
	Relays     []string
}

// RegisterContextVMTools registers ContextVM MCP-over-Nostr tools.
func RegisterContextVMTools(tools *agent.ToolRegistry, opts ContextVMToolOpts) {
	pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})

	resolveKeyer := func(ctx context.Context) (nostr.Keyer, error) {
		if opts.Keyer == nil {
			return nil, fmt.Errorf("no signing keyer configured")
		}
		return opts.Keyer, nil
	}

	// contextvm_discover: find ContextVM MCP servers (kind 11316).
	tools.Register("contextvm_discover", func(ctx context.Context, args map[string]any) (string, error) {
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
	})

	// contextvm_tools_list: send a tools/list MCP request to a ContextVM server (kind 25910).
	tools.Register("contextvm_tools_list", func(ctx context.Context, args map[string]any) (string, error) {
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

		tools2, err := contextvm.ListTools(ctx, pool, ks, relays, serverPubKey)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"server_pubkey": serverPubKey,
			"tools":         tools2,
			"count":         len(tools2),
		})
		return string(out), nil
	})

	// contextvm_call: call an MCP tool on a ContextVM server (kind 25910).
	tools.Register("contextvm_call", func(ctx context.Context, args map[string]any) (string, error) {
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

		result, err := contextvm.CallTool(ctx, pool, ks, relays, serverPubKey, toolName, toolArgs)
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
	})

	// contextvm_raw: send an arbitrary MCP JSON-RPC message to a ContextVM server.
	tools.Register("contextvm_raw", func(ctx context.Context, args map[string]any) (string, error) {
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

		respRaw, err := contextvm.SendRaw(ctx, pool, ks, relays, serverPubKey, msg)
		if err != nil {
			return "", err
		}
		return string(respRaw), nil
	})
}
