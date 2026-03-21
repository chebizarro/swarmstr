package main

import (
	"errors"
	"strings"
	"testing"

	hookspkg "metiq/internal/hooks"
	"metiq/internal/store/state"
)

func TestFireSessionResetHooks_OrderAndPayload(t *testing.T) {
	mgr := hookspkg.NewManager()
	var order []string
	var beforeCtx map[string]any
	var endCtx map[string]any

	mgr.Register(&hookspkg.Hook{
		HookKey: "before",
		Source:  hookspkg.SourceBundled,
		Manifest: hookspkg.HookManifest{Metadata: &hookspkg.HookMetaWrap{OpenClaw: &hookspkg.OpenClawHookMeta{
			Events: []string{"session:before_reset"},
		}}},
		Handler: func(event *hookspkg.Event) error {
			order = append(order, event.Name)
			beforeCtx = event.Context
			return nil
		},
	})
	mgr.Register(&hookspkg.Hook{
		HookKey: "end",
		Source:  hookspkg.SourceBundled,
		Manifest: hookspkg.HookManifest{Metadata: &hookspkg.HookMetaWrap{OpenClaw: &hookspkg.OpenClawHookMeta{
			Events: []string{"session:end"},
		}}},
		Handler: func(event *hookspkg.Event) error {
			order = append(order, event.Name)
			endCtx = event.Context
			return nil
		},
	})

	entries := []state.TranscriptEntryDoc{
		{EntryID: "u1", Role: "user", Text: "hello", Unix: 1},
		{EntryID: "a1", Role: "assistant", Text: "world", Unix: 2},
		{EntryID: "d1", Role: "deleted", Text: "ignore", Unix: 3},
	}
	fireSessionResetHooks(mgr, "acp:test", "slash:/reset", true, entries)

	if len(order) != 2 {
		t.Fatalf("expected 2 hook events, got %d (%v)", len(order), order)
	}
	if order[0] != "session:before_reset" || order[1] != "session:end" {
		t.Fatalf("unexpected hook ordering: %v", order)
	}
	if beforeCtx == nil {
		t.Fatal("missing before_reset context")
	}
	if got, _ := beforeCtx["session_id"].(string); got != "acp:test" {
		t.Fatalf("before_reset session_id mismatch: %q", got)
	}
	if got, _ := beforeCtx["trigger"].(string); got != "slash:/reset" {
		t.Fatalf("before_reset trigger mismatch: %q", got)
	}
	if got, _ := beforeCtx["acp"].(bool); !got {
		t.Fatalf("before_reset acp mismatch: %v", beforeCtx["acp"])
	}
	if got, _ := beforeCtx["previous_message_count"].(int); got != 2 {
		t.Fatalf("before_reset previous_message_count mismatch: %v", beforeCtx["previous_message_count"])
	}
	if transcript, _ := beforeCtx["previous_transcript"].(string); !strings.Contains(transcript, "user: hello") {
		t.Fatalf("before_reset transcript missing expected content: %q", transcript)
	}
	if endCtx == nil {
		t.Fatal("missing session:end context")
	}
	if got, _ := endCtx["trigger"].(string); got != "slash:/reset" {
		t.Fatalf("session:end trigger mismatch: %q", got)
	}
	if got, _ := endCtx["acp"].(bool); !got {
		t.Fatalf("session:end acp mismatch: %v", endCtx["acp"])
	}
	if got, _ := endCtx["previous_message_count"].(int); got != 2 {
		t.Fatalf("session:end previous_message_count mismatch: %v", endCtx["previous_message_count"])
	}
}

func TestFireSessionResetHooks_HookErrorsAreNonFatal(t *testing.T) {
	mgr := hookspkg.NewManager()
	var order []string
	mgr.Register(&hookspkg.Hook{
		HookKey: "before",
		Source:  hookspkg.SourceBundled,
		Manifest: hookspkg.HookManifest{Metadata: &hookspkg.HookMetaWrap{OpenClaw: &hookspkg.OpenClawHookMeta{
			Events: []string{"session:before_reset"},
		}}},
		Handler: func(event *hookspkg.Event) error {
			order = append(order, event.Name)
			return errors.New("boom")
		},
	})
	mgr.Register(&hookspkg.Hook{
		HookKey: "end",
		Source:  hookspkg.SourceBundled,
		Manifest: hookspkg.HookManifest{Metadata: &hookspkg.HookMetaWrap{OpenClaw: &hookspkg.OpenClawHookMeta{
			Events: []string{"session:end"},
		}}},
		Handler: func(event *hookspkg.Event) error {
			order = append(order, event.Name)
			return nil
		},
	})

	fireSessionResetHooks(mgr, "sess-1", "slash:/new", false, nil)

	if len(order) != 2 {
		t.Fatalf("expected both hook events to fire despite error, got %v", order)
	}
	if order[0] != "session:before_reset" || order[1] != "session:end" {
		t.Fatalf("unexpected ordering with hook errors: %v", order)
	}
}
