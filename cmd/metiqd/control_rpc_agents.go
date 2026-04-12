package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleAgentRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	dmBus := h.deps.dmBus
	docsRepo := h.deps.docsRepo
	memoryIndex := h.deps.memoryIndex
	tools := h.deps.tools
	pluginMgr := h.deps.pluginMgr
	agentJobs := h.deps.agentJobs
	sessionRouter := h.deps.sessionRouter
	agentRegistry := h.deps.agentRegistry
	agentRuntime := h.deps.agentRuntime
	toolRegistry := h.deps.toolRegistry

	switch method {
	case methods.MethodAgent:
		req, err := methods.DecodeAgentParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req.SessionID == "" {
			req.SessionID = in.FromPubKey
		}
		if agentJobs == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("agent runtime not configured")
		}
		var rt agent.Runtime
		if sessionRouter != nil && agentRegistry != nil {
			activeAgentID := sessionRouter.Get(req.SessionID)
			rt = agentRegistry.Get(activeAgentID)
		}
		if rt == nil {
			rt = agentRuntime
		}
		if rt == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("agent runtime not configured")
		}
		activeAgentID := ""
		if sessionRouter != nil {
			activeAgentID = sessionRouter.Get(req.SessionID)
		}
		rt = applyAgentProfileFilter(ctx, rt, req.SessionID, cfg, docsRepo)
		var fallbackRuntimes []agent.Runtime
		primaryLabel := strings.TrimSpace(cfg.Agent.DefaultModel)
		if primaryLabel == "" {
			primaryLabel = "primary"
		}
		runtimeLabels := []string{primaryLabel}
		if agCfg, ok := resolveAgentConfigByID(cfg, activeAgentID); ok {
			if strings.TrimSpace(agCfg.Model) != "" {
				primaryLabel = strings.TrimSpace(agCfg.Model)
				runtimeLabels[0] = primaryLabel
			}
			for _, fbModel := range agCfg.FallbackModels {
				fbModel = strings.TrimSpace(fbModel)
				if fbModel == "" {
					continue
				}
				fbRt, fbErr := buildRuntimeForAgentModel(cfg, agCfg, fbModel, toolRegistry)
				if fbErr == nil && fbRt != nil {
					fbRt = applyAgentProfileFilterForAgent(ctx, fbRt, activeAgentID, cfg, docsRepo)
					fallbackRuntimes = append(fallbackRuntimes, fbRt)
					runtimeLabels = append(runtimeLabels, fbModel)
				}
			}
		}
		runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
		snapshot := agentJobs.Begin(runID, req.SessionID)
		go executeAgentRunWithFallbacks(runID, req, rt, fallbackRuntimes, runtimeLabels, memoryIndex, agentJobs)
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"run_id": runID, "status": "accepted", "accepted_at": snapshot.StartedAt})}, true, nil
	case methods.MethodAgentWait:
		req, err := methods.DecodeAgentWaitParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if agentJobs == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("agent runtime not configured")
		}
		snap, ok := agentJobs.Wait(ctx, req.RunID, time.Duration(req.TimeoutMS)*time.Millisecond)
		if !ok {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("run not found")
		}
		if snap.Status == "pending" {
			return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"run_id": req.RunID, "status": "timeout"})}, true, nil
		}
		out := map[string]any{"run_id": req.RunID, "status": snap.Status, "started_at": snap.StartedAt, "ended_at": snap.EndedAt}
		if snap.Err != "" {
			out["error"] = snap.Err
		}
		if snap.Result != "" {
			out["result"] = snap.Result
		}
		if snap.FallbackUsed {
			out["fallback_used"] = true
			out["fallback_from"] = snap.FallbackFrom
			out["fallback_to"] = snap.FallbackTo
			if snap.FallbackReason != "" {
				out["fallback_reason"] = truncateRunes(snap.FallbackReason, 200)
			}
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodAgentIdentityGet:
		req, err := methods.DecodeAgentIdentityParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		agentID := strings.TrimSpace(req.AgentID)
		sessionID := strings.TrimSpace(req.SessionID)
		if sessionID == "" {
			sessionID = in.FromPubKey
		}
		if agentID == "" && sessionRouter != nil {
			agentID = sessionRouter.Get(sessionID)
		}
		if agentID == "" {
			agentID = "main"
		}
		displayName := "Metiq Agent"
		if docsRepo != nil {
			if ag, err2 := docsRepo.GetAgent(ctx, agentID); err2 == nil && ag.Name != "" {
				displayName = ag.Name
			}
		}
		pubkey := strings.TrimSpace(in.FromPubKey)
		if dmBus != nil {
			pubkey = dmBus.PublicKey()
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agent_id": agentID, "display_name": displayName, "session_id": sessionID, "pubkey": pubkey})}, true, nil
	case methods.MethodAgentsList:
		req, err := methods.DecodeAgentsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.ListAgents(ctx, docsRepo, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodAgentsCreate:
		req, err := methods.DecodeAgentsCreateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if _, err := docsRepo.GetAgent(ctx, req.AgentID); err == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("agent %q already exists", req.AgentID)
		} else if !errors.Is(err, state.ErrNotFound) {
			return nostruntime.ControlRPCResult{}, true, err
		}
		doc := state.AgentDoc{Version: 1, AgentID: req.AgentID, Name: req.Name, Workspace: req.Workspace, Model: req.Model, Meta: req.Meta}
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if agentRegistry != nil {
			if rt, rtErr := agent.BuildRuntimeForModel(req.Model, tools); rtErr == nil {
				agentRegistry.Set(req.AgentID, rt)
			} else {
				log.Printf("agents.create: runtime build warning id=%s model=%q err=%v", req.AgentID, req.Model, rtErr)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent": doc}}, true, nil
	case methods.MethodAgentsUpdate:
		req, err := methods.DecodeAgentsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		doc, err := docsRepo.GetAgent(ctx, req.AgentID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req.Name != "" {
			doc.Name = req.Name
		}
		if req.Workspace != "" {
			doc.Workspace = req.Workspace
		}
		if req.Model != "" {
			doc.Model = req.Model
		}
		doc.Meta = mergeSessionMeta(doc.Meta, req.Meta)
		if doc.Version == 0 {
			doc.Version = 1
		}
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if agentRegistry != nil && req.Model != "" {
			if rt, rtErr := agent.BuildRuntimeForModel(doc.Model, tools); rtErr == nil {
				agentRegistry.Set(req.AgentID, rt)
			} else {
				log.Printf("agents.update: runtime rebuild warning id=%s model=%q err=%v", req.AgentID, doc.Model, rtErr)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent": doc}}, true, nil
	case methods.MethodAgentsDelete:
		req, err := methods.DecodeAgentsDeleteParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		doc, err := docsRepo.GetAgent(ctx, req.AgentID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		doc.Deleted = true
		doc.Meta = mergeSessionMeta(doc.Meta, map[string]any{"deleted_at": time.Now().Unix()})
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if agentRegistry != nil {
			agentRegistry.Remove(req.AgentID)
		}
		if sessionRouter != nil {
			for sessionID, aid := range sessionRouter.List() {
				if aid == req.AgentID {
					sessionRouter.Unassign(sessionID)
				}
			}
		}
		sessions, sessErr := docsRepo.ListSessions(ctx, 5000)
		if sessErr != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("agents.delete: list sessions for cleanup: %w", sessErr)
		}
		for _, sess := range sessions {
			if sess.Meta == nil {
				continue
			}
			aid, _ := sess.Meta["agent_id"].(string)
			if aid != req.AgentID {
				continue
			}
			sessionID := strings.TrimSpace(sess.SessionID)
			if sessionID == "" {
				continue
			}
			if _, err := updateExistingSessionDoc(ctx, docsRepo, sessionID, sess.PeerPubKey, func(session *state.SessionDoc) error {
				if session.Meta != nil {
					delete(session.Meta, "agent_id")
				}
				return nil
			}); err != nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("agents.delete: cleanup session %q: %w", sessionID, err)
			}
			if sessionRouter != nil {
				sessionRouter.Unassign(sessionID)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent_id": req.AgentID, "deleted": true}}, true, nil
	case methods.MethodAgentsAssign:
		req, err := methods.DecodeAgentsAssignParams(in.Params)
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
		if sessionRouter != nil {
			sessionRouter.Assign(req.SessionID, req.AgentID)
		}
		persisted := true
		if err := updateSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, map[string]any{"agent_id": req.AgentID})
			return nil
		}); err != nil {
			persisted = false
			log.Printf("agents.assign: persist session meta warning session=%s err=%v", req.SessionID, err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent_id": req.AgentID, "persisted": persisted, "durability": "best_effort"}}, true, nil
	case methods.MethodAgentsUnassign:
		req, err := methods.DecodeAgentsUnassignParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if sessionRouter != nil {
			sessionRouter.Unassign(req.SessionID)
		}
		persisted := true
		if _, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			if session.Meta != nil {
				delete(session.Meta, "agent_id")
			}
			return nil
		}); err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				persisted = false
				log.Printf("agents.unassign: load session warning session=%s err=%v", req.SessionID, err)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session_id": req.SessionID, "persisted": persisted, "durability": "best_effort"}}, true, nil
	case methods.MethodAgentsActive:
		req, err := methods.DecodeAgentsActiveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var registered []string
		if agentRegistry != nil {
			registered = agentRegistry.Registered()
			sort.Strings(registered)
		}
		var assignments []map[string]any
		if sessionRouter != nil {
			for sessionID, agentID := range sessionRouter.List() {
				assignments = append(assignments, map[string]any{"session_id": sessionID, "agent_id": agentID})
			}
			sortRecordsByKeyDesc(assignments, "session_id")
		}
		if req.Limit > 0 && len(assignments) > req.Limit {
			assignments = assignments[:req.Limit]
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"registered": registered, "assignments": assignments}}, true, nil
	case methods.MethodAgentsFilesList:
		req, err := methods.DecodeAgentsFilesListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.ListAgentFiles(ctx, docsRepo, req.AgentID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodAgentsFilesGet:
		req, err := methods.DecodeAgentsFilesGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.GetAgentFile(ctx, docsRepo, req.AgentID, req.Name)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodAgentsFilesSet:
		req, err := methods.DecodeAgentsFilesSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.SetAgentFile(ctx, docsRepo, req.AgentID, req.Name, req.Content)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodToolsCatalog:
		req, err := methods.DecodeToolsCatalogParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := methods.IsKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		agentID := methods.DefaultAgentID(req.AgentID)
		groups := buildToolCatalogGroups(cfg, tools, req.IncludePlugins, pluginMgr)
		if req.Profile != nil && *req.Profile != "" {
			profileID := strings.TrimSpace(strings.ToLower(*req.Profile))
			if agent.LookupProfile(profileID) == nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown profile %q; valid: %s", profileID, strings.Join(agent.ProfileListSorted(), ", "))
			}
			groups = agent.FilterCatalogByProfile(groups, profileID)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profiles": defaultToolProfiles(), "groups": groups})}, true, nil
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
