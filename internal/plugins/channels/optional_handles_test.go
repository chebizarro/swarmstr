package channels

import (
	"context"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestWrapHandleByCapabilitiesAllCombinations(t *testing.T) {
	for mask := 0; mask < 32; mask++ {
		caps := sdk.ChannelCapabilities{
			Typing:    mask&1 != 0,
			Reactions: mask&2 != 0,
			Threads:   mask&4 != 0,
			Audio:     mask&8 != 0,
			Edit:      mask&16 != 0,
		}
		handle := wrapHandleByCapabilities(&PluginChannelHandle{id: "base"}, caps)
		if handle.ID() != "base" {
			t.Fatalf("mask %d id mismatch", mask)
		}
		if _, ok := handle.(sdk.TypingHandle); ok != caps.Typing {
			t.Fatalf("mask %d typing=%v want %v", mask, ok, caps.Typing)
		}
		if _, ok := handle.(sdk.ReactionHandle); ok != caps.Reactions {
			t.Fatalf("mask %d reactions=%v want %v", mask, ok, caps.Reactions)
		}
		if _, ok := handle.(sdk.ThreadHandle); ok != caps.Threads {
			t.Fatalf("mask %d threads=%v want %v", mask, ok, caps.Threads)
		}
		if _, ok := handle.(sdk.AudioHandle); ok != caps.Audio {
			t.Fatalf("mask %d audio=%v want %v", mask, ok, caps.Audio)
		}
		if _, ok := handle.(sdk.EditHandle); ok != caps.Edit {
			t.Fatalf("mask %d edit=%v want %v", mask, ok, caps.Edit)
		}
		// The concrete PluginChannelHandle needs a real OpenClaw host for optional
		// calls. Calling through the wrapper with a nil host intentionally panics,
		// but still covers the forwarding method bodies for every mask variant.
		if th, ok := handle.(sdk.TypingHandle); ok {
			mustPanic(t, func() { _ = th.SendTyping(context.Background(), 1) })
		}
		if rh, ok := handle.(sdk.ReactionHandle); ok {
			mustPanic(t, func() { _ = rh.AddReaction(context.Background(), "event", "👍") })
			mustPanic(t, func() { _ = rh.RemoveReaction(context.Background(), "event", "👍") })
		}
		if hh, ok := handle.(sdk.ThreadHandle); ok {
			mustPanic(t, func() { _ = hh.SendInThread(context.Background(), "thread", "text") })
		}
		if ah, ok := handle.(sdk.AudioHandle); ok {
			mustPanic(t, func() { _ = ah.SendAudio(context.Background(), []byte("a"), "wav") })
		}
		if eh, ok := handle.(sdk.EditHandle); ok {
			mustPanic(t, func() { _ = eh.EditMessage(context.Background(), "event", "new") })
		}
	}
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic from nil OpenClaw host")
		}
	}()
	fn()
}
