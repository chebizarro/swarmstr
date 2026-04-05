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
