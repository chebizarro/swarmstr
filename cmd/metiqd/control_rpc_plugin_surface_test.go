package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	pluginapprovalpkg "metiq/internal/gateway/pluginapproval"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func pluginSurfaceCall(t *testing.T, h controlRPCHandler, method, params string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handlePluginSurfaceRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, cfg)
	if !handled {
		t.Fatalf("method %s was not handled by plugin surface dispatch", method)
	}
	return result, err
}

func pluginTestConfig() state.ConfigDoc {
	return state.ConfigDoc{
		Extra: map[string]any{
			"extensions": map[string]any{
				"entries": map[string]any{
					"weather":       map[string]any{"enabled": true},
					"disabled-plug": map[string]any{"enabled": false},
				},
				"installs": map[string]any{
					"weather":        map[string]any{"source": "npm", "installPath": "/x/weather", "version": "1.2.3"},
					"orphan-install": map[string]any{"source": "git"},
				},
			},
		},
	}
}

func TestBuildPluginListMergesConfig(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	list := h.buildPluginList(pluginTestConfig())
	if len(list) != 3 {
		t.Fatalf("expected 3 plugins, got %d: %+v", len(list), list)
	}
	byID := map[string]map[string]any{}
	for _, rec := range list {
		byID[rec["id"].(string)] = rec
	}
	weather := byID["weather"]
	if weather == nil || weather["enabled"] != true || weather["loaded"] != false {
		t.Fatalf("unexpected weather record: %+v", weather)
	}
	if weather["source"] != "npm" || weather["version"] != "1.2.3" {
		t.Fatalf("weather install metadata missing: %+v", weather)
	}
	if byID["disabled-plug"]["enabled"] != false {
		t.Fatalf("disabled-plug should be disabled: %+v", byID["disabled-plug"])
	}
	// Install without an entry is not enabled (not eligible for loading).
	orphan := byID["orphan-install"]
	if orphan["enabled"] != false || orphan["hasEntry"] != false || orphan["source"] != "git" {
		t.Fatalf("unexpected orphan-install record: %+v", orphan)
	}
}

func TestPluginsListAndSearchHandlers(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	cfg := pluginTestConfig()

	res, err := pluginSurfaceCall(t, h, methods.MethodPluginsList, `{}`, cfg)
	if err != nil {
		t.Fatalf("plugins.list: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["count"].(int) != 3 {
		t.Fatalf("expected count 3, got %v", payload["count"])
	}

	// enabled filter.
	res, err = pluginSurfaceCall(t, h, methods.MethodPluginsList, `{"enabled":true}`, cfg)
	if err != nil {
		t.Fatalf("plugins.list enabled filter: %v", err)
	}
	if res.Result.(map[string]any)["count"].(int) != 1 {
		t.Fatalf("expected 1 enabled plugin, got %v", res.Result)
	}

	// search ranks by substring.
	res, err = pluginSurfaceCall(t, h, methods.MethodPluginsSearch, `{"query":"weather"}`, cfg)
	if err != nil {
		t.Fatalf("plugins.search: %v", err)
	}
	results := res.Result.(map[string]any)["results"].([]map[string]any)
	if len(results) != 1 || results[0]["id"] != "weather" {
		t.Fatalf("unexpected search results: %+v", results)
	}
	if _, ok := results[0]["score"]; !ok {
		t.Fatalf("search result missing score: %+v", results[0])
	}
}

func TestPluginsRefreshWithoutManager(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	res, err := pluginSurfaceCall(t, h, methods.MethodPluginsRefresh, `{}`, pluginTestConfig())
	if err != nil {
		t.Fatalf("plugins.refresh: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["reloaded"] != false {
		t.Fatalf("expected reloaded=false without manager, got %v", payload["reloaded"])
	}
	if payload["count"].(int) != 3 {
		t.Fatalf("expected refreshed listing of 3, got %v", payload["count"])
	}
}

func TestPluginApprovalRPCLifecycle(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{pluginApprovals: pluginapprovalpkg.NewManager()})
	cfg := state.ConfigDoc{}

	res, err := pluginSurfaceCall(t, h, methods.MethodPluginApprovalRequest,
		`{"pluginId":"weather","action":"network.fetch","reason":"forecast"}`, cfg)
	if err != nil {
		t.Fatalf("plugin.approval.request: %v", err)
	}
	payload := res.Result.(map[string]any)
	id := payload["id"].(string)
	if id == "" || payload["status"] != pluginapprovalpkg.StatusPending {
		t.Fatalf("unexpected request result: %+v", payload)
	}

	res, err = pluginSurfaceCall(t, h, methods.MethodPluginApprovalList, `{}`, cfg)
	if err != nil {
		t.Fatalf("plugin.approval.list: %v", err)
	}
	approvals := res.Result.(map[string]any)["approvals"].([]pluginapprovalpkg.Record)
	if len(approvals) != 1 || approvals[0].ID != id {
		t.Fatalf("unexpected approval list: %+v", approvals)
	}

	res, err = pluginSurfaceCall(t, h, methods.MethodPluginApprovalResolve,
		`{"id":"`+id+`","decision":"approve","decidedBy":"op"}`, cfg)
	if err != nil {
		t.Fatalf("plugin.approval.resolve: %v", err)
	}
	if res.Result.(map[string]any)["status"] != pluginapprovalpkg.StatusApproved {
		t.Fatalf("unexpected resolve result: %+v", res.Result)
	}

	// waitDecision after resolution returns the decision immediately.
	res, err = pluginSurfaceCall(t, h, methods.MethodPluginApprovalWaitDecision, `{"id":"`+id+`"}`, cfg)
	if err != nil {
		t.Fatalf("plugin.approval.waitDecision: %v", err)
	}
	wr := res.Result.(pluginapprovalpkg.WaitResult)
	if wr.Status != pluginapprovalpkg.StatusApproved || wr.Decision != pluginapprovalpkg.DecisionApprove {
		t.Fatalf("unexpected wait result: %+v", wr)
	}
}

func TestPluginApprovalRPCErrors(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{pluginApprovals: pluginapprovalpkg.NewManager()})
	cfg := state.ConfigDoc{}

	// missing action rejected by schema normalization.
	if _, err := pluginSurfaceCall(t, h, methods.MethodPluginApprovalRequest, `{"pluginId":"x"}`, cfg); err == nil {
		t.Fatal("expected error for missing action")
	}
	// invalid decision rejected by schema normalization.
	if _, err := pluginSurfaceCall(t, h, methods.MethodPluginApprovalResolve, `{"id":"z","decision":"maybe"}`, cfg); err == nil {
		t.Fatal("expected error for invalid decision")
	}
	// waitDecision on unknown id surfaces a not-found error.
	if _, err := pluginSurfaceCall(t, h, methods.MethodPluginApprovalWaitDecision, `{"id":"nope"}`, cfg); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestPluginApprovalSurfaceUnavailable(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	if _, err := pluginSurfaceCall(t, h, methods.MethodPluginApprovalList, `{}`, state.ConfigDoc{}); err == nil {
		t.Fatal("expected unavailable error when approval manager is nil")
	}
}

func TestPluginsSetEnabledPersists(t *testing.T) {
	initial := baseConfigMutationDoc()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WriteConfigFile(path, initial); err != nil {
		t.Fatalf("seed config file: %v", err)
	}
	withRuntimeConfigFile(t, path)

	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "author")
	if _, err := docsRepo.PutConfig(context.Background(), initial); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	cfgState := newRuntimeConfigStore(initial)
	h := configMutationHandler(docsRepo, cfgState)

	res, err := pluginSurfaceCall(t, h, methods.MethodPluginsSetEnabled,
		`{"pluginId":"weather","enabled":false}`, cfgState.Get())
	if err != nil {
		t.Fatalf("plugins.setEnabled: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != true || payload["pluginId"] != "weather" || payload["enabled"] != false {
		t.Fatalf("unexpected setEnabled result: %+v", payload)
	}

	// The runtime config now carries plugins.entries.weather.enabled=false.
	rawExt, _ := cfgState.Get().Extra["extensions"].(map[string]any)
	entries, _ := rawExt["entries"].(map[string]any)
	weather, _ := entries["weather"].(map[string]any)
	if weather == nil {
		t.Fatalf("weather entry not persisted: %+v", rawExt)
	}
	if enabled, ok := weather["enabled"].(bool); !ok || enabled {
		t.Fatalf("expected weather enabled=false, got %+v", weather)
	}
}
