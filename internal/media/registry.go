// Package media — transcriber registry.
//
// The registry allows the daemon to select a transcription backend by name
// (e.g. "openai", "groq", "deepgram") based on the media_understanding config
// field. Providers are registered at init time.
package media

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// TranscriberFactory constructs a Transcriber.  It is called once per daemon
// startup; the result is kept for the lifetime of the process.
type TranscriberFactory func() Transcriber

var (
	registryMu sync.RWMutex
	registry   = map[string]TranscriberFactory{}
	regOrder   []string
)

// RegisterTranscriber registers a Transcriber factory under the given name.
// Names are case-insensitive. Panics on duplicate registration.
func RegisterTranscriber(name string, factory TranscriberFactory) {
	key := strings.ToLower(name)
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[key]; ok {
		panic(fmt.Sprintf("transcriber %q already registered", name))
	}
	registry[key] = factory
	regOrder = append(regOrder, key)
}

// ListTranscribers returns the names of all registered transcriber backends,
// in registration order.
func ListTranscribers() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, len(regOrder))
	copy(out, regOrder)
	return out
}

// ResolveTranscriber returns a Transcriber for the given backend name.
// Returns an error if the name is not registered.
func ResolveTranscriber(name string) (Transcriber, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	registryMu.RLock()
	factory, ok := registry[key]
	registryMu.RUnlock()
	if !ok {
		available := ListTranscribers()
		sort.Strings(available)
		return nil, fmt.Errorf("unknown transcriber %q (available: %s)", name, strings.Join(available, ", "))
	}
	return factory(), nil
}

// DefaultTranscriber selects and returns the best available transcriber based
// on which API keys are present in the environment.
// Priority: openai → groq → deepgram → mistral → google → moonshot → minimax → nil.
func DefaultTranscriber() Transcriber {
	candidates := []struct {
		name   string
		envKey string
	}{
		{"openai", "OPENAI_API_KEY"},
		{"groq", "GROQ_API_KEY"},
		{"deepgram", "DEEPGRAM_API_KEY"},
		{"mistral", "MISTRAL_API_KEY"},
		{"google", "GOOGLE_API_KEY"},
		{"moonshot", "MOONSHOT_API_KEY"},
		{"minimax", "MINIMAX_API_KEY"},
	}
	for _, c := range candidates {
		if strings.TrimSpace(os.Getenv(c.envKey)) != "" {
			t, err := ResolveTranscriber(c.name)
			if err == nil && t.Configured() {
				return t
			}
		}
	}
	return nil
}

// init registers all built-in transcriber backends.
func init() {
	RegisterTranscriber("openai", func() Transcriber { return NewOpenAITranscriber() })
	RegisterTranscriber("groq", func() Transcriber { return NewGroqTranscriber() })
	RegisterTranscriber("deepgram", func() Transcriber { return NewDeepgramTranscriber() })
	RegisterTranscriber("mistral", func() Transcriber { return NewMistralTranscriber() })
}
