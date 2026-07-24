package ws

import (
	"testing"

	"metiq/internal/agent"
)

func TestRuntimeChatProjectionReplacementUsageAndDedicatedEvents(t *testing.T) {
	capture := &captureEmitter{}
	stream := NewChatStream(capture, "run-1", "session-1", "main")
	projection := NewRuntimeChatProjection(stream, capture, "main")
	sink := projection.RuntimeEventSink()

	sink(agent.RuntimeEvent{Type: agent.RuntimeEventStreamStart, SessionID: "session-1", TurnID: "run-1"})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantDelta, SessionID: "session-1", TurnID: "run-1", Delta: "hel"})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventUsage, Usage: agent.TurnUsage{InputTokens: 4, OutputTokens: 2, CacheReadTokens: 1}})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventThinkingDelta, TS: 10, SessionID: "session-1", TurnID: "run-1", Delta: "reason"})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantToolCallDelta, TS: 11, SessionID: "session-1", TurnID: "run-1", ToolCallID: "call-1", ToolName: "lookup", Delta: `{"q":"x"}`})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantMessage, Message: &agent.AssistantMessage{Content: "hello"}})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventStreamEnd})

	if capture.Count() != 6 {
		t.Fatalf("event count=%d events=%#v payloads=%#v", capture.Count(), capture.events, capture.payloads)
	}
	wantEvents := []string{EventChat, EventChat, EventThinkingDelta, EventToolProgress, EventChat, EventChat}
	for i, want := range wantEvents {
		if capture.events[i] != want {
			t.Fatalf("event[%d]=%q want=%q", i, capture.events[i], want)
		}
	}
	thinking := capture.payloads[2].(ThinkingDeltaPayload)
	if thinking.Text != "reason" || thinking.TS != 10 || thinking.SessionID != "session-1" {
		t.Fatalf("thinking=%+v", thinking)
	}
	tool := capture.payloads[3].(ToolLifecyclePayload)
	if tool.ToolCallID != "call-1" || tool.ToolName != "lookup" || tool.Data.(map[string]any)["phase"] != "assistant_tool_call_delta" {
		t.Fatalf("tool delta=%+v", tool)
	}
	replacement := capture.payloads[4].(ChatDeltaEvent)
	if !replacement.Replace || replacement.DeltaText != "hello" || replacement.Seq != 2 {
		t.Fatalf("replacement=%+v", replacement)
	}
	usage := replacement.Usage.(map[string]any)
	if usage["inputTokens"] != int64(4) || usage["outputTokens"] != int64(2) || usage["cacheReadTokens"] != int64(1) {
		t.Fatalf("replacement usage=%#v", usage)
	}
	final := capture.payloads[5].(ChatFinalEvent)
	if final.Seq != 3 || final.Usage.(map[string]any)["outputTokens"] != int64(2) {
		t.Fatalf("final=%+v", final)
	}
}

func TestRuntimeChatProjectionLegacyCallbackDeduplicatesStructuredDelta(t *testing.T) {
	capture := &captureEmitter{}
	projection := NewRuntimeChatProjection(NewChatStream(capture, "run-1", "session-1", "main"), capture, "main")
	projection.Start()
	projection.LegacyDelta("hel")
	projection.HandleRuntimeEvent(agent.RuntimeEvent{Type: agent.RuntimeEventStreamStart})
	projection.HandleRuntimeEvent(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantDelta, Delta: "hel"})
	projection.LegacyDelta("lo")
	projection.HandleRuntimeEvent(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantDelta, Delta: "lo"})
	projection.HandleRuntimeEvent(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantMessage, Message: &agent.AssistantMessage{Content: "hello"}})
	projection.HandleRuntimeEvent(agent.RuntimeEvent{Type: agent.RuntimeEventStreamEnd})

	if capture.Count() != 5 {
		t.Fatalf("event count=%d payloads=%#v", capture.Count(), capture.payloads)
	}
	for idx, want := range []string{"", "hel", "lo", "hello"} {
		if idx == 0 {
			if got := capture.payloads[idx].(ChatStatusEvent); got.Seq != 0 {
				t.Fatalf("status=%+v", got)
			}
			continue
		}
		got := capture.payloads[idx].(ChatDeltaEvent)
		if got.DeltaText != want {
			t.Fatalf("delta[%d]=%q want=%q", idx, got.DeltaText, want)
		}
	}
	if got := capture.payloads[4].(ChatFinalEvent); got.Seq != 4 {
		t.Fatalf("final=%+v", got)
	}
}

func TestRuntimeChatProjectionCallbackOnlyStillStreams(t *testing.T) {
	capture := &captureEmitter{}
	projection := NewRuntimeChatProjection(NewChatStream(capture, "run-1", "session-1", "main"), capture, "main")
	projection.Start()
	projection.LegacyDelta("callback-only")

	if capture.Count() != 2 {
		t.Fatalf("event count=%d payloads=%#v", capture.Count(), capture.payloads)
	}
	if got := capture.payloads[1].(ChatDeltaEvent); got.DeltaText != "callback-only" || got.Seq != 1 {
		t.Fatalf("delta=%+v", got)
	}
}

func TestRuntimeChatProjectionCancellationIsAbortedAndTerminal(t *testing.T) {
	capture := &captureEmitter{}
	projection := NewRuntimeChatProjection(NewChatStream(capture, "run-1", "session-1", "main"), capture, "main")
	sink := projection.RuntimeEventSink()
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventStreamStart})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantDelta, Delta: "partial"})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventStreamError, Error: "context canceled"})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventAssistantDelta, Delta: "late"})
	sink(agent.RuntimeEvent{Type: agent.RuntimeEventStreamEnd})

	if capture.Count() != 3 {
		t.Fatalf("event count=%d payloads=%#v", capture.Count(), capture.payloads)
	}
	aborted, ok := capture.payloads[2].(ChatAbortedEvent)
	if !ok || aborted.StopReason != "canceled" || aborted.ErrorMessage != "context canceled" {
		t.Fatalf("terminal=%#v", capture.payloads[2])
	}
}

func TestRuntimeChatProjectionNormalizesErrors(t *testing.T) {
	tests := []struct {
		message    string
		kind       ChatErrorKind
		stopReason string
	}{
		{"provider deadline exceeded", ChatErrorTimeout, "timeout"},
		{"HTTP 429 rate_limit exceeded", ChatErrorRateLimit, "rate_limit"},
		{"maximum context length exceeded", ChatErrorContextLength, "context_length"},
		{"request refused by safety policy", ChatErrorRefusal, "refusal"},
		{"provider exploded", ChatErrorUnknown, "provider_error"},
	}
	for _, tc := range tests {
		t.Run(tc.stopReason, func(t *testing.T) {
			capture := &captureEmitter{}
			projection := NewRuntimeChatProjection(NewChatStream(capture, "run", "session", "main"), capture, "main")
			projection.HandleRuntimeEvent(agent.RuntimeEvent{Type: agent.RuntimeEventStreamError, Error: tc.message, Usage: agent.TurnUsage{OutputTokens: 9}})
			if capture.Count() != 1 {
				t.Fatalf("event count=%d", capture.Count())
			}
			got := capture.payloads[0].(ChatErrorEvent)
			if got.ErrorKind != tc.kind || got.StopReason != tc.stopReason || got.ErrorMessage != tc.message {
				t.Fatalf("error=%+v", got)
			}
		})
	}
}
