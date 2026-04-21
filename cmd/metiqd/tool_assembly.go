package main

import (
	"context"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

func resolveAgentTurnToolSurface(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, sessionID, agentID string, rt agent.Runtime, base agent.ToolExecutor, constraints turnToolConstraints) (agent.Runtime, agent.ToolExecutor, []agent.ToolDefinition) {
	allowed := resolvedTurnRuntimeToolAllowlist(ctx, cfg, docsRepo, sessionID, agentID, constraints)
	rt = filterRuntimeByAllowedTools(rt, allowed)
	exec := agent.FilteredToolExecutor(base, allowed)
	return rt, exec, agent.ToolDefinitions(exec)
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
	result := agent.PartitionTools(descs, budget.ToolDefsMax, agent.DefaultAutoToolSearchPercentage, agent.DefaultCriticalToolNames())

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
