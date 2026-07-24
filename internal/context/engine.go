// Package context provides a pluggable context engine abstraction for metiq.
//
// A ContextEngine manages the conversation history for an agent session.
// It is responsible for:
//   - Ingesting new messages (from user, assistant, tool calls)
//   - Assembling the ordered message list for the next model call
//   - Compacting the context when it grows too large (summarisation/pruning)
//   - Bootstrapping from historical data (e.g. replaying Nostr transcript events)
//
// Built-in engines:
//   - "legacy"   — wraps the existing store/state transcript + memory.Index (default)
//   - "windowed" — fixed-size sliding window, no compaction
//
// Custom engines can be registered via RegisterContextEngine().
//
// Configuration:
//
//	"extra": {
//	  "context_engine": "legacy"
//	}
package context

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// ActiveRecallProvider optionally supplies per-turn memory context during
// Assemble. Implementations should be fast and honor ctx cancellation.
type ActiveRecallProvider interface {
	AssembleActiveRecall(ctx context.Context, sessionID string, latest Message, recent []Message, maxChars int) (string, error)
}

// ─── Core types ───────────────────────────────────────────────────────────────

// ToolCallRef identifies a tool invocation within an assistant message.
// This mirrors agent.ToolCallRef but lives in the context package to avoid
// an import cycle.
type ToolCallRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ArgsJSON string `json:"args_json,omitempty"`
}

// Message is a single message in the context window.
type Message struct {
	// Role is one of "user", "assistant", "system", "tool".
	Role string `json:"role"`
	// Content is the plain text or structured content.
	Content string `json:"content"`
	// ToolCallID links tool results back to a tool call (role "tool" only).
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls records which tools the assistant invoked (role "assistant" only).
	// Present when the assistant message triggered tool use during the turn.
	ToolCalls []ToolCallRef `json:"tool_calls,omitempty"`
	// ID is an optional stable identifier for the message (e.g. event ID).
	ID string `json:"id,omitempty"`
	// Unix is the message timestamp (seconds since epoch).
	Unix int64 `json:"unix,omitempty"`
}

// AssembleResult is returned by Engine.Assemble().
type PromptAuthority string

const (
	PromptAuthorityAssembled              PromptAuthority = "assembled"
	PromptAuthorityPreassemblyMayOverflow PromptAuthority = "preassembly_may_overflow"
)

func ResolvePromptAuthority(authority PromptAuthority) PromptAuthority {
	switch authority {
	case PromptAuthorityPreassemblyMayOverflow:
		return PromptAuthorityPreassemblyMayOverflow
	default:
		return PromptAuthorityAssembled
	}
}

// AuthoritativePromptTokens returns the token count runners should compare
// against overflow thresholds for this assembly.
func AuthoritativePromptTokens(result AssembleResult, preassemblyTokens int) int {
	if ResolvePromptAuthority(result.PromptAuthority) == PromptAuthorityPreassemblyMayOverflow && preassemblyTokens > result.EstimatedTokens {
		return preassemblyTokens
	}
	return result.EstimatedTokens
}

type AssembleResult struct {
	// Messages are ordered messages ready to pass to the model.
	Messages []Message `json:"messages"`
	// EstimatedTokens is a rough token count estimate.
	EstimatedTokens int `json:"estimated_tokens"`
	// PromptAuthority controls which token estimate the runner treats as authoritative
	// for overflow prechecks. Defaults to "assembled".
	PromptAuthority PromptAuthority `json:"prompt_authority,omitempty"`
	// SystemPromptAddition is optional engine-supplied dynamic text appended to
	// the runtime system prompt at turn assembly time (e.g. memory context).
	// Unlike the static provider/system prompt prefix, this addition is treated
	// as non-cacheable prompt material so per-turn context churn does not bust
	// the reusable system-prompt cache prefix.
	SystemPromptAddition string `json:"system_prompt_addition,omitempty"`
	// ContextProjection describes whether this assembly should be projected into
	// the model call every turn or can be treated as persistent bootstrap context
	// by runtimes with backend threads.
	ContextProjection *ContextProjection `json:"context_projection,omitempty"`
	// PromptCache reports basic cache-stability telemetry for this assembly.
	PromptCache *PromptCacheInfo `json:"prompt_cache,omitempty"`
}

// IngestResult is returned by Engine.Ingest().
type IngestResult struct {
	// Ingested reports whether the message was recorded (false if duplicate).
	Ingested bool `json:"ingested"`
}

// IngestBatchResult is returned by optional batch ingest implementations.
type IngestBatchResult struct {
	IngestedCount int `json:"ingested_count"`
}

// IngestBatcher is implemented by engines that can ingest a completed turn atomically.
type IngestBatcher interface {
	IngestBatch(ctx context.Context, sessionID string, messages []Message) (IngestBatchResult, error)
}

// RunIngestBatch invokes batch ingest when available, otherwise falls back to Ingest.
func RunIngestBatch(ctx context.Context, engine Engine, sessionID string, messages []Message) (IngestBatchResult, error) {
	if batcher, ok := engine.(IngestBatcher); ok {
		return batcher.IngestBatch(ctx, sessionID, messages)
	}
	result := IngestBatchResult{}
	for _, msg := range messages {
		ingested, err := engine.Ingest(ctx, sessionID, msg)
		if err != nil {
			return result, err
		}
		if ingested.Ingested {
			result.IngestedCount++
		}
	}
	return result, nil
}

// CompactResult is returned by Engine.Compact().
type CompactResult struct {
	// OK reports whether compaction succeeded.
	OK bool `json:"ok"`
	// Compacted reports whether compaction actually ran (false if unnecessary).
	Compacted bool `json:"compacted"`
	// Summary is a human-readable description of what was compacted.
	Summary string `json:"summary,omitempty"`
	// Reason optionally explains why compaction did not run or failed.
	Reason string `json:"reason,omitempty"`
	// TokensBefore / TokensAfter report the token counts pre and post compaction.
	TokensBefore int `json:"tokens_before"`
	TokensAfter  int `json:"tokens_after,omitempty"`
	// FirstKeptMessageID identifies the first transcript message retained after compaction.
	FirstKeptMessageID string `json:"first_kept_message_id,omitempty"`
	// CutPoint is the original message index where retained context begins.
	CutPoint int `json:"cut_point,omitempty"`
	// DroppedMessages and KeptMessages report pruning counts.
	DroppedMessages int `json:"dropped_messages,omitempty"`
	KeptMessages    int `json:"kept_messages,omitempty"`
	// PreviousSummaryTokens is non-zero when existing summary context was carried forward.
	PreviousSummaryTokens int `json:"previous_summary_tokens,omitempty"`
	// SummaryTokens estimates the active summary token count after compaction.
	SummaryTokens int `json:"summary_tokens,omitempty"`
	// LLMAssisted reports whether compaction used model inference.
	LLMAssisted bool `json:"llm_assisted,omitempty"`
	// Details carries implementation-specific planner metadata.
	Details map[string]any `json:"details,omitempty"`
}

// BootstrapResult is returned by Engine.Bootstrap().
type BootstrapResult struct {
	// Bootstrapped reports whether bootstrap ran.
	Bootstrapped bool `json:"bootstrapped"`
	// ImportedMessages is the number of historical messages loaded.
	ImportedMessages int `json:"imported_messages,omitempty"`
}

// ─── Engine interface ─────────────────────────────────────────────────────────

// Engine is the core context management interface.
// Implementations must be safe for concurrent use.
type Engine interface {
	// Ingest records a new message from the conversation.
	Ingest(ctx context.Context, sessionID string, msg Message) (IngestResult, error)

	// Assemble returns ordered messages for the next model call.
	Assemble(ctx context.Context, sessionID string, maxTokens int) (AssembleResult, error)

	// Compact summarises or prunes old context to reduce token usage.
	// Implementations may be no-ops if they don't support compaction.
	Compact(ctx context.Context, sessionID string) (CompactResult, error)

	// Bootstrap initialises the engine from historical data.
	// Called once per session on first access.
	Bootstrap(ctx context.Context, sessionID string, messages []Message) (BootstrapResult, error)

	// Close releases resources held by this engine.
	Close() error
}

// ─── Factory and registry ─────────────────────────────────────────────────────

// Factory constructs an Engine for a given session.
type Factory func(sessionID string, opts map[string]any) (Engine, error)

var (
	regMu     sync.RWMutex
	factories = map[string]Factory{}
	regOrder  []string
)

// RegisterContextEngine registers a context engine factory by name.
// Names are case-insensitive. Panics on duplicate registration.
func RegisterContextEngine(name string, factory Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := factories[name]; ok {
		panic(fmt.Sprintf("context engine %q already registered", name))
	}
	factories[name] = factory
	regOrder = append(regOrder, name)
}

// ListContextEngines returns the names of all registered engines.
func ListContextEngines() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, len(regOrder))
	copy(out, regOrder)
	return out
}

// NewEngine creates a new Engine by name.
func NewEngine(name, sessionID string, opts map[string]any) (Engine, error) {
	regMu.RLock()
	factory, ok := factories[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("context engine %q not registered (available: %v)", name, ListContextEngines())
	}
	return factory(sessionID, opts)
}

// ─── No-op compaction engine (base struct for embedding) ─────────────────────

// NoOpCompact provides a no-op Compact implementation for engines that don't
// need it.
type NoOpCompact struct{}

func (NoOpCompact) Compact(_ context.Context, _ string) (CompactResult, error) {
	return CompactResult{OK: true, Compacted: false}, nil
}

// ─── Built-in: windowed engine ────────────────────────────────────────────────

// WindowedEngine is a simple sliding-window context engine that keeps the last
// N messages without any compaction.
type WindowedEngine struct {
	NoOpCompact
	mu              sync.Mutex
	sessions        map[string][]Message
	summaries       map[string]string
	promptCacheLast map[string]string
	maxMsgs         int
	activeRecall    ActiveRecallProvider
}

// NewWindowedEngine creates a WindowedEngine keeping up to maxMsgs messages per session.
func NewWindowedEngine(maxMsgs int) *WindowedEngine {
	if maxMsgs <= 0 {
		maxMsgs = 50
	}
	return &WindowedEngine{sessions: map[string][]Message{}, summaries: map[string]string{}, promptCacheLast: map[string]string{}, maxMsgs: maxMsgs}
}

func (e *WindowedEngine) SetActiveRecallProvider(provider ActiveRecallProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeRecall = provider
}

func (e *WindowedEngine) Ingest(_ context.Context, sessionID string, msg Message) (IngestResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	msgs := e.sessions[sessionID]
	// Deduplicate by ID.
	if msg.ID != "" {
		for _, m := range msgs {
			if m.ID == msg.ID {
				return IngestResult{Ingested: false}, nil
			}
		}
	}
	msgs = append(msgs, msg)
	if len(msgs) > e.maxMsgs {
		msgs = msgs[len(msgs)-e.maxMsgs:]
	}
	e.sessions[sessionID] = msgs
	return IngestResult{Ingested: true}, nil
}

func (e *WindowedEngine) Assemble(ctx context.Context, sessionID string, maxTokens int) (AssembleResult, error) {
	e.mu.Lock()
	msgs := make([]Message, len(e.sessions[sessionID]))
	copy(msgs, e.sessions[sessionID])
	summary := e.summaries[sessionID]
	activeRecall := e.activeRecall
	e.mu.Unlock()

	// A non-positive budget is the compatibility/unlimited mode.
	if maxTokens <= 0 {
		result := AssembleResult{Messages: msgs, EstimatedTokens: EstimateMessagesTokens(msgs)}
		cacheParts := promptCacheParts{Summary: normalizePromptCacheSummary(summary)}
		if cacheParts.Summary != "" {
			result.SystemPromptAddition = cacheParts.Summary
			result.EstimatedTokens += EstimateTextTokens(cacheParts.Summary)
		}
		if activeRecall != nil {
			if latest, ok := latestUserMessage(msgs); ok {
				recallText, err := activeRecall.AssembleActiveRecall(ctx, sessionID, latest, msgs, 0)
				if err != nil {
					return result, err
				}
				cacheParts.Recall = normalizePromptCacheRecall(recallText)
				if cacheParts.Recall != "" {
					if result.SystemPromptAddition != "" {
						result.SystemPromptAddition += "\n\n"
					}
					result.SystemPromptAddition += cacheParts.Recall
					result.EstimatedTokens += EstimateTextTokens(cacheParts.Recall)
				}
			}
		}
		e.annotateAssembleResultWithParts(sessionID, &result, cacheParts)
		return result, nil
	}

	fullSummary := normalizePromptCacheSummary(summary)
	projection := fitWindowProjection(msgs, fullSummary, maxTokens)
	result := AssembleResult{
		Messages:             projection.Messages,
		SystemPromptAddition: projection.Summary,
		EstimatedTokens:      projection.Tokens,
	}
	cacheParts := promptCacheParts{Summary: projection.Summary}
	remainingTokens := maxTokens - projection.Tokens
	if activeRecall != nil && remainingTokens > 0 {
		if latest, ok := latestUserMessage(msgs); ok {
			maxChars := CharacterCapacity(remainingTokens, DefaultCharsPerToken)
			recallText, err := activeRecall.AssembleActiveRecall(ctx, sessionID, latest, msgs, maxChars)
			if err != nil {
				return result, err
			}
			cacheParts.Recall = normalizePromptCacheRecall(recallText)
			result.SystemPromptAddition, result.EstimatedTokens = fitRecallToBudget(
				result.Messages, projection.Summary, cacheParts.Recall, maxTokens,
			)
			// Cache metadata must describe only the material that survived clipping.
			switch {
			case result.SystemPromptAddition == projection.Summary:
				cacheParts.Recall = ""
			case projection.Summary != "":
				cacheParts.Recall = strings.TrimPrefix(result.SystemPromptAddition, projection.Summary+"\n\n")
			default:
				cacheParts.Recall = result.SystemPromptAddition
			}
		}
	}
	e.annotateAssembleResultWithParts(sessionID, &result, cacheParts)
	return result, nil
}

func latestUserMessage(messages []Message) (Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Content != "" {
			return messages[i], true
		}
	}
	return Message{}, false
}

func (e *WindowedEngine) Bootstrap(_ context.Context, sessionID string, messages []Message) (BootstrapResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	msgs := make([]Message, len(messages))
	copy(msgs, messages)
	if len(msgs) > e.maxMsgs {
		msgs = msgs[len(msgs)-e.maxMsgs:]
	}
	e.sessions[sessionID] = msgs
	delete(e.summaries, sessionID)
	delete(e.promptCacheLast, sessionID)
	return BootstrapResult{Bootstrapped: true, ImportedMessages: len(msgs)}, nil
}

func (e *WindowedEngine) Close() error { return nil }

// init registers built-in engines.
func init() {
	RegisterContextEngine("windowed", func(sessionID string, opts map[string]any) (Engine, error) {
		maxMsgs := 50
		if v, ok := opts["max_messages"].(float64); ok {
			maxMsgs = int(v)
		}
		engine := NewWindowedEngine(maxMsgs)
		if provider, ok := opts["active_recall"].(ActiveRecallProvider); ok {
			engine.SetActiveRecallProvider(provider)
		}
		return engine, nil
	})
}
