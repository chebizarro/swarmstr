package musicgen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type ProviderInvoker interface {
	InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error)
}
type Provider interface {
	ID() string
	Name() string
	Configured() bool
	Generate(ctx context.Context, req MusicGenerationRequest) (*MusicGenerationResult, error)
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
func (p *PluginProvider) Generate(ctx context.Context, req MusicGenerationRequest) (*MusicGenerationResult, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("music provider %q has no plugin host", p.providerID)
	}
	res, err := p.host.InvokeProvider(ctx, p.providerID, "generate", map[string]any{"prompt": req.Prompt, "duration": req.Duration, "format": req.Format, "model": req.Model, "genre": req.Genre})
	if err != nil {
		return nil, err
	}
	out, err := parseResult(res)
	if out != nil && out.Provider == "" {
		out.Provider = p.providerID
	}
	return out, err
}
func parseResult(v any) (*MusicGenerationResult, error) {
	if v == nil {
		return &MusicGenerationResult{}, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out MusicGenerationResult
	if err := json.Unmarshal(data, &out); err == nil && (out.Audio.URL != "" || out.Audio.Base64 != "" || out.Audio.LocalPath != "") {
		return &out, nil
	}
	var alt struct {
		URL      string `json:"url"`
		Base64   string `json:"base64"`
		AudioURL string `json:"audio_url"`
		Format   string `json:"format"`
		Duration int    `json:"duration"`
	}
	if err := json.Unmarshal(data, &alt); err != nil {
		return nil, err
	}
	return &MusicGenerationResult{Audio: GeneratedAudio{URL: firstNonEmpty(alt.URL, alt.AudioURL), Base64: alt.Base64, Format: alt.Format}, Duration: alt.Duration}, nil
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

// HTTPProvider is a lightweight adapter for hosted music generation gateways
// such as Suno/Udio-compatible HTTP APIs.
type HTTPProvider struct {
	IDValue, NameValue                  string
	APIKeyEnv, BaseURLEnv, GeneratePath string
}

func NewSunoProvider() *HTTPProvider {
	return &HTTPProvider{IDValue: "suno", NameValue: "Suno", APIKeyEnv: "SUNO_API_KEY", BaseURLEnv: "SUNO_BASE_URL", GeneratePath: "/generate"}
}
func NewUdioProvider() *HTTPProvider {
	return &HTTPProvider{IDValue: "udio", NameValue: "Udio", APIKeyEnv: "UDIO_API_KEY", BaseURLEnv: "UDIO_BASE_URL", GeneratePath: "/generate"}
}
func (p *HTTPProvider) ID() string   { return p.IDValue }
func (p *HTTPProvider) Name() string { return p.NameValue }
func (p *HTTPProvider) Configured() bool {
	return strings.TrimSpace(os.Getenv(p.APIKeyEnv)) != "" && strings.TrimSpace(os.Getenv(p.BaseURLEnv)) != ""
}
func (p *HTTPProvider) Generate(ctx context.Context, req MusicGenerationRequest) (*MusicGenerationResult, error) {
	if !p.Configured() {
		return nil, fmt.Errorf("%s music provider is not configured (%s and %s)", p.ID(), p.APIKeyEnv, p.BaseURLEnv)
	}
	body, _ := json.Marshal(req)
	url := strings.TrimRight(os.Getenv(p.BaseURLEnv), "/") + "/" + strings.TrimLeft(p.GeneratePath, "/")
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+os.Getenv(p.APIKeyEnv))
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s music provider HTTP %s: %s", p.ID(), resp.Status, strings.TrimSpace(string(data)))
	}
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	out, err := parseResult(v)
	if out != nil && out.Provider == "" {
		out.Provider = p.ID()
	}
	return out, err
}
