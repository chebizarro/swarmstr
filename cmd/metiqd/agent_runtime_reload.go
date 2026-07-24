package main

import (
	"log"
	"strings"
	"sync"

	"metiq/internal/agent"
	"metiq/internal/config"
	"metiq/internal/store/state"
)

// Hot-reload propagation for agent runtime parameters (swarmstr-v2uq).
//
// Agents are provisioned once at startup; before this file, config hot-reload
// (file watch or SIGHUP) updated storage encryption, key rings, relay
// policies, heartbeat, and profile settings but left agent runtimes running
// with their startup-time model/provider/system-prompt/fallback/light-model
// parameters. reprovisionChangedAgentRuntimes closes that gap by rebuilding
// only the runtimes whose runtime-affecting config actually changed.

var (
	agentRuntimeReloadMu     sync.Mutex
	agentRuntimeReloadSeeded bool
	agentRuntimeReloadLast   state.ConfigDoc
)

// seedAgentRuntimeReloadBaseline records the config snapshot whose agents were
// provisioned at startup so later hot-reloads only rebuild changed runtimes.
func seedAgentRuntimeReloadBaseline(cfg state.ConfigDoc) {
	agentRuntimeReloadMu.Lock()
	defer agentRuntimeReloadMu.Unlock()
	agentRuntimeReloadLast = cfg
	agentRuntimeReloadSeeded = true
}

// reprovisionChangedAgentRuntimes rebuilds agent runtimes whose
// runtime-affecting parameters (model, provider, credentials, system prompt,
// fallback chain, light-model routing, context/thinking/timeout settings)
// changed since the last applied snapshot, and drops runtimes for agents
// removed from the config. Build failures keep the previous runtime so a bad
// reload never leaves an agent without a working runtime.
func reprovisionChangedAgentRuntimes(cfg state.ConfigDoc) {
	agentRuntimeReloadMu.Lock()
	if !agentRuntimeReloadSeeded {
		// No baseline yet (nothing was provisioned through startup): record
		// this snapshot and treat it as already applied.
		agentRuntimeReloadLast = cfg
		agentRuntimeReloadSeeded = true
		agentRuntimeReloadMu.Unlock()
		return
	}
	oldCfg := agentRuntimeReloadLast
	agentRuntimeReloadLast = cfg
	agentRuntimeReloadMu.Unlock()

	changed, removed := config.ChangedAgentRuntimes(oldCfg, cfg)
	if len(changed) == 0 && len(removed) == 0 {
		return
	}
	controller := currentAgentRunController()
	registry := controller.agentRegistry
	if registry == nil {
		return
	}
	tools := controlToolRegistry
	for _, agCfg := range changed {
		agentID := strings.TrimSpace(agCfg.ID)
		model := strings.TrimSpace(agCfg.Model)
		if agentID == "" || model == "" {
			continue
		}
		rt, err := buildConfiguredAgentRuntime(cfg, agCfg, tools)
		if err != nil {
			log.Printf("config reload: agent runtime rebuild failed id=%s model=%q err=%v — keeping previous runtime", agentID, model, err)
			continue
		}
		if agentID == "main" {
			registry.SetDefault(rt)
			controlAgentRuntime = rt
			if controlServices != nil {
				controlServices.session.agentRuntime = rt
			}
		} else {
			registry.Set(agentID, rt)
		}
		log.Printf("config reload: agent runtime reprovisioned id=%s model=%q provider=%q", agentID, model, agCfg.Provider)
	}
	for _, agentID := range removed {
		if agentID == "main" {
			continue
		}
		registry.Remove(agentID)
		log.Printf("config reload: agent runtime removed id=%s", agentID)
	}
}

// buildConfiguredAgentRuntime builds the effective runtime for one configured
// agent using the same layering as startup auto-provisioning: an optional
// fallback-chain provider (layer 1), optional light-model routing (layer 2),
// then the provider runtime.
func buildConfiguredAgentRuntime(cfg state.ConfigDoc, agCfg state.AgentConfig, tools agent.ToolExecutor) (agent.Runtime, error) {
	model := strings.TrimSpace(agCfg.Model)
	override := resolveModelProviderOverride(cfg, agCfg, model)

	var effectiveProvider agent.Provider
	if len(agCfg.FallbackModels) > 0 {
		fbOverrides := make(map[string]agent.ProviderOverride)
		for _, fbModel := range agCfg.FallbackModels {
			fbModel = strings.TrimSpace(fbModel)
			if fbModel == "" {
				continue
			}
			fbOverrides[fbModel] = resolveModelProviderOverride(cfg, agCfg, fbModel)
		}
		fbProvider, fbErr := agent.NewFallbackChainProvider(
			model,
			override.APIKey,
			override.BaseURL,
			override.PromptCache,
			agCfg.FallbackModels,
			fbOverrides,
			override.SystemPrompt,
		)
		if fbErr == nil {
			effectiveProvider = fbProvider
		} else {
			log.Printf("config reload: fallback chain build warning id=%s err=%v — falling back to standard provider", agCfg.ID, fbErr)
		}
	}
	if effectiveProvider == nil {
		baseProv, baseErr := buildProviderForAgentModel(cfg, agCfg, model)
		if baseErr != nil {
			return nil, baseErr
		}
		effectiveProvider = baseProv
	}
	if lightModel := strings.TrimSpace(agCfg.LightModel); lightModel != "" {
		lightProv, lightErr := buildProviderForAgentModel(cfg, agCfg, lightModel)
		if lightErr != nil {
			log.Printf("config reload: light model build warning id=%s light=%q err=%v — routing disabled", agCfg.ID, lightModel, lightErr)
		} else {
			effectiveProvider = agent.NewRoutedProvider(effectiveProvider, model, lightProv, lightModel, agCfg.LightModelThreshold)
		}
	}
	return agent.NewProviderRuntime(effectiveProvider, tools)
}
