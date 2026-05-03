package videogen

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
	Capabilities() ProviderCapabilities
	Generate(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error)
	CheckJob(ctx context.Context, jobID string) (*VideoGenerationResult, error)
}

type PluginProvider struct {
	providerID, name string
	host             ProviderInvoker
	caps             ProviderCapabilities
	raw              map[string]any
}

func NewPluginProvider(providerID string, raw map[string]any, host ProviderInvoker) *PluginProvider {
	p := &PluginProvider{providerID: normalizeID(providerID), name: stringValue(raw["name"]), host: host, raw: cloneMap(raw), caps: parseCapabilities(raw)}
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
func (p *PluginProvider) Capabilities() ProviderCapabilities { return p.caps }
func (p *PluginProvider) Generate(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("video provider %q has no plugin host", p.providerID)
	}
	res, err := p.host.InvokeProvider(ctx, p.providerID, "generate", map[string]any{"prompt": req.Prompt, "model": req.Model, "duration": req.Duration, "resolution": req.Resolution, "aspect_ratio": req.AspectRatio, "fps": req.FPS, "mode": pluginMode(req.Mode), "source_asset": req.SourceAsset})
	if err != nil {
		return nil, err
	}
	out, err := parseResult(res)
	if out != nil && out.Provider == "" {
		out.Provider = p.providerID
	}
	return out, err
}
func (p *PluginProvider) CheckJob(ctx context.Context, jobID string) (*VideoGenerationResult, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("video provider %q has no plugin host", p.providerID)
	}
	res, err := p.host.InvokeProvider(ctx, p.providerID, "check_job", map[string]any{"job_id": jobID})
	if err != nil {
		return nil, err
	}
	out, err := parseResult(res)
	if out != nil && out.Provider == "" {
		out.Provider = p.providerID
	}
	return out, err
}

func parseResult(v any) (*VideoGenerationResult, error) {
	if v == nil {
		return &VideoGenerationResult{}, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out VideoGenerationResult
	if err := json.Unmarshal(data, &out); err == nil && (out.Status != "" || len(out.Videos) > 0 || out.JobID != "") {
		if out.Status == "" && len(out.Videos) > 0 {
			out.Status = "completed"
		}
		return &out, nil
	}
	var alt struct {
		Video  GeneratedVideo `json:"video"`
		URL    string         `json:"url"`
		Status string         `json:"status"`
		JobID  string         `json:"job_id"`
	}
	if err := json.Unmarshal(data, &alt); err != nil {
		return nil, err
	}
	out = VideoGenerationResult{Status: alt.Status, JobID: alt.JobID}
	if alt.Video.URL != "" || alt.Video.Base64 != "" || alt.Video.LocalPath != "" {
		out.Videos = []GeneratedVideo{alt.Video}
	} else if alt.URL != "" {
		out.Videos = []GeneratedVideo{{URL: alt.URL, Format: "mp4"}}
	}
	if out.Status == "" && len(out.Videos) > 0 {
		out.Status = "completed"
	}
	return &out, nil
}
func parseCapabilities(raw map[string]any) ProviderCapabilities {
	c := ProviderCapabilities{Generate: true, SupportsAsync: true}
	caps := asMap(firstPresent(raw, "capabilities", "caps"))
	if len(caps) == 0 {
		caps = raw
	}
	c.Generate = boolDefault(firstPresent(caps, "generate"), c.Generate)
	c.ImageToVideo = boolDefault(firstPresent(caps, "image_to_video", "imageToVideo"), false)
	c.VideoToVideo = boolDefault(firstPresent(caps, "video_to_video", "videoToVideo"), false)
	c.SupportsAsync = boolDefault(firstPresent(caps, "supports_async", "supportsAsync"), c.SupportsAsync)
	c.Resolutions = stringSlice(firstPresent(caps, "resolutions"))
	c.AspectRatios = stringSlice(firstPresent(caps, "aspect_ratios", "aspectRatios"))
	if n := intValue(firstPresent(caps, "max_duration", "maxDuration")); n > 0 {
		c.MaxDuration = n
	}
	return c
}
func canonicalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "generate", "text-to-video", "text_to_video":
		return "generate"
	case "imagetovideo", "image-to-video", "image_to_video":
		return "image_to_video"
	case "videotovideo", "video-to-video", "video_to_video":
		return "video_to_video"
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}
func pluginMode(mode string) string {
	switch canonicalMode(mode) {
	case "image_to_video":
		return "imageToVideo"
	case "video_to_video":
		return "videoToVideo"
	default:
		return "generate"
	}
}
func normalizeID(id string) string { return strings.ToLower(strings.TrimSpace(id)) }
func isMissingProviderMethod(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unknown provider method") || strings.Contains(s, "not a function") || strings.Contains(s, "is not executable")
}
func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
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
func intValue(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
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
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
func stringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := []string{}
		for _, x := range t {
			if s := stringValue(x); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
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

// HTTPProvider is a lightweight adapter for hosted video generation gateways
// such as Runway/Pika-compatible HTTP APIs. It supports async job status via
// a configurable job endpoint.
type HTTPProvider struct {
	IDValue, NameValue    string
	APIKeyEnv, BaseURLEnv string
	GeneratePath, JobPath string
	Caps                  ProviderCapabilities
}

func NewRunwayProvider() *HTTPProvider {
	return &HTTPProvider{IDValue: "runway", NameValue: "Runway", APIKeyEnv: "RUNWAY_API_KEY", BaseURLEnv: "RUNWAY_BASE_URL", GeneratePath: "/v1/video/generations", JobPath: "/v1/video/generations/{job_id}", Caps: ProviderCapabilities{Generate: true, ImageToVideo: true, SupportsAsync: true, Resolutions: []string{"720P", "1080P"}, MaxDuration: 10}}
}
func NewPikaProvider() *HTTPProvider {
	return &HTTPProvider{IDValue: "pika", NameValue: "Pika", APIKeyEnv: "PIKA_API_KEY", BaseURLEnv: "PIKA_BASE_URL", GeneratePath: "/generate", JobPath: "/jobs/{job_id}", Caps: ProviderCapabilities{Generate: true, ImageToVideo: true, SupportsAsync: true, Resolutions: []string{"720P", "1080P"}, MaxDuration: 10}}
}
func (p *HTTPProvider) ID() string   { return p.IDValue }
func (p *HTTPProvider) Name() string { return p.NameValue }
func (p *HTTPProvider) Configured() bool {
	return strings.TrimSpace(os.Getenv(p.APIKeyEnv)) != "" && strings.TrimSpace(os.Getenv(p.BaseURLEnv)) != ""
}
func (p *HTTPProvider) Capabilities() ProviderCapabilities { return p.Caps }
func (p *HTTPProvider) Generate(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error) {
	return p.do(ctx, http.MethodPost, p.GeneratePath, req)
}
func (p *HTTPProvider) CheckJob(ctx context.Context, jobID string) (*VideoGenerationResult, error) {
	return p.do(ctx, http.MethodGet, strings.ReplaceAll(p.JobPath, "{job_id}", jobID), nil)
}
func (p *HTTPProvider) do(ctx context.Context, method, path string, body any) (*VideoGenerationResult, error) {
	if !p.Configured() {
		return nil, fmt.Errorf("%s video provider is not configured (%s and %s)", p.ID(), p.APIKeyEnv, p.BaseURLEnv)
	}
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = strings.NewReader(string(b))
	}
	url := strings.TrimRight(os.Getenv(p.BaseURLEnv), "/") + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv(p.APIKeyEnv))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s video provider HTTP %s: %s", p.ID(), resp.Status, strings.TrimSpace(string(data)))
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
