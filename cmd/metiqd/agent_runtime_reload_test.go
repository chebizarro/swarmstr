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
	prevApplied := agentRuntimeReloadApplied
	t.Cleanup(func() {
		controlAgentRegistry = prevRegistry
		controlAgentRuntime = prevRuntime
		agentRuntimeReloadApplied = prevApplied
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

	seedAgentRuntimeReloadBaseline(cfgA, map[string]bool{"helper": true})

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
	prevApplied := agentRuntimeReloadApplied
	t.Cleanup(func() {
		controlAgentRegistry = prevRegistry
		controlAgentRuntime = prevRuntime
		agentRuntimeReloadApplied = prevApplied
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
	seedAgentRuntimeReloadBaseline(cfgA, map[string]bool{"helper": true})

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

// A rebuild that fails for reasons outside the config (here: a missing env
// var) must be retried when the same config is reloaded again; the applied
// baseline only advances once the runtime actually installs.
func TestReprovisionChangedAgentRuntimesRetriesFailedRebuild(t *testing.T) {
	prevRegistry := controlAgentRegistry
	prevRuntime := controlAgentRuntime
	prevApplied := agentRuntimeReloadApplied
	t.Cleanup(func() {
		controlAgentRegistry = prevRegistry
		controlAgentRuntime = prevRuntime
		agentRuntimeReloadApplied = prevApplied
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
	seedAgentRuntimeReloadBaseline(cfgA, map[string]bool{"helper": true})

	cfgHTTP := state.ConfigDoc{Agents: []state.AgentConfig{{ID: "helper", Model: "http"}}}
	t.Setenv("METIQ_AGENT_HTTP_URL", "")
	t.Setenv("METIQ_AGENT_HTTP_API_KEY", "")
	reprovisionChangedAgentRuntimes(cfgHTTP)
	if registry.Get("helper") != initialRT {
		t.Fatal("failed rebuild must keep the previous runtime")
	}

	t.Setenv("METIQ_AGENT_HTTP_URL", "http://127.0.0.1:9")
	reprovisionChangedAgentRuntimes(cfgHTTP)
	retried := registry.Get("helper")
	if retried == initialRT {
		t.Fatal("reloading the same config after a failed rebuild must retry the rebuild")
	}

	reprovisionChangedAgentRuntimes(cfgHTTP)
	if registry.Get("helper") != retried {
		t.Fatal("once installed, re-applying the same config must not rebuild again")
	}
}

// An agent whose runtime build failed at startup is left out of the seeded
// baseline, so the next reload of the unchanged config retries it; agents that
// did install at startup are not rebuilt.
func TestReprovisionChangedAgentRuntimesRetriesStartupBuildFailure(t *testing.T) {
	prevRegistry := controlAgentRegistry
	prevRuntime := controlAgentRuntime
	prevApplied := agentRuntimeReloadApplied
	t.Cleanup(func() {
		controlAgentRegistry = prevRegistry
		controlAgentRuntime = prevRuntime
		agentRuntimeReloadApplied = prevApplied
	})

	cfg := state.ConfigDoc{Agents: []state.AgentConfig{
		{ID: "stable", Model: "echo"},
		{ID: "flaky", Model: "http"},
	}}
	defaultRT, err := buildConfiguredAgentRuntime(cfg, state.AgentConfig{ID: "main", Model: "echo"}, nil)
	if err != nil {
		t.Fatalf("build default runtime: %v", err)
	}
	stableRT, err := buildConfiguredAgentRuntime(cfg, cfg.Agents[0], nil)
	if err != nil {
		t.Fatalf("build stable runtime: %v", err)
	}
	t.Setenv("METIQ_AGENT_HTTP_URL", "")
	t.Setenv("METIQ_AGENT_HTTP_API_KEY", "")
	if _, err := buildConfiguredAgentRuntime(cfg, cfg.Agents[1], nil); err == nil {
		t.Fatal("expected flaky startup build to fail")
	}
	registry := agent.NewAgentRuntimeRegistry(defaultRT)
	registry.Set("stable", stableRT)
	controlAgentRegistry = registry
	seedAgentRuntimeReloadBaseline(cfg, map[string]bool{"stable": true})

	t.Setenv("METIQ_AGENT_HTTP_URL", "http://127.0.0.1:9")
	reprovisionChangedAgentRuntimes(cfg)
	if got := registry.Get("flaky"); got == defaultRT {
		t.Fatal("agent that failed at startup must be provisioned on the next reload")
	}
	if registry.Get("stable") != stableRT {
		t.Fatal("agent installed at startup must not be rebuilt by an unchanged reload")
	}
}
