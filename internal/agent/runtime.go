package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ToolCallRef identifies a tool invocation within an assistant message.
// It mirrors the structure of ToolCall but stores args as a JSON string
// for lossless serialisation in conversation history.
type ToolCallRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ArgsJSON string `json:"args_json,omitempty"`
}

// ConversationMessage is one message in the prior conversation history passed
// to the provider.  Role is "user", "assistant", "system", or "tool".
type ConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCallID is set for role="tool" messages linking results to calls.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls is set on role="assistant" messages that requested tool use.
	ToolCalls []ToolCallRef `json:"tool_calls,omitempty"`
}

type Turn struct {
	SessionID string
	// TurnID is an optional caller-supplied correlation identifier for
	// observability. metiq maps Nostr event IDs into this field.
	TurnID   string
	UserText string
	Context  string
	// Images carries vision content for multi-modal providers.
	// Each element is either a URL reference or inline base64 data.
	// Text-only providers (echo, http, ollama) ignore this field.
	Images []ImageRef
	// Tools lists available tool definitions for native function-calling.
	// When non-empty, providers that support it (Anthropic, OpenAI, Gemini)
	// include these in the API request so the model can invoke them.
	Tools []ToolDefinition
	// History is the prior conversation for multi-turn context.
	// Messages are ordered oldest-first.
	History []ConversationMessage
	// Executor is the tool executor for agentic loops inside providers.
	Executor ToolExecutor
	// ThinkingBudget enables extended thinking for providers that support it.
	// 0 means disabled; a positive value specifies the token budget for the
	// model's internal reasoning phase (Anthropic: budget_tokens in the
	// thinking config block).  The caller should ensure MaxTokens (if set) is
	// strictly greater than ThinkingBudget.
	ThinkingBudget int
	// ToolEventSink receives start/progress/result/error events emitted by the
	// shared tool loop. Leave nil when runtime tool events are not needed.
	ToolEventSink ToolLifecycleSink
}

// ImageRef is a resolved image reference for passing to vision providers.
// Exactly one of URL or Base64 is set.
type ImageRef struct {
	URL      string // remote URL; provider may pass as image_url reference
	Base64   string // base64-encoded binary (no data URI prefix)
	MimeType string // e.g. "image/jpeg", "image/png", "image/webp"
}

type TurnResult struct {
	Text       string
	ToolTraces []ToolTrace
	Outcome    TurnOutcome
	StopReason TurnStopReason
	// HistoryDelta is the ordered sequence of conversation messages produced
	// during this turn.  On a plain text turn it contains one assistant message.
	// On tool turns it contains assistant tool-call messages, tool result
	// messages, and the final assistant text (if any).  Callers should persist
	// these into the context engine so future turns see prior tool usage.
	HistoryDelta []ConversationMessage
	// Usage reports token consumption for the turn (if the provider supports it).
	Usage TurnUsage
}

// TurnUsage holds provider-reported token counts for a single turn.
type TurnUsage struct {
	InputTokens  int64
	OutputTokens int64
}

// TurnOutcome classifies the terminal result shape of a turn.
// It is runtime-only in this tranche and intentionally not persisted yet.
type TurnOutcome string

const (
	TurnOutcomeCompleted          TurnOutcome = "completed"
	TurnOutcomeCompletedWithTools TurnOutcome = "completed_with_tools"
	TurnOutcomeToolOnlyCompleted  TurnOutcome = "tool_only_completed"
	TurnOutcomeForcedSummary      TurnOutcome = "forced_summary"
	TurnOutcomeBlocked            TurnOutcome = "blocked"
	TurnOutcomeAborted            TurnOutcome = "aborted"
	TurnOutcomeFailed             TurnOutcome = "failed"
)

// TurnStopReason explains why a turn terminated.
type TurnStopReason string

const (
	TurnStopReasonModelText     TurnStopReason = "model_text"
	TurnStopReasonToolExecution TurnStopReason = "tool_execution"
	TurnStopReasonForcedSummary TurnStopReason = "forced_summary"
	TurnStopReasonLoopBlocked   TurnStopReason = "loop_blocked"
	TurnStopReasonMaxIterations TurnStopReason = "max_iterations"
	TurnStopReasonProviderError TurnStopReason = "provider_error"
	TurnStopReasonCancelled     TurnStopReason = "cancelled"
)

// TurnExecutionError wraps a turn failure while carrying any tool work that
// completed before the error occurred.  Callers can extract the partial result
// via PartialTurnResult to persist completed tool interactions even when the
// overall turn fails (e.g. timeout or context cancellation).
type TurnExecutionError struct {
	Cause   error
	Partial TurnResult
}

func (e *TurnExecutionError) Error() string { return e.Cause.Error() }
func (e *TurnExecutionError) Unwrap() error { return e.Cause }

// PartialTurnResult extracts completed tool work from a failed turn.
// Returns the partial result and true if err wraps a TurnExecutionError
// with non-empty HistoryDelta or ToolTraces; otherwise returns zero and false.
func PartialTurnResult(err error) (TurnResult, bool) {
	var te *TurnExecutionError
	if errors.As(err, &te) {
		if len(te.Partial.HistoryDelta) > 0 || len(te.Partial.ToolTraces) > 0 {
			return te.Partial, true
		}
	}
	return TurnResult{}, false
}

type Runtime interface {
	ProcessTurn(context.Context, Turn) (TurnResult, error)
}

// StreamingRuntime extends Runtime with incremental text delivery.
// Implementations call onChunk for each text token (or small group) as it
// arrives from the provider, enabling real-time display of partial responses.
type StreamingRuntime interface {
	Runtime
	// ProcessTurnStreaming processes a turn and delivers text chunks via onChunk
	// as they arrive.  The returned TurnResult is the complete response including
	// ToolTraces.  onChunk may be nil (degrades to buffered delivery).
	ProcessTurnStreaming(ctx context.Context, turn Turn, onChunk func(text string)) (TurnResult, error)
}

type ProviderRuntime struct {
	provider Provider
	tools    ToolExecutor
}

// ToolCallToRef converts a ToolCall (with map args) to a ToolCallRef (with
// JSON-string args) suitable for conversation history storage.
func ToolCallToRef(tc ToolCall) ToolCallRef {
	ref := ToolCallRef{ID: tc.ID, Name: tc.Name}
	if len(tc.Args) > 0 {
		if b, err := json.Marshal(tc.Args); err == nil {
			ref.ArgsJSON = string(b)
		}
	}
	return ref
}

func NewProviderRuntime(provider Provider, tools ToolExecutor) (*ProviderRuntime, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	return &ProviderRuntime{provider: provider, tools: tools}, nil
}

func NewRuntimeFromEnv(tools ToolExecutor) (Runtime, error) {
	provider, err := NewProviderFromEnv()
	if err != nil {
		return nil, err
	}
	return NewProviderRuntime(provider, tools)
}

// Filtered returns a Runtime that only permits tool calls in the allowed set.
// If allowed is nil, all tools are permitted (equivalent to the original runtime).
// A non-nil empty map means deny-all (strict fail-closed).
// Only ProviderRuntime instances are filtered; other Runtime implementations are
// returned unchanged.
func (r *ProviderRuntime) Filtered(allowed map[string]bool) Runtime {
	if allowed == nil {
		return r
	}
	return &ProviderRuntime{
		provider: r.provider,
		tools:    &ProfileFilteredExecutor{Base: r.tools, Allowed: allowed},
	}
}

func (r *ProviderRuntime) ProcessTurn(ctx context.Context, turn Turn) (TurnResult, error) {
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}
	// Inject session ID into context so tools can read it without requiring
	// the LLM to echo it back as an explicit parameter.
	if turn.SessionID != "" {
		ctx = ContextWithSessionID(ctx, turn.SessionID)
	}
	// Auto-inject tool definitions when the executor provides them and the
	// caller hasn't already populated turn.Tools.
	if len(turn.Tools) == 0 && r.tools != nil {
		if dp, ok := r.tools.(interface{ Definitions() []ToolDefinition }); ok {
			turn.Tools = dp.Definitions()
		}
	}
	// Inject the executor so providers can run the agentic tool loop internally.
	if turn.Executor == nil {
		turn.Executor = r.tools
	}
	gen, err := r.provider.Generate(ctx, turn)
	if err != nil {
		return TurnResult{}, err
	}
	return r.buildResult(ctx, gen)
}

// ProcessTurnStreaming processes a turn with incremental text delivery.
// If the underlying provider implements StreamingProvider, text tokens are
// delivered via onChunk as they arrive; otherwise Generate() is called and
// the full text is delivered in one onChunk call.  Tool calls are executed
// after streaming completes using the configured ToolExecutor.
func (r *ProviderRuntime) ProcessTurnStreaming(ctx context.Context, turn Turn, onChunk func(text string)) (TurnResult, error) {
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}
	if turn.SessionID != "" {
		ctx = ContextWithSessionID(ctx, turn.SessionID)
	}
	// Auto-inject tool definitions (same as ProcessTurn).
	if len(turn.Tools) == 0 && r.tools != nil {
		if dp, ok := r.tools.(interface{ Definitions() []ToolDefinition }); ok {
			turn.Tools = dp.Definitions()
		}
	}
	if turn.Executor == nil {
		turn.Executor = r.tools
	}

	var gen ProviderResult
	var err error

	if sp, ok := r.provider.(StreamingProvider); ok {
		gen, err = sp.Stream(ctx, turn, onChunk)
	} else {
		gen, err = r.provider.Generate(ctx, turn)
		if err == nil && onChunk != nil {
			onChunk(gen.Text)
		}
	}
	if err != nil {
		return TurnResult{}, err
	}

	return r.buildResult(ctx, gen)
}

// buildResult executes any tool calls from gen and assembles the TurnResult.
func (r *ProviderRuntime) buildResult(ctx context.Context, gen ProviderResult) (TurnResult, error) {
	result := TurnResult{
		Text:         strings.TrimSpace(gen.Text),
		ToolTraces:   nil,
		Outcome:      gen.Outcome,
		StopReason:   gen.StopReason,
		HistoryDelta: gen.HistoryDelta,
		Usage:        TurnUsage{InputTokens: gen.Usage.InputTokens, OutputTokens: gen.Usage.OutputTokens},
	}
	for _, call := range gen.ToolCalls {
		trace := ToolTrace{Call: call}
		if r.tools == nil {
			trace.Error = "no tool executor configured"
			result.ToolTraces = append(result.ToolTraces, trace)
			continue
		}
		value, err := r.tools.Execute(ctx, call)
		if err != nil {
			trace.Error = err.Error()
		} else {
			trace.Result = value
		}
		result.ToolTraces = append(result.ToolTraces, trace)
	}

	if result.Text == "" && len(result.ToolTraces) == 0 {
		return TurnResult{}, fmt.Errorf("provider returned empty response")
	}
	if result.Text == "" && len(result.ToolTraces) > 0 {
		result.Text = "tool execution complete"
	}
	if result.Outcome == "" || result.StopReason == "" {
		inferredOutcome, inferredStopReason := inferTurnClassification(result)
		if result.Outcome == "" {
			result.Outcome = inferredOutcome
		}
		if result.StopReason == "" {
			result.StopReason = inferredStopReason
		}
	}
	return result, nil
}

func inferTurnClassification(result TurnResult) (TurnOutcome, TurnStopReason) {
	switch {
	case len(result.ToolTraces) > 0 && strings.TrimSpace(result.Text) != "":
		if strings.TrimSpace(result.Text) == "tool execution complete" {
			return TurnOutcomeToolOnlyCompleted, TurnStopReasonToolExecution
		}
		return TurnOutcomeCompletedWithTools, TurnStopReasonModelText
	case len(result.ToolTraces) > 0:
		return TurnOutcomeToolOnlyCompleted, TurnStopReasonToolExecution
	default:
		return TurnOutcomeCompleted, TurnStopReasonModelText
	}
}
