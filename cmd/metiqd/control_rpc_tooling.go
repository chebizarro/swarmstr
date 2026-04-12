package main

import (
	"context"
	"fmt"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
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
