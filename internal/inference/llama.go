// Package inference provides backend-specific inference clients with optimizations
// for prompt caching and resource management.
//
// The package currently supports:
//   - llama.cpp with slot affinity for KV cache reuse
//   - Anthropic API with proper request formatting
//
// Slot Affinity
//
// The SlotID function deterministically assigns each agent to a KV cache slot
// on the llama.cpp server based on its stable identifier (e.g., Nostr pubkey).
// This ensures that an agent's conversation context is reused across turns,
// dramatically reducing time-to-first-token for multi-turn conversations.
//
// Configuration
//
// The SlotCount variable must match the --parallel value passed to the llama.cpp
// server. Update this value in your config before starting the agent:
//
//	inference.SlotCount = 12 // matches llama.cpp --parallel 12
//
// Usage
//
// Long-running agents should pass their stable ID to enable slot affinity:
//
//	client := &inference.Client{LlamaURL: "http://localhost:8080"}
//	resp, err := client.Complete(ctx, inference.BackendLlama, messages, agentPubkey, model, maxTokens)
//
// Short-lived agents or overflow requests should pass an empty agentID for
// dynamic slot assignment:
//
//	resp, err := client.Complete(ctx, inference.BackendLlama, messages, "", model, maxTokens)
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"

	"metiq/internal/agent"
)

// ─── Constants ────────────────────────────────────────────────────────────────

// Backend identifies the inference backend for routing.
type Backend int

const (
	BackendLlama Backend = iota
	BackendAnthropic
)

// DynamicSlot signals the server to assign any idle slot.
// Use this for short-lived agents or overflow requests.
const DynamicSlot = -1

// SlotCount is the number of KV cache slots available on the llama.cpp server.
// MUST match the --parallel value passed to the llama.cpp server.
// This is a package-level variable loaded from config, not a hardcoded constant,
// so it can change when --parallel is adjusted without recompiling.
var SlotCount = 6

// ─── Request Types ────────────────────────────────────────────────────────────

// LlamaRequest is the request format for llama.cpp /completion endpoint.
// IDSlot and CachePrompt are llama.cpp extensions — never send to Anthropic API.
type LlamaRequest struct {
	Model       string               `json:"model"`
	Messages    []agent.LLMMessage   `json:"messages"`
	MaxTokens   int                  `json:"max_tokens"`
	Stream      bool                 `json:"stream"`
	IDSlot      int                  `json:"id_slot"`      // llama.cpp extension — never send to Anthropic
	CachePrompt bool                 `json:"cache_prompt"` // llama.cpp extension — never send to Anthropic
}

// AnthropicRequest is the request format for Anthropic API /v1/messages endpoint.
// Must never contain id_slot or cache_prompt — these will cause errors.
type AnthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []agent.LLMMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
}

// ─── Slot Assignment ──────────────────────────────────────────────────────────

// SlotID deterministically assigns a slot based on agent identity.
// Uses FNV-32a hash — stable across processes and Go versions unlike map hash.
// agentID should be the agent's Nostr pubkey or stable internal ID.
//
// Returns a value in the range [0, SlotCount-1], or DynamicSlot (-1) if agentID is empty.
//
// For long-running agents, pass their stable ID to enable KV cache reuse across turns.
// For short-lived agents or overflow requests, pass an empty agentID to get dynamic assignment.
func SlotID(agentID string) int {
	if agentID == "" {
		return DynamicSlot
	}
	h := fnv.New32a()
	h.Write([]byte(agentID))
	slot := int(h.Sum32()) % SlotCount
	return slot
}

// ─── Request Builders ─────────────────────────────────────────────────────────

// BuildLlamaRequest constructs a llama.cpp request with slot affinity.
// cache_prompt must be true — without it the slot is used but cache is not reused.
//
// If agentID is empty, IDSlot is set to DynamicSlot (-1) for dynamic assignment.
// Otherwise, IDSlot is deterministically assigned based on agentID.
//
// IMPORTANT: The SlotCount config value must match the --parallel value on the server.
func BuildLlamaRequest(messages []agent.LLMMessage, agentID, model string, maxTokens int) LlamaRequest {
	return LlamaRequest{
		Model:       model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Stream:      true,
		IDSlot:      SlotID(agentID),
		CachePrompt: true,
	}
}

// BuildAnthropicRequest constructs an Anthropic API request.
// Never includes id_slot or cache_prompt fields.
func BuildAnthropicRequest(messages []agent.LLMMessage, model string, maxTokens int) AnthropicRequest {
	return AnthropicRequest{
		Model:     model,
		Messages:  messages,
		MaxTokens: maxTokens,
		Stream:    true,
	}
}

// ─── Client Interface ─────────────────────────────────────────────────────────

// Client provides inference with backend-specific routing.
type Client struct {
	// LlamaURL is the base URL for the llama.cpp server (e.g., http://localhost:8080).
	LlamaURL string
	// AnthropicAPIKey is the API key for Anthropic API calls.
	AnthropicAPIKey string
	// HTTPClient is the HTTP client to use for requests. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// Complete sends a completion request to the appropriate backend.
// Routes to BackendLlama or BackendAnthropic based on the backend parameter.
//
// For BackendLlama:
//   - Posts to {LlamaURL}/completion with LlamaRequest (includes id_slot, cache_prompt)
//   - agentID enables slot affinity for long-running agents
//   - Pass empty agentID for dynamic slot assignment
//
// For BackendAnthropic:
//   - Posts to https://api.anthropic.com/v1/messages with AnthropicRequest
//   - id_slot and cache_prompt are never sent
//
// Validates IDSlot is in range [0, SlotCount-1] or exactly -1 before sending.
func (c *Client) Complete(ctx context.Context, backend Backend, messages []agent.LLMMessage, agentID, model string, maxTokens int) ([]byte, error) {
	switch backend {
	case BackendLlama:
		req := BuildLlamaRequest(messages, agentID, model, maxTokens)
		
		// Validate slot assignment.
		if req.IDSlot != DynamicSlot && (req.IDSlot < 0 || req.IDSlot >= SlotCount) {
			return nil, fmt.Errorf("invalid slot assignment: %d (valid range: 0-%d or %d for dynamic)", req.IDSlot, SlotCount-1, DynamicSlot)
		}
		
		return c.postJSON(ctx, c.LlamaURL+"/completion", req)
		
	case BackendAnthropic:
		req := BuildAnthropicRequest(messages, model, maxTokens)
		return c.postJSON(ctx, "https://api.anthropic.com/v1/messages", req)
		
	default:
		return nil, fmt.Errorf("unknown backend: %d", backend)
	}
}

// postJSON marshals the request body to JSON and posts it to the given URL.
func (c *Client) postJSON(ctx context.Context, url string, body interface{}) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	if c.AnthropicAPIKey != "" {
		req.Header.Set("X-API-Key", c.AnthropicAPIKey)
	}
	
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
	
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	
	return result, nil
}
