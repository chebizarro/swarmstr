package realtimevoice

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
	CreateBridge(ctx context.Context, cfg BridgeConfig) (Bridge, error)
	ListVoices(ctx context.Context) ([]VoiceInfo, error)
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
func (p *PluginProvider) CreateBridge(ctx context.Context, cfg BridgeConfig) (Bridge, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("voice provider %q has no plugin host", p.providerID)
	}
	res, err := p.host.InvokeProvider(ctx, p.providerID, "create_bridge", map[string]any{"model": cfg.Model, "voice": cfg.Voice, "language": cfg.Language, "input_format": cfg.InputFormat, "output_format": cfg.OutputFormat, "system_prompt": cfg.SystemPrompt})
	if err != nil {
		return nil, err
	}
	id := sessionID(res)
	if id == "" {
		return nil, fmt.Errorf("voice provider %q did not return session_id", p.providerID)
	}
	bctx, cancel := context.WithCancel(ctx)
	return &pluginBridge{sessionID: id, host: p.host, providerID: p.providerID, ctx: bctx, cancel: cancel, onAudio: cfg.OnAudio, onTranscript: cfg.OnTranscript, done: make(chan struct{})}, nil
}
func (p *PluginProvider) ListVoices(ctx context.Context) ([]VoiceInfo, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("voice provider %q has no plugin host", p.providerID)
	}
	res, err := p.host.InvokeProvider(ctx, p.providerID, "list_voices", nil)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	var direct []VoiceInfo
	if err := json.Unmarshal(data, &direct); err == nil {
		return direct, nil
	}
	var wrap struct {
		Voices []VoiceInfo `json:"voices"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return nil, err
	}
	return wrap.Voices, nil
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
