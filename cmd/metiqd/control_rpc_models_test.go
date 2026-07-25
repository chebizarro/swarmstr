package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func modelsCall(t *testing.T, h controlRPCHandler, method, params string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	t.Helper()
	return h.handleModelsRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, cfg)
}

// blankProviderEnv clears every provider API-key env var so config-based
// assertions are deterministic regardless of the developer's environment.
func blankProviderEnv(t *testing.T) {
	t.Helper()
	for _, env := range modelProviderEnv {
		t.Setenv(env, "")
	}
}

func TestModelsAuthStatusReportsProviders(t *testing.T) {
	blankProviderEnv(t)
	cfg := state.ConfigDoc{Providers: state.ProvidersConfig{
		"openai":    {Enabled: true, APIKey: "sk-test", Model: "gpt-4o"},
		"anthropic": {Enabled: true, APIKey: "sk-ant-oat01-abc#refresh"},
	}}
	h := newControlRPCHandler(controlRPCDeps{})
	res, handled, err := modelsCall(t, h, methods.MethodModelsAuthStatus, `{}`, cfg)
	if !handled || err != nil {
		t.Fatalf("authStatus handled=%v err=%v", handled, err)
	}
	providers := res.Result.(map[string]any)["providers"].([]map[string]any)
	byID := map[string]map[string]any{}
	for _, p := range providers {
		byID[p["provider"].(string)] = p
	}
	if byID["openai"]["configured"] != true || byID["openai"]["authMethod"] != "api_key" || byID["openai"]["source"] != "config" {
		t.Fatalf("unexpected openai status: %+v", byID["openai"])
	}
	if byID["openai"]["model"] != "gpt-4o" {
		t.Fatalf("expected openai model echoed: %+v", byID["openai"])
	}
	if byID["anthropic"]["authMethod"] != "oauth" {
		t.Fatalf("expected anthropic oauth: %+v", byID["anthropic"])
	}
	if byID["cohere"]["configured"] != false || byID["cohere"]["authMethod"] != "none" || byID["cohere"]["source"] != "none" {
		t.Fatalf("expected cohere unconfigured: %+v", byID["cohere"])
	}
}

func TestModelsAuthStatusSingleProviderFromEnv(t *testing.T) {
	blankProviderEnv(t)
	t.Setenv("XAI_API_KEY", "present")
	h := newControlRPCHandler(controlRPCDeps{})
	res, _, err := modelsCall(t, h, methods.MethodModelsAuthStatus, `{"provider":"xai"}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("authStatus: %v", err)
	}
	providers := res.Result.(map[string]any)["providers"].([]map[string]any)
	if len(providers) != 1 || providers[0]["provider"] != "xai" {
		t.Fatalf("expected single xai entry: %+v", providers)
	}
	if providers[0]["configured"] != true || providers[0]["source"] != "env" {
		t.Fatalf("expected env-configured xai: %+v", providers[0])
	}
}

func TestModelsAuthLogoutClearsConfigCred(t *testing.T) {
	blankProviderEnv(t)
	initial := baseConfigMutationDoc()
	initial.Providers = state.ProvidersConfig{"openai": {Enabled: true, APIKey: "sk-secret", Model: "gpt-4o"}}
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

	res, _, err := modelsCall(t, h, methods.MethodModelsAuthLogout, `{"provider":"openai"}`, cfgState.Get())
	if err != nil {
		t.Fatalf("authLogout: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != true || payload["cleared"] != true {
		t.Fatalf("unexpected authLogout result: %+v", payload)
	}
	if payload["remaining_env"] != false {
		t.Fatalf("expected no remaining env credential: %+v", payload)
	}
	if key := cfgState.Get().Providers["openai"].APIKey; key != "" {
		t.Fatalf("api key not cleared: %q", key)
	}

	// Idempotent: a second logout has nothing left to clear.
	res2, _, err := modelsCall(t, h, methods.MethodModelsAuthLogout, `{"provider":"openai"}`, cfgState.Get())
	if err != nil {
		t.Fatalf("authLogout (2nd): %v", err)
	}
	if res2.Result.(map[string]any)["cleared"] != false {
		t.Fatalf("second logout should be a no-op: %+v", res2.Result)
	}
}

func TestModelsAuthLogoutRequiresProvider(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	_, handled, err := modelsCall(t, h, methods.MethodModelsAuthLogout, `{}`, state.ConfigDoc{})
	if !handled {
		t.Fatal("expected method handled")
	}
	if err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("expected provider-required error, got %v", err)
	}
}

func TestModelsProbeReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cfg := state.ConfigDoc{Providers: state.ProvidersConfig{
		"openai": {Enabled: true, APIKey: "sk", BaseURL: srv.URL},
	}}
	h := newControlRPCHandler(controlRPCDeps{})
	res, _, err := modelsCall(t, h, methods.MethodModelsProbe, `{"provider":"openai","model":"gpt-4o"}`, cfg)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["reachable"] != true || p["configured"] != true {
		t.Fatalf("expected reachable+configured: %+v", p)
	}
	if p["status_code"] != http.StatusNoContent {
		t.Fatalf("expected 204 status: %+v", p)
	}
	if p["model"] != "gpt-4o" {
		t.Fatalf("expected model echoed: %+v", p)
	}
}

func TestModelsProbeUnknownEndpoint(t *testing.T) {
	blankProviderEnv(t)
	h := newControlRPCHandler(controlRPCDeps{})
	res, _, err := modelsCall(t, h, methods.MethodModelsProbe, `{"provider":"customx"}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["reachable"] != false || p["configured"] != false {
		t.Fatalf("expected unreachable+unconfigured: %+v", p)
	}
	if p["reason"] != "no known endpoint for provider" {
		t.Fatalf("expected no-endpoint reason: %+v", p)
	}
}

func TestModelsProbeRequiresProvider(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	_, _, err := modelsCall(t, h, methods.MethodModelsProbe, `{}`, state.ConfigDoc{})
	if err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("expected provider-required error, got %v", err)
	}
}

func TestModelsRPCUnownedMethodNotHandled(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	_, handled, _ := modelsCall(t, h, methods.MethodModelsList, `{}`, state.ConfigDoc{})
	if handled {
		t.Fatal("models.list must not be claimed by the models-auth handler")
	}
}
