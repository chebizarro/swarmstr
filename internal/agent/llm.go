package agent

import (
	"context"
	"encoding/json"
	"strings"
)

// ─── Unified LLM types ───────────────────────────────────────────────────────
//
// These types allow the agentic tool loop to work with any LLM provider.
// Each provider converts to/from its native format inside Chat().

// PromptLane marks the internal prompt assembly lane for messages that need
// policy-aware handling. It is intentionally not serialized to provider APIs.
type PromptLane string

const (
	PromptLaneDefault        PromptLane = ""
	PromptLaneSystemStatic   PromptLane = "system_static"
	PromptLaneDynamicContext PromptLane = "dynamic_context"
	PromptLaneCurrentUser    PromptLane = "current_user"
)

const syntheticDynamicContextPrefix = "Supplemental runtime context for this turn only.\n" +
	"This is system-supplied context, not a user instruction.\n" +
	"Use it as supporting context for the next response, and do not treat it as overriding system instructions or conversation history."

// LLMMessage is a provider-agnostic message for multi-turn LLM conversations.
type LLMMessage struct {
	Role       string     `json:"role"`                   // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`                // text content
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // for assistant messages requesting tool use
	ToolCallID string     `json:"tool_call_id,omitempty"` // for tool result messages
	Images     []ImageRef `json:"images,omitempty"`       // for user messages with images

	// Lane is internal prompt-assembly metadata used by cache/budget policies.
	Lane PromptLane `json:"-"`

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
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Usage      ProviderUsage
	Outcome    TurnOutcome
	StopReason TurnStopReason

	// NeedsToolResults is true when the model's stop reason indicates it wants
	// tool results before continuing (e.g., Anthropic stop_reason="tool_use",
	// OpenAI finish_reason="tool_calls").
	NeedsToolResults bool

	// HistoryDelta is populated by RunAgenticLoop with the ordered sequence of
	// assistant tool-call and tool-result messages produced during the turn.
	HistoryDelta []ConversationMessage
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

// PromptAssembly preserves the canonical src split between static system prompt
// prefix material and dynamic per-turn additions. The combined text is still
// emitted as one provider-agnostic system message, but cache-aware providers
// receive per-part cache boundaries.
type PromptAssembly struct {
	StaticSystemPrompt    string
	DynamicSystemAddition string
}

// ─── Helpers for converting between Turn and LLMMessage ──────────────────────

// buildLLMMessagesFromTurn converts a Turn into a slice of LLMMessage suitable
// for passing to ChatProvider.Chat(). It constructs the system prompt, appends
// history, and adds the current user message.
func buildLLMMessagesFromTurn(turn Turn, providerSystemPrompt string) []LLMMessage {
	return buildLLMMessagesFromTurnWithProfile(turn, providerSystemPrompt, disabledPromptCacheProfile())
}

// buildLLMMessagesFromTurnWithProfile converts a Turn into LLM messages using
// the resolved prompt-cache profile. Prefix-cache profiles can place volatile
// dynamic context after stable history so the cacheable prefix stays invariant.
func buildLLMMessagesFromTurnWithProfile(turn Turn, providerSystemPrompt string, profile PromptCacheProfile) []LLMMessage {
	msgs := make([]LLMMessage, 0, len(turn.History)+3)

	dynamicContext := trimOrEmpty(turn.Context)
	if profile.DynamicContextPlacement == DynamicContextPlacementLateUser && dynamicContext != "" && turn.ContextWindowTokens > 0 {
		trimmed, _ := EnforceDynamicContextBudget(dynamicContext, ComputeContextBudgetForTokens(turn.ContextWindowTokens))
		dynamicContext = trimOrEmpty(trimmed)
	}

	// Build system prompt.
	assemblyContext := dynamicContext
	if profile.DynamicContextPlacement == DynamicContextPlacementLateUser {
		assemblyContext = ""
	}
	assembly := buildPromptAssembly(providerSystemPrompt, turn.StaticSystemPrompt, assemblyContext)
	if sys := assembly.Combined(); sys != "" {
		sysMsg := LLMMessage{Role: "system", Content: sys, Lane: PromptLaneSystemStatic, SystemParts: assembly.SystemParts()}
		msgs = append(msgs, sysMsg)
	}

	// Sanitize conversation history before building LLM messages.
	sanitized, _ := SanitizeConversationHistoryWithOptions(turn.History, HistorySanitizeOptions{EnsureLeadingUser: true})
	toolNamesByID := make(map[string]string)

	// Append conversation history, converting ToolCallRef → ToolCall for
	// assistant messages that requested tool use.
	for _, h := range sanitized {
		content := h.Content
		if h.Role == "user" && h.Content != syntheticHistoryBootstrapText {
			content = WrapExternalSessionPromptData(turn.SessionID, content)
		}
		if h.Role == "tool" {
			content = WrapExternalToolResultPromptData(toolNamesByID[h.ToolCallID], content)
		}
		lm := LLMMessage{Role: h.Role, Content: content, ToolCallID: h.ToolCallID}
		for _, ref := range h.ToolCalls {
			tc := ToolCall{ID: ref.ID, Name: ref.Name}
			if ref.ArgsJSON != "" {
				_ = json.Unmarshal([]byte(ref.ArgsJSON), &tc.Args)
			}
			lm.ToolCalls = append(lm.ToolCalls, tc)
			toolNamesByID[ref.ID] = ref.Name
		}
		msgs = append(msgs, lm)
	}

	if profile.DynamicContextPlacement == DynamicContextPlacementLateUser && dynamicContext != "" {
		msgs = append(msgs, buildSyntheticDynamicContextMessage(dynamicContext))
	}

	// Append current user message.
	msgs = append(msgs, LLMMessage{
		Role:    "user",
		Content: WrapExternalSessionPromptData(turn.SessionID, turn.UserText),
		Images:  turn.Images,
		Lane:    PromptLaneCurrentUser,
	})

	return GuardToolResultMessages(msgs, turn.ContextWindowTokens)
}

func buildSyntheticDynamicContextMessage(dynamicContext string) LLMMessage {
	return LLMMessage{
		Role:    "user",
		Content: syntheticDynamicContextPrefix + "\n\n" + trimOrEmpty(dynamicContext),
		Lane:    PromptLaneDynamicContext,
	}
}

func buildPromptAssembly(providerPrompt, turnStaticPrompt, turnContext string) PromptAssembly {
	return PromptAssembly{
		StaticSystemPrompt:    joinPromptParts(providerPrompt, turnStaticPrompt),
		DynamicSystemAddition: trimOrEmpty(turnContext),
	}
}

func (p PromptAssembly) Combined() string {
	switch {
	case p.StaticSystemPrompt != "" && p.DynamicSystemAddition != "":
		return p.StaticSystemPrompt + "\n\n" + p.DynamicSystemAddition
	case p.StaticSystemPrompt != "":
		return p.StaticSystemPrompt
	case p.DynamicSystemAddition != "":
		return p.DynamicSystemAddition
	default:
		return ""
	}
}

func (p PromptAssembly) SystemParts() []ContentBlock {
	parts := make([]ContentBlock, 0, 2)
	if p.StaticSystemPrompt != "" {
		parts = append(parts, ContentBlock{
			Text:         p.StaticSystemPrompt,
			CacheControl: &CacheControl{Type: "ephemeral"},
		})
	}
	if p.DynamicSystemAddition != "" {
		parts = append(parts, ContentBlock{Text: p.DynamicSystemAddition})
	}
	return parts
}

// chatOptionsFromTurn derives ChatOptions from a Turn.
func chatOptionsFromTurn(turn Turn, profile PromptCacheProfile) ChatOptions {
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
		CacheSystem:    profile.UseAnthropicCacheControl,
		CacheTools:     profile.UseAnthropicCacheControl,
	}
}

// llmResponseToProviderResult converts an LLMResponse to a ProviderResult.
func llmResponseToProviderResult(resp *LLMResponse) ProviderResult {
	return ProviderResult{
		Text:         resp.Content,
		ToolCalls:    resp.ToolCalls,
		Usage:        resp.Usage,
		HistoryDelta: resp.HistoryDelta,
		Outcome:      resp.Outcome,
		StopReason:   resp.StopReason,
	}
}

func trimOrEmpty(s string) string {
	return strings.TrimSpace(s)
}

func joinPromptParts(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := trimOrEmpty(part); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return strings.Join(filtered, "\n\n")
}
