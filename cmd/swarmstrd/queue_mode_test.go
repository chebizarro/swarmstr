package main

import (
	"testing"

	"swarmstr/internal/autoreply"
	"swarmstr/internal/store/state"
)

func TestResolveQueueRuntimeSettings_Defaults(t *testing.T) {
	cfg := state.ConfigDoc{}
	q := resolveQueueRuntimeSettings(cfg, nil, "", 10)
	if q.Mode != "collect" || q.Cap != 10 || q.Drop != autoreply.QueueDropSummarize {
		t.Fatalf("unexpected defaults: %+v", q)
	}
}

func TestResolveQueueRuntimeSettings_Precedence(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{
		"messages": map[string]any{
			"queue": map[string]any{
				"mode": "followup",
				"cap":  12.0,
				"drop": "oldest",
				"by_channel": map[string]any{
					"telegram-main": "steer",
				},
			},
		},
	}}
	session := &state.SessionEntry{QueueMode: "queue", QueueCap: 25, QueueDrop: "newest"}

	q := resolveQueueRuntimeSettings(cfg, session, "telegram-main", 20)
	if q.Mode != "queue" {
		t.Fatalf("session mode should win, got %q", q.Mode)
	}
	if q.Cap != 25 {
		t.Fatalf("session cap should win, got %d", q.Cap)
	}
	if q.Drop != autoreply.QueueDropNewest {
		t.Fatalf("session drop should win, got %q", q.Drop)
	}
}

func TestResolveQueueRuntimeSettings_ChannelOverride(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{
		"messages": map[string]any{
			"queue": map[string]any{
				"mode": "collect",
				"by_channel": map[string]any{
					"discord-main": "steer-backlog",
				},
			},
		},
	}}
	q := resolveQueueRuntimeSettings(cfg, nil, "discord-main", 20)
	if q.Mode != "steer-backlog" {
		t.Fatalf("channel override mismatch: %q", q.Mode)
	}
}

func TestQueueModeHelpers(t *testing.T) {
	if !queueModeCollect("collect") || !queueModeCollect("") {
		t.Fatal("collect mode helper mismatch")
	}
	for _, m := range []string{"followup", "queue", "steer-backlog", "steer+backlog"} {
		if !queueModeSequential(m) {
			t.Fatalf("expected sequential mode for %q", m)
		}
	}
	if queueModeSequential("collect") {
		t.Fatal("collect must not be sequential")
	}
}
