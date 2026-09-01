package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"metiq/internal/gateway/methods"
	hookspkg "metiq/internal/hooks"
	nostruntime "metiq/internal/nostr/runtime"
	pluginregistry "metiq/internal/plugins/registry"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

func hookStatusEntry(status hookspkg.HookStatus, baseDir, handlerPath string) map[string]any {
	entry := hookspkg.StatusToMap(status)
	entry["baseDir"] = baseDir
	entry["handlerPath"] = handlerPath
	entry["unknownEvents"] = []string{}
	entry["enabledByConfig"] = status.Enabled
	entry["requirementsSatisfied"] = status.Requires == nil || status.Eligible
	entry["loadable"] = status.Eligible
	entry["managedByPlugin"] = false
	if !status.Enabled {
		entry["blockedReason"] = "disabled"
	} else if !status.Eligible {
		entry["blockedReason"] = "missing handler or requirements"
	}
	return entry
}

func pluginHookStatusEntry(hook *pluginregistry.RegisteredHook) map[string]any {
	events := make([]string, 0, len(hook.Events))
	for _, event := range hook.Events {
		events = append(events, string(event))
	}
	name := hook.ID
	description := ""
	filePath := ""
	baseDir := ""
	handlerPath := ""
	if raw := hook.Raw; raw != nil {
		if value, ok := raw["name"].(string); ok && strings.TrimSpace(value) != "" {
			name = strings.TrimSpace(value)
		}
		if value, ok := raw["description"].(string); ok {
			description = strings.TrimSpace(value)
		}
		if value, ok := raw["filePath"].(string); ok {
			filePath = strings.TrimSpace(value)
		}
		if value, ok := raw["baseDir"].(string); ok {
			baseDir = strings.TrimSpace(value)
		}
		if value, ok := raw["handlerPath"].(string); ok {
			handlerPath = strings.TrimSpace(value)
		}
	}
	return map[string]any{
		"name": name, "description": description, "source": string(hook.Source),
		"pluginId": hook.PluginID, "filePath": filePath, "baseDir": baseDir,
		"handlerPath": handlerPath, "hookKey": hook.ID, "events": events,
		"unknownEvents": []string{}, "always": false, "enabledByConfig": true,
		"requirementsSatisfied": true, "loadable": true, "managedByPlugin": true,
		"requirements": map[string]any{}, "missing": map[string]any{},
		"configChecks": []any{}, "install": []any{}, "priority": hook.Priority,
	}
}

func (h controlRPCHandler) buildHooksStatus(cfg state.ConfigDoc, agentID string) (map[string]any, error) {
	agentID = defaultAgentID(agentID)
	workspaceDir := workspace.ResolveWorkspaceDir(cfg, agentID)
	entries := make([]map[string]any, 0)

	// Bundled and managed hooks are already loaded into the live manager. Do not
	// include its startup workspace entries because those may belong to another
	// agent; the selected workspace is scanned below.
	if h.deps.hooksMgrFull != nil {
		for _, status := range h.deps.hooksMgrFull.List() {
			if status.Source == hookspkg.SourceWorkspace {
				continue
			}
			baseDir := filepath.Dir(status.FilePath)
			entries = append(entries, hookStatusEntry(status, baseDir, filepath.Join(baseDir, "handler.sh")))
		}

		managedOnly, _ := cfg.Extra["managed_hooks_only"].(bool)
		if !managedOnly {
			workspaceHooks, err := hookspkg.ScanDir(filepath.Join(workspaceDir, "hooks"), hookspkg.SourceWorkspace)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			for _, hook := range workspaceHooks {
				handlerPath := filepath.Join(hook.BaseDir, "handler.sh")
				if _, err := os.Stat(handlerPath); err == nil {
					// StatusFor only checks handler presence; a sentinel avoids preparing or
					// executing a shell hook during this read-only projection.
					hook.Handler = func(*hookspkg.Event) error { return nil }
				}
				entries = append(entries, hookStatusEntry(h.deps.hooksMgrFull.StatusFor(hook), hook.BaseDir, handlerPath))
			}
		}
	}

	// Plugin hook registrations are runtime facts and are shared across agents,
	// matching the runtime registry used when an agent turn fires hooks.
	if h.deps.pluginRegistry != nil {
		for _, hook := range h.deps.pluginRegistry.Hooks().List() {
			entries = append(entries, pluginHookStatusEntry(hook))
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i]["hookKey"].(string) < entries[j]["hookKey"].(string)
	})
	return map[string]any{
		"agentId": agentID, "workspaceDir": workspaceDir,
		"managedHooksDir": hookspkg.ManagedHooksDir(), "hooks": entries,
	}, nil
}

func (h controlRPCHandler) handleHooksStatusRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	if method != methods.MethodHooksStatus {
		return nostruntime.ControlRPCResult{}, false, nil
	}
	req, err := methods.DecodeHooksStatusParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if err := isKnownAgentID(ctx, h.deps.docsRepo, req.AgentID); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	result, err := h.buildHooksStatus(cfg, req.AgentID)
	return nostruntime.ControlRPCResult{Result: result}, true, err
}
