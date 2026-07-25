package main

// control_rpc_skills_surface.go — control-RPC handlers for the skills discovery
// quartet (skills.search / skills.detail / skills.securityVerdicts /
// skills.skillCard, swarmstr-xfny.1) and the chunked skill-archive upload flow
// (skills.upload.begin / skills.upload.chunk / skills.upload.commit,
// swarmstr-xfny.2). Served over the control-RPC tooling surface only, mirroring
// the skills.curator.*/skills.proposals.* wiring.

import (
	"context"
	"errors"
	"fmt"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	skillspkg "metiq/internal/skills"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleSkillsSurfaceRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	docsRepo := h.deps.docsRepo
	switch method {
	case methods.MethodSkillsSearch:
		req, err := methods.DecodeSkillsSearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := skillspkg.SearchSkills(cfg, defaultAgentID(""), req.Query, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsDetail:
		req, err := methods.DecodeSkillsDetailParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := skillspkg.SkillDetail(cfg, defaultAgentID(""), req.Slug)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("skill %q not found", req.Slug)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsSecurityVerdicts:
		req, err := methods.DecodeSkillsSecurityVerdictsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := skillspkg.BuildSecurityVerdicts(cfg, defaultAgentID(req.AgentID))
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsSkillCard:
		req, err := methods.DecodeSkillsSkillCardParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := skillspkg.BuildSkillCard(cfg, defaultAgentID(req.AgentID), req.SkillKey)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("skill %q not found", req.SkillKey)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsUploadBegin:
		req, err := methods.DecodeSkillsUploadBeginParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := skillspkg.NewUploadStore().Begin(skillspkg.UploadBeginInput{
			Kind:           req.Kind,
			Slug:           req.Slug,
			SizeBytes:      req.SizeBytes,
			Sha256:         req.Sha256,
			Force:          req.Force,
			IdempotencyKey: req.IdempotencyKey,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsUploadChunk:
		req, err := methods.DecodeSkillsUploadChunkParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		content, err := req.Content()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := skillspkg.NewUploadStore().Chunk(req.UploadID, req.Offset, content)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("upload %q not found", req.UploadID)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsUploadCommit:
		req, err := methods.DecodeSkillsUploadCommitParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := skillspkg.NewUploadStore().Commit(req.UploadID, req.Sha256)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("upload %q not found", req.UploadID)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
