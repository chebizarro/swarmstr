package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/gateway/methods"
	hookspkg "metiq/internal/hooks"
	nostruntime "metiq/internal/nostr/runtime"
	pluginregistry "metiq/internal/plugins/registry"
	"metiq/internal/store/state"
)

func TestHooksStatusProjectsSelectedWorkspaceAndPluginRegistry(t *testing.T) {
	workspaceDir := t.TempDir()
	t.Setenv("METIQ_WORKSPACE", workspaceDir)
	hookDir := filepath.Join(workspaceDir, "hooks", "workspace-hook")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `---
name: Workspace Hook
description: selected agent hook
metadata:
  openclaw:
    events: [agent:start]
---
`
	if err := os.WriteFile(filepath.Join(hookDir, "HOOK.md"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "handler.sh"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	manager := hookspkg.NewManager()
	manager.SetEnabled("workspace-hook", true)
	plugins := pluginregistry.NewUnifiedRegistry()
	if _, err := plugins.Hooks().Register("plugin-a", pluginregistry.HookRegistrationData{
		HookID: "plugin-hook", Events: []pluginregistry.HookEvent{pluginregistry.HookAgentEnd},
		Source: pluginregistry.HookSourceNative, Raw: map[string]any{"name": "Plugin Hook"},
	}); err != nil {
		t.Fatal(err)
	}
	h := newControlRPCHandler(controlRPCDeps{hooksMgrFull: manager, pluginRegistry: plugins})
	raw, _ := json.Marshal(map[string]any{"agentId": "main"})
	result, handled, err := h.handleHooksStatusRPC(context.Background(), nostruntime.ControlRPCInbound{Method: methods.MethodHooksStatus, Params: raw, Internal: true}, methods.MethodHooksStatus, state.ConfigDoc{Extra: map[string]any{}})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	report := result.Result.(map[string]any)
	if report["workspaceDir"] != workspaceDir || report["agentId"] != "main" {
		t.Fatalf("report identity=%+v", report)
	}
	entries := report["hooks"].([]map[string]any)
	if len(entries) != 2 {
		t.Fatalf("hooks=%+v", entries)
	}
	byKey := map[string]map[string]any{}
	for _, entry := range entries {
		byKey[entry["hookKey"].(string)] = entry
	}
	if byKey["workspace-hook"]["loadable"] != true || byKey["workspace-hook"]["managedByPlugin"] != false {
		t.Fatalf("workspace hook=%+v", byKey["workspace-hook"])
	}
	if byKey["plugin-hook"]["pluginId"] != "plugin-a" || byKey["plugin-hook"]["managedByPlugin"] != true {
		t.Fatalf("plugin hook=%+v", byKey["plugin-hook"])
	}
}
