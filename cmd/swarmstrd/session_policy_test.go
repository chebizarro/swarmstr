package main

import (
	"testing"
	"time"

	"swarmstr/internal/store/state"
)

func TestParseResetTrigger_StripsStructuralPrefixes(t *testing.T) {
	trigger, remainder := parseResetTrigger("[Dec 4 17:35] /new continue with this")
	if trigger != "/new" {
		t.Fatalf("trigger = %q, want /new", trigger)
	}
	if remainder != "continue with this" {
		t.Fatalf("remainder = %q", remainder)
	}
}

func TestResolveSessionFreshnessPolicy_ByTypeAndChannel(t *testing.T) {
	cfg := state.ConfigDoc{
		Extra: map[string]any{
			"session_reset": map[string]any{
				"default": map[string]any{"idle_minutes": 120.0, "daily_reset": false},
				"group":   map[string]any{"idle_minutes": 30.0},
				"thread":  map[string]any{"daily_reset": true},
				"channels": map[string]any{
					"telegram-main": map[string]any{"idle_minutes": 5.0},
				},
			},
		},
	}

	direct := resolveSessionFreshnessPolicy(cfg, "direct", "")
	if direct.IdleMinutes != 120 || direct.DailyReset {
		t.Fatalf("direct policy mismatch: %+v", direct)
	}
	group := resolveSessionFreshnessPolicy(cfg, "group", "")
	if group.IdleMinutes != 30 || group.DailyReset {
		t.Fatalf("group policy mismatch: %+v", group)
	}
	thread := resolveSessionFreshnessPolicy(cfg, "thread", "")
	if thread.IdleMinutes != 120 || !thread.DailyReset {
		t.Fatalf("thread policy mismatch: %+v", thread)
	}
	channel := resolveSessionFreshnessPolicy(cfg, "group", "telegram-main")
	if channel.IdleMinutes != 5 {
		t.Fatalf("channel override mismatch: %+v", channel)
	}
}

func TestShouldAutoRotateSession_IdleAndDaily(t *testing.T) {
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.Local)
	entry := state.SessionEntry{UpdatedAt: now.Add(-2 * time.Hour)}

	if !shouldAutoRotateSession(entry, now, sessionFreshnessPolicy{IdleMinutes: 60}) {
		t.Fatal("expected idle reset")
	}
	if shouldAutoRotateSession(entry, now, sessionFreshnessPolicy{IdleMinutes: 180}) {
		t.Fatal("unexpected idle reset")
	}

	yesterday := state.SessionEntry{UpdatedAt: now.Add(-24 * time.Hour)}
	if !shouldAutoRotateSession(yesterday, now, sessionFreshnessPolicy{DailyReset: true}) {
		t.Fatal("expected daily reset")
	}
	if shouldAutoRotateSession(yesterday, now, sessionFreshnessPolicy{DailyReset: false}) {
		t.Fatal("unexpected daily reset")
	}
}
