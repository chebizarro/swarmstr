package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	"metiq/internal/media"
)

type ProviderInvoker interface {
	InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error)
}

type Provider interface {
	ID() string
	Name() string
	Configured() bool
	Capabilities() ProviderCapabilities
	Generate(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error)
}

type PluginProvider struct {
	providerID string
	name       string
	host       ProviderInvoker
	caps       ProviderCapabilities
	raw        map[string]any
}

func NewPluginProvider(providerID string, raw map[string]any, host ProviderInvoker) *PluginProvider {
	p := &PluginProvider{providerID: normalizeID(providerID), name: stringValue(raw["name"]), host: host, raw: cloneMap(raw)}
	if p.name == "" {
		p.name = providerID
	}
	p.caps = parseCapabilities(raw)
	return p
}

func (p *PluginProvider) ID() string { return p.providerID }
func (p *PluginProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return p.providerID
}
func (p *PluginProvider) Capabilities() ProviderCapabilities { return p.caps }
func (p *PluginProvider) Configured() bool {
	if p == nil || p.host == nil {
		return false
	}
	result, err := p.host.InvokeProvider(context.Background(), p.providerID, "configured", nil)
	if err != nil {
		return isMissingProviderMethod(err)
	}
	return boolDefault(result, true)
}
func (p *PluginProvider) Generate(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("image provider %q has no plugin host", p.providerID)
	}
	payload := map[string]any{"prompt": req.Prompt, "negative_prompt": req.NegativePrompt, "model": req.Model, "size": req.Size, "quality": req.Quality, "format": req.Format, "n": req.N, "mode": req.Mode, "source_image": req.SourceImage, "mask": req.Mask}
	result, err := p.host.InvokeProvider(ctx, p.providerID, "generate", payload)
	if err != nil {
		return nil, err
	}
	out, err := parseImageGenerationResult(result)
	if out != nil && out.Provider == "" {
		out.Provider = p.providerID
	}
	return out, err
}

// OpenAIProvider implements OpenAI's Images API (generation, edits, variations).
type OpenAIProvider struct {
	APIKey, BaseURL, Model string
	HTTPClient             *http.Client
}

func NewOpenAIProvider() *OpenAIProvider { return &OpenAIProvider{} }
func (p *OpenAIProvider) ID() string     { return "openai" }
func (p *OpenAIProvider) Name() string   { return "OpenAI Images" }
func (p *OpenAIProvider) Configured() bool {
	return strings.TrimSpace(firstNonEmpty(p.APIKey, os.Getenv("OPENAI_API_KEY"))) != ""
}
func (p *OpenAIProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Generate: true, Edit: true, Variation: true, Inpaint: true, Sizes: []string{"1024x1024", "1536x1024", "1024x1536"}, Formats: []string{"png", "jpeg", "webp"}, MaxN: 10}
}
func (p *OpenAIProvider) Generate(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	if !p.Configured() {
		return nil, fmt.Errorf("openai image provider is not configured (OPENAI_API_KEY)")
	}
	mode := canonicalMode(req.Mode)
	if mode == "generate" {
		return p.jsonImageRequest(ctx, "images/generations", req)
	}
	return p.multipartImageRequest(ctx, map[string]string{"edit": "images/edits", "variation": "images/variations"}[mode], req)
}

func (p *OpenAIProvider) endpoint(path string) string {
	base := strings.TrimRight(firstNonEmpty(p.BaseURL, os.Getenv("OPENAI_BASE_URL"), "https://api.openai.com/v1"), "/")
	return base + "/" + strings.TrimLeft(path, "/")
}
func (p *OpenAIProvider) apiKey() string { return firstNonEmpty(p.APIKey, os.Getenv("OPENAI_API_KEY")) }
func (p *OpenAIProvider) model(req ImageGenerationRequest) string {
	return firstNonEmpty(req.Model, p.Model, os.Getenv("OPENAI_IMAGE_MODEL"), "gpt-image-1")
}
func (p *OpenAIProvider) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return http.DefaultClient
}
func (p *OpenAIProvider) jsonImageRequest(ctx context.Context, path string, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	payload := map[string]any{"model": p.model(req), "prompt": req.Prompt, "n": req.N, "size": req.Size}
	if req.Quality != "" {
		payload["quality"] = req.Quality
	}
	if req.Format != "" {
		payload["output_format"] = req.Format
	}
	if req.NegativePrompt != "" {
		payload["negative_prompt"] = req.NegativePrompt
	}
	body, _ := json.Marshal(payload)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+p.apiKey())
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := p.client().Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseOpenAIImageHTTPResponse(resp, p.ID(), p.model(req))
}
func (p *OpenAIProvider) multipartImageRequest(ctx context.Context, path string, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("model", p.model(req))
	_ = mw.WriteField("prompt", req.Prompt)
	_ = mw.WriteField("n", strconv.Itoa(req.N))
	_ = mw.WriteField("size", req.Size)
	if req.SourceImage != nil {
		data, err := sourceImageBytes(ctx, *req.SourceImage)
		if err != nil {
			return nil, err
		}
		fw, err := mw.CreateFormFile("image", "image"+extensionWithDot(req.SourceImage.Mime, "png"))
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(data); err != nil {
			return nil, err
		}
	}
	if req.Mask != "" {
		mask, err := base64.StdEncoding.DecodeString(req.Mask)
		if err != nil {
			return nil, fmt.Errorf("decode mask: %w", err)
		}
		fw, err := mw.CreateFormFile("mask", "mask.png")
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(mask); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(path), &b)
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+p.apiKey())
	hreq.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := p.client().Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseOpenAIImageHTTPResponse(resp, p.ID(), p.model(req))
}

func sourceImageBytes(ctx context.Context, src SourceImage) ([]byte, error) {
	if src.Base64 != "" && src.URL != "" {
		return nil, fmt.Errorf("source image must use either base64 or url, not both")
	}
	if src.Base64 != "" {
		data, err := base64.StdEncoding.DecodeString(src.Base64)
		if err != nil {
			return nil, fmt.Errorf("decode source image: %w", err)
		}
		return data, nil
	}
	if src.URL != "" {
		return media.FetchURLBytes(ctx, src.URL)
	}
	return nil, fmt.Errorf("source image is empty")
}

func extensionWithDot(mime, fallback string) string {
	ext := extensionFromMime(mime)
	if ext == "" || ext == "bin" {
		ext = fallback
	}
	return "." + strings.TrimPrefix(ext, ".")
}

// HTTPProvider is a lightweight provider for Midjourney/Stable-Diffusion-compatible gateways.
type HTTPProvider struct {
	IDValue, NameValue, APIKeyEnv, BaseURLEnv, EndpointPath string
	Caps                                                    ProviderCapabilities
	HTTPClient                                              *http.Client
}

func NewMidjourneyProvider() *HTTPProvider {
	return &HTTPProvider{IDValue: "midjourney", NameValue: "Midjourney", APIKeyEnv: "MIDJOURNEY_API_KEY", BaseURLEnv: "MIDJOURNEY_BASE_URL", EndpointPath: "/generate", Caps: ProviderCapabilities{Generate: true, Variation: true, MaxN: 4}}
}
func NewStableDiffusionProvider() *HTTPProvider {
	return &HTTPProvider{IDValue: "stable-diffusion", NameValue: "Stable Diffusion", APIKeyEnv: "STABILITY_API_KEY", BaseURLEnv: "STABILITY_BASE_URL", EndpointPath: "/v1/generation", Caps: ProviderCapabilities{Generate: true, Edit: true, Inpaint: true, Sizes: []string{"512x512", "768x768", "1024x1024"}, Formats: []string{"png", "jpeg", "webp"}, MaxN: 4}}
}
func (p *HTTPProvider) ID() string   { return p.IDValue }
func (p *HTTPProvider) Name() string { return p.NameValue }
func (p *HTTPProvider) Configured() bool {
	return os.Getenv(p.APIKeyEnv) != "" && os.Getenv(p.BaseURLEnv) != ""
}
func (p *HTTPProvider) Capabilities() ProviderCapabilities { return p.Caps }
func (p *HTTPProvider) Generate(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	if !p.Configured() {
		return nil, fmt.Errorf("%s image provider is not configured (%s and %s)", p.ID(), p.APIKeyEnv, p.BaseURLEnv)
	}
	payload, _ := json.Marshal(req)
	endpoint := strings.TrimRight(os.Getenv(p.BaseURLEnv), "/") + "/" + strings.TrimLeft(p.EndpointPath, "/")
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Authorization", "Bearer "+os.Getenv(p.APIKeyEnv))
	hreq.Header.Set("Content-Type", "application/json")
	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s provider HTTP %s: %s", p.ID(), resp.Status, strings.TrimSpace(string(data)))
	}
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	out, err := parseImageGenerationResult(v)
	if out != nil {
		out.Provider = p.ID()
		if out.Model == "" {
			out.Model = req.Model
		}
	}
	return out, err
}

func parseOpenAIImageHTTPResponse(resp *http.Response, provider, model string) (*ImageGenerationResult, error) {
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai images HTTP %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var root map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		return nil, err
	}
	out, err := parseImageGenerationResult(root)
	if out != nil {
		out.Provider = provider
		if out.Model == "" {
			out.Model = model
		}
	}
	return out, err
}

func parseImageGenerationResult(v any) (*ImageGenerationResult, error) {
	if v == nil {
		return &ImageGenerationResult{}, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var direct ImageGenerationResult
	if err := json.Unmarshal(data, &direct); err == nil && len(direct.Images) > 0 {
		return &direct, nil
	}
	var wrapped struct {
		Data []struct {
			URL           string `json:"url"`
			B64JSON       string `json:"b64_json"`
			Base64        string `json:"base64"`
			Mime          string `json:"mime"`
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
		Model string     `json:"model"`
		Usage *UsageInfo `json:"usage"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, err
	}
	out := &ImageGenerationResult{Model: wrapped.Model, Usage: wrapped.Usage}
	for _, item := range wrapped.Data {
		out.Images = append(out.Images, GeneratedImage{URL: item.URL, Base64: firstNonEmpty(item.Base64, item.B64JSON), Mime: firstNonEmpty(item.Mime, "image/png"), Width: item.Width, Height: item.Height})
	}
	return out, nil
}

func parseCapabilities(raw map[string]any) ProviderCapabilities {
	c := ProviderCapabilities{Generate: true, MaxN: 1}
	if raw == nil {
		return c
	}
	caps := asMap(firstPresent(raw, "capabilities", "caps"))
	if len(caps) == 0 {
		caps = raw
	}
	if v, ok := caps["generate"]; ok {
		c.Generate = boolDefault(v, c.Generate)
	}
	c.Edit = boolDefault(firstPresent(caps, "edit", "edits"), false)
	c.Variation = boolDefault(firstPresent(caps, "variation", "variations"), false)
	c.Inpaint = boolDefault(caps["inpaint"], false)
	c.Outpaint = boolDefault(caps["outpaint"], false)
	c.Sizes = stringSlice(firstPresent(caps, "sizes", "size"))
	c.Formats = stringSlice(firstPresent(caps, "formats", "format"))
	if n := intValue(firstPresent(caps, "max_n", "maxN")); n > 0 {
		c.MaxN = n
	}
	return c
}
func isMissingProviderMethod(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unknown provider method") || strings.Contains(s, "not a function") || strings.Contains(s, "is not executable")
}
func canonicalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "generate", "text-to-image", "text_to_image":
		return "generate"
	case "edit", "inpaint", "outpaint":
		return "edit"
	case "variation", "variations", "vary":
		return "variation"
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}
func normalizeID(id string) string { return strings.ToLower(strings.TrimSpace(id)) }
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
		return strings.EqualFold(t, "true") || t == "1"
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
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	default:
		return 0
	}
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
	case string:
		if t != "" {
			return []string{t}
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
