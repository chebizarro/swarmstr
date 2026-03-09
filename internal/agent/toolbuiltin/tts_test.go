package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"swarmstr/internal/tts"
)

// stubTTSProvider is a minimal tts.Provider for testing.
type stubTTSProvider struct {
	id         string
	configured bool
}

func (s *stubTTSProvider) ID() string         { return s.id }
func (s *stubTTSProvider) Name() string       { return "Stub/" + s.id }
func (s *stubTTSProvider) Voices() []string   { return []string{"stub-voice"} }
func (s *stubTTSProvider) Configured() bool   { return s.configured }
func (s *stubTTSProvider) Convert(_ context.Context, text, voice string) ([]byte, string, error) {
	return []byte("audio:" + text), "mp3", nil
}

func newStubTTSManager(providerID string, configured bool) *tts.Manager {
	m := tts.NewManager()
	m.Register(&stubTTSProvider{id: providerID, configured: configured})
	return m
}

func TestTTSTool_MissingText(t *testing.T) {
	m := newStubTTSManager("stub", true)
	tool := TTSTool(m)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing text")
	}
}

func TestTTSTool_NoConfiguredProvider(t *testing.T) {
	m := newStubTTSManager("stub", false) // not configured
	tool := TTSTool(m)
	_, err := tool(context.Background(), map[string]any{"text": "hello"})
	if err == nil {
		t.Error("expected error when no provider is configured")
	}
}

func TestTTSTool_UnknownProvider(t *testing.T) {
	m := newStubTTSManager("stub", true)
	tool := TTSTool(m)
	_, err := tool(context.Background(), map[string]any{
		"text":     "hello",
		"provider": "doesnotexist",
	})
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestTTSTool_Success(t *testing.T) {
	m := newStubTTSManager("stub", true)
	tool := TTSTool(m)

	result, err := tool(context.Background(), map[string]any{
		"text":     "hello world",
		"provider": "stub",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should start with the MEDIA: prefix.
	if !strings.HasPrefix(result, MediaPrefix) {
		t.Errorf("expected result to start with %q, got: %q", MediaPrefix, result)
	}

	// After the first newline, should be valid JSON.
	parts := strings.SplitN(result, "\n", 2)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts separated by newline, got: %q", result)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(parts[1]), &out); err != nil {
		t.Fatalf("parse JSON part: %v", err)
	}
	if out["provider"] != "stub" {
		t.Errorf("expected provider=stub, got %v", out["provider"])
	}
	if out["format"] != "mp3" {
		t.Errorf("expected format=mp3, got %v", out["format"])
	}
	if out["audio_path"] == "" {
		t.Error("expected non-empty audio_path")
	}
}

func TestTTSTool_DefaultProviderAutoDetect(t *testing.T) {
	m := newStubTTSManager("alpha", true) // configured; will be first alphabetically after "elevenlabs" etc.
	tool := TTSTool(m)

	// Don't specify provider — tool should auto-detect from the manager.
	result, err := tool(context.Background(), map[string]any{"text": "auto"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(result, MediaPrefix) {
		t.Errorf("expected MEDIA: prefix, got: %q", result)
	}
}

func TestDefaultConfiguredProvider(t *testing.T) {
	m := tts.NewManager() // no env vars set — built-ins likely unconfigured
	// Just verify it returns a string (may be "" in CI).
	_ = m.DefaultConfiguredProvider()

	m2 := tts.NewManager()
	m2.Register(&stubTTSProvider{id: "zzz-stub", configured: true})
	p := m2.DefaultConfiguredProvider()
	if p == "" {
		t.Error("expected a configured provider to be found")
	}
}
