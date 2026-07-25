package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"

	artifactspkg "metiq/internal/gateway/artifacts"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

// artifacts.* handlers (WS-A/A7 deferred slice). Artifacts live in the
// workspace-rooted content-addressed store under
// <workspace>/.metiq/artifacts; the store is cheap to construct, so handlers
// derive it from the live config on every call instead of daemon wiring.

func (h controlRPCHandler) artifactsStore(cfg state.ConfigDoc) *artifactspkg.Store {
	return artifactspkg.NewStore(filepath.Join(workspace.ResolveWorkspaceDir(cfg, ""), ".metiq", "artifacts"))
}

func artifactNotFoundErr(err error, artifactID string) error {
	if errors.Is(err, artifactspkg.ErrNotFound) {
		return fmt.Errorf("artifact %q not found", artifactID)
	}
	return err
}

func (h controlRPCHandler) handleArtifactsList(_ context.Context, in nostruntime.ControlRPCInbound, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeArtifactsListParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	summaries, err := h.artifactsStore(cfg).List(artifactspkg.Query{
		SessionKey: req.SessionKey,
		RunID:      req.RunID,
		TaskID:     req.TaskID,
		AgentID:    req.AgentID,
	})
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"artifacts": summaries}}, true, nil
}

func (h controlRPCHandler) handleArtifactsGet(_ context.Context, in nostruntime.ControlRPCInbound, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeArtifactsGetParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	summary, err := h.artifactsStore(cfg).Get(req.ArtifactID, artifactspkg.Query{
		SessionKey: req.SessionKey,
		RunID:      req.RunID,
		TaskID:     req.TaskID,
		AgentID:    req.AgentID,
	})
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, artifactNotFoundErr(err, req.ArtifactID)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"artifact": summary}}, true, nil
}

func (h controlRPCHandler) handleArtifactsDownload(_ context.Context, in nostruntime.ControlRPCInbound, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeArtifactsDownloadParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	summary, data, err := h.artifactsStore(cfg).Download(req.ArtifactID, artifactspkg.Query{
		SessionKey: req.SessionKey,
		RunID:      req.RunID,
		TaskID:     req.TaskID,
		AgentID:    req.AgentID,
	})
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, artifactNotFoundErr(err, req.ArtifactID)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"artifact": summary,
		"encoding": "base64",
		"data":     base64.StdEncoding.EncodeToString(data),
	}}, true, nil
}
