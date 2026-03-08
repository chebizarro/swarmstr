// Package tts implements text-to-speech synthesis for swarmstr.
// It provides a Provider interface with an OpenAI TTS backend and a local
// Kokoro backend, wrapped by a Manager that handles provider selection and
// audio output persistence.
package tts

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Provider is the interface for TTS synthesis backends.
type Provider interface {
	// ID returns the stable provider identifier (e.g. "openai").
	ID() string
	// Name returns the human-readable display name.
	Name() string
	// Voices returns the list of supported voice identifiers.
	Voices() []string
	// Configured returns true if this provider is usable (e.g. API key present).
	Configured() bool
	// Convert synthesises text to audio bytes.
	// Returns raw audio data, the format name (e.g. "mp3", "wav"), and any error.
	Convert(ctx context.Context, text, voice string) ([]byte, string, error)
}

// Manager manages TTS providers and audio output.
type Manager struct {
	providers map[string]Provider
}

// NewManager creates a Manager pre-loaded with all built-in providers.
func NewManager() *Manager {
	m := &Manager{
		providers: map[string]Provider{},
	}
	m.Register(&OpenAIProvider{})
	m.Register(&KokoroProvider{})
	return m
}

// Register adds a provider to the manager (replacing any with the same ID).
func (m *Manager) Register(p Provider) {
	m.providers[strings.ToLower(p.ID())] = p
}

// Get returns the provider with the given ID (case-insensitive), or nil.
func (m *Manager) Get(id string) Provider {
	return m.providers[strings.ToLower(id)]
}

// Providers returns all registered providers sorted by ID, with availability info.
func (m *Manager) Providers() []map[string]any {
	ids := make([]string, 0, len(m.providers))
	for id := range m.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		p := m.providers[id]
		result = append(result, map[string]any{
			"id":         p.ID(),
			"name":       p.Name(),
			"configured": p.Configured(),
			"voices":     p.Voices(),
		})
	}
	return result
}

// ConvertResult holds the output of a Convert call.
type ConvertResult struct {
	// AudioPath is the path to the temporary audio file (caller should clean up if desired).
	AudioPath string
	// AudioBase64 contains the base64-encoded audio for small outputs (≤ 512 KiB).
	// Empty for large outputs — fetch from AudioPath instead.
	AudioBase64 string
	// Format is the audio format name, e.g. "mp3" or "wav".
	Format string
	// Provider is the provider ID that synthesised the audio.
	Provider string
	// Voice is the voice that was used.
	Voice string
}

// Convert synthesises text using the named provider, writes the audio to a
// temporary file, and returns a ConvertResult.
func (m *Manager) Convert(ctx context.Context, providerID, text, voice string) (*ConvertResult, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	p, ok := m.providers[providerID]
	if !ok {
		return nil, fmt.Errorf("unknown TTS provider %q", providerID)
	}
	if !p.Configured() {
		return nil, fmt.Errorf("TTS provider %q is not configured (check environment variables)", providerID)
	}

	if voice == "" {
		voices := p.Voices()
		if len(voices) > 0 {
			voice = voices[0]
		}
	}

	data, format, err := p.Convert(ctx, text, voice)
	if err != nil {
		return nil, err
	}

	ext := "." + strings.TrimPrefix(format, ".")
	f, ferr := os.CreateTemp("", "swarmstr-tts-*"+ext)
	if ferr != nil {
		return nil, fmt.Errorf("create temp file: %w", ferr)
	}
	if _, werr := f.Write(data); werr != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, fmt.Errorf("write audio file: %w", werr)
	}
	f.Close()

	// Inline base64 for small outputs (≤ 512 KiB).
	audioB64 := ""
	if len(data) <= 512*1024 {
		audioB64 = base64.StdEncoding.EncodeToString(data)
	}

	return &ConvertResult{
		AudioPath:   filepath.ToSlash(f.Name()),
		AudioBase64: audioB64,
		Format:      format,
		Provider:    providerID,
		Voice:       voice,
	}, nil
}
