package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/agent/toolrepair"
	ctxengine "metiq/internal/context"
	pluginhooks "metiq/internal/plugins/hooks"
	"metiq/internal/policy"
	sessioncheckpoint "metiq/internal/session/checkpoint"
)

// ErrTurnInterrupted marks an intentional user interrupt of an active turn.
// It should classify as an aborted/cancelled turn rather than provider failure.
var ErrTurnInterrupted = errors.New("turn interrupted by user input")

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
	// ID is an optional stable transcript/context entry ID.
	ID string `json:"id,omitempty"`
	// Unix is an optional message timestamp in seconds.
	Unix int64 `json:"unix,omitempty"`
}

type Turn struct {
	SessionID string
	// TurnID is an optional caller-supplied correlation identifier for
	// observability. metiq maps Nostr event IDs into this field.
	TurnID   string
	UserText string
	// UserMessageID/Unix optionally identify the current user message when the
	// runtime calls the context engine AfterTurn hook.
	UserMessageID string
	UserUnix      int64
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
	// ContextEngine receives the post-turn lifecycle hook after a successful
	// runtime turn. Leave nil when the caller manages context lifecycle itself.
	ContextEngine ctxengine.Engine
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
	// RuntimeEventSink receives canonical runtime lifecycle events spanning
	// provider streaming, tool lifecycle, transcript, and usage surfaces.
	RuntimeEventSink RuntimeEventSink
	// ContextWindowTokens is the approximate context window available to the
	// provider. Shared history/tool-result guards use this to bound prompt size.
	ContextWindowTokens int
	// ResponseFormat requests provider-native structured output / JSON mode for
	// this turn when supported by the selected provider.
	ResponseFormat *ResponseFormatConfig
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
	// ToolPolicy gates every tool call before execution. Nil preserves the
	// profile/default behavior.
	ToolPolicy *policy.ToolPolicy
	// ToolPolicyAgentID scopes per-agent policy rules when set.
	ToolPolicyAgentID string
	// PostSamplingHooks run after provider sampling/tool execution has produced
	// a complete TurnResult. Hooks are called at the natural turn boundary and
	// must not block waiting for future user input; use them to enqueue
	// best-effort post-turn work such as autonomous session-memory extraction.
	PostSamplingHooks []PostSamplingHook

	// SteeringDrain non-blockingly returns additional user input that arrived
	// while this turn was active. Agentic loops drain it at model boundaries.
	SteeringDrain func(context.Context) []InjectedUserInput

	// DeferredTools holds tool definitions that are deferred from inline
	// sending. When non-nil and non-empty, the agentic loop registers a
	// tool_search built-in tool that lets the model discover deferred tools
	// on demand, reducing per-request context usage.
	DeferredTools        *DeferredToolSet
	TurnCheckpointSink   func(context.Context, sessioncheckpoint.TurnCheckpoint) error
	ResumeCheckpoint     *sessioncheckpoint.TurnCheckpoint
	ResumeCheckpointSafe bool
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
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTurnInterrupted) {
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

// PostSamplingHook observes a completed turn after model sampling and tool
// execution have settled. Implementations should return quickly or enqueue
// asynchronous work; runtime calls hooks only for successful turns.
type PostSamplingHook interface {
	AfterSampling(context.Context, Turn, TurnResult)
}

// PostSamplingHookFunc adapts a function into a PostSamplingHook.
type PostSamplingHookFunc func(context.Context, Turn, TurnResult)

func (f PostSamplingHookFunc) AfterSampling(ctx context.Context, turn Turn, result TurnResult) {
	if f != nil {
		f(ctx, turn, result)
	}
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
	provider  Provider
	tools     ToolExecutor
	lifecycle *runtimeLifecycleState
}

// ToolCallToRef converts a ToolCall (with map args) to a ToolCallRef (with
// JSON-string args) suitable for conversation history storage.
func ToolCallToRef(tc ToolCall) ToolCallRef {
	return ToolCallRefForPersistence(nil, tc)
}

func NewProviderRuntime(provider Provider, tools ToolExecutor) (*ProviderRuntime, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	return &ProviderRuntime{provider: provider, tools: tools, lifecycle: newRuntimeLifecycleState()}, nil
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
		provider:  r.provider,
		tools:     FilteredToolExecutor(r.tools, allowed),
		lifecycle: r.lifecycle,
	}
}

func (r *ProviderRuntime) ProcessTurn(ctx context.Context, turn Turn) (result TurnResult, err error) {
	turn.ToolEventSink = toolLifecycleSinkForRuntime(turn.ToolEventSink, turn.RuntimeEventSink)
	ctx = ensureMutationTrackingContext(ctx)
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}
	r.ensureSessionStarted(ctx, turn)
	defer func() { emitAgentEndHook(ctx, turn, result, err) }()
	if err = emitBeforeAgentHook(ctx, turn); err != nil {
		return TurnResult{}, err
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
		return TurnResult{}, turnCancellationCause(ctx, err)
	}
	result, err = r.buildResult(ctx, turn, gen, trackedTools)
	if err != nil {
		return TurnResult{}, err
	}
	emitTurnUsageRuntimeEvent(turn, result.Usage)
	runContextAfterTurn(ctx, turn, result)
	runPostSamplingHooks(ctx, turn, result)
	return result, nil
}

// ProcessTurnStreaming processes a turn with incremental text delivery.
// If the underlying provider implements StreamingProvider, text tokens are
// delivered via onChunk as they arrive; otherwise Generate() is called and
// the full text is delivered in one onChunk call.  Tool calls are executed
// after streaming completes using the configured ToolExecutor.
func (r *ProviderRuntime) ProcessTurnStreaming(ctx context.Context, turn Turn, onChunk func(text string)) (result TurnResult, err error) {
	turn.ToolEventSink = toolLifecycleSinkForRuntime(turn.ToolEventSink, turn.RuntimeEventSink)
	ctx = ensureMutationTrackingContext(ctx)
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}
	r.ensureSessionStarted(ctx, turn)
	defer func() { emitAgentEndHook(ctx, turn, result, err) }()
	if err = emitBeforeAgentHook(ctx, turn); err != nil {
		return TurnResult{}, err
	}
	if turn.SessionID != "" {
		ctx = ContextWithSessionID(ctx, turn.SessionID)
	}
	frozenTools := SnapshotToolExecutor(r.tools)
	trackedTools := NewMutationTrackingToolExecutor(frozenTools)
	if len(turn.Tools) == 0 && trackedTools != nil {
		if dp, ok := trackedTools.(interface{ Definitions() []ToolDefinition }); ok {
			turn.Tools = dp.Definitions()
		}
	}
	if turn.Executor == nil {
		turn.Executor = trackedTools
	}

	emitStreamLifecycleRuntimeEvent(turn, RuntimeEventStreamStart, nil)
	var gen ProviderResult
	var lastStreamUsage ProviderUsage

	startedAt := time.Now()
	if sp, ok := r.provider.(EventStreamingProvider); ok {
		var normalizer *toolrepair.StreamNormalizer
		if len(turn.Tools) > 0 {
			defs := make([]toolrepair.ToolDefinition, 0, len(turn.Tools))
			for _, def := range turn.Tools {
				defs = append(defs, toolrepair.ToolDefinition{Name: def.Name})
			}
			normalizer = toolrepair.NewStreamNormalizer(defs)
		}
		var normalizedText strings.Builder
		var promotedCalls []ToolCall
		sawTextEvent := false
		emitStableText := func(text string) {
			if text == "" {
				return
			}
			normalizedText.WriteString(text)
			if onChunk != nil {
				onChunk(text)
			}
			emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{Type: RuntimeEventAssistantDelta, SessionID: turn.SessionID, TurnID: turn.TurnID, ContentBlockIndex: 0, Delta: text, Trace: turn.Trace})
		}
		consumeNormalized := func(out toolrepair.StreamOutput) {
			emitStableText(out.Text)
			for _, repaired := range out.Calls {
				promotedCalls = append(promotedCalls, ToolCall{ID: repaired.ID, Name: repaired.Name, Args: repaired.Args})
			}
		}
		gen, err = sp.StreamEvents(ctx, turn, func(evt ProviderStreamEvent) {
			switch evt.Type {
			case ProviderStreamTextDelta:
				if evt.TextDelta != "" {
					sawTextEvent = true
					if normalizer != nil {
						consumeNormalized(normalizer.Feed(evt.TextDelta))
					} else {
						emitStableText(evt.TextDelta)
					}
				}
			case ProviderStreamThinkingDelta:
				emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{Type: RuntimeEventThinkingDelta, SessionID: turn.SessionID, TurnID: turn.TurnID, Delta: evt.ThinkingDelta, Trace: turn.Trace})
			case ProviderStreamToolCallDelta:
				d := evt.ToolCallDelta
				emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{Type: RuntimeEventAssistantToolCallDelta, SessionID: turn.SessionID, TurnID: turn.TurnID, ContentBlockIndex: d.Index, ToolCallID: d.ID, ToolName: d.Name, Delta: d.ArgumentsDelta, Trace: turn.Trace})
			case ProviderStreamUsage:
				if hasProviderUsage(evt.Usage) && !providerUsageEqual(evt.Usage, lastStreamUsage) {
					lastStreamUsage = evt.Usage
					emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{Type: RuntimeEventUsage, SessionID: turn.SessionID, TurnID: turn.TurnID, Usage: providerUsageToTurnUsage(evt.Usage), Trace: turn.Trace})
				}
			}
		})
		if err == nil && normalizer != nil {
			consumeNormalized(normalizer.Flush())
			if sawTextEvent {
				gen.Text = normalizedText.String()
			}
			for _, call := range promotedCalls {
				idx := len(gen.ToolCalls)
				gen.ToolCalls = append(gen.ToolCalls, call)
				args, _ := json.Marshal(call.Args)
				emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{Type: RuntimeEventAssistantToolCallDelta, SessionID: turn.SessionID, TurnID: turn.TurnID, ContentBlockIndex: idx, ToolCallID: call.ID, ToolName: call.Name, Delta: string(args), Trace: turn.Trace})
			}
		}
		emitProviderRoundtripSpan(ctx, "stream_events", turn, startedAt, err)
	} else if sp, ok := r.provider.(StreamingProvider); ok {
		gen, err = sp.Stream(ctx, turn, runtimeEventStreamingCallback(turn, onChunk))
		emitProviderRoundtripSpan(ctx, "stream", turn, startedAt, err)
	} else {
		gen, err = r.provider.Generate(ctx, turn)
		emitProviderRoundtripSpan(ctx, "stream_generate", turn, startedAt, err)
		if err == nil && onChunk != nil {
			runtimeEventStreamingCallback(turn, onChunk)(gen.Text)
		}
	}
	if err != nil {
		err = turnCancellationCause(ctx, err)
		emitStreamLifecycleRuntimeEvent(turn, RuntimeEventStreamError, err)
		return TurnResult{}, err
	}

	if len(gen.ToolCalls) > 0 && len(gen.HistoryDelta) == 0 {
		refs := make([]ToolCallRef, 0, len(gen.ToolCalls))
		for _, call := range gen.ToolCalls {
			refs = append(refs, ToolCallRefForPersistence(trackedTools, call))
		}
		gen.HistoryDelta = append(gen.HistoryDelta, ConversationMessage{Role: "assistant", Content: strings.TrimSpace(gen.Text), ToolCalls: refs})
	}

	result, err = r.buildResult(ctx, turn, gen, trackedTools)
	if err != nil {
		emitStreamLifecycleRuntimeEvent(turn, RuntimeEventStreamError, err)
		return TurnResult{}, err
	}
	if !hasProviderUsage(lastStreamUsage) || !providerUsageEqual(lastStreamUsage, gen.Usage) {
		emitTurnUsageRuntimeEvent(turn, result.Usage)
	}
	emitAssistantMessageRuntimeEvent(turn, gen)
	runContextAfterTurn(ctx, turn, result)
	runPostSamplingHooks(ctx, turn, result)
	emitStreamLifecycleRuntimeEvent(turn, RuntimeEventStreamEnd, nil)
	return result, nil
}

func emitStreamLifecycleRuntimeEvent(turn Turn, eventType RuntimeEventType, err error) {
	evt := RuntimeEvent{Type: eventType, SessionID: turn.SessionID, TurnID: turn.TurnID, Trace: turn.Trace}
	if err != nil {
		evt.Error = err.Error()
	}
	emitRuntimeEvent(turn.RuntimeEventSink, evt)
}

func emitProviderRoundtripSpan(ctx context.Context, phase string, turn Turn, startedAt time.Time, callErr error) {
	fields := map[string]any{
		"phase": phase,
	}
	if turn.SessionID != "" {
		fields["session_id"] = turn.SessionID
	}
	if turn.TurnID != "" {
		fields["turn_id"] = turn.TurnID
	}
	if callErr != nil {
		fields["error"] = callErr.Error()
	}
	EmitTurnSpan(ctx, "provider_call", time.Since(startedAt), fields)
}

func runtimeEventStreamingCallback(turn Turn, onChunk func(text string)) func(string) {
	if turn.RuntimeEventSink == nil {
		return onChunk
	}
	return func(text string) {
		if onChunk != nil {
			onChunk(text)
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{
			Type:              RuntimeEventAssistantDelta,
			SessionID:         turn.SessionID,
			TurnID:            turn.TurnID,
			ContentBlockIndex: 0,
			Delta:             text,
			Trace:             turn.Trace,
		})
	}
}

func emitTurnUsageRuntimeEvent(turn Turn, usage TurnUsage) {
	if turn.RuntimeEventSink == nil || !hasUsage(usage) {
		return
	}
	emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{
		Type:      RuntimeEventUsage,
		SessionID: turn.SessionID,
		TurnID:    turn.TurnID,
		Usage:     usage,
		Trace:     turn.Trace,
	})
}

func emitAssistantMessageRuntimeEvent(turn Turn, gen ProviderResult) {
	if turn.RuntimeEventSink == nil {
		return
	}
	emitRuntimeEvent(turn.RuntimeEventSink, RuntimeEvent{
		Type:      RuntimeEventAssistantMessage,
		SessionID: turn.SessionID,
		TurnID:    turn.TurnID,
		Message: &AssistantMessage{
			Content:   strings.TrimSpace(gen.Text),
			ToolCalls: append([]ToolCall(nil), gen.ToolCalls...),
		},
		Trace: turn.Trace,
	})
}

func runContextAfterTurn(ctx context.Context, turn Turn, result TurnResult) {
	if turn.ContextEngine == nil || strings.TrimSpace(turn.SessionID) == "" {
		return
	}
	params := ctxengine.AfterTurnParams{
		Messages:       buildAfterTurnContextMessages(turn, result),
		PrePromptCount: len(turn.History),
		TokenBudget:    turn.ContextWindowTokens,
		CurrentTokens:  int(result.Usage.InputTokens + result.Usage.OutputTokens),
	}
	if err := ctxengine.RunAfterTurn(ctx, turn.ContextEngine, turn.SessionID, params); err != nil {
		log.Printf("context engine after-turn session=%s turn=%s err=%v", turn.SessionID, turn.TurnID, err)
	}
}

func buildAfterTurnContextMessages(turn Turn, result TurnResult) []ctxengine.Message {
	messages := make([]ctxengine.Message, 0, len(turn.History)+1+len(result.HistoryDelta))
	for _, m := range turn.History {
		messages = append(messages, contextMessageFromConversation(m))
	}
	if strings.TrimSpace(turn.UserText) != "" {
		messages = append(messages, ctxengine.Message{Role: "user", Content: turn.UserText, ID: strings.TrimSpace(turn.UserMessageID), Unix: turn.UserUnix})
	}
	nowUnix := time.Now().Unix()
	for i, m := range result.HistoryDelta {
		ctxMsg := contextMessageFromConversation(m)
		if ctxMsg.ID == "" {
			ctxMsg.ID = synthesizeTurnHistoryContextID(turn.TurnID, i, m)
		}
		if ctxMsg.Unix == 0 {
			ctxMsg.Unix = nowUnix
		}
		messages = append(messages, ctxMsg)
	}
	return messages
}

func contextMessageFromConversation(m ConversationMessage) ctxengine.Message {
	msg := ctxengine.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, ID: strings.TrimSpace(m.ID), Unix: m.Unix}
	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ctxengine.ToolCallRef{ID: tc.ID, Name: tc.Name, ArgsJSON: tc.ArgsJSON})
	}
	return msg
}

func synthesizeTurnHistoryContextID(turnID string, index int, m ConversationMessage) string {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		turnID = fmt.Sprintf("anon:%d", time.Now().UnixNano())
	}
	switch {
	case m.Role == "assistant" && len(m.ToolCalls) > 0:
		return fmt.Sprintf("turn:%s:toolcall:%d", turnID, index)
	case m.Role == "tool" && m.ToolCallID != "":
		return fmt.Sprintf("turn:%s:tool:%s", turnID, m.ToolCallID)
	case m.Role == "assistant":
		return fmt.Sprintf("turn:%s:assistant:%d", turnID, index)
	default:
		return fmt.Sprintf("turn:%s:msg:%d", turnID, index)
	}
}

// buildResult executes any tool calls from gen and assembles the TurnResult.
func (r *ProviderRuntime) buildResult(ctx context.Context, turn Turn, gen ProviderResult, tools ToolExecutor) (TurnResult, error) {
	result := TurnResult{
		Text:         strings.TrimSpace(gen.Text),
		ToolTraces:   nil,
		Outcome:      gen.Outcome,
		StopReason:   gen.StopReason,
		HistoryDelta: RedactConversationMessagesForPersistence(tools, gen.HistoryDelta),
		Usage: TurnUsage{
			InputTokens:         gen.Usage.InputTokens,
			OutputTokens:        gen.Usage.OutputTokens,
			CacheReadTokens:     gen.Usage.CacheReadTokens,
			CacheCreationTokens: gen.Usage.CacheCreationTokens,
		},
	}
	for _, call := range gen.ToolCalls {
		descriptor, _ := ToolDescriptorForExecutor(tools, call.Name)
		trace := ToolTrace{
			Call:       NewToolRedactor().RedactToolCall(call, descriptor),
			Descriptor: descriptor,
		}
		if tools == nil {
			trace.Error = "no tool executor configured"
			result.ToolTraces = append(result.ToolTraces, trace)
			continue
		}
		toolCtx := ContextWithToolPolicy(ctx, turn.ToolPolicy, turn.ToolPolicyAgentID)
		execResult := executeSingleToolCall(toolCtx, tools, call, turn.SessionID, turn.TurnID, turn.ToolEventSink, turn.Trace, turn.HookInvoker)
		if strings.HasPrefix(execResult.Content, "error: ") {
			trace.Error = strings.TrimPrefix(execResult.Content, "error: ")
		} else {
			trace.Result = execResult.Content
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

func runPostSamplingHooks(ctx context.Context, turn Turn, result TurnResult) {
	for _, hook := range turn.PostSamplingHooks {
		if hook == nil {
			continue
		}
		hook := hook
		hookCtx := context.WithoutCancel(ctx)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("post-sampling hook panic session=%s turn=%s panic=%v", turn.SessionID, turn.TurnID, r)
				}
			}()
			hook.AfterSampling(hookCtx, turn, result)
		}()
	}
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
