// Package toolbuiltin — channel-specific action tools.
//
// These tools are registered globally but only function when a channel handle
// has been injected into the context via WithChannelHandle.  The channel
// pipeline in main.go calls WithChannelHandle(ctx, rawHandle) before invoking
// ProcessTurn so that tools like add_reaction operate on the correct channel.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"

	"swarmstr/internal/agent"
	"swarmstr/internal/plugins/sdk"
)

// channelHandleKey is the context key used to carry the active channel handle.
type channelHandleKey struct{}

// WithChannelHandle attaches a sdk.ChannelHandle to the context so that
// channel-action tools can retrieve it during tool execution.
func WithChannelHandle(ctx context.Context, h sdk.ChannelHandle) context.Context {
	return context.WithValue(ctx, channelHandleKey{}, h)
}

// ChannelHandleFrom retrieves the sdk.ChannelHandle stored in ctx by
// WithChannelHandle.  Returns nil if none is present.
func ChannelHandleFrom(ctx context.Context) sdk.ChannelHandle {
	h, _ := ctx.Value(channelHandleKey{}).(sdk.ChannelHandle)
	return h
}

// AddReactionTool returns a ToolFunc that attaches an emoji reaction to a
// message.  Requires the channel handle to implement sdk.ReactionHandle.
func AddReactionTool() agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		h := ChannelHandleFrom(ctx)
		if h == nil {
			return "", fmt.Errorf("add_reaction: not in a channel session")
		}
		rh, ok := h.(sdk.ReactionHandle)
		if !ok {
			return "", fmt.Errorf("add_reaction: channel does not support reactions")
		}
		eventID := agent.ArgString(args, "event_id")
		emoji := agent.ArgString(args, "emoji")
		if eventID == "" || emoji == "" {
			return "", fmt.Errorf("add_reaction: event_id and emoji are required")
		}
		if err := rh.AddReaction(ctx, eventID, emoji); err != nil {
			return "", fmt.Errorf("add_reaction: %w", err)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "event_id": eventID, "emoji": emoji})
		return string(b), nil
	}
}

// RemoveReactionTool returns a ToolFunc that removes an emoji reaction from a
// message.  Requires the channel handle to implement sdk.ReactionHandle.
func RemoveReactionTool() agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		h := ChannelHandleFrom(ctx)
		if h == nil {
			return "", fmt.Errorf("remove_reaction: not in a channel session")
		}
		rh, ok := h.(sdk.ReactionHandle)
		if !ok {
			return "", fmt.Errorf("remove_reaction: channel does not support reactions")
		}
		eventID := agent.ArgString(args, "event_id")
		emoji := agent.ArgString(args, "emoji")
		if eventID == "" || emoji == "" {
			return "", fmt.Errorf("remove_reaction: event_id and emoji are required")
		}
		if err := rh.RemoveReaction(ctx, eventID, emoji); err != nil {
			return "", fmt.Errorf("remove_reaction: %w", err)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "event_id": eventID, "emoji": emoji})
		return string(b), nil
	}
}

// SendTypingTool returns a ToolFunc that sends a typing indicator to the
// active channel.  Requires the channel handle to implement sdk.TypingHandle.
func SendTypingTool() agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		h := ChannelHandleFrom(ctx)
		if h == nil {
			return "", fmt.Errorf("send_typing: not in a channel session")
		}
		th, ok := h.(sdk.TypingHandle)
		if !ok {
			return "", fmt.Errorf("send_typing: channel does not support typing indicators")
		}
		durationMS := 0
		if v, ok := args["duration_ms"]; ok {
			switch n := v.(type) {
			case float64:
				durationMS = int(n)
			case int:
				durationMS = n
			}
		}
		if err := th.SendTyping(ctx, durationMS); err != nil {
			return "", fmt.Errorf("send_typing: %w", err)
		}
		b, _ := json.Marshal(map[string]any{"ok": true})
		return string(b), nil
	}
}

// SendInThreadTool returns a ToolFunc that posts a reply into an existing
// thread.  Requires the channel handle to implement sdk.ThreadHandle.
func SendInThreadTool() agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		h := ChannelHandleFrom(ctx)
		if h == nil {
			return "", fmt.Errorf("send_in_thread: not in a channel session")
		}
		th, ok := h.(sdk.ThreadHandle)
		if !ok {
			return "", fmt.Errorf("send_in_thread: channel does not support threads")
		}
		threadID := agent.ArgString(args, "thread_id")
		text := agent.ArgString(args, "text")
		if threadID == "" || text == "" {
			return "", fmt.Errorf("send_in_thread: thread_id and text are required")
		}
		if err := th.SendInThread(ctx, threadID, text); err != nil {
			return "", fmt.Errorf("send_in_thread: %w", err)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "thread_id": threadID})
		return string(b), nil
	}
}

// EditMessageTool returns a ToolFunc that edits a previously sent message.
// Requires the channel handle to implement sdk.EditHandle.
func EditMessageTool() agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		h := ChannelHandleFrom(ctx)
		if h == nil {
			return "", fmt.Errorf("edit_message: not in a channel session")
		}
		eh, ok := h.(sdk.EditHandle)
		if !ok {
			return "", fmt.Errorf("edit_message: channel does not support message editing")
		}
		eventID := agent.ArgString(args, "event_id")
		text := agent.ArgString(args, "text")
		if eventID == "" || text == "" {
			return "", fmt.Errorf("edit_message: event_id and text are required")
		}
		if err := eh.EditMessage(ctx, eventID, text); err != nil {
			return "", fmt.Errorf("edit_message: %w", err)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "event_id": eventID})
		return string(b), nil
	}
}
