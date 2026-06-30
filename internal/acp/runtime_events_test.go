package acp

import (
	"context"
	"errors"
	"testing"
)

type sequenceRuntime struct{ events []RuntimeEvent }

func (r sequenceRuntime) EnsureSession(context.Context, EnsureInput) (RuntimeHandle, error) {
	return RuntimeHandle{}, nil
}
func (r sequenceRuntime) RunTurn(context.Context, TurnInput) (<-chan RuntimeEvent, error) {
	ch := make(chan RuntimeEvent, len(r.events))
	for _, ev := range r.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}
func (r sequenceRuntime) Cancel(context.Context, CancelInput) error { return nil }
func (r sequenceRuntime) Close(context.Context, CloseInput) error   { return nil }

func TestStartAcpRuntimeTurnSeparatesLiveEventsAndTerminalResult(t *testing.T) {
	h, err := StartAcpRuntimeTurn(context.Background(), sequenceRuntime{events: []RuntimeEvent{
		{Kind: EventTextDelta, Text: "hi", Stream: "output", Tag: "agent_message_chunk"},
		{Kind: EventToolCall, Text: "tool", ToolCallID: "tc1", Title: "Read"},
		{Kind: EventDone, StopReason: "complete"},
	}}, TurnInput{RequestID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	var events []AcpRuntimeEvent
	for ev := range h.Events() {
		events = append(events, ev)
	}
	if len(events) != 2 {
		t.Fatalf("live events = %d, want 2", len(events))
	}
	if events[0].Type != "text_delta" || events[0].Text != "hi" || events[0].Stream != "output" {
		t.Fatalf("unexpected text event: %+v", events[0])
	}
	if events[1].Type != "tool_call" || events[1].ToolCallID != "tc1" {
		t.Fatalf("unexpected tool event: %+v", events[1])
	}
	res := <-h.Result()
	if res.Status != "completed" || res.StopReason != "complete" {
		t.Fatalf("result = %+v, want completed", res)
	}
}

func TestMapRuntimeTurnResultBlockedAndFailed(t *testing.T) {
	blocked := RuntimeEvent{Kind: EventDone, StopReason: "blocked"}
	if got := MapRuntimeTurnResult(&blocked, nil); got.Status != "blocked" || got.StopReason != "blocked" {
		t.Fatalf("blocked result = %+v", got)
	}
	failed := RuntimeEvent{Kind: EventError, Text: "boom", Code: "E_BOOM", Retryable: true}
	got := MapRuntimeTurnResult(&failed, nil)
	if got.Status != "failed" || got.Error == nil || got.Error.Code != "E_BOOM" || !got.Error.Retryable {
		t.Fatalf("failed result = %+v", got)
	}
	got = MapRuntimeTurnResult(nil, errors.New("plain"))
	if got.Status != "failed" || got.Error == nil || got.Error.Code != AcpCodeTurnFailed {
		t.Fatalf("error result = %+v", got)
	}
}
