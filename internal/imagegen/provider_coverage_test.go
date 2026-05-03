package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type imageInvoker struct {
	result any
	err    error
	calls  []map[string]any
}

func (i *imageInvoker) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	if p, ok := params.(map[string]any); ok {
		i.calls = append(i.calls, p)
	}
	return i.result, i.err
}

func TestImageRegistryAndPluginProviderCoverage(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Default(); err == nil {
		t.Fatal("expected empty default error")
	}
	host := &imageInvoker{result: map[string]any{"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString([]byte("png")), "mime": "image/png"}}, "model": "plugin-model"}}
	p := NewPluginProvider(" Image ", map[string]any{"name": "Image Plugin", "capabilities": map[string]any{"edit": "true", "variations": true, "sizes": []any{"1024x1024"}, "formats": []string{"png"}, "maxN": float64(3)}}, host)
	if p.ID() != "image" || p.Name() != "Image Plugin" || !p.Configured() {
		t.Fatalf("unexpected plugin provider metadata")
	}
	caps := p.Capabilities()
	if !caps.Edit || !caps.Variation || caps.MaxN != 3 || len(caps.Sizes) != 1 || len(caps.Formats) != 1 {
		t.Fatalf("unexpected caps: %+v", caps)
	}
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(nil); err == nil {
		t.Fatal("expected nil register error")
	}
	if got, ok := reg.Get("IMAGE"); !ok || got.ID() != "image" || len(reg.List()) != 1 || len(reg.ListConfigured()) != 1 {
		t.Fatal("registry lookup/list failed")
	}
	def, err := reg.Default()
	if err != nil || def.ID() != "image" {
		t.Fatalf("default=%v err=%v", def, err)
	}
	res, err := p.Generate(context.Background(), ImageGenerationRequest{Prompt: "cat", Mode: "edit", SourceImage: &SourceImage{Base64: base64.StdEncoding.EncodeToString([]byte("src")), Mime: "image/png"}})
	if err != nil || res.Provider != "image" || res.Model != "plugin-model" || len(res.Images) != 1 {
		t.Fatalf("Generate result=%+v err=%v", res, err)
	}
	if host.calls[0]["mode"] != "edit" {
		t.Fatalf("unexpected params: %+v", host.calls[0])
	}
}

func TestImageOpenAIProviderHTTPFlows(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("auth header = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"url": "https://cdn/image.png", "width": 32, "height": 32}}, "model": "server-model"})
	}))
	defer srv.Close()
	p := &OpenAIProvider{APIKey: "key", BaseURL: srv.URL, Model: "model"}
	if p.ID() != "openai" || p.Name() != "OpenAI Images" || !p.Configured() || !p.Capabilities().Inpaint {
		t.Fatal("unexpected OpenAI metadata")
	}
	if _, err := p.Generate(context.Background(), ImageGenerationRequest{Prompt: "cat", N: 1, Size: "1024x1024", Quality: "high", Format: "png", NegativePrompt: "dog"}); err != nil {
		t.Fatalf("json generate: %v", err)
	}
	if _, err := p.Generate(context.Background(), ImageGenerationRequest{Prompt: "edit", Mode: "variation", N: 1, SourceImage: &SourceImage{Base64: base64.StdEncoding.EncodeToString([]byte("png")), Mime: "image/png"}}); err != nil {
		t.Fatalf("multipart variation: %v", err)
	}
	joined := strings.Join(paths, ",")
	if !strings.Contains(joined, "/images/generations") || !strings.Contains(joined, "/images/variations") {
		t.Fatalf("unexpected paths: %v", paths)
	}
}

func TestImageHTTPProviderAndHelpers(t *testing.T) {
	p := NewMidjourneyProvider()
	t.Setenv("MIDJOURNEY_API_KEY", "")
	t.Setenv("MIDJOURNEY_BASE_URL", "")
	if _, err := p.Generate(context.Background(), ImageGenerationRequest{Prompt: "x"}); err == nil {
		t.Fatal("expected unconfigured error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/generate" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"base64": base64.StdEncoding.EncodeToString([]byte("png")), "mime": "image/png"}}})
	}))
	defer srv.Close()
	t.Setenv("MIDJOURNEY_API_KEY", "key")
	t.Setenv("MIDJOURNEY_BASE_URL", srv.URL)
	res, err := p.Generate(context.Background(), ImageGenerationRequest{Prompt: "x", Model: "m"})
	if err != nil || res.Provider != "midjourney" || res.Model != "m" || len(res.Images) != 1 {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	if NewStableDiffusionProvider().ID() != "stable-diffusion" {
		t.Fatal("stable diffusion provider id mismatch")
	}
	if _, err := sourceImageBytes(context.Background(), SourceImage{Base64: "bad"}); err == nil {
		t.Fatal("expected bad base64 error")
	}
	if _, err := sourceImageBytes(context.Background(), SourceImage{}); err == nil {
		t.Fatal("expected empty source error")
	}
	if extensionWithDot("image/png", "jpg") != ".png" || mimeFromExt("jpg") != "image/jpeg" || extensionFromMime("image/webp") != "webp" {
		t.Fatal("extension/mime helpers mismatch")
	}
	if !boolDefault("1", false) || intValue(int64(2)) != 2 || len(stringSlice([]any{"a", "b"})) != 2 || !isMissingProviderMethod(imageErr("is not executable")) {
		t.Fatal("conversion helpers mismatch")
	}
}

type imageErr string

func (e imageErr) Error() string { return string(e) }

func TestImageProviderErrorAndMetadataBranches(t *testing.T) {
	openai := NewOpenAIProvider()
	if openai.ID() != "openai" || openai.Name() == "" || openai.Configured() {
		t.Fatalf("unexpected new openai metadata")
	}
	if _, err := openai.Generate(context.Background(), ImageGenerationRequest{Prompt: "x"}); err == nil {
		t.Fatal("expected unconfigured openai error")
	}
	stable := NewStableDiffusionProvider()
	if stable.Name() != "Stable Diffusion" || !stable.Capabilities().Inpaint {
		t.Fatalf("unexpected stable metadata")
	}
	if _, err := parseOpenAIImageHTTPResponse(&http.Response{StatusCode: 400, Status: "400 Bad Request", Body: http.NoBody}, "openai", "m"); err == nil {
		t.Fatal("expected openai http error")
	}
	if _, err := sourceImageBytes(context.Background(), SourceImage{Base64: "a", URL: "https://example.com"}); err == nil {
		t.Fatal("expected mutually exclusive source error")
	}
	rt := NewRuntime(nil, nil)
	if rt.Registry() == nil {
		t.Fatal("runtime registry missing")
	}
	if _, err := rt.Generate(context.Background(), "missing", ImageGenerationRequest{Prompt: "x"}); err == nil {
		t.Fatal("expected missing provider error")
	}
	if intValue(7) != 7 || extensionFromMime("image/jpeg") != "jpg" || mimeFromExt("webp") != "image/webp" {
		t.Fatal("helper branch mismatch")
	}
}
