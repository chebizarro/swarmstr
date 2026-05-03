package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	pluginhooks "metiq/internal/plugins/hooks"
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
	// StaticSystemPrompt carries long-lived system prompt additions that should
	// remain in the cacheable/static prompt lane (for example pinned knowledge
	// or workspace bootstrap material). Providers that support prompt caching
	// treat this as part of the static system prefix rather than per-turn
	// dynamic context.
	StaticSystemPrompt string
	// Context carries genuinely per-turn dynamic prompt additions (for example
	// memory search results or engine-supplied dynamic turn context).
	Context string
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
	// ContextWindowTokens is the approximate context window available to the
	// provider. Shared history/tool-result guards use this to bound prompt size.
	ContextWindowTokens int
	// Trace carries task/run/step correlation IDs for observability. When a
	// turn runs inside a task context, all emitted events inherit these IDs.
	Trace TraceContext
	// MaxAgenticIterations overrides the model-tier default for the maximum
	// number of tool→LLM round-trips.  0 means use the model-tier default.
	MaxAgenticIterations int
	// LastAssistantTime is the timestamp of the most recent assistant message
	// in the conversation. Passed through to the agentic loop for the
	// time-based microcompact trigger. Zero means unknown/disabled.
	LastAssistantTime time.Time
	// HookInvoker emits OpenClaw before_tool_call/after_tool_call hooks.
	HookInvoker *pluginhooks.HookInvoker

	// SteeringDrain non-blockingly returns additional user input that arrived
	// while this turn was active. Agentic loops drain it at model boundaries.
	SteeringDrain func(context.Context) []InjectedUserInput

	// DeferredTools holds tool definitions that are deferred from inline
	// sending. When non-nil and non-empty, the agentic loop registers a
	// tool_search built-in tool that lets the model discover deferred tools
	// on demand, reducing per-request context usage.
	DeferredTools *DeferredToolSet
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
	InputTokens         int64 `json:"input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
}

// TurnTelemetry is the minimal structured runtime snapshot for a completed or
// failed turn. metiqd persists and emits this without adding a separate
// analytics pipeline.
type TurnTelemetry struct {
	TurnID         string
	StartedAtMS    int64
	EndedAtMS      int64
	DurationMS     int64
	Outcome        TurnOutcome
	StopReason     TurnStopReason
	LoopBlocked    bool
	Error          string
	FallbackUsed   bool
	FallbackFrom   string
	FallbackTo     string
	FallbackReason string
	Usage          TurnUsage
	Trace          TraceContext
}

// TurnResultMetadata is the canonical persisted subset of a terminal turn
// result. metiqd stores this alongside HistoryDelta so callers do not have to
// reconstruct terminal state from logs.
type TurnResultMetadata struct {
	Outcome    TurnOutcome    `json:"outcome,omitempty"`
	StopReason TurnStopReason `json:"stop_reason,omitempty"`
	Usage      TurnUsage      `json:"usage,omitempty"`
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

// ClassifyTurnError maps a failed turn to the canonical outcome/stop-reason
// taxonomy. If err carries a TurnExecutionError partial classification, that
// wins; otherwise context cancellation/deadline map to aborted/cancelled and
// all other failures map to failed/provider_error.
func ClassifyTurnError(err error) (TurnOutcome, TurnStopReason, bool) {
	if err == nil {
		return "", "", false
	}
	var te *TurnExecutionError
	if errors.As(err, &te) {
		if te.Partial.Outcome != "" || te.Partial.StopReason != "" {
			return te.Partial.Outcome, te.Partial.StopReason, true
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return TurnOutcomeAborted, TurnStopReasonCancelled, true
	}
	return TurnOutcomeFailed, TurnStopReasonProviderError, true
}

// ClassifyTurnResult infers terminal classification when a runtime returns a
// plain TurnResult without explicitly populating Outcome/StopReason.
func ClassifyTurnResult(result TurnResult) (TurnOutcome, TurnStopReason) {
	return inferTurnClassification(result)
}

// BuildTurnResultMetadata projects the canonical terminal classification and
// usage into a persisted form. When err wraps a TurnExecutionError, any partial
// usage/classification carried by the error wins.
func BuildTurnResultMetadata(result TurnResult, err error) (TurnResultMetadata, bool) {
	meta := TurnResultMetadata{Usage: result.Usage}
	if err != nil {
		var te *TurnExecutionError
		if errors.As(err, &te) {
			if te.Partial.Usage.InputTokens > 0 || te.Partial.Usage.OutputTokens > 0 {
				meta.Usage = te.Partial.Usage
			}
		}
		if outcome, stopReason, ok := ClassifyTurnError(err); ok {
			meta.Outcome = outcome
			meta.StopReason = stopReason
		}
	} else {
		meta.Outcome = result.Outcome
		meta.StopReason = result.StopReason
		if meta.Outcome == "" || meta.StopReason == "" {
			inferredOutcome, inferredStopReason := ClassifyTurnResult(result)
			if meta.Outcome == "" {
				meta.Outcome = inferredOutcome
			}
			if meta.StopReason == "" {
				meta.StopReason = inferredStopReason
			}
		}
	}
	if meta.Outcome == "" && meta.StopReason == "" && meta.Usage.InputTokens == 0 && meta.Usage.OutputTokens == 0 {
		return TurnResultMetadata{}, false
	}
	return meta, true
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
		tools:    FilteredToolExecutor(r.tools, allowed),
	}
}

func (r *ProviderRuntime) ProcessTurn(ctx context.Context, turn Turn) (TurnResult, error) {
	ctx = ensureMutationTrackingContext(ctx)
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}
	// Inject session ID into context so tools can read it without requiring
	// the LLM to echo it back as an explicit parameter.
	if turn.SessionID != "" {
		ctx = ContextWithSessionID(ctx, turn.SessionID)
	}
	frozenTools := SnapshotToolExecutor(r.tools)
	trackedTools := NewMutationTrackingToolExecutor(frozenTools)
	// Auto-inject tool definitions when the executor provides them and the
	// caller hasn't already populated turn.Tools.
	if len(turn.Tools) == 0 && trackedTools != nil {
		if dp, ok := trackedTools.(interface{ Definitions() []ToolDefinition }); ok {
			turn.Tools = dp.Definitions()
		}
	}
	// Inject the executor so providers can run the agentic tool loop internally.
	if turn.Executor == nil {
		turn.Executor = trackedTools
	}
	gen, err := r.provider.Generate(ctx, turn)
	if err != nil {
		return TurnResult{}, err
	}
	return r.buildResult(ctx, gen, trackedTools)
}

// ProcessTurnStreaming processes a turn with incremental text delivery.
// If the underlying provider implements StreamingProvider, text tokens are
// delivered via onChunk as they arrive; otherwise Generate() is called and
// the full text is delivered in one onChunk call.  Tool calls are executed
// after streaming completes using the configured ToolExecutor.
func (r *ProviderRuntime) ProcessTurnStreaming(ctx context.Context, turn Turn, onChunk func(text string)) (TurnResult, error) {
	ctx = ensureMutationTrackingContext(ctx)
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}
	if turn.SessionID != "" {
		ctx = ContextWithSessionID(ctx, turn.SessionID)
	}
	frozenTools := SnapshotToolExecutor(r.tools)
	trackedTools := NewMutationTrackingToolExecutor(frozenTools)
	// Auto-inject tool definitions (same as ProcessTurn).
	if len(turn.Tools) == 0 && trackedTools != nil {
		if dp, ok := trackedTools.(interface{ Definitions() []ToolDefinition }); ok {
			turn.Tools = dp.Definitions()
		}
	}
	if turn.Executor == nil {
		turn.Executor = trackedTools
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

	// When the streaming response produced tool calls, the single-shot stream
	// bypassed the agentic tool→LLM→tool cycle. Fall back to the non-streaming
	// Generate path which runs the full agentic loop (tool execution → model
	// synthesis → repeat until text). This re-processes the prompt but
	// correctly handles tool execution and produces a complete text response.
	//
	// The streaming response may have already emitted partial text tokens via
	// onChunk (e.g. "Let me look that up…"). The Generate path produces the
	// complete synthesised response in gen.Text and emits it to onChunk so
	// the client can display the final answer.
	if len(gen.ToolCalls) > 0 && trackedTools != nil {
		gen, err = r.provider.Generate(ctx, turn)
		if err != nil {
			return TurnResult{}, err
		}
		if onChunk != nil && gen.Text != "" {
			onChunk(gen.Text)
		}
	}

	return r.buildResult(ctx, gen, trackedTools)
}

// buildResult executes any tool calls from gen and assembles the TurnResult.
func (r *ProviderRuntime) buildResult(ctx context.Context, gen ProviderResult, tools ToolExecutor) (TurnResult, error) {
	result := TurnResult{
		Text:         strings.TrimSpace(gen.Text),
		ToolTraces:   nil,
		Outcome:      gen.Outcome,
		StopReason:   gen.StopReason,
		HistoryDelta: gen.HistoryDelta,
		Usage: TurnUsage{
			InputTokens:         gen.Usage.InputTokens,
			OutputTokens:        gen.Usage.OutputTokens,
			CacheReadTokens:     gen.Usage.CacheReadTokens,
			CacheCreationTokens: gen.Usage.CacheCreationTokens,
		},
	}
	for _, call := range gen.ToolCalls {
		trace := ToolTrace{Call: call}
		if tools == nil {
			trace.Error = "no tool executor configured"
			result.ToolTraces = append(result.ToolTraces, trace)
			continue
		}
		value, err := tools.Execute(ctx, call)
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
		// Safety net: the agentic loop and streaming fallback should produce
		// proper text responses, but if a provider returns raw tool calls
		// without text, summarise the tool results so the user gets
		// actionable information rather than a dead-end placeholder.
		var sb strings.Builder
		for _, trace := range result.ToolTraces {
			if trace.Error != "" {
				fmt.Fprintf(&sb, "[%s] error: %s\n", trace.Call.Name, trace.Error)
			} else if trace.Result != "" {
				snippet := trace.Result
				if len(snippet) > 300 {
					snippet = snippet[:300] + "…"
				}
				fmt.Fprintf(&sb, "[%s] %s\n", trace.Call.Name, snippet)
			}
		}
		if sb.Len() > 0 {
			result.Text = sb.String()
		} else {
			result.Text = "Tools executed but produced no output."
		}
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
		return TurnOutcomeCompletedWithTools, TurnStopReasonModelText
	case len(result.ToolTraces) > 0:
		return TurnOutcomeToolOnlyCompleted, TurnStopReasonToolExecution
	default:
		return TurnOutcomeCompleted, TurnStopReasonModelText
	}
}
