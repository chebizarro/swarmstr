package toolbuiltin_test

import (
	"context"
	"errors"
	"testing"

	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/plugins/sdk"
)

// ── Stub handles ──────────────────────────────────────────────────────────────

type stubBase struct{ id string }

func (s *stubBase) ID() string                             { return s.id }
func (s *stubBase) Send(_ context.Context, _ string) error { return nil }
func (s *stubBase) Close()                                 {}

type stubReactionHandle struct {
	stubBase
	added   []string
	removed []string
	err     error
}

func (s *stubReactionHandle) AddReaction(_ context.Context, eventID, emoji string) error {
	if s.err != nil {
		return s.err
	}
	s.added = append(s.added, eventID+":"+emoji)
	return nil
}

func (s *stubReactionHandle) RemoveReaction(_ context.Context, eventID, emoji string) error {
	if s.err != nil {
		return s.err
	}
	s.removed = append(s.removed, eventID+":"+emoji)
	return nil
}

type stubTypingHandle struct {
	stubBase
	durations []int
	err       error
}

func (s *stubTypingHandle) SendTyping(_ context.Context, durationMS int) error {
	if s.err != nil {
		return s.err
	}
	s.durations = append(s.durations, durationMS)
	return nil
}

type stubThreadHandle struct {
	stubBase
	sent []string
	err  error
}

func (s *stubThreadHandle) SendInThread(_ context.Context, threadID, text string) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, threadID+":"+text)
	return nil
}

type stubEditHandle struct {
	stubBase
	edits []string
	err   error
}

func (s *stubEditHandle) EditMessage(_ context.Context, eventID, newText string) error {
	if s.err != nil {
		return s.err
	}
	s.edits = append(s.edits, eventID+":"+newText)
	return nil
}

// Verify interfaces are satisfied.
var _ sdk.ReactionHandle = (*stubReactionHandle)(nil)
var _ sdk.TypingHandle = (*stubTypingHandle)(nil)
var _ sdk.ThreadHandle = (*stubThreadHandle)(nil)
var _ sdk.EditHandle = (*stubEditHandle)(nil)

// ── WithChannelHandle / ChannelHandleFrom ─────────────────────────────────────

func TestChannelHandleContext(t *testing.T) {
	ctx := context.Background()
	if toolbuiltin.ChannelHandleFrom(ctx) != nil {
		t.Fatal("expected nil handle from empty context")
	}
	h := &stubBase{id: "ch1"}
	ctx2 := toolbuiltin.WithChannelHandle(ctx, h)
	if toolbuiltin.ChannelHandleFrom(ctx2) != h {
		t.Fatal("handle not stored in context")
	}
}

// ── add_reaction ─────────────────────────────────────────────────────────────

func TestAddReaction_NoHandle(t *testing.T) {
	fn := toolbuiltin.AddReactionTool()
	_, err := fn(context.Background(), map[string]any{"event_id": "e1", "emoji": "👍"})
	if err == nil || err.Error() == "" {
		t.Fatal("expected error when no channel handle in context")
	}
}

func TestAddReaction_NoReactionSupport(t *testing.T) {
	fn := toolbuiltin.AddReactionTool()
	ctx := toolbuiltin.WithChannelHandle(context.Background(), &stubBase{id: "ch1"})
	_, err := fn(ctx, map[string]any{"event_id": "e1", "emoji": "👍"})
	if err == nil {
		t.Fatal("expected error when handle does not support reactions")
	}
}

func TestAddReaction_MissingArgs(t *testing.T) {
	fn := toolbuiltin.AddReactionTool()
	h := &stubReactionHandle{stubBase: stubBase{id: "ch1"}}
	ctx := toolbuiltin.WithChannelHandle(context.Background(), h)
	_, err := fn(ctx, map[string]any{"event_id": "e1"})
	if err == nil {
		t.Fatal("expected error for missing emoji")
	}
}

func TestAddReaction_Success(t *testing.T) {
	fn := toolbuiltin.AddReactionTool()
	h := &stubReactionHandle{stubBase: stubBase{id: "ch1"}}
	ctx := toolbuiltin.WithChannelHandle(context.Background(), h)
	out, err := fn(ctx, map[string]any{"event_id": "e1", "emoji": "👍"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.added) != 1 || h.added[0] != "e1:👍" {
		t.Fatalf("reaction not recorded: %v", h.added)
	}
	if out == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestAddReaction_Error(t *testing.T) {
	fn := toolbuiltin.AddReactionTool()
	h := &stubReactionHandle{stubBase: stubBase{id: "ch1"}, err: errors.New("rate limited")}
	ctx := toolbuiltin.WithChannelHandle(context.Background(), h)
	_, err := fn(ctx, map[string]any{"event_id": "e1", "emoji": "👍"})
	if err == nil {
		t.Fatal("expected error from handle")
	}
}

// ── remove_reaction ───────────────────────────────────────────────────────────

func TestRemoveReaction_Success(t *testing.T) {
	fn := toolbuiltin.RemoveReactionTool()
	h := &stubReactionHandle{stubBase: stubBase{id: "ch1"}}
	ctx := toolbuiltin.WithChannelHandle(context.Background(), h)
	_, err := fn(ctx, map[string]any{"event_id": "e1", "emoji": "👍"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.removed) != 1 || h.removed[0] != "e1:👍" {
		t.Fatalf("removal not recorded: %v", h.removed)
	}
}

// ── send_typing ───────────────────────────────────────────────────────────────

func TestSendTyping_Success(t *testing.T) {
	fn := toolbuiltin.SendTypingTool()
	h := &stubTypingHandle{stubBase: stubBase{id: "ch1"}}
	ctx := toolbuiltin.WithChannelHandle(context.Background(), h)
	_, err := fn(ctx, map[string]any{"duration_ms": float64(500)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.durations) != 1 || h.durations[0] != 500 {
		t.Fatalf("duration not recorded: %v", h.durations)
	}
}

func TestSendTyping_NoHandle(t *testing.T) {
	fn := toolbuiltin.SendTypingTool()
	_, err := fn(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when no handle")
	}
}

func TestSendTyping_NotSupported(t *testing.T) {
	fn := toolbuiltin.SendTypingTool()
	ctx := toolbuiltin.WithChannelHandle(context.Background(), &stubBase{id: "ch1"})
	_, err := fn(ctx, nil)
	if err == nil {
		t.Fatal("expected error when channel does not support typing")
	}
}

// ── send_in_thread ────────────────────────────────────────────────────────────

func TestSendInThread_Success(t *testing.T) {
	fn := toolbuiltin.SendInThreadTool()
	h := &stubThreadHandle{stubBase: stubBase{id: "ch1"}}
	ctx := toolbuiltin.WithChannelHandle(context.Background(), h)
	_, err := fn(ctx, map[string]any{"thread_id": "t1", "text": "hello thread"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.sent) != 1 || h.sent[0] != "t1:hello thread" {
		t.Fatalf("sent not recorded: %v", h.sent)
	}
}

func TestSendInThread_MissingArgs(t *testing.T) {
	fn := toolbuiltin.SendInThreadTool()
	h := &stubThreadHandle{stubBase: stubBase{id: "ch1"}}
	ctx := toolbuiltin.WithChannelHandle(context.Background(), h)
	_, err := fn(ctx, map[string]any{"thread_id": "t1"})
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

// ── edit_message ──────────────────────────────────────────────────────────────

func TestEditMessage_Success(t *testing.T) {
	fn := toolbuiltin.EditMessageTool()
	h := &stubEditHandle{stubBase: stubBase{id: "ch1"}}
	ctx := toolbuiltin.WithChannelHandle(context.Background(), h)
	_, err := fn(ctx, map[string]any{"event_id": "m1", "text": "updated"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.edits) != 1 || h.edits[0] != "m1:updated" {
		t.Fatalf("edit not recorded: %v", h.edits)
	}
}

func TestEditMessage_NotSupported(t *testing.T) {
	fn := toolbuiltin.EditMessageTool()
	ctx := toolbuiltin.WithChannelHandle(context.Background(), &stubBase{id: "ch1"})
	_, err := fn(ctx, map[string]any{"event_id": "m1", "text": "updated"})
	if err == nil {
		t.Fatal("expected error when channel does not support editing")
	}
}
