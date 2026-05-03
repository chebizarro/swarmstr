package imagegen

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"metiq/internal/media"
)

type Runtime struct {
	registry  *Registry
	outputDir func() string
}

func NewRuntime(reg *Registry, outputDir func() string) *Runtime {
	if reg == nil {
		reg = NewRegistry()
	}
	return &Runtime{registry: reg, outputDir: outputDir}
}
func (r *Runtime) Registry() *Registry { return r.registry }
func (r *Runtime) Generate(ctx context.Context, providerID string, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	req, err := normalizeRequest(req)
	if err != nil {
		return nil, err
	}
	p, err := r.resolveProvider(providerID)
	if err != nil {
		return nil, err
	}
	res, err := p.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Images) == 0 {
		return nil, fmt.Errorf("no images generated")
	}
	if res.Provider == "" {
		res.Provider = p.ID()
	}
	for i := range res.Images {
		if err := r.persistImage(ctx, &res.Images[i], i); err != nil {
			return nil, err
		}
		res.Images[i].Base64 = ""
	}
	return res, nil
}
func (r *Runtime) resolveProvider(id string) (Provider, error) {
	if strings.TrimSpace(id) != "" {
		if p, ok := r.registry.Get(id); ok {
			return p, nil
		}
		return nil, fmt.Errorf("image generation provider not found: %s", id)
	}
	return r.registry.Default()
}
func normalizeRequest(req ImageGenerationRequest) (ImageGenerationRequest, error) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return req, fmt.Errorf("prompt is required")
	}
	req.Mode = canonicalMode(req.Mode)
	if req.N <= 0 {
		req.N = 1
	}
	if req.Size == "" {
		req.Size = "1024x1024"
	}
	if req.Quality == "" {
		req.Quality = "medium"
	}
	if req.Format == "" {
		req.Format = "png"
	}
	if req.SourceImage != nil && req.SourceImage.URL != "" && req.SourceImage.Base64 != "" {
		return req, fmt.Errorf("source_image must use either url or base64, not both")
	}
	if (req.Mode == "edit" || req.Mode == "variation") && (req.SourceImage == nil || (req.SourceImage.URL == "" && req.SourceImage.Base64 == "")) {
		return req, fmt.Errorf("source_image is required for %s mode", req.Mode)
	}
	if req.Mask != "" && req.Mode != "edit" {
		return req, fmt.Errorf("mask is only valid for edit mode")
	}
	return req, nil
}
func (r *Runtime) persistImage(ctx context.Context, img *GeneratedImage, idx int) error {
	if img.LocalPath != "" {
		return nil
	}
	var data []byte
	var err error
	if img.Base64 != "" {
		data, err = base64.StdEncoding.DecodeString(img.Base64)
	} else if img.URL != "" {
		data, err = media.FetchURLBytes(ctx, img.URL)
	} else {
		return fmt.Errorf("image %d has neither base64 nor url", idx)
	}
	if err != nil {
		return err
	}
	dir := r.dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ext := extensionFromMime(img.Mime)
	if ext == "" {
		ext = "png"
	}
	path := filepath.Join(dir, fmt.Sprintf("image_%d_%02d.%s", time.Now().UnixNano(), idx, ext))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	img.LocalPath = filepath.ToSlash(path)
	if img.Mime == "" {
		img.Mime = mimeFromExt(ext)
	}
	return nil
}
func (r *Runtime) dir() string {
	if r != nil && r.outputDir != nil {
		if d := strings.TrimSpace(r.outputDir()); d != "" {
			return d
		}
	}
	return filepath.Join(os.TempDir(), "metiq-media", "images")
}
func extensionFromMime(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if strings.Contains(mime, "png") {
		return "png"
	}
	if strings.Contains(mime, "jpeg") || strings.Contains(mime, "jpg") {
		return "jpg"
	}
	if strings.Contains(mime, "webp") {
		return "webp"
	}
	if strings.Contains(mime, "gif") {
		return "gif"
	}
	return "bin"
}
func mimeFromExt(ext string) string {
	switch strings.TrimPrefix(strings.ToLower(ext), ".") {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}
