package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type fakeProvider struct {
	id         string
	configured bool
	res        *ImageGenerationResult
	got        ImageGenerationRequest
}

func (f *fakeProvider) ID() string       { return f.id }
func (f *fakeProvider) Name() string     { return f.id }
func (f *fakeProvider) Configured() bool { return f.configured }
func (f *fakeProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Generate: true}
}
func (f *fakeProvider) Generate(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	f.got = req
	return f.res, nil
}

func TestRuntimePersistsBase64AndToolJSON(t *testing.T) {
	dir := t.TempDir()
	p := &fakeProvider{id: "z", configured: true, res: &ImageGenerationResult{Images: []GeneratedImage{{Base64: base64.StdEncoding.EncodeToString([]byte("pngdata")), Mime: "image/png", Width: 10, Height: 20}}}}
	reg := NewRegistry()
	_ = reg.Register(p)
	rt := NewRuntime(reg, func() string { return dir })
	out, err := Tool(rt)(context.Background(), map[string]any{"prompt": "cat", "provider": "z"})
	if err != nil {
		t.Fatal(err)
	}
	var res ImageGenerationResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].LocalPath == "" {
		t.Fatalf("missing image path: %#v", res)
	}
	if res.Images[0].Base64 != "" {
		t.Fatal("base64 should be cleared")
	}
	data, err := os.ReadFile(res.Images[0].LocalPath)
	if err != nil || string(data) != "pngdata" {
		t.Fatalf("persisted data=%q err=%v", data, err)
	}
}
func TestNormalizeEditRequiresSource(t *testing.T) {
	_, err := normalizeRequest(ImageGenerationRequest{Prompt: "x", Mode: "edit"})
	if err == nil || !strings.Contains(err.Error(), "source_image") {
		t.Fatalf("expected source error, got %v", err)
	}
}

type fakeHost struct {
	results map[string]any
	err     error
	calls   []string
	params  []any
}

func (h *fakeHost) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	h.calls = append(h.calls, method)
	h.params = append(h.params, params)
	if h.err != nil {
		return nil, h.err
	}
	return h.results[method], nil
}
func TestPluginProviderGenerateParsesData(t *testing.T) {
	h := &fakeHost{results: map[string]any{"configured": true, "generate": map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString([]byte("x"))}}}}}
	p := NewPluginProvider("plug", map[string]any{"capabilities": map[string]any{"edit": true}}, h)
	if !p.Configured() {
		t.Fatal("configured false")
	}
	res, err := p.Generate(context.Background(), ImageGenerationRequest{Prompt: "x", N: 1, Mode: "generate"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "plug" || len(res.Images) != 1 {
		t.Fatalf("bad result: %#v", res)
	}
}
