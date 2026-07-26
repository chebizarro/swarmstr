package main

// control_rpc_skills_surface.go — control-RPC handlers for the skills discovery
// quartet (skills.search / skills.detail / skills.securityVerdicts /
// skills.skillCard, swarmstr-xfny.1) and the chunked skill-archive upload flow
// (skills.upload.begin / skills.upload.chunk / skills.upload.commit,
// swarmstr-xfny.2). Served over the control-RPC tooling surface only, mirroring
// the skills.curator.*/skills.proposals.* wiring.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
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
	case methods.MethodSkillsProposalsHistoryStatus:
		req, err := methods.DecodeSkillsProposalsHistoryStatusParams(in.Params)
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
		out, err := skillspkg.HistoryStatus(ctx, cfg, defaultAgentID(req.AgentID))
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"status": out}}, true, nil
	case methods.MethodSkillsProposalsHistoryScan:
		req, err := methods.DecodeSkillsProposalsHistoryScanParams(in.Params)
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
		out, err := skillspkg.HistoryScan(ctx, cfg, defaultAgentID(req.AgentID), skillspkg.SkillHistoryScanParams{
			Direction: req.Direction,
			Cursor:    req.Cursor,
			Limit:     req.Limit,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsProposalsRequestRevision:
		req, err := methods.DecodeSkillsProposalsRequestRevisionParams(in.Params)
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
		out, err := h.launchProposalRevisionRun(cfg, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("proposal %q not found", req.ProposalID)
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

// proposalRevisionRuns maps an idempotency key (proposalId\x00idempotencyKey) to
// the managed run launched for it, so a replayed skills.proposals.requestRevision
// returns the same run instead of double-launching an agent. The map self-heals:
// once a run leaves the job registry's retention window, a replay re-launches and
// replaces the stale mapping, so the map stays bounded to live/recent runs.
var proposalRevisionRuns = struct {
	mu    sync.Mutex
	byKey map[string]string
}{byKey: map[string]string{}}

// launchProposalRevisionRun resolves a pending proposal, mints (or replays) a
// managed run whose instruction is the revision request against the proposal's
// skill draft, and returns {runId, status}. This is metiq's native equivalent of
// OpenClaw's forwardSkillWorkshopRevisionToChatSend: the managed-run machinery
// (launchManagedRun), not chat.send, tracks the revision agent run.
func (h controlRPCHandler) launchProposalRevisionRun(cfg state.ConfigDoc, req methods.SkillsProposalsRequestRevisionRequest) (map[string]any, error) {
	store := skillspkg.NewProposalStore(cfg, defaultAgentID(req.AgentID))
	rec, err := store.Load(req.ProposalID)
	if err != nil {
		return nil, err
	}
	if rec.Status != "pending" {
		return nil, fmt.Errorf("proposal %q is %s; only pending proposals can be revised", rec.ID, rec.Status)
	}
	draft, err := store.DraftContent(req.ProposalID)
	if err != nil {
		return nil, err
	}

	controller := currentAgentRunController()
	jobs := controller.jobs
	if jobs == nil {
		return nil, fmt.Errorf("agent job registry not configured")
	}
	targetAgent := strings.TrimSpace(req.TargetAgentID)
	if targetAgent == "" {
		targetAgent = defaultAgentID(req.AgentID)
	}
	// Resolve the runtime exactly once and carry it through registration + launch
	// so a concurrent runtime reload cannot re-route the run mid-flight.
	resolvedAgent, rt := controller.resolveInboundChannelRuntime(targetAgent, req.SessionKey)
	if rt == nil {
		return nil, fmt.Errorf("agent runtime not configured for %q", targetAgent)
	}
	agentReq := methods.AgentRequest{
		SessionID:      req.SessionKey,
		SessionKey:     req.SessionKey,
		Message:        proposalRevisionMessage(rec, draft, req.Instructions),
		AgentID:        resolvedAgent,
		IdempotencyKey: req.IdempotencyKey,
		Label:          "skill-proposal-revision",
	}

	// Unkeyed requests are never deduplicated: mint a unique id, register, launch.
	if req.IdempotencyKey == "" {
		runID := fmt.Sprintf("skill-revision-%d", time.Now().UnixNano())
		jobs.Begin(runID, req.SessionKey)
		return h.launchRevisionRun(controller, jobs, runID, rt, agentReq, false)
	}

	// Idempotency: reserve the run id AND register the job atomically under the
	// lock, so a concurrent replay observes a live job (never a not-yet-begun
	// reservation it could misclassify as stale and relaunch). Publishing the map
	// entry only after jobs.Begin closes the double-launch window.
	idemKey := req.ProposalID + "\x00" + req.IdempotencyKey
	proposalRevisionRuns.mu.Lock()
	evictStaleRevisionRunsLocked(jobs)
	if existing, ok := proposalRevisionRuns.byKey[idemKey]; ok {
		if snap, live := jobs.Get(existing); live {
			proposalRevisionRuns.mu.Unlock()
			return map[string]any{"runId": existing, "status": snap.Status, "idempotent": true}, nil
		}
		delete(proposalRevisionRuns.byKey, idemKey)
	}
	runID := revisionRunIDForKey(idemKey)
	jobs.Begin(runID, req.SessionKey)
	proposalRevisionRuns.byKey[idemKey] = runID
	proposalRevisionRuns.mu.Unlock()
	return h.launchRevisionRun(controller, jobs, runID, rt, agentReq, true)
}

// launchRevisionRun launches an already-registered managed run and returns its
// {runId, status}. The job MUST already be jobs.Begin'd by the caller.
func (h controlRPCHandler) launchRevisionRun(controller agentRunController, jobs *agentJobRegistry, runID string, rt agent.Runtime, agentReq methods.AgentRequest, idempotent bool) (map[string]any, error) {
	if err := controller.launchManagedRun(runID, agentReq, rt, nil, nil, h.deps.memoryIndex, jobs, nil); err != nil {
		return nil, err
	}
	status := "pending"
	if snap, ok := jobs.Get(runID); ok {
		status = snap.Status
	}
	out := map[string]any{"runId": runID, "status": status}
	if idempotent {
		out["idempotent"] = true
	}
	return out, nil
}

// evictStaleRevisionRunsLocked drops idempotency mappings whose managed run has
// left the job registry's retention window, bounding the map to live/recent runs.
// Caller must hold proposalRevisionRuns.mu.
func evictStaleRevisionRunsLocked(jobs *agentJobRegistry) {
	for key, runID := range proposalRevisionRuns.byKey {
		if _, live := jobs.Get(runID); !live {
			delete(proposalRevisionRuns.byKey, key)
		}
	}
}

func revisionRunIDForKey(idemKey string) string {
	sum := sha256.Sum256([]byte(idemKey))
	return "skill-revision-" + hex.EncodeToString(sum[:])[:16]
}

// proposalRevisionMessage renders the revision request into an agent prompt: the
// proposal target + operator instructions + the current SKILL.md draft.
func proposalRevisionMessage(rec skillspkg.ProposalRecord, draft, instructions string) string {
	skillKey := strings.TrimSpace(rec.Target.SkillKey)
	if skillKey == "" {
		skillKey = strings.TrimSpace(rec.Target.SkillName)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Revise skill proposal %s (%q) targeting skill %q (kind %s).\n\n", rec.ID, rec.Title, skillKey, rec.Kind)
	b.WriteString("Revision instructions:\n")
	b.WriteString(instructions)
	b.WriteString("\n\nCurrent draft (SKILL.md):\n")
	b.WriteString(draft)
	return b.String()
}
