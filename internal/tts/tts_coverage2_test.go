package tts

import (
	"context"
	"os"
	"testing"
)

type fakeConfiguredProvider struct {
	id     string
	voices []string
}

func (p *fakeConfiguredProvider) ID() string      { return p.id }
func (p *fakeConfiguredProvider) Name() string     { return "Fake " + p.id }
func (p *fakeConfiguredProvider) Voices() []string { return p.voices }
func (p *fakeConfiguredProvider) Configured() bool { return true }
func (p *fakeConfiguredProvider) Convert(_ context.Context, text, voice string) ([]byte, string, error) {
	return []byte("audio:" + text), "mp3", nil
}

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	providers := m.Providers()
	if len(providers) < 2 {
		t.Errorf("expected at least 2 providers, got %d", len(providers))
	}
}

func TestManager_RegisterAndGet(t *testing.T) {
	m := &Manager{providers: map[string]Provider{}}
	fp := &fakeConfiguredProvider{id: "test-prov", voices: []string{"v1"}}
	m.Register(fp)

	got := m.Get("test-prov")
	if got == nil {
		t.Fatal("expected to find test-prov")
	}
	if got.ID() != "test-prov" {
		t.Errorf("expected test-prov, got %s", got.ID())
	}

	// Case insensitive
	got = m.Get("TEST-PROV")
	if got == nil {
		t.Fatal("expected case-insensitive lookup")
	}

	// Not found
	if m.Get("nonexistent") != nil {
		t.Error("expected nil for nonexistent")
	}
}

func TestManager_Providers(t *testing.T) {
	m := &Manager{providers: map[string]Provider{}}
	m.Register(&fakeConfiguredProvider{id: "beta", voices: []string{"v1"}})
	m.Register(&fakeConfiguredProvider{id: "alpha", voices: []string{"v2"}})

	list := m.Providers()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	// Should be sorted
	if list[0]["id"] != "alpha" {
		t.Errorf("expected alpha first, got %s", list[0]["id"])
	}
}

func TestManager_DefaultConfiguredProvider(t *testing.T) {
	m := &Manager{providers: map[string]Provider{}}
	m.Register(&fakeConfiguredProvider{id: "beta"})
	m.Register(&fakeConfiguredProvider{id: "alpha"})

	got := m.DefaultConfiguredProvider()
	if got != "alpha" {
		t.Errorf("expected alpha (first sorted), got %s", got)
	}
}

func TestManager_Convert_Success(t *testing.T) {
	m := &Manager{providers: map[string]Provider{}}
	m.Register(&fakeConfiguredProvider{id: "fake", voices: []string{"v1", "v2"}})

	result, err := m.Convert(context.Background(), "fake", "hello world", "v1")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(result.AudioPath)

	if result.Format != "mp3" {
		t.Errorf("expected mp3, got %s", result.Format)
	}
	if result.Provider != "fake" {
		t.Errorf("expected fake, got %s", result.Provider)
	}
	if result.Voice != "v1" {
		t.Errorf("expected v1, got %s", result.Voice)
	}
	if result.AudioBase64 == "" {
		t.Error("expected base64 for small audio")
	}

	// File should exist
	if _, err := os.Stat(result.AudioPath); err != nil {
		t.Errorf("audio file not found: %v", err)
	}
}

func TestManager_Convert_DefaultVoice(t *testing.T) {
	m := &Manager{providers: map[string]Provider{}}
	m.Register(&fakeConfiguredProvider{id: "fake", voices: []string{"default-voice"}})

	result, err := m.Convert(context.Background(), "fake", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(result.AudioPath)

	if result.Voice != "default-voice" {
		t.Errorf("expected default-voice, got %s", result.Voice)
	}
}

func TestManager_Convert_UnknownProvider(t *testing.T) {
	m := &Manager{providers: map[string]Provider{}}
	_, err := m.Convert(context.Background(), "nonexistent", "text", "")
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}
