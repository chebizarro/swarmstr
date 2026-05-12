// Package inference provides backend-specific inference clients with optimizations
// for prompt caching and resource management.
//
// The package currently supports:
//   - llama.cpp with slot affinity for KV cache reuse
//   - Anthropic API with proper request formatting
//
// # Slot Affinity
//
// The SlotID function deterministically assigns each agent to a KV cache slot
// on the llama.cpp server based on its stable identifier (e.g., Nostr pubkey).
// This ensures that an agent's conversation context is reused across turns,
// dramatically reducing time-to-first-token for multi-turn conversations.
//
// # Configuration
//
// The SlotCount variable must match the --parallel value passed to the llama.cpp
// server. Update this value in your config before starting the agent:
//
//	inference.SlotCount = 12 // matches llama.cpp --parallel 12
//
// # Usage
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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"

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

// ChunkCallback receives provider text deltas synchronously as they arrive.
// The callback is invoked from the response-reading goroutine; slow callbacks
// intentionally apply backpressure to the HTTP stream. Returning an error stops
// the stream and returns that error to the caller.
type ChunkCallback func(chunk []byte) error

// ─── Request Types ────────────────────────────────────────────────────────────

// LlamaRequest is the request format for llama.cpp /completion endpoint.
// IDSlot and CachePrompt are llama.cpp extensions — never send to Anthropic API.
type LlamaRequest struct {
	Model       string             `json:"model"`
	Messages    []agent.LLMMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Stream      bool               `json:"stream"`
	IDSlot      int                `json:"id_slot"`      // llama.cpp extension — never send to Anthropic
	CachePrompt bool               `json:"cache_prompt"` // llama.cpp extension — never send to Anthropic
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
	_, _ = h.Write([]byte(agentID))
	slot := int(h.Sum32()) % SlotCount
	return slot
}

// ─── Request Builders ─────────────────────────────────────────────────────────

// BuildLlamaRequest constructs a streaming llama.cpp request with slot affinity.
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

// BuildAnthropicRequest constructs a streaming Anthropic API request.
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

// Complete sends a streaming completion request to the appropriate backend and
// returns the concatenated text chunks. Use CompleteStream to receive chunks
// incrementally as they arrive.
//
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
	var result bytes.Buffer
	err := c.CompleteStream(ctx, backend, messages, agentID, model, maxTokens, func(chunk []byte) error {
		_, writeErr := result.Write(chunk)
		return writeErr
	})
	if err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

// CompleteStream sends a streaming completion request and invokes onChunk for
// each provider text delta as it arrives. onChunk is synchronous: if it blocks,
// response reading pauses, which gives callers natural backpressure. Cancelling
// ctx aborts the HTTP request and stops further callbacks.
func (c *Client) CompleteStream(ctx context.Context, backend Backend, messages []agent.LLMMessage, agentID, model string, maxTokens int, onChunk ChunkCallback) error {
	if onChunk == nil {
		return errors.New("onChunk callback is required")
	}

	switch backend {
	case BackendLlama:
		req := BuildLlamaRequest(messages, agentID, model, maxTokens)

		// Validate slot assignment.
		if req.IDSlot != DynamicSlot && (req.IDSlot < 0 || req.IDSlot >= SlotCount) {
			return fmt.Errorf("invalid slot assignment: %d (valid range: 0-%d or %d for dynamic)", req.IDSlot, SlotCount-1, DynamicSlot)
		}

		return c.postStreamingJSON(ctx, c.LlamaURL+"/completion", req, backend, onChunk)

	case BackendAnthropic:
		req := BuildAnthropicRequest(messages, model, maxTokens)
		return c.postStreamingJSON(ctx, "https://api.anthropic.com/v1/messages", req, backend, onChunk)

	default:
		return fmt.Errorf("unknown backend: %d", backend)
	}
}

// postStreamingJSON marshals the request body to JSON, posts it to the given
// URL, and parses the response as an SSE stream incrementally.
func (c *Client) postStreamingJSON(ctx context.Context, url string, body interface{}, backend Backend, onChunk ChunkCallback) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.AnthropicAPIKey != "" {
		req.Header.Set("X-API-Key", c.AnthropicAPIKey)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(readLimitedString(resp.Body, 4096)))
	}

	sawChunk := false
	if err := parseSSE(ctx, resp.Body, func(event sseEvent) error {
		chunks, err := extractChunks(backend, event)
		if err != nil {
			return err
		}
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			sawChunk = true
			if err := onChunk(chunk); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, errSSEDone) {
			if !sawChunk {
				return errors.New("stream completed without any chunks")
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	if !sawChunk {
		return errors.New("stream completed without any chunks")
	}

	return nil
}

// ─── SSE Parsing ──────────────────────────────────────────────────────────────

var errSSEDone = errors.New("SSE stream done")

type sseEvent struct {
	Event string
	Data  string
}

func parseSSE(ctx context.Context, r io.Reader, onEvent func(sseEvent) error) error {
	reader := bufio.NewReader(r)
	var eventType string
	var dataLines []string

	dispatch := func() error {
		if len(dataLines) == 0 {
			eventType = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		event := sseEvent{Event: eventType, Data: data}
		eventType = ""

		if strings.TrimSpace(event.Data) == "[DONE]" {
			return errSSEDone
		}
		return onEvent(event)
	}

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			if errors.Is(err, io.EOF) {
				return dispatch()
			}
			return fmt.Errorf("read SSE stream: %w", err)
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
		} else if strings.HasPrefix(line, ":") {
			// SSE comment/heartbeat line.
		} else if field, value, ok := strings.Cut(line, ":"); ok {
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				eventType = value
			case "data":
				dataLines = append(dataLines, value)
			}
		} else if line == "data" {
			dataLines = append(dataLines, "")
		}

		if errors.Is(err, io.EOF) {
			return dispatch()
		}
	}
}

func extractChunks(backend Backend, event sseEvent) ([][]byte, error) {
	data := strings.TrimSpace(event.Data)
	if data == "" || data == "[DONE]" {
		return nil, nil
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		if event.Event == "error" {
			return nil, fmt.Errorf("stream error: %s", data)
		}
		return nil, fmt.Errorf("decode SSE data: %w", err)
	}
	if err := streamPayloadError(event, payload); err != nil {
		return nil, err
	}

	switch backend {
	case BackendLlama:
		return extractLlamaChunks(payload), nil
	case BackendAnthropic:
		return extractAnthropicChunks(payload), nil
	default:
		return nil, fmt.Errorf("unknown backend: %d", backend)
	}
}

func streamPayloadError(event sseEvent, payload map[string]any) error {
	if event.Event == "error" || payload["type"] == "error" || payload["error"] != nil {
		return fmt.Errorf("stream error: %s", formatStreamError(payload))
	}
	return nil
}

func formatStreamError(payload map[string]any) string {
	if errPayload, ok := payload["error"].(map[string]any); ok {
		if msg, ok := errPayload["message"].(string); ok && msg != "" {
			if typ, ok := errPayload["type"].(string); ok && typ != "" {
				return typ + ": " + msg
			}
			return msg
		}
	}
	if msg, ok := payload["message"].(string); ok && msg != "" {
		return msg
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "unknown stream error"
	}
	return string(data)
}

func extractLlamaChunks(payload map[string]any) [][]byte {
	var chunks [][]byte

	// llama.cpp /completion streams typically send {"content":"..."}.
	appendString(&chunks, payload["content"])
	appendString(&chunks, payload["response"])

	// OpenAI-compatible endpoints may send choices with delta.content or text.
	appendChoiceChunks(&chunks, payload)

	return chunks
}

func extractAnthropicChunks(payload map[string]any) [][]byte {
	if typ, _ := payload["type"].(string); typ != "" && typ != "content_block_delta" {
		return nil
	}

	var chunks [][]byte
	if delta, ok := payload["delta"].(map[string]any); ok {
		appendString(&chunks, delta["text"])
	}
	appendChoiceChunks(&chunks, payload)
	return chunks
}

func appendChoiceChunks(chunks *[][]byte, payload map[string]any) {
	choices, ok := payload["choices"].([]any)
	if !ok {
		return
	}
	for _, choice := range choices {
		choiceMap, ok := choice.(map[string]any)
		if !ok {
			continue
		}
		if delta, ok := choiceMap["delta"].(map[string]any); ok {
			appendString(chunks, delta["content"])
		}
		appendString(chunks, choiceMap["text"])
	}
}

func appendString(chunks *[][]byte, value any) {
	text, ok := value.(string)
	if !ok || text == "" {
		return
	}
	*chunks = append(*chunks, []byte(text))
}

func readLimitedString(r io.Reader, limit int64) string {
	var b strings.Builder
	_, _ = io.Copy(&b, io.LimitReader(r, limit))
	return b.String()
}
