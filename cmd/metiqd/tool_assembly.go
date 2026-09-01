package main

import (
	"context"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

var privateSessionBlockedFleetTools = map[string]struct{}{
	"fleet_tasks":          {},
	"nostr_publish":        {},
	"nostr_send_dm":        {},
	"nostr_profile_set":    {},
	"nostr_relay_list_set": {},
	"nostr_zap_send":       {},
	"nostr_agent_rpc":      {},
	"nostr_agent_send":     {},
}

var localScratchTaskTools = map[string]struct{}{
	"task_add":    {},
	"task_list":   {},
	"task_update": {},
	"task_remove": {},
}

func mutableTurnToolAllowlist(allowed map[string]bool, base agent.ToolExecutor) map[string]bool {
	if allowed != nil {
		return agent.CloneAllowedToolIDs(allowed)
	}
	allowed = make(map[string]bool)
	for _, def := range agent.ToolDefinitions(base) {
		allowed[def.Name] = true
	}
	return allowed
}

func resolveAgentTurnToolSurface(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, sessionID, agentID string, rt agent.Runtime, base agent.ToolExecutor, constraints turnToolConstraints) (agent.Runtime, agent.ToolExecutor, []agent.ToolDefinition) {
	allowed := resolvedTurnRuntimeToolAllowlist(ctx, cfg, docsRepo, sessionID, agentID, constraints)
	privateMode := sessionUsesPrivateAgentMode(ctx, docsRepo, sessionID)
	if cfg.FleetTasks.Enabled && !privateMode {
		allowed = mutableTurnToolAllowlist(allowed, base)
		for name := range localScratchTaskTools {
			delete(allowed, name)
		}
	}
	if privateMode {
		allowed = mutableTurnToolAllowlist(allowed, base)
		for name := range privateSessionBlockedFleetTools {
			delete(allowed, name)
		}
	}
	rt = filterRuntimeByAllowedTools(rt, allowed)
	exec := agent.FilteredToolExecutor(base, allowed)
	return rt, exec, agent.ToolDefinitions(exec)
}

func sessionUsesPrivateAgentMode(ctx context.Context, docsRepo *state.DocsRepository, sessionID string) bool {
	if docsRepo == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	doc, err := docsRepo.GetSession(ctx, sessionID)
	if err != nil || doc.Meta == nil {
		return false
	}
	raw, _ := doc.Meta["session_mode"].(string)
	return strings.EqualFold(strings.TrimSpace(raw), "private")
}

// partitionTurnTools splits tools from an executor into inline and deferred
// sets. When the executor exposes descriptors (which carry origin metadata),
// PartitionTools determines whether deferral is worthwhile. Otherwise all
// tools stay inline. Returns the inline definitions and an optional deferred
// set (nil when deferral is inactive).
func partitionTurnTools(exec agent.ToolExecutor, contextWindowTokens int) ([]agent.ToolDefinition, *agent.DeferredToolSet) {
	if exec == nil || contextWindowTokens <= 0 {
		return agent.ToolDefinitions(exec), nil
	}

	// Try to get descriptors from the executor.
	var descs []agent.ToolDescriptor
	if provider, ok := exec.(interface{ Descriptors() []agent.ToolDescriptor }); ok {
		descs = provider.Descriptors()
	}
	if len(descs) == 0 {
		// No descriptor metadata — can't determine origin, inline everything.
		return agent.ToolDefinitions(exec), nil
	}

	profile := agent.ProfileFromContextWindowTokens(contextWindowTokens)
	budget := agent.ComputeContextBudget(profile)
	result := agent.PartitionTools(descs, budget.ToolDefsMax, agent.DefaultAutoToolSearchPercentage, agent.DefaultCriticalToolNames(), budget.MaxToolCount)

	if result.Deferred.Count() == 0 {
		// Below threshold — everything inlined.
		return result.Inline, nil
	}
	return result.Inline, result.Deferred
}

func availableRegistryToolIDs(registry *agent.ToolRegistry) map[string]struct{} {
	if registry == nil {
		return nil
	}
	descs := registry.Descriptors()
	if len(descs) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(descs))
	for _, desc := range descs {
		out[desc.Name] = struct{}{}
	}
	return out
}
