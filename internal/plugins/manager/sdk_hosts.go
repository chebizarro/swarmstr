// Package manager – concrete sdk.Host implementations for main.go wiring.
//
// BuildHostForConfig constructs a sdk.Host from the runtime components
// already present in swarmstrd: configState, DM transport, agent runtime.
package manager

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"metiq/internal/agent"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

// ─── ConfigHost ──────────────────────────────────────────────────────────────

// configStateReader is satisfied by the runtime config store in main.go.
type configStateReader interface {
	Get() state.ConfigDoc
}

type configHostImpl struct{ cfg configStateReader }

func (c *configHostImpl) Get(key string) any {
	doc := c.cfg.Get()
	return navigateDotPath(doc, key)
}

// navigateDotPath traverses a ConfigDoc using a dot-separated key path.
// It first checks typed fields then falls back to Extra.
func navigateDotPath(doc state.ConfigDoc, key string) any {
	if key == "" {
		return nil
	}
	parts := strings.SplitN(key, ".", 2)
	top := parts[0]
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}

	switch top {
	case "dm":
		if rest == "" {
			return map[string]any{"policy": doc.DM.Policy, "allow_from": doc.DM.AllowFrom}
		}
		switch rest {
		case "policy":
			return doc.DM.Policy
		case "allow_from":
			return doc.DM.AllowFrom
		}
	case "relays":
		if rest == "" {
			return map[string]any{"read": doc.Relays.Read, "write": doc.Relays.Write}
		}
		switch rest {
		case "read":
			return doc.Relays.Read
		case "write":
			return doc.Relays.Write
		}
	case "agent":
		if rest == "" {
			return map[string]any{"default_model": doc.Agent.DefaultModel}
		}
		switch rest {
		case "default_model":
			return doc.Agent.DefaultModel
		}
	}
	// Fall back to Extra map.
	if doc.Extra == nil {
		return nil
	}
	v, ok := doc.Extra[top]
	if !ok {
		return nil
	}
	if rest == "" {
		return v
	}
	// Navigate nested map.
	if m, ok := v.(map[string]any); ok {
		return nestedGet(m, rest)
	}
	return nil
}

func nestedGet(m map[string]any, path string) any {
	parts := strings.SplitN(path, ".", 2)
	v, ok := m[parts[0]]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return v
	}
	if nested, ok := v.(map[string]any); ok {
		return nestedGet(nested, parts[1])
	}
	return nil
}

// ─── LogHost ─────────────────────────────────────────────────────────────────

type logHostImpl struct {
	log      *slog.Logger
	pluginID string
}

func (l *logHostImpl) Info(msg string, args ...any) {
	l.log.Info(msg, append([]any{"plugin", l.pluginID}, args...)...)
}
func (l *logHostImpl) Warn(msg string, args ...any) {
	l.log.Warn(msg, append([]any{"plugin", l.pluginID}, args...)...)
}
func (l *logHostImpl) Error(msg string, args ...any) {
	l.log.Error(msg, append([]any{"plugin", l.pluginID}, args...)...)
}

// ─── AgentHost ───────────────────────────────────────────────────────────────

// agentRuntimeReader is satisfied by agent.Runtime.
type agentRuntimeReader interface {
	ProcessTurn(context.Context, agent.Turn) (agent.TurnResult, error)
}

type agentHostImpl struct{ rt agentRuntimeReader }

func (a *agentHostImpl) Complete(ctx context.Context, prompt string, opts sdk.CompletionOpts) (string, error) {
	turn := agent.Turn{
		SessionID: "_plugin",
		UserText:  prompt,
	}
	if opts.SystemPrompt != "" {
		turn.Context = opts.SystemPrompt
	}
	res, err := a.rt.ProcessTurn(ctx, turn)
	if err != nil {
		return "", fmt.Errorf("agent completion: %w", err)
	}
	return res.Text, nil
}

// ─── StorageHost – in-memory per-plugin KV ───────────────────────────────────

type inMemoryStorage struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func (s *inMemoryStorage) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key], nil
}

func (s *inMemoryStorage) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *inMemoryStorage) Del(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// ─── BuildHost ───────────────────────────────────────────────────────────────

// BuildHost assembles a sdk.Host from runtime components.
// Pass nil for optional components (agent runtime, nostr host) if not yet ready.
func BuildHost(cfg configStateReader, rt agentRuntimeReader) *sdk.Host {
	h := &sdk.Host{
		Config: &configHostImpl{cfg: cfg},
		Log: &logHostImpl{
			log:      slog.Default().With("component", "plugin-sdk"),
			pluginID: "?",
		},
		Storage: &inMemoryStorage{data: map[string][]byte{}},
	}
	if rt != nil {
		h.Agent = &agentHostImpl{rt: rt}
	}
	// NostrHost is left nil unless the caller explicitly sets it after the DM
	// transport is started; the stub in goja_host.go will return a safe error.
	return h
}
