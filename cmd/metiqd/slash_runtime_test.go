package main

import (
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func TestApplyFastSlash_TogglesAndPersists(t *testing.T) {
	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sessionID := "sess-fast"

	if got := applyFastSlash(ss, sessionID, []string{"on"}); !strings.Contains(got, "enabled") {
		t.Fatalf("unexpected response: %q", got)
	}
	se, ok := ss.Get(sessionID)
	if !ok || !se.FastMode {
		t.Fatalf("expected fast mode persisted on: %+v", se)
	}

	if got := applyFastSlash(ss, sessionID, []string{"off"}); !strings.Contains(got, "disabled") {
		t.Fatalf("unexpected response: %q", got)
	}
	se, ok = ss.Get(sessionID)
	if !ok || se.FastMode {
		t.Fatalf("expected fast mode persisted off: %+v", se)
	}
}

func TestApplyUsageSlash_SetAndReport(t *testing.T) {
	ss, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sessionID := "sess-usage"

	if got := applyUsageSlash(ss, sessionID, []string{"full"}); !strings.Contains(got, "set to full") {
		t.Fatalf("unexpected set response: %q", got)
	}
	se, ok := ss.Get(sessionID)
	if !ok || se.ResponseUsage != "full" {
		t.Fatalf("expected response usage persisted: %+v", se)
	}
	if err := ss.AddTokens(sessionID, 120, 45, 0, 0); err != nil {
		t.Fatalf("add tokens: %v", err)
	}
	report := applyUsageSlash(ss, sessionID, nil)
	for _, want := range []string{
		"Usage mode: full",
		"Input tokens: 120",
		"Output tokens: 45",
		"Total tokens: 165",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("missing %q in report:\n%s", want, report)
		}
	}
}
