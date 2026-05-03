package musicgen

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
func (r *Runtime) Generate(ctx context.Context, providerID string, req MusicGenerationRequest) (*MusicGenerationResult, error) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if req.Duration <= 0 {
		req.Duration = 30
	}
	if req.Format == "" {
		req.Format = "mp3"
	}
	p, err := r.resolve(providerID)
	if err != nil {
		return nil, err
	}
	res, err := p.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("music provider returned nil result")
	}
	if res.Provider == "" {
		res.Provider = p.ID()
	}
	if res.Duration == 0 {
		res.Duration = req.Duration
	}
	if res.Audio.Format == "" {
		res.Audio.Format = req.Format
	}
	if err := r.persist(ctx, &res.Audio); err != nil {
		return nil, err
	}
	res.Audio.Base64 = ""
	return res, nil
}
func (r *Runtime) resolve(id string) (Provider, error) {
	if strings.TrimSpace(id) != "" {
		if p, ok := r.registry.Get(id); ok {
			return p, nil
		}
		return nil, fmt.Errorf("music generation provider not found: %s", id)
	}
	return r.registry.Default()
}
func (r *Runtime) persist(ctx context.Context, a *GeneratedAudio) error {
	if a.LocalPath != "" {
		return nil
	}
	var data []byte
	var err error
	if a.Base64 != "" {
		data, err = base64.StdEncoding.DecodeString(a.Base64)
	} else if a.URL != "" {
		data, err = media.FetchURLBytes(ctx, a.URL)
	} else {
		return fmt.Errorf("generated audio has neither base64 nor url")
	}
	if err != nil {
		return err
	}
	dir := r.dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ext := strings.TrimPrefix(a.Format, ".")
	if ext == "" {
		ext = "mp3"
	}
	path := filepath.Join(dir, fmt.Sprintf("music_%d.%s", time.Now().UnixNano(), ext))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	a.LocalPath = filepath.ToSlash(path)
	a.Format = ext
	return nil
}
func (r *Runtime) dir() string {
	if r.outputDir != nil {
		if d := strings.TrimSpace(r.outputDir()); d != "" {
			return d
		}
	}
	return filepath.Join(os.TempDir(), "metiq-media", "music")
}
