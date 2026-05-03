package hooks

import (
	"context"
	"time"

	"metiq/internal/plugins/registry"
)

func (i *HookInvoker) EmitBeforeToolCall(ctx context.Context, event BeforeToolCallEvent) (*BeforeToolCallResult, error) {
	result, err := i.Emit(ctx, registry.HookBeforeToolCall, event, EmitOptions{StopOnReject: true, HandlerTimeout: DefaultHookTimeout})
	out := &BeforeToolCallResult{Approved: true}
	if result != nil {
		out.Approved = !result.Rejected
		out.RejectionReason = result.RejectReason
		args := cloneMap(event.Args)
		for _, mutation := range result.Mutations {
			if direct, ok := asMap(mutation["mutated_args"]); ok {
				args = MergeMap(args, direct)
			}
			if direct, ok := asMap(mutation["args"]); ok {
				args = MergeMap(args, direct)
			}
		}
		if len(args) > 0 {
			out.MutatedArgs = args
		}
	}
	return out, err
}
func (i *HookInvoker) EmitAfterToolCall(ctx context.Context, event AfterToolCallEvent) (*EmitResult, error) {
	return i.Emit(ctx, registry.HookAfterToolCall, event, EmitOptions{HandlerTimeout: DefaultHookTimeout})
}
func (i *HookInvoker) EmitMessageReceived(ctx context.Context, event MessageReceivedEvent) (*EmitResult, error) {
	return i.Emit(ctx, registry.HookMessageReceived, event, EmitOptions{HandlerTimeout: DefaultHookTimeout})
}
func (i *HookInvoker) EmitMessageSending(ctx context.Context, event MessageSendingEvent) (*MessageSendingResult, error) {
	result, err := i.Emit(ctx, registry.HookMessageSending, event, EmitOptions{StopOnReject: true, HandlerTimeout: DefaultHookTimeout})
	out := &MessageSendingResult{Text: event.Text}
	if result != nil {
		out.Reject = result.Rejected
		out.Reason = result.RejectReason
		for _, mutation := range result.Mutations {
			if text, ok := mutation["text"].(string); ok {
				out.Text = text
			}
			if text, ok := mutation["reply_text"].(string); ok {
				out.Text = text
			}
			out.Mutation = MergeMap(out.Mutation, mutation)
		}
	}
	return out, err
}
func (i *HookInvoker) EmitMessageSent(ctx context.Context, event MessageSentEvent) (*EmitResult, error) {
	return i.Emit(ctx, registry.HookMessageSent, event, EmitOptions{HandlerTimeout: DefaultHookTimeout})
}
func DurationMillis(start time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	return time.Since(start).Milliseconds()
}
