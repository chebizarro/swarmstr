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
		// calls. The host RPC layer guards its nil receiver and returns an error,
		// which still covers the forwarding method bodies for every mask variant.
		if th, ok := handle.(sdk.TypingHandle); ok {
			mustErr(t, th.SendTyping(context.Background(), 1))
		}
		if rh, ok := handle.(sdk.ReactionHandle); ok {
			mustErr(t, rh.AddReaction(context.Background(), "event", "👍"))
			mustErr(t, rh.RemoveReaction(context.Background(), "event", "👍"))
		}
		if hh, ok := handle.(sdk.ThreadHandle); ok {
			mustErr(t, hh.SendInThread(context.Background(), "thread", "text"))
		}
		if ah, ok := handle.(sdk.AudioHandle); ok {
			mustErr(t, ah.SendAudio(context.Background(), []byte("a"), "wav"))
		}
		if eh, ok := handle.(sdk.EditHandle); ok {
			mustErr(t, eh.EditMessage(context.Background(), "event", "new"))
		}
	}
}

func mustErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error from nil OpenClaw host")
	}
}
