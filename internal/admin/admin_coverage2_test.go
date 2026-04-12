package admin

import (
	"testing"

	"metiq/internal/store/state"
)

func TestTruncateText(t *testing.T) {
	if got := truncateText("hello", 10); got != "hello" {
		t.Errorf("expected hello, got %q", got)
	}
	if got := truncateText("hello world", 5); got != "hello" {
		t.Errorf("expected hello, got %q", got)
	}
	if got := truncateText("any", 0); got != "" {
		t.Errorf("expected empty for limit=0, got %q", got)
	}
	if got := truncateText("", 5); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestSessionDocDeleted(t *testing.T) {
	// No meta
	if sessionDocDeleted(state.SessionDoc{}) {
		t.Error("expected false for nil meta")
	}
	// Meta without deleted
	if sessionDocDeleted(state.SessionDoc{Meta: map[string]any{"key": "val"}}) {
		t.Error("expected false when no deleted key")
	}
	// Deleted true
	if !sessionDocDeleted(state.SessionDoc{Meta: map[string]any{"deleted": true}}) {
		t.Error("expected true when deleted=true")
	}
	// Deleted false
	if sessionDocDeleted(state.SessionDoc{Meta: map[string]any{"deleted": false}}) {
		t.Error("expected false when deleted=false")
	}
}

func TestSessionDocActivityMS(t *testing.T) {
	doc := state.SessionDoc{LastReplyAt: 100, LastInboundAt: 200}
	if got := sessionDocActivityMS(doc); got != 200000 {
		t.Errorf("expected 200000, got %d", got)
	}
	doc2 := state.SessionDoc{LastReplyAt: 300, LastInboundAt: 100}
	if got := sessionDocActivityMS(doc2); got != 300000 {
		t.Errorf("expected 300000, got %d", got)
	}
}

func TestNormalizeCompatAgentID(t *testing.T) {
	if got := normalizeCompatAgentID("  MyAgent  "); got != "myagent" {
		t.Errorf("expected myagent, got %q", got)
	}
	// Long ID truncated
	long := make([]rune, 100)
	for i := range long {
		long[i] = 'a'
	}
	got := normalizeCompatAgentID(string(long))
	if len([]rune(got)) != 64 {
		t.Errorf("expected 64 runes, got %d", len([]rune(got)))
	}
}

func TestParseAgentIDFromSessionKey(t *testing.T) {
	if got := parseAgentIDFromSessionKey("agent:myagent:sess-1"); got != "myagent" {
		t.Errorf("expected myagent, got %q", got)
	}
	if got := parseAgentIDFromSessionKey("dm:abc123"); got != "" {
		t.Errorf("expected empty for non-agent key, got %q", got)
	}
	if got := parseAgentIDFromSessionKey(""); got != "" {
		t.Errorf("expected empty for empty key, got %q", got)
	}
	if got := parseAgentIDFromSessionKey("agent:x"); got != "" {
		t.Errorf("expected empty for short key, got %q", got)
	}
}

func TestMergeSessionMeta(t *testing.T) {
	base := map[string]any{"a": 1, "b": 2}
	patch := map[string]any{"b": 3, "c": 4}
	got := mergeSessionMeta(base, patch)
	if got["a"] != 1 {
		t.Error("base key a should be preserved")
	}
	if got["b"] != 3 {
		t.Error("patch key b should override")
	}
	if got["c"] != 4 {
		t.Error("patch key c should be added")
	}

	// nil value deletes
	patch2 := map[string]any{"a": nil}
	got2 := mergeSessionMeta(got, patch2)
	if _, ok := got2["a"]; ok {
		t.Error("nil patch should delete key")
	}
}

func TestMergeSessionMeta_NilBase(t *testing.T) {
	got := mergeSessionMeta(nil, map[string]any{"k": "v"})
	if got["k"] != "v" {
		t.Error("expected k=v")
	}
}

func TestCompactText(t *testing.T) {
	if got := compactText("  hello   world  "); got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
	if got := compactText(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDeriveSessionTitle(t *testing.T) {
	if got := deriveSessionTitle("My Title"); got != "My Title" {
		t.Errorf("expected passthrough, got %q", got)
	}
}
