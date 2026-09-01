package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	skillspkg "metiq/internal/skills"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleToolingRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	docsRepo := h.deps.docsRepo
	configState := h.deps.configState
	tools := h.deps.tools
	pluginMgr := h.deps.pluginMgr

	_ = configState
	_ = pluginMgr
	switch method {
	case methods.MethodModelsList:
		req, err := methods.DecodeModelsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"models": defaultModelsCatalog(cfg.Providers)}}, true, nil
	case methods.MethodToolsCatalog:
		req, err := methods.DecodeToolsCatalogParams(in.Params)
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
		agentID := defaultAgentID(req.AgentID)
		groups := buildToolCatalogGroups(cfg, tools, req.IncludePlugins, pluginMgr)
		if req.Profile != nil && *req.Profile != "" {
			profileID := strings.TrimSpace(strings.ToLower(*req.Profile))
			if agent.LookupProfile(profileID) == nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown profile %q; valid: %s", profileID, strings.Join(agent.ProfileListSorted(), ", "))
			}
			groups = agent.FilterCatalogByProfile(groups, profileID)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profiles": defaultToolProfiles(), "groups": groups})}, true, nil
	case methods.MethodToolsProfileGet:
		req, err := methods.DecodeToolsProfileGetParams(in.Params)
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
		agentID := defaultAgentID(req.AgentID)
		doc, _ := docsRepo.GetAgent(ctx, agentID)
		profileID := agent.DefaultProfile
		if p, ok := doc.Meta[agent.AgentProfileKey].(string); ok && p != "" {
			profileID = p
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profile": profileID})}, true, nil
	case methods.MethodToolsProfileSet:
		req, err := methods.DecodeToolsProfileSetParams(in.Params)
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
		if agent.LookupProfile(req.Profile) == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown profile %q; valid: %s", req.Profile, strings.Join(agent.ProfileListSorted(), ", "))
		}
		agentID := defaultAgentID(req.AgentID)
		doc, _ := docsRepo.GetAgent(ctx, agentID)
		if doc.AgentID == "" {
			doc = state.AgentDoc{Version: 1, AgentID: agentID}
		}
		if doc.Meta == nil {
			doc.Meta = map[string]any{}
		}
		doc.Meta[agent.AgentProfileKey] = req.Profile
		if _, err := docsRepo.PutAgent(ctx, agentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profile": req.Profile})}, true, nil
	case methods.MethodSkillsStatus:
		req, err := methods.DecodeSkillsStatusParams(in.Params)
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
		agentID := defaultAgentID(req.AgentID)
		return nostruntime.ControlRPCResult{Result: buildSkillsStatusReport(cfg, agentID)}, true, nil
	case methods.MethodSkillsBins:
		req, err := methods.DecodeSkillsBinsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: applySkillsBins(cfg)}, true, nil
	case methods.MethodSkillsInstall:
		req, err := methods.DecodeSkillsInstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		_, installResult, err := applySkillInstall(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: installResult}, true, nil
	case methods.MethodSkillsUpdate:
		req, err := methods.DecodeSkillsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		_, entry, err := applySkillUpdate(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "skillKey": strings.ToLower(strings.TrimSpace(req.SkillKey)), "config": entry}}, true, nil
	case methods.MethodSkillsCuratorStatus:
		req, err := methods.DecodeSkillsCuratorStatusParams(in.Params)
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
		out, err := skillspkg.BuildCuratorStatus(cfg, defaultAgentID(req.AgentID))
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsCuratorPin, methods.MethodSkillsCuratorUnpin, methods.MethodSkillsCuratorRestore:
		req, err := methods.DecodeSkillsCuratorSkillParams(in.Params)
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
		agentID := defaultAgentID(req.AgentID)
		var entry map[string]any
		switch method {
		case methods.MethodSkillsCuratorPin:
			entry, err = skillspkg.SetCuratorPin(cfg, agentID, req.Skill, true)
		case methods.MethodSkillsCuratorUnpin:
			entry, err = skillspkg.SetCuratorPin(cfg, agentID, req.Skill, false)
		default:
			entry, err = skillspkg.RestoreCuratorSkill(cfg, agentID, req.Skill)
		}
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("skill %q not found", req.Skill)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"skill": entry}}, true, nil
	case methods.MethodSkillsProposalsList:
		req, err := methods.DecodeSkillsProposalsListParams(in.Params)
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
		out, err := skillspkg.NewProposalStore(cfg, defaultAgentID(req.AgentID)).List()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsProposalsInspect:
		req, err := methods.DecodeSkillsProposalsInspectParams(in.Params)
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
		out, err := skillspkg.NewProposalStore(cfg, defaultAgentID(req.AgentID)).Inspect(req.ProposalID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("proposal %q not found", req.ProposalID)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
	case methods.MethodSkillsProposalsCreate, methods.MethodSkillsProposalsUpdate:
		req, err := methods.DecodeSkillsProposalsCreateParams(in.Params)
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
		store := skillspkg.NewProposalStore(cfg, defaultAgentID(req.AgentID))
		var rec skillspkg.ProposalRecord
		if method == methods.MethodSkillsProposalsUpdate {
			rec, err = store.Update(proposalCreateInput(req))
		} else {
			rec, err = store.Create(proposalCreateInput(req))
		}
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := appendSkillProposalEvent(cfg, req.AgentID, rec, "created", "", map[string]any{"kind": rec.Kind}, nil); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"proposal": rec}}, true, nil
	case methods.MethodSkillsProposalsRevise:
		req, err := methods.DecodeSkillsProposalsReviseParams(in.Params)
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
		rec, err := skillspkg.NewProposalStore(cfg, defaultAgentID(req.AgentID)).Revise(req.ProposalID, proposalReviseInput(req))
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("proposal %q not found", req.ProposalID)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		if err := appendSkillProposalEvent(cfg, req.AgentID, rec, "revised", "", nil, nil); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"proposal": rec}}, true, nil
	case methods.MethodSkillsProposalsApply, methods.MethodSkillsProposalsReject, methods.MethodSkillsProposalsQuarantine:
		req, err := methods.DecodeSkillsProposalsIDParams(in.Params)
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
		store := skillspkg.NewProposalStore(cfg, defaultAgentID(req.AgentID))
		switch method {
		case methods.MethodSkillsProposalsApply:
			out, applyErr := store.Apply(req.ProposalID)
			if applyErr != nil {
				if errors.Is(applyErr, state.ErrNotFound) {
					return nostruntime.ControlRPCResult{}, true, fmt.Errorf("proposal %q not found", req.ProposalID)
				}
				return nostruntime.ControlRPCResult{}, true, applyErr
			}
			rec, loadErr := store.Load(req.ProposalID)
			if loadErr != nil {
				return nostruntime.ControlRPCResult{}, true, loadErr
			}
			if err := appendSkillProposalEvent(cfg, req.AgentID, rec, "applied", "", nil, nil); err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
			return nostruntime.ControlRPCResult{Result: out}, true, nil
		default:
			var rec skillspkg.ProposalRecord
			if method == methods.MethodSkillsProposalsReject {
				rec, err = store.Reject(req.ProposalID, req.Reason)
			} else {
				rec, err = store.Quarantine(req.ProposalID, req.Reason)
			}
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					return nostruntime.ControlRPCResult{}, true, fmt.Errorf("proposal %q not found", req.ProposalID)
				}
				return nostruntime.ControlRPCResult{}, true, err
			}
			eventType := "quarantined"
			if method == methods.MethodSkillsProposalsReject {
				eventType = "rejected"
			}
			if err := appendSkillProposalEvent(cfg, req.AgentID, rec, eventType, "", map[string]any{"reason": req.Reason}, nil); err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
			return nostruntime.ControlRPCResult{Result: map[string]any{"proposal": rec}}, true, nil
		}
	case methods.MethodPluginsInstall:
		req, err := methods.DecodePluginsInstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyPluginInstallRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodPluginsUninstall:
		req, err := methods.DecodePluginsUninstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyPluginUninstallRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodPluginsUpdate:
		req, err := methods.DecodePluginsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyPluginUpdateRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodPluginsRegistryList:
		req, err := methods.DecodePluginsRegistryListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := handlePluginsRegistryList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodPluginsRegistryGet:
		req, err := methods.DecodePluginsRegistryGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := handlePluginsRegistryGet(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodPluginsRegistrySearch:
		req, err := methods.DecodePluginsRegistrySearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := handlePluginsRegistrySearch(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
