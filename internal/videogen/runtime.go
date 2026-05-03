package videogen

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
	registry              *Registry
	outputDir             func() string
	pollInterval, maxWait time.Duration
}

func NewRuntime(reg *Registry, outputDir func() string, pollInterval, maxWait time.Duration) *Runtime {
	if reg == nil {
		reg = NewRegistry()
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	if maxWait <= 0 {
		maxWait = 5 * time.Minute
	}
	return &Runtime{registry: reg, outputDir: outputDir, pollInterval: pollInterval, maxWait: maxWait}
}
func (r *Runtime) Generate(ctx context.Context, providerID string, req VideoGenerationRequest) (*VideoGenerationResult, error) {
	req, err := normalizeRequest(req)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("video provider returned nil result")
	}
	if res.Provider == "" {
		res.Provider = p.ID()
	}
	if strings.EqualFold(res.Status, "pending") && res.JobID != "" {
		res, err = r.poll(ctx, p, res.JobID)
		if err != nil {
			return nil, err
		}
		if res.Provider == "" {
			res.Provider = p.ID()
		}
	}
	if strings.EqualFold(res.Status, "failed") {
		if res.Error != "" {
			return nil, fmt.Errorf("video generation failed: %s", res.Error)
		}
		return nil, fmt.Errorf("video generation failed")
	}
	if strings.EqualFold(res.Status, "completed") && len(res.Videos) == 0 {
		return nil, fmt.Errorf("video generation completed with no videos")
	}
	for i := range res.Videos {
		if err := r.persist(ctx, &res.Videos[i], i); err != nil {
			return nil, err
		}
		res.Videos[i].Base64 = ""
	}
	return res, nil
}
func normalizeRequest(req VideoGenerationRequest) (VideoGenerationRequest, error) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return req, fmt.Errorf("prompt is required")
	}
	req.Mode = canonicalMode(req.Mode)
	if req.Duration <= 0 {
		req.Duration = 5
	}
	if req.Resolution == "" {
		req.Resolution = "720P"
	}
	if req.AspectRatio == "" {
		req.AspectRatio = "16:9"
	}
	if req.SourceAsset != nil && req.SourceAsset.URL != "" && req.SourceAsset.Base64 != "" {
		return req, fmt.Errorf("source_asset must use either url or base64, not both")
	}
	if (req.Mode == "image_to_video" || req.Mode == "video_to_video") && (req.SourceAsset == nil || (req.SourceAsset.URL == "" && req.SourceAsset.Base64 == "")) {
		return req, fmt.Errorf("source_asset is required for %s mode", req.Mode)
	}
	return req, nil
}
func (r *Runtime) resolve(id string) (Provider, error) {
	if strings.TrimSpace(id) != "" {
		if p, ok := r.registry.Get(id); ok {
			return p, nil
		}
		return nil, fmt.Errorf("video generation provider not found: %s", id)
	}
	return r.registry.Default()
}
func (r *Runtime) poll(ctx context.Context, p Provider, jobID string) (*VideoGenerationResult, error) {
	timeout := time.NewTimer(r.maxWait)
	defer timeout.Stop()
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("video generation timed out after %v", r.maxWait)
		case <-ticker.C:
			res, err := p.CheckJob(ctx, jobID)
			if err != nil {
				return nil, err
			}
			if res == nil {
				continue
			}
			switch strings.ToLower(res.Status) {
			case "completed", "succeeded", "success":
				res.Status = "completed"
				return res, nil
			case "failed", "error":
				return nil, fmt.Errorf("video generation failed")
			}
		}
	}
}
func (r *Runtime) persist(ctx context.Context, v *GeneratedVideo, idx int) error {
	if v.LocalPath != "" {
		return nil
	}
	var data []byte
	var err error
	if v.Base64 != "" {
		data, err = base64.StdEncoding.DecodeString(v.Base64)
	} else if v.URL != "" {
		data, err = media.FetchURLBytes(ctx, v.URL)
	} else {
		return fmt.Errorf("completed video has neither local_path, base64, nor url")
	}
	if err != nil {
		return err
	}
	dir := r.dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ext := strings.TrimPrefix(v.Format, ".")
	if ext == "" {
		ext = "mp4"
	}
	path := filepath.Join(dir, fmt.Sprintf("video_%d_%02d.%s", time.Now().UnixNano(), idx, ext))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	v.LocalPath = filepath.ToSlash(path)
	v.Format = ext
	return nil
}
func (r *Runtime) dir() string {
	if r.outputDir != nil {
		if d := strings.TrimSpace(r.outputDir()); d != "" {
			return d
		}
	}
	return filepath.Join(os.TempDir(), "metiq-media", "videos")
}
