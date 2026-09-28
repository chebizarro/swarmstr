package config

import (
	"testing"

	"metiq/internal/store/state"
)

func TestAgentRuntimeFingerprintStableForIdenticalConfig(t *testing.T) {
	cfg := state.ConfigDoc{
		Agents:    []state.AgentConfig{{ID: "main", Model: "claude-sonnet-4", SystemPrompt: "be brief"}},
		Providers: map[string]state.ProviderEntry{"anthropic": {APIKey: "key-1"}, "openai": {APIKey: "key-2"}},
	}
	clone := state.ConfigDoc{
		Agents:    []state.AgentConfig{{ID: "main", Model: "claude-sonnet-4", SystemPrompt: "be brief"}},
		Providers: map[string]state.ProviderEntry{"openai": {APIKey: "key-2"}, "anthropic": {APIKey: "key-1"}},
	}
	if AgentRuntimeFingerprint(cfg, cfg.Agents[0]) != AgentRuntimeFingerprint(clone, clone.Agents[0]) {
		t.Fatal("identical agent and provider config must fingerprint identically")
	}
}

func TestAgentRuntimeFingerprintProviderTableChangeAffectsAllAgents(t *testing.T) {
	base := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", Model: "claude-sonnet-4"},
			{ID: "helper", Model: "gpt-5"},
		},
		Providers: map[string]state.ProviderEntry{"anthropic": {APIKey: "key-1"}},
	}
	rotated := base
	rotated.Providers = map[string]state.ProviderEntry{"anthropic": {APIKey: "key-2"}}
	for _, ag := range base.Agents {
		if AgentRuntimeFingerprint(base, ag) == AgentRuntimeFingerprint(rotated, ag) {
			t.Fatalf("agent %s: provider table change must change the fingerprint", ag.ID)
		}
	}
}

func TestAgentRuntimeFingerprintRuntimeParameterFields(t *testing.T) {
	base := state.AgentConfig{ID: "a", Model: "claude-sonnet-4"}
	cfg := state.ConfigDoc{Agents: []state.AgentConfig{base}}
	basePrint := AgentRuntimeFingerprint(cfg, base)
	mutations := []func(*state.AgentConfig){
		func(ag *state.AgentConfig) { ag.ContextWindow = 4096 },
		func(ag *state.AgentConfig) { ag.MaxContextTokens = 2048 },
		func(ag *state.AgentConfig) { ag.SystemPrompt = "new prompt" },
		func(ag *state.AgentConfig) { ag.EnabledTools = []string{"exec"} },
		func(ag *state.AgentConfig) { ag.LightModel = "claude-haiku-3-5" },
		func(ag *state.AgentConfig) { ag.LightModelThreshold = 0.5 },
		func(ag *state.AgentConfig) { ag.ThinkingLevel = "high" },
		func(ag *state.AgentConfig) { ag.TurnTimeoutSecs = 600 },
		func(ag *state.AgentConfig) { ag.Model = "claude-opus-4" },
		func(ag *state.AgentConfig) { ag.Provider = "anthropic" },
		func(ag *state.AgentConfig) { ag.FallbackModels = []string{"gpt-5"} },
	}
	for i, mutate := range mutations {
		next := base
		mutate(&next)
		if AgentRuntimeFingerprint(cfg, next) == basePrint {
			t.Fatalf("mutation %d: expected fingerprint change", i)
		}
	}
}
