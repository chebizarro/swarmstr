package realtimestt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ProviderInvoker interface {
	InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error)
}
type Provider interface {
	ID() string
	Name() string
	Configured() bool
	CreateSession(ctx context.Context, cfg SessionConfig) (Session, error)
}

type PluginProvider struct {
	providerID, name string
	host             ProviderInvoker
	raw              map[string]any
}

func NewPluginProvider(providerID string, raw map[string]any, host ProviderInvoker) *PluginProvider {
	p := &PluginProvider{providerID: normalizeID(providerID), name: stringValue(raw["name"]), host: host, raw: cloneMap(raw)}
	if p.name == "" {
		p.name = providerID
	}
	return p
}
func (p *PluginProvider) ID() string { return p.providerID }
func (p *PluginProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return p.providerID
}
func (p *PluginProvider) Configured() bool {
	if p == nil || p.host == nil {
		return false
	}
	res, err := p.host.InvokeProvider(context.Background(), p.providerID, "configured", nil)
	if err != nil {
		return isMissingProviderMethod(err)
	}
	return boolDefault(res, true)
}
func (p *PluginProvider) CreateSession(ctx context.Context, cfg SessionConfig) (Session, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("transcription provider %q has no plugin host", p.providerID)
	}
	res, err := p.host.InvokeProvider(ctx, p.providerID, "create_session", map[string]any{"language": cfg.Language, "model": cfg.Model, "sample_rate": cfg.SampleRate, "encoding": cfg.Encoding, "channels": cfg.Channels})
	if err != nil {
		return nil, err
	}
	id := sessionID(res)
	if id == "" {
		return nil, fmt.Errorf("transcription provider %q did not return session_id", p.providerID)
	}
	sctx, cancel := context.WithCancel(ctx)
	return &pluginSession{id: id, host: p.host, providerID: p.providerID, ctx: sctx, cancel: cancel, onTranscript: cfg.OnTranscript, done: make(chan struct{})}, nil
}
func sessionID(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	m := asMap(v)
	return firstNonEmpty(stringValue(m["session_id"]), stringValue(m["sessionId"]), stringValue(m["id"]))
}
func normalizeID(id string) string { return strings.ToLower(strings.TrimSpace(id)) }
func isMissingProviderMethod(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unknown provider method") || strings.Contains(s, "not a function") || strings.Contains(s, "is not executable")
}
func boolDefault(v any, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		if t == "" {
			return def
		}
		return t == "1" || strings.EqualFold(t, "true")
	default:
		return def
	}
}
func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func firstPresent(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if m != nil {
			if v, ok := m[k]; ok {
				return v
			}
		}
	}
	return nil
}
