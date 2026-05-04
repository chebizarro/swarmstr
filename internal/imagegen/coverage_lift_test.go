package imagegen

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type stubImageProvider struct {
	id         string
	configured bool
	result     *ImageGenerationResult
}

func (p stubImageProvider) ID() string       { return p.id }
func (p stubImageProvider) Name() string     { return p.id }
func (p stubImageProvider) Configured() bool { return p.configured }
func (p stubImageProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Generate: true}
}
func (p stubImageProvider) Generate(context.Context, ImageGenerationRequest) (*ImageGenerationResult, error) {
	return p.result, nil
}

func TestRuntimeNormalizeAndPersistCoverage(t *testing.T) {
	badRequests := []ImageGenerationRequest{
		{},
		{Prompt: "x", SourceImage: &SourceImage{URL: "https://example.test/a.png", Base64: "abc"}},
		{Prompt: "x", Mode: "edit"},
		{Prompt: "x", Mode: "variation"},
		{Prompt: "x", Mask: "abc"},
	}
	for _, req := range badRequests {
		if _, err := normalizeRequest(req); err == nil {
			t.Fatalf("normalizeRequest(%+v) expected error", req)
		}
	}

	rt := NewRuntime(nil, func() string { return t.TempDir() })
	img := &GeneratedImage{LocalPath: "already-there"}
	if err := rt.persistImage(context.Background(), img, 0); err != nil || img.LocalPath != "already-there" {
		t.Fatalf("persist existing path img=%+v err=%v", img, err)
	}
	if err := rt.persistImage(context.Background(), &GeneratedImage{}, 1); err == nil || !strings.Contains(err.Error(), "neither base64 nor url") {
		t.Fatalf("expected missing image data error, got %v", err)
	}

	img = &GeneratedImage{Base64: base64.StdEncoding.EncodeToString([]byte("gif-data")), Mime: "image/gif"}
	if err := rt.persistImage(context.Background(), img, 2); err != nil {
		t.Fatalf("persist base64: %v", err)
	}
	if img.LocalPath == "" || !strings.HasSuffix(img.LocalPath, ".gif") {
		t.Fatalf("expected gif local path, got %+v", img)
	}
	if _, err := os.Stat(img.LocalPath); err != nil {
		t.Fatalf("persisted file missing: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("png-data")) }))
	defer srv.Close()
	img = &GeneratedImage{URL: srv.URL + "/image.png"}
	if err := rt.persistImage(context.Background(), img, 3); err != nil {
		t.Fatalf("persist url: %v", err)
	}
	if img.LocalPath == "" {
		t.Fatalf("expected URL image to be persisted, got %+v", img)
	}
}

func TestRuntimeGenerateDefaultProviderAndRegistryErrors(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(stubImageProvider{}); err == nil {
		t.Fatal("expected blank provider id error")
	}
	if err := reg.Register(stubImageProvider{id: "fallback", configured: true, result: &ImageGenerationResult{Images: []GeneratedImage{{Base64: base64.StdEncoding.EncodeToString([]byte("png"))}}}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rt := NewRuntime(reg, func() string { return dir })
	res, err := rt.Generate(context.Background(), "", ImageGenerationRequest{Prompt: "cat"})
	if err != nil || res.Provider != "fallback" || len(res.Images) != 1 || res.Images[0].Base64 != "" || res.Images[0].LocalPath == "" {
		t.Fatalf("Generate result=%+v err=%v", res, err)
	}
	if _, err := rt.Generate(context.Background(), "missing", ImageGenerationRequest{Prompt: "cat"}); err == nil {
		t.Fatal("expected explicit missing provider error")
	}
}
