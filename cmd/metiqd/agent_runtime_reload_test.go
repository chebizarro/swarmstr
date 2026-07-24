package main

import (
	"testing"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

func TestReprovisionChangedAgentRuntimes(t *testing.T) {
	// Snapshot and restore globals touched by the reload path.
	prevRegistry := controlAgentRegistry
	prevRuntime := controlAgentRuntime
	prevSeeded, prevLast := agentRuntimeReloadSeeded, agentRuntimeReloadLast
	t.Cleanup(func() {
		controlAgentRegistry = prevRegistry
		controlAgentRuntime = prevRuntime
		agentRuntimeReloadSeeded, agentRuntimeReloadLast = prevSeeded, prevLast
	})

	cfgA := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "helper", Model: "claude-sonnet-4", SystemPrompt: "be brief"},
		},
		Providers: map[string]state.ProviderEntry{
			"anthropic": {APIKey: "test-key"},
		},
	}

	defaultRT, err := buildConfiguredAgentRuntime(cfgA, state.AgentConfig{ID: "main", Model: "claude-sonnet-4"}, nil)
	if err != nil {
		t.Fatalf("build default runtime: %v", err)
	}
	initialRT, err := buildConfiguredAgentRuntime(cfgA, cfgA.Agents[0], nil)
	if err != nil {
		t.Fatalf("build initial runtime: %v", err)
	}
	registry := agent.NewAgentRuntimeRegistry(defaultRT)
	registry.Set("helper", initialRT)
	controlAgentRegistry = registry
	controlAgentRuntime = defaultRT

	seedAgentRuntimeReloadBaseline(cfgA)

	// Unchanged config → runtime instance preserved.
	reprovisionChangedAgentRuntimes(cfgA)
	if registry.Get("helper") != initialRT {
		t.Fatal("unchanged config must not rebuild the runtime")
	}

	// Changed system prompt → runtime rebuilt.
	cfgB := state.ConfigDoc{
		Agents: []state.AgentConfig{
			{ID: "helper", Model: "claude-sonnet-4", SystemPrompt: "be thorough"},
		},
		Providers: cfgA.Providers,
	}
	reprovisionChangedAgentRuntimes(cfgB)
	rebuilt := registry.Get("helper")
	if rebuilt == initialRT {
		t.Fatal("changed system_prompt must rebuild the runtime")
	}

	// Re-applying the same config → no further rebuild.
	reprovisionChangedAgentRuntimes(cfgB)
	if registry.Get("helper") != rebuilt {
		t.Fatal("re-applying identical config must not rebuild the runtime")
	}

	// Agent removed from config → registry entry dropped (falls back to default).
	cfgC := state.ConfigDoc{Providers: cfgA.Providers}
	reprovisionChangedAgentRuntimes(cfgC)
	if registry.Get("helper") != defaultRT {
		t.Fatal("removed agent must fall back to the default runtime")
	}
}

func TestReprovisionChangedAgentRuntimesKeepsRuntimeOnBuildFailure(t *testing.T) {
	prevRegistry := controlAgentRegistry
	prevRuntime := controlAgentRuntime
	prevSeeded, prevLast := agentRuntimeReloadSeeded, agentRuntimeReloadLast
	t.Cleanup(func() {
		controlAgentRegistry = prevRegistry
		controlAgentRuntime = prevRuntime
		agentRuntimeReloadSeeded, agentRuntimeReloadLast = prevSeeded, prevLast
	})

	cfgA := state.ConfigDoc{
		Agents:    []state.AgentConfig{{ID: "helper", Model: "claude-sonnet-4"}},
		Providers: map[string]state.ProviderEntry{"anthropic": {APIKey: "test-key"}},
	}
	initialRT, err := buildConfiguredAgentRuntime(cfgA, cfgA.Agents[0], nil)
	if err != nil {
		t.Fatalf("build initial runtime: %v", err)
	}
	registry := agent.NewAgentRuntimeRegistry(initialRT)
	registry.Set("helper", initialRT)
	controlAgentRegistry = registry
	seedAgentRuntimeReloadBaseline(cfgA)

	// New config names a provider that does not exist → rebuild fails and the
	// previous runtime must be preserved.
	cfgBad := state.ConfigDoc{
		Agents:    []state.AgentConfig{{ID: "helper", Model: "claude-sonnet-4", Provider: "missing"}},
		Providers: map[string]state.ProviderEntry{"anthropic": {APIKey: "test-key"}},
	}
	reprovisionChangedAgentRuntimes(cfgBad)
	if registry.Get("helper") != initialRT {
		t.Fatal("failed rebuild must keep the previous runtime")
	}
}
