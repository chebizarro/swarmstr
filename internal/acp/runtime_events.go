package acp

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// AcpRuntimeEvent is the acp-core-compatible live event emitted while a turn is running.
type AcpRuntimeEvent struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	Stream     string `json:"stream,omitempty"`
	Tag        string `json:"tag,omitempty"`
	Used       int    `json:"used,omitempty"`
	Size       int    `json:"size,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Status     string `json:"status,omitempty"`
	Title      string `json:"title,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	Message    string `json:"message,omitempty"`
	Code       string `json:"code,omitempty"`
	DetailCode string `json:"detailCode,omitempty"`
	Retryable  bool   `json:"retryable,omitempty"`
}

// AcpRuntimeTurnResultError describes a terminal acp-core-compatible turn failure.
type AcpRuntimeTurnResultError struct {
	Message    string `json:"message"`
	Code       string `json:"code,omitempty"`
	DetailCode string `json:"detailCode,omitempty"`
	Retryable  bool   `json:"retryable,omitempty"`
}

// AcpRuntimeTurnResult is the acp-core-compatible terminal turn result, kept separate from live events.
type AcpRuntimeTurnResult struct {
	Status     string                     `json:"status"`
	StopReason string                     `json:"stopReason,omitempty"`
	Error      *AcpRuntimeTurnResultError `json:"error,omitempty"`
}

// AcpRuntimeTurnHandle exposes separated live events and terminal result for a started turn.
type AcpRuntimeTurnHandle interface {
	RequestID() string
	Events() <-chan AcpRuntimeEvent
	Result() <-chan AcpRuntimeTurnResult
	Cancel(ctx context.Context, reason string) error
	CloseStream(ctx context.Context, reason string) error
}

type acpRuntimeTurnHandle struct {
	requestID string
	events    <-chan AcpRuntimeEvent
	result    <-chan AcpRuntimeTurnResult
	cancel    func(context.Context, string) error
	close     func(context.Context, string) error
}

func (h *acpRuntimeTurnHandle) RequestID() string                   { return h.requestID }
func (h *acpRuntimeTurnHandle) Events() <-chan AcpRuntimeEvent      { return h.events }
func (h *acpRuntimeTurnHandle) Result() <-chan AcpRuntimeTurnResult { return h.result }
func (h *acpRuntimeTurnHandle) Cancel(ctx context.Context, reason string) error {
	if h.cancel == nil {
		return nil
	}
	return h.cancel(ctx, reason)
}
func (h *acpRuntimeTurnHandle) CloseStream(ctx context.Context, reason string) error {
	if h.close == nil {
		return nil
	}
	return h.close(ctx, reason)
}

// StartAcpRuntimeTurn adapts the existing BackendRuntime RunTurn stream into an acp-core-style turn handle.
func StartAcpRuntimeTurn(ctx context.Context, runtime BackendRuntime, input TurnInput) (AcpRuntimeTurnHandle, error) {
	ch, err := runtime.RunTurn(ctx, input)
	if err != nil {
		return nil, ToAcpRuntimeError(err, AcpCodeTurnFailed, "ACP turn failed")
	}
	out := make(chan AcpRuntimeEvent)
	result := make(chan AcpRuntimeTurnResult, 1)
	turnCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	finish := func(res AcpRuntimeTurnResult) {
		once.Do(func() {
			result <- res
			close(result)
			close(out)
		})
	}
	go func() {
		for {
			select {
			case <-turnCtx.Done():
				finish(MapRuntimeTurnResult(nil, turnCtx.Err()))
				return
			case ev, ok := <-ch:
				if !ok {
					finish(AcpRuntimeTurnResult{Status: "failed", Error: &AcpRuntimeTurnResultError{Message: "ACP turn ended without terminal event", Code: AcpCodeTurnFailed}})
					return
				}
				mapped := MapRuntimeEvent(ev)
				if ev.Kind.IsTerminal() {
					finish(MapRuntimeTurnResult(&ev, nil))
					return
				}
				select {
				case out <- mapped:
				case <-turnCtx.Done():
					finish(MapRuntimeTurnResult(nil, turnCtx.Err()))
					return
				}
			}
		}
	}()
	return &acpRuntimeTurnHandle{
		requestID: strings.TrimSpace(input.RequestID),
		events:    out,
		result:    result,
		cancel: func(ctx context.Context, reason string) error {
			cancel()
			return runtime.Cancel(ctx, CancelInput{Handle: input.Handle, Reason: reason})
		},
		close: func(context.Context, string) error {
			cancel()
			return nil
		},
	}, nil
}

// MapRuntimeEvent converts swarmstr RuntimeEvent into an acp-core-compatible live event shape.
func MapRuntimeEvent(ev RuntimeEvent) AcpRuntimeEvent {
	mapped := AcpRuntimeEvent{Type: string(ev.Kind), Text: ev.Text, Tag: ev.Tag}
	switch ev.Kind {
	case EventTextDelta:
		mapped.Stream = ev.Stream
	case EventStatus:
		mapped.Used = ev.Used
		mapped.Size = ev.Size
	case EventToolCall, EventApprovalRequest:
		mapped.Type = "tool_call"
		mapped.ToolCallID = ev.ToolCallID
		mapped.Title = ev.Title
		if ev.Kind == EventApprovalRequest {
			mapped.Status = "approval_requested"
		}
	case EventDone:
		mapped.StopReason = ev.StopReason
	case EventError:
		mapped.Message = firstNonEmpty(ev.Text, "ACP turn failed")
		mapped.Code = firstNonEmpty(ev.Code, AcpCodeTurnFailed)
		mapped.Retryable = ev.Retryable
	}
	return mapped
}

// MapRuntimeTurnResult derives the separated terminal result from a terminal event or manager/runtime error.
func MapRuntimeTurnResult(terminal *RuntimeEvent, err error) AcpRuntimeTurnResult {
	if terminal != nil {
		switch terminal.Kind {
		case EventDone:
			status := "completed"
			if strings.EqualFold(terminal.StopReason, "cancelled") || strings.EqualFold(terminal.StopReason, "canceled") {
				status = "cancelled"
			}
			if strings.EqualFold(terminal.StopReason, "blocked") {
				status = "blocked"
			}
			return AcpRuntimeTurnResult{Status: status, StopReason: terminal.StopReason}
		case EventError:
			return AcpRuntimeTurnResult{Status: "failed", Error: &AcpRuntimeTurnResultError{Message: firstNonEmpty(terminal.Text, "ACP turn failed"), Code: firstNonEmpty(terminal.Code, AcpCodeTurnFailed), Retryable: terminal.Retryable}}
		}
	}
	if err == nil {
		return AcpRuntimeTurnResult{Status: "completed"}
	}
	if errors.Is(err, context.Canceled) {
		return AcpRuntimeTurnResult{Status: "cancelled", StopReason: "cancelled"}
	}
	acpErr := ToAcpRuntimeError(err, AcpCodeTurnFailed, "ACP turn failed")
	return AcpRuntimeTurnResult{Status: "failed", Error: &AcpRuntimeTurnResultError{Message: acpErr.Message, Code: acpErr.Code, DetailCode: acpErr.Detail, Retryable: acpErr.Retryable}}
}
