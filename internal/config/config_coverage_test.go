package config

import (
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/nostr/events"
)

// ─── parseConfigDurationMS ───────────────────────────────────────────────────

func TestParseConfigDurationMS(t *testing.T) {
	cases := []struct {
		input any
		want  int
		ok    bool
	}{
		{float64(500), 500, true},
		{int(1000), 1000, true},
		{int64(2000), 2000, true},
		{"1s", 1000, true},
		{"500ms", 500, true},
		{"", 0, false},
		{"bad", 0, false},
		{nil, 0, false},
		{true, 0, false},
	}
	for _, c := range cases {
		got, ok := parseConfigDurationMS(c.input)
		if got != c.want || ok != c.ok {
			t.Errorf("parseConfigDurationMS(%v) = %d,%v want %d,%v", c.input, got, ok, c.want, c.ok)
		}
	}
}

// ─── ConfigFileExists / ConfigFileModTime ────────────────────────────────────

func TestConfigFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if ConfigFileExists(path) {
		t.Error("should not exist yet")
	}
	os.WriteFile(path, []byte("{}"), 0644)
	if !ConfigFileExists(path) {
		t.Error("should exist")
	}
	// Directory should not count
	if ConfigFileExists(dir) {
		t.Error("directory should not count as file")
	}
}

func TestConfigFileModTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Non-existent
	mt := ConfigFileModTime(path)
	if !mt.IsZero() {
		t.Error("should be zero for missing file")
	}

	os.WriteFile(path, []byte("{}"), 0644)
	mt = ConfigFileModTime(path)
	if mt.IsZero() {
		t.Error("should not be zero for existing file")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	p, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if p == "" {
		t.Error("should return a path")
	}
}

// ─── IsBunkerURL ─────────────────────────────────────────────────────────────

func TestIsBunkerURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"", false},
		{"nsec1abc", false},
		{"bunker://remote", true},
		{"nostrconnect://relay", true},
		{"BUNKER://relay", true},
		{"file:///path", false},
	}
	for _, c := range cases {
		cfg := BootstrapConfig{SignerURL: c.url}
		if got := IsBunkerURL(cfg); got != c.want {
			t.Errorf("IsBunkerURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// ─── KeypairFromHex ──────────────────────────────────────────────────────────

func TestKeypairFromHex(t *testing.T) {
	hex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	nsec, npub, err := KeypairFromHex(hex)
	if err != nil {
		t.Fatal(err)
	}
	if nsec == "" || npub == "" {
		t.Error("should return non-empty values")
	}
}

func TestKeypairFromHex_InvalidHex(t *testing.T) {
	_, _, err := KeypairFromHex("not-hex")
	if err == nil {
		t.Error("expected error")
	}
}

func TestKeypairFromHex_WrongLength(t *testing.T) {
	_, _, err := KeypairFromHex("0123456789abcdef")
	if err == nil {
		t.Error("expected error for short key")
	}
}

// ─── EffectiveStateKind / EffectiveTranscriptKind ────────────────────────────

func TestEffectiveStateKind(t *testing.T) {
	cfg := BootstrapConfig{}
	if got := cfg.EffectiveStateKind(); got != events.KindStateDoc {
		t.Errorf("default: %d", got)
	}
	cfg.StateKind = 30078
	if got := cfg.EffectiveStateKind(); got != events.Kind(30078) {
		t.Errorf("custom: %d", got)
	}
}

func TestEffectiveTranscriptKind(t *testing.T) {
	cfg := BootstrapConfig{}
	if got := cfg.EffectiveTranscriptKind(); got != events.KindTranscriptDoc {
		t.Errorf("default: %d", got)
	}
	cfg.TranscriptKind = 31234
	if got := cfg.EffectiveTranscriptKind(); got != events.Kind(31234) {
		t.Errorf("custom: %d", got)
	}
}

// ─── Redact edge cases ──────────────────────────────────────────────────────

func TestRedactSlice(t *testing.T) {
	result := redactSlice(nil)
	if result != nil {
		t.Error("nil should return nil")
	}

	result = redactSlice([]any{"hello", map[string]any{"api_key": "secret"}})
	if len(result) != 2 {
		t.Errorf("len: %d", len(result))
	}
	m, ok := result[1].(map[string]any)
	if !ok {
		t.Fatal("expected map")
	}
	if m["api_key"] != RedactedValue {
		t.Errorf("api_key not redacted: %v", m["api_key"])
	}
}
