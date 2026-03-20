package agent

import (
	"context"
	"strings"
)

// ─── Unified LLM types ───────────────────────────────────────────────────────
//
// These types allow the agentic tool loop to work with any LLM provider.
// Each provider converts to/from its native format inside Chat().

// LLMMessage is a provider-agnostic message for multi-turn LLM conversations.
type LLMMessage struct {
	Role       string     `json:"role"`                  // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`               // text content
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // for assistant messages requesting tool use
	ToolCallID string     `json:"tool_call_id,omitempty"` // for tool result messages
	Images     []ImageRef `json:"images,omitempty"`      // for user messages with images

	// SystemParts splits the system prompt into structured blocks for cache_control.
	// Only meaningful when Role == "system". Providers that support prompt caching
	// (Anthropic) use per-block cache_control; others concatenate the text.
	SystemParts []ContentBlock `json:"-"`
}

// ContentBlock is a structured content block within a system prompt.
// Enables per-block cache_control for Anthropic prompt caching.
type ContentBlock struct {
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl marks a content block for prompt caching.
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// LLMResponse is the result of a single LLM API call.
type LLMResponse struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     ProviderUsage

	// NeedsToolResults is true when the model's stop reason indicates it wants
	// tool results before continuing (e.g., Anthropic stop_reason="tool_use",
	// OpenAI finish_reason="tool_calls").
	NeedsToolResults bool
}

// ChatProvider makes a single LLM API call and returns the response.
// Implementations handle converting LLMMessage to/from the provider's native
// format. The agentic loop uses this interface to drive tool→LLM→tool cycles.
type ChatProvider interface {
	Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error)
}

// ChatOptions configures a single LLM API call.
type ChatOptions struct {
	MaxTokens      int
	ThinkingBudget int
	CacheSystem    bool // apply cache_control to system prompt blocks
	CacheTools     bool // apply cache_control to the last tool definition
}

// ─── Helpers for converting between Turn and LLMMessage ──────────────────────

// buildLLMMessagesFromTurn converts a Turn into a slice of LLMMessage suitable
// for passing to ChatProvider.Chat(). It constructs the system prompt, appends
// history, and adds the current user message.
func buildLLMMessagesFromTurn(turn Turn, providerSystemPrompt string) []LLMMessage {
	msgs := make([]LLMMessage, 0, len(turn.History)+2)

	// Build system prompt.
	sys := combineSystemPrompts(providerSystemPrompt, turn.Context)
	if sys != "" {
		sysMsg := LLMMessage{Role: "system", Content: sys}
		// Set up SystemParts for cache_control: the static system prompt is
		// marked ephemeral so providers that support prompt caching (Anthropic)
		// can reuse the KV cache across turns.
		sysMsg.SystemParts = []ContentBlock{
			{Text: sys, CacheControl: &CacheControl{Type: "ephemeral"}},
		}
		msgs = append(msgs, sysMsg)
	}

	// Append conversation history.
	for _, h := range turn.History {
		msgs = append(msgs, LLMMessage{Role: h.Role, Content: h.Content, ToolCallID: h.ToolCallID})
	}

	// Append current user message.
	msgs = append(msgs, LLMMessage{
		Role:    "user",
		Content: turn.UserText,
		Images:  turn.Images,
	})

	return msgs
}

// combineSystemPrompts merges a provider-level system prompt with a turn-level
// context string. Returns the combined prompt or "" if both are empty.
func combineSystemPrompts(providerPrompt, turnContext string) string {
	p := trimOrEmpty(providerPrompt)
	c := trimOrEmpty(turnContext)
	switch {
	case p != "" && c != "":
		return p + "\n\n" + c
	case p != "":
		return p
	case c != "":
		return c
	default:
		return ""
	}
}

// chatOptionsFromTurn derives ChatOptions from a Turn.
func chatOptionsFromTurn(turn Turn) ChatOptions {
	maxTokens := 4096
	if turn.ThinkingBudget > 0 {
		maxTokens = turn.ThinkingBudget + turn.ThinkingBudget/2
		if maxTokens < 16000 {
			maxTokens = 16000
		}
	}
	return ChatOptions{
		MaxTokens:      maxTokens,
		ThinkingBudget: turn.ThinkingBudget,
		CacheSystem:    true,
		CacheTools:     true,
	}
}

// llmResponseToProviderResult converts an LLMResponse to a ProviderResult.
func llmResponseToProviderResult(resp *LLMResponse) ProviderResult {
	return ProviderResult{
		Text:      resp.Content,
		ToolCalls: resp.ToolCalls,
		Usage:     resp.Usage,
	}
}

func trimOrEmpty(s string) string {
	return strings.TrimSpace(s)
}
