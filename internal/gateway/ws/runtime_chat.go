package ws

import (
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
)

// RuntimeChatProjection maps the provider-neutral runtime event stream onto the
// closed protocol-v4 chat union. Provider thinking and tool-call fragments stay
// on their dedicated gateway events rather than being folded into assistant text.
type RuntimeChatProjection struct {
	mu            sync.Mutex
	stream        *ChatStream
	emitter       EventEmitter
	agentID       string
	text          string
	usage         agent.TurnUsage
	started       bool
	ended         bool
	legacyPending []string
}

func NewRuntimeChatProjection(stream *ChatStream, emitter EventEmitter, agentID string) *RuntimeChatProjection {
	return &RuntimeChatProjection{stream: stream, emitter: emitter, agentID: strings.TrimSpace(agentID)}
}

// RuntimeEventSink returns the stable callback installed on agent.Turn. The
// projection imports only the agent runtime contract, never provider adapters.
func (p *RuntimeChatProjection) RuntimeEventSink() agent.RuntimeEventSink {
	if p == nil {
		return nil
	}
	return p.HandleRuntimeEvent
}

// Start establishes the initial chat state synchronously. Runtime stream-start
// events are idempotent with this call so callback-only and structured runtimes
// share the same ordering contract.
func (p *RuntimeChatProjection) Start() {
	if p == nil || p.stream == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.startLocked()
}

// LegacyDelta bridges the callback-based StreamingRuntime contract into the
// v4 projection. ProviderRuntime emits the same chunk through both contracts;
// legacyPending lets the following structured delta acknowledge that chunk
// without publishing it twice.
func (p *RuntimeChatProjection) LegacyDelta(text string) {
	if p == nil || p.stream == nil || text == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended {
		return
	}
	p.startLocked()
	p.legacyPending = append(p.legacyPending, text)
	p.text += text
	p.stream.Delta(text, false)
}

func (p *RuntimeChatProjection) startLocked() {
	if p.started || p.ended {
		return
	}
	p.started = true
	p.stream.Status(ChatPhaseStartingModel)
}

func (p *RuntimeChatProjection) HandleRuntimeEvent(evt agent.RuntimeEvent) {
	if p == nil || p.stream == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended {
		return
	}

	switch evt.Type {
	case agent.RuntimeEventStreamStart:
		p.startLocked()
	case agent.RuntimeEventAssistantDelta:
		if evt.Delta == "" {
			return
		}
		p.startLocked()
		if len(p.legacyPending) > 0 && p.legacyPending[0] == evt.Delta {
			p.legacyPending = p.legacyPending[1:]
			return
		}
		p.text += evt.Delta
		p.stream.Delta(evt.Delta, false)
	case agent.RuntimeEventAssistantMessage:
		if evt.Message == nil {
			return
		}
		// The terminal assistant message is authoritative after stream
		// normalization/tool-call repair, so refresh the visible content even
		// when it differs from the raw deltas already delivered.
		p.text = evt.Message.Content
		p.stream.DeltaWithMetadata(p.text, true, ChatAssistantMessage(p.text), chatUsageFromTurnUsage(p.usage))
	case agent.RuntimeEventUsage:
		p.usage = evt.Usage
	case agent.RuntimeEventThinkingDelta:
		if evt.Delta != "" && p.emitter != nil {
			p.emitter.Emit(EventThinkingDelta, ThinkingDeltaPayload{
				TS: timestampOrNow(evt.TS), AgentID: p.agentID,
				SessionID: runtimeSessionID(evt, p.stream), TurnID: evt.TurnID, Text: evt.Delta,
			})
		}
	case agent.RuntimeEventAssistantToolCallDelta:
		if p.emitter != nil {
			p.emitter.Emit(EventToolProgress, ToolLifecyclePayload{
				TS: timestampOrNow(evt.TS), AgentID: p.agentID,
				SessionID: runtimeSessionID(evt, p.stream), TurnID: evt.TurnID,
				ToolCallID: evt.ToolCallID, ToolName: evt.ToolName,
				Data: map[string]any{
					"phase": "assistant_tool_call_delta", "content_block_index": evt.ContentBlockIndex,
					"arguments_delta": evt.Delta,
				},
			})
		}
	case agent.RuntimeEventStreamEnd:
		p.ended = true
		p.stream.Final(ChatAssistantMessage(p.text), chatUsageFromTurnUsage(p.usage), "", false)
	case agent.RuntimeEventStreamError:
		p.ended = true
		aborted, kind, stopReason := classifyRuntimeChatError(evt.Error)
		message := ChatAssistantMessage(p.text)
		if aborted {
			p.stream.Aborted(message, evt.Error, stopReason)
			return
		}
		p.stream.Error(message, evt.Error, kind, chatUsageFromTurnUsage(p.usage), stopReason)
	}
}

func runtimeSessionID(evt agent.RuntimeEvent, stream *ChatStream) string {
	if sessionID := strings.TrimSpace(evt.SessionID); sessionID != "" {
		return sessionID
	}
	if stream == nil {
		return ""
	}
	return stream.base.SessionKey
}

func timestampOrNow(ts int64) int64 {
	if ts > 0 {
		return ts
	}
	return time.Now().UnixMilli()
}

func chatUsageFromTurnUsage(usage agent.TurnUsage) any {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheCreationTokens == 0 {
		return nil
	}
	return map[string]any{
		"inputTokens": usage.InputTokens, "outputTokens": usage.OutputTokens,
		"cacheReadTokens": usage.CacheReadTokens, "cacheCreationTokens": usage.CacheCreationTokens,
	}
}

func classifyRuntimeChatError(message string) (aborted bool, kind ChatErrorKind, stopReason string) {
	normalized := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(normalized, "context canceled"), strings.Contains(normalized, "cancelled"),
		strings.Contains(normalized, "canceled"), strings.Contains(normalized, "interrupted"), strings.Contains(normalized, "aborted"):
		return true, ChatErrorUnknown, "canceled"
	case strings.Contains(normalized, "deadline exceeded"), strings.Contains(normalized, "timed out"), strings.Contains(normalized, "timeout"):
		return false, ChatErrorTimeout, "timeout"
	case strings.Contains(normalized, "rate_limit"), strings.Contains(normalized, "rate limit"),
		strings.Contains(normalized, "too many requests"), strings.Contains(normalized, "http 429"), strings.Contains(normalized, "status 429"):
		return false, ChatErrorRateLimit, "rate_limit"
	case strings.Contains(normalized, "context_length"), strings.Contains(normalized, "context length"),
		strings.Contains(normalized, "context window"), strings.Contains(normalized, "too many tokens"), strings.Contains(normalized, "prompt is too long"):
		return false, ChatErrorContextLength, "context_length"
	case strings.Contains(normalized, "refusal"), strings.Contains(normalized, "refused"), strings.Contains(normalized, "safety policy"):
		return false, ChatErrorRefusal, "refusal"
	default:
		return false, ChatErrorUnknown, "provider_error"
	}
}
