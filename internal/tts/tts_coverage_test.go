package tts

import (
	"context"
	"testing"
)

// ─── resolveVoiceID ──────────────────────────────────────────────────────────

func TestResolveVoiceID_ByName(t *testing.T) {
	p := &ElevenLabsProvider{}
	got := p.resolveVoiceID("Rachel")
	if got != "21m00Tcm4TlvDq8ikWAM" {
		t.Errorf("Rachel: %q", got)
	}
}

func TestResolveVoiceID_CaseInsensitive(t *testing.T) {
	p := &ElevenLabsProvider{}
	got := p.resolveVoiceID("rachel")
	if got != "21m00Tcm4TlvDq8ikWAM" {
		t.Errorf("rachel: %q", got)
	}
}

func TestResolveVoiceID_ByID(t *testing.T) {
	p := &ElevenLabsProvider{}
	got := p.resolveVoiceID("21m00Tcm4TlvDq8ikWAM")
	if got != "21m00Tcm4TlvDq8ikWAM" {
		t.Errorf("by ID: %q", got)
	}
}

func TestResolveVoiceID_Unknown(t *testing.T) {
	p := &ElevenLabsProvider{}
	got := p.resolveVoiceID("custom-voice-id")
	if got != "custom-voice-id" {
		t.Errorf("passthrough: %q", got)
	}
}

// ─── DefaultConfiguredProvider ───────────────────────────────────────────────

type fakeProvider struct {
	id         string
	configured bool
}

func (f *fakeProvider) ID() string   { return f.id }
func (f *fakeProvider) Name() string { return f.id }
func (f *fakeProvider) Voices() []string { return nil }
func (f *fakeProvider) Configured() bool { return f.configured }
func (f *fakeProvider) Convert(ctx context.Context, text, voice string) ([]byte, string, error) {
	return nil, "", nil
}

func TestDefaultConfiguredProvider(t *testing.T) {
	m := NewManager()
	m.Register(&fakeProvider{id: "zz-provider", configured: false})
	m.Register(&fakeProvider{id: "aa-provider", configured: true})

	got := m.DefaultConfiguredProvider()
	if got != "aa-provider" {
		t.Errorf("expected aa-provider (alphabetical first configured), got %q", got)
	}
}

func TestDefaultConfiguredProvider_None(t *testing.T) {
	m := NewManager()
	m.Register(&fakeProvider{id: "unconfigured", configured: false})

	got := m.DefaultConfiguredProvider()
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDefaultConfiguredProvider_Empty(t *testing.T) {
	m := NewManager()
	got := m.DefaultConfiguredProvider()
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
