package config

import (
	"testing"

	"metiq/internal/store/state"
)

func TestChangedAgentRuntimesDetectsParameterChanges(t *testing.T) {
	oldCfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", Model: "claude-sonnet-4", SystemPrompt: "be brief"},
			{ID: "helper", Model: "gpt-5", ContextWindow: 8192},
			{ID: "gone", Model: "gemini-pro"},
		},
		Providers: map[string]state.ProviderEntry{
			"anthropic": {APIKey: "key-1"},
		},
	}

	// No changes → nothing to rebuild.
	changed, removed := ChangedAgentRuntimes(oldCfg, oldCfg)
	if len(changed) != 0 || len(removed) != 0 {
		t.Fatalf("identical config: changed=%v removed=%v", changed, removed)
	}

	newCfg := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", Model: "claude-sonnet-4", SystemPrompt: "be thorough"}, // system_prompt changed
			{ID: "helper", Model: "gpt-5", ContextWindow: 8192},                 // unchanged
			{ID: "fresh", Model: "grok-4"},                                      // added
		},
		Providers: map[string]state.ProviderEntry{
			"anthropic": {APIKey: "key-1"},
		},
	}
	changed, removed = ChangedAgentRuntimes(oldCfg, newCfg)
	if len(changed) != 2 || changed[0].ID != "main" || changed[1].ID != "fresh" {
		t.Fatalf("changed = %#v", changed)
	}
	if len(removed) != 1 || removed[0] != "gone" {
		t.Fatalf("removed = %#v", removed)
	}
}

func TestChangedAgentRuntimesProviderTableChangeAffectsAllAgents(t *testing.T) {
	base := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "main", Model: "claude-sonnet-4"},
			{ID: "helper", Model: "gpt-5"},
		},
		Providers: map[string]state.ProviderEntry{"anthropic": {APIKey: "key-1"}},
	}
	rotated := base
	rotated.Providers = map[string]state.ProviderEntry{"anthropic": {APIKey: "key-2"}}
	changed, removed := ChangedAgentRuntimes(base, rotated)
	if len(changed) != 2 || len(removed) != 0 {
		t.Fatalf("expected all agents to rebuild on provider change: changed=%v removed=%v", changed, removed)
	}
}

func TestChangedAgentRuntimesRuntimeParameterFields(t *testing.T) {
	base := state.ConfigDoc{Agents: []state.AgentConfig{{ID: "a", Model: "claude-sonnet-4"}}}
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
		next := state.ConfigDoc{Agents: []state.AgentConfig{{ID: "a", Model: "claude-sonnet-4"}}}
		mutate(&next.Agents[0])
		changed, _ := ChangedAgentRuntimes(base, next)
		if len(changed) != 1 {
			t.Fatalf("mutation %d: expected rebuild, got changed=%v", i, changed)
		}
	}
}
