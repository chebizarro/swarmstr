// Package toolbuiltin fleet.go — inter-agent fleet communication tools.
//
// fleet_agents   — list known Cascadia fleet agents from the NIP-51 directory.
// nostr_agent_rpc — send a DM to a fleet agent and wait for its reply.
//
// The fleet directory is populated by the NIP-51 allowlist watcher in main
// and passed in via FleetDirectoryFunc.  The RPC correlator is injected via
// RPCWaiterFunc so the tool can suspend until a reply arrives or times out.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"swarmstr/internal/agent"
	nostruntime "swarmstr/internal/nostr/runtime"
)

// rpcResultCache prevents the model from looping by caching recent RPC outcomes.
// If the same agent was already tried (and timed out) within cacheTTL seconds,
// the tool returns the cached result immediately instead of waiting again.
var (
	rpcCacheMu  sync.Mutex
	rpcCache    = map[string]rpcCacheEntry{}
	rpcCacheTTL = 60 * time.Second
)

type rpcCacheEntry struct {
	result    string
	isTimeout bool
	at        time.Time
}

func rpcCacheLookup(agentHex string) (result string, found bool) {
	rpcCacheMu.Lock()
	defer rpcCacheMu.Unlock()
	e, ok := rpcCache[agentHex]
	if !ok || time.Since(e.at) > rpcCacheTTL {
		return "", false
	}
	return e.result, true
}

func rpcCacheStore(agentHex, result string, isTimeout bool) {
	rpcCacheMu.Lock()
	defer rpcCacheMu.Unlock()
	rpcCache[agentHex] = rpcCacheEntry{result: result, isTimeout: isTimeout, at: time.Now()}
}

// ─── Fleet directory ──────────────────────────────────────────────────────────

// FleetEntry describes a known fleet agent.
type FleetEntry struct {
	Pubkey string `json:"pubkey"`
	Name   string `json:"name,omitempty"`
	Relay  string `json:"relay,omitempty"`
}

// FleetDirectoryFunc returns the current set of known fleet agents.
// It is called on every tool invocation so it always reflects the live NIP-51 state.
type FleetDirectoryFunc func() []FleetEntry

// ─── RPC correlator interface ─────────────────────────────────────────────────

// RPCWaiter registers a pending reply expectation for a given sender pubkey and
// returns a channel that receives the reply text (or is closed on timeout/cancel).
// The caller MUST call the returned cancel func to release the registration.
type RPCWaiter func(fromPubkeyHex string) (replyCh <-chan string, cancel func())

// ─── fleet_agents tool ────────────────────────────────────────────────────────

var FleetAgentsDef = agent.ToolDefinition{
	Name:        "fleet_agents",
	Description: "List the known Cascadia fleet agents loaded from the NIP-51 agent directory. Returns each agent's pubkey, display name, and preferred relay.",
	Parameters: agent.ToolParameters{
		Type:       "object",
		Properties: map[string]agent.ToolParamProp{},
	},
}

// FleetAgentsTool returns a tool that lists all known fleet agents.
func FleetAgentsTool(getAgents FleetDirectoryFunc) agent.ToolFunc {
	return func(_ context.Context, _ map[string]any) (string, error) {
		agents := getAgents()
		if len(agents) == 0 {
			// Directory not yet populated — NIP-51 list fetch is still in progress.
			// Return an explicit error so the model doesn't loop trying other approaches.
			return "", fmt.Errorf("fleet directory not ready yet — NIP-51 agent list is still loading from relay. Wait a few seconds and try again.")
		}
		out, _ := json.Marshal(map[string]any{
			"agents": agents,
			"count":  len(agents),
		})
		return string(out), nil
	}
}

// ─── nostr_agent_rpc tool ─────────────────────────────────────────────────────

var NostrAgentRPCDef = agent.ToolDefinition{
	Name: "nostr_agent_rpc",
	Description: "Send a message to a specific fleet agent and wait for their reply. " +
		"Use this ONLY when you need to send a message and get a response — NOT to check who exists (use fleet_agents for that), NOT to check if agents are online. " +
		"Call this at most ONCE per agent per turn. If it times out, report that the agent is unreachable and stop — do not retry.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"to": {
				Type:        "string",
				Description: "Target agent: display name (e.g. \"Stew\"), npub, or hex pubkey.",
			},
			"message": {
				Type:        "string",
				Description: "Message to send to the agent.",
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Seconds to wait for a reply (default 60, max 300).",
			},
		},
		Required: []string{"to", "message"},
	},
}

// NostrAgentRPCTool returns a tool that sends a DM to a fleet agent and
// synchronously waits for the reply.
//
// Parameters:
//   - opts       — Nostr credentials + relays (must have DMTransport set)
//   - getAgents  — fleet directory lookup
//   - waitReply  — RPC correlator from main; suspends the call until reply arrives
func NostrAgentRPCTool(opts NostrToolOpts, getAgents FleetDirectoryFunc, waitReply RPCWaiter) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if opts.DMTransport == nil {
			return "", fmt.Errorf("nostr_agent_rpc: DM transport not available")
		}
		if waitReply == nil {
			return "", fmt.Errorf("nostr_agent_rpc: RPC correlator not available")
		}

		toRaw, _ := args["to"].(string)
		toRaw = strings.TrimSpace(toRaw)
		if toRaw == "" {
			return "", fmt.Errorf("nostr_agent_rpc: 'to' is required")
		}
		msgText, _ := args["message"].(string)
		msgText = strings.TrimSpace(msgText)
		if msgText == "" {
			return "", fmt.Errorf("nostr_agent_rpc: 'message' is required")
		}
		timeoutSec := 10
		if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
			timeoutSec = int(v)
			if timeoutSec > 60 {
				timeoutSec = 60
			}
		}

		// Resolve target to hex pubkey.
		toPubkeyHex, resolvedName, err := resolveFleetTarget(toRaw, getAgents)
		if err != nil {
			return "", fmt.Errorf("nostr_agent_rpc: %w", err)
		}

		// Cache check: if this agent already timed out recently, return immediately.
		// This prevents the model from looping by retrying the same agent.
		if cached, found := rpcCacheLookup(toPubkeyHex); found {
			return cached, nil
		}

		// Register reply waiter BEFORE sending to avoid missing a fast reply.
		replyCh, cancelWait := waitReply(toPubkeyHex)
		defer cancelWait()

		// Send the DM.
		sendCtx, sendCancel := context.WithTimeout(ctx, 15*time.Second)
		defer sendCancel()
		if err := opts.DMTransport.SendDM(sendCtx, toPubkeyHex, msgText); err != nil {
			result := fmt.Sprintf(`{"status":"send_failed","agent":%q,"error":%q,"action_required":"STOP — report this failure to the user and do not call any more tools."}`,
				resolvedName, err.Error())
			rpcCacheStore(toPubkeyHex, result, true)
			return result, nil
		}

		// Wait for reply.
		deadline := time.Duration(timeoutSec) * time.Second
		select {
		case reply, ok := <-replyCh:
			if !ok {
				result := fmt.Sprintf(`{"status":"disconnected","agent":%q,"action_required":"STOP — report this failure to the user and do not call any more tools."}`, resolvedName)
				rpcCacheStore(toPubkeyHex, result, true)
				return result, nil
			}
			out, _ := json.Marshal(map[string]any{
				"status": "ok",
				"from":   resolvedName,
				"pubkey": toPubkeyHex,
				"reply":  reply,
			})
			result := string(out)
			rpcCacheStore(toPubkeyHex, result, false)
			return result, nil

		case <-time.After(deadline):
			result := fmt.Sprintf(`{"status":"timeout","agent":%q,"action_required":"STOP — %s did not reply within %ds. They are offline or busy. Report this to the user immediately and do not call nostr_agent_rpc again this turn."}`,
				resolvedName, resolvedName, timeoutSec)
			rpcCacheStore(toPubkeyHex, result, true)
			return result, nil

		case <-ctx.Done():
			result := fmt.Sprintf(`{"status":"cancelled","agent":%q,"action_required":"STOP — context cancelled."}`, resolvedName)
			return result, nil
		}
	}
}

// resolveFleetTarget turns a name, npub, or hex pubkey into a hex pubkey.
// Name lookup is case-insensitive against the fleet directory.
func resolveFleetTarget(raw string, getAgents FleetDirectoryFunc) (hexPubkey, displayName string, err error) {
	// Try direct pubkey parse first.
	pk, pkErr := nostruntime.ParsePubKey(raw)
	if pkErr == nil {
		hexPubkey = pk.Hex()
		// Try to find a name for it in the directory.
		if getAgents != nil {
			for _, a := range getAgents() {
				if strings.EqualFold(a.Pubkey, hexPubkey) && a.Name != "" {
					displayName = a.Name
					break
				}
			}
		}
		if displayName == "" {
			displayName = hexPubkey[:12] + "..."
		}
		return hexPubkey, displayName, nil
	}

	// Try name lookup in fleet directory.
	if getAgents != nil {
		lowerRaw := strings.ToLower(strings.TrimSpace(raw))
		for _, a := range getAgents() {
			if strings.ToLower(a.Name) == lowerRaw {
				pk2, err2 := nostruntime.ParsePubKey(a.Pubkey)
				if err2 != nil {
					return "", "", fmt.Errorf("fleet agent %q has invalid pubkey: %w", a.Name, err2)
				}
				return pk2.Hex(), a.Name, nil
			}
		}
	}

	return "", "", fmt.Errorf("could not resolve agent %q — provide a name, npub, or hex pubkey", raw)
}
