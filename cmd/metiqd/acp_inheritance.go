package main

import (
	"context"
	"encoding/json"
	"strings"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	"metiq/internal/store/state"
)

type acpTaskPayloadKey struct{}

func contextWithACPTaskPayload(ctx context.Context, payload acppkg.TaskPayload) context.Context {
	return context.WithValue(ctx, acpTaskPayloadKey{}, payload)
}

func acpTaskPayloadFromContext(ctx context.Context) (acppkg.TaskPayload, bool) {
	payload, ok := ctx.Value(acpTaskPayloadKey{}).(acppkg.TaskPayload)
	return payload, ok
}

func encodeACPConversationMessages(messages []agent.ConversationMessage) []map[string]any {
	if len(messages) == 0 {
		return nil
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func decodeACPConversationMessages(messages []map[string]any) []agent.ConversationMessage {
	if len(messages) == 0 {
		return nil
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil
	}
	var out []agent.ConversationMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func normalizeACPEnabledTools(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func configuredAgentToolProfile(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, agentID string) string {
	agentID = defaultAgentID(agentID)
	for _, ac := range cfg.Agents {
		if strings.TrimSpace(ac.ID) != agentID {
			continue
		}
		if profileID := strings.TrimSpace(ac.ToolProfile); profileID != "" {
			return profileID
		}
		break
	}
	if docsRepo != nil {
		if agentDoc, err := docsRepo.GetAgent(ctx, agentID); err == nil && agentDoc.Meta != nil {
			if raw, ok := agentDoc.Meta[agent.AgentProfileKey].(string); ok {
				return strings.TrimSpace(raw)
			}
		}
	}
	return ""
}

func configuredAgentEnabledTools(cfg state.ConfigDoc, agentID string) []string {
	agentID = defaultAgentID(agentID)
	for _, ac := range cfg.Agents {
		if strings.TrimSpace(ac.ID) != agentID {
			continue
		}
		return agent.NormalizeAllowedToolNames(ac.EnabledTools)
	}
	return nil
}

func resolveACPAgentID(ctx context.Context, sessionStore *state.SessionStore, sessionID, explicitAgentID string) string {
	if agentID := strings.TrimSpace(explicitAgentID); agentID != "" {
		return defaultAgentID(agentID)
	}
	if scope := agent.MemoryScopeFromContext(ctx); strings.TrimSpace(scope.AgentID) != "" {
		return defaultAgentID(scope.AgentID)
	}
	if sessionStore != nil && strings.TrimSpace(sessionID) != "" {
		if entry, ok := sessionStore.Get(sessionID); ok && strings.TrimSpace(entry.AgentID) != "" {
			return defaultAgentID(entry.AgentID)
		}
	}
	if controlSessionRouter != nil && strings.TrimSpace(sessionID) != "" {
		if routed := strings.TrimSpace(controlSessionRouter.Get(sessionID)); routed != "" {
			return defaultAgentID(routed)
		}
	}
	return defaultAgentID("")
}

func maxContextTokensForAgent(cfg state.ConfigDoc, agentID string) int {
	maxCtxTokens := 100_000
	agentID = defaultAgentID(agentID)
	for _, ac := range cfg.Agents {
		if strings.TrimSpace(ac.ID) == agentID && ac.MaxContextTokens > 0 {
			return ac.MaxContextTokens
		}
	}
	return maxCtxTokens
}

func assembleACPContextMessages(ctx context.Context, cfg state.ConfigDoc, sessionID, agentID string) []map[string]any {
	if controlContextEngine == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	assembled, err := controlContextEngine.Assemble(ctx, sessionID, maxContextTokensForAgent(cfg, agentID))
	if err != nil || len(assembled.Messages) == 0 {
		return nil
	}
	messages := make([]agent.ConversationMessage, 0, len(assembled.Messages))
	for _, msg := range assembled.Messages {
		messages = append(messages, conversationMessageFromContext(msg))
	}
	return encodeACPConversationMessages(messages)
}

func allowedToolIDsForProfile(cfg state.ConfigDoc, profileID string) map[string]bool {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" || profileID == agent.DefaultProfile {
		return nil
	}
	if agent.LookupProfile(profileID) == nil {
		return map[string]bool{}
	}
	if controlToolRegistry == nil {
		return map[string]bool{}
	}
	groups := buildToolCatalogGroups(cfg, controlToolRegistry, nil, controlPluginMgr)
	if len(groups) == 0 {
		return map[string]bool{}
	}
	return agent.AllowedToolIDs(groups, profileID)
}

func filterRuntimeByAllowedTools(rt agent.Runtime, allowed map[string]bool) agent.Runtime {
	if allowed == nil {
		return rt
	}
	filterable, ok := rt.(interface {
		Filtered(map[string]bool) agent.Runtime
	})
	if !ok {
		return rt
	}
	return filterable.Filtered(allowed)
}

type turnToolConstraints struct {
	ToolProfile  string
	EnabledTools []string
}

func intersectTurnToolConstraints(allowed map[string]bool, cfg state.ConfigDoc, constraints turnToolConstraints) map[string]bool {
	if profileID := strings.TrimSpace(constraints.ToolProfile); profileID != "" {
		allowed = agent.IntersectAllowedToolIDs(allowed, allowedToolIDsForProfile(cfg, profileID))
	}
	allowed = agent.IntersectAllowedToolIDs(allowed, agent.AllowedToolIDsFromNames(constraints.EnabledTools))
	return allowed
}

func sessionTurnToolConstraints(ctx context.Context, docsRepo *state.DocsRepository, sessionID string) turnToolConstraints {
	if docsRepo == nil || strings.TrimSpace(sessionID) == "" {
		return turnToolConstraints{}
	}
	doc, err := docsRepo.GetSession(ctx, sessionID)
	if err != nil || doc.Meta == nil {
		return turnToolConstraints{}
	}
	rawConstraints, ok := doc.Meta["turn_constraints"].(map[string]any)
	if !ok {
		return turnToolConstraints{}
	}
	constraints := turnToolConstraints{}
	if rawProfile, ok := rawConstraints["tool_profile"].(string); ok {
		constraints.ToolProfile = strings.TrimSpace(strings.ToLower(rawProfile))
	}
	constraints.EnabledTools = agent.NormalizeAllowedToolNames(stringSliceValue(rawConstraints["enabled_tools"]))
	return constraints
}

func resolvedAgentRuntimeToolAllowlist(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, agentID string) map[string]bool {
	allowed := allowedToolIDsForProfile(cfg, configuredAgentToolProfile(ctx, cfg, docsRepo, agentID))
	return agent.IntersectAllowedToolIDs(allowed, agent.AllowedToolIDsFromNames(configuredAgentEnabledTools(cfg, agentID)))
}

func resolvedSessionRuntimeToolAllowlist(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, sessionID, agentID string) map[string]bool {
	allowed := resolvedAgentRuntimeToolAllowlist(ctx, cfg, docsRepo, agentID)
	return intersectTurnToolConstraints(allowed, cfg, sessionTurnToolConstraints(ctx, docsRepo, sessionID))
}

func resolvedTurnRuntimeToolAllowlist(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, sessionID, agentID string, constraints turnToolConstraints) map[string]bool {
	allowed := resolvedSessionRuntimeToolAllowlist(ctx, cfg, docsRepo, sessionID, agentID)
	return intersectTurnToolConstraints(allowed, cfg, constraints)
}

func applyAgentProfileFilterForAgent(ctx context.Context, rt agent.Runtime, agentID string, cfg state.ConfigDoc, docsRepo *state.DocsRepository) agent.Runtime {
	return filterRuntimeByAllowedTools(rt, resolvedAgentRuntimeToolAllowlist(ctx, cfg, docsRepo, agentID))
}

func applyACPTaskRuntimeConstraints(ctx context.Context, rt agent.Runtime, agentID string, payload acppkg.TaskPayload, cfg state.ConfigDoc, docsRepo *state.DocsRepository) agent.Runtime {
	allowed := resolvedAgentRuntimeToolAllowlist(ctx, cfg, docsRepo, agentID)
	allowed = intersectTurnToolConstraints(allowed, cfg, turnToolConstraints{ToolProfile: payload.ToolProfile, EnabledTools: payload.EnabledTools})
	return filterRuntimeByAllowedTools(rt, allowed)
}

func cloneACPContextMessages(messages []map[string]any) []map[string]any {
	if len(messages) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		copyMessage := make(map[string]any, len(message))
		for key, value := range message {
			copyMessage[key] = value
		}
		out = append(out, copyMessage)
	}
	return out
}

func buildInheritedACPTaskPayload(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository, sessionStore *state.SessionStore, payload acppkg.TaskPayload) acppkg.TaskPayload {
	out := payload
	inherited, _ := acpTaskPayloadFromContext(ctx)
	sessionID := strings.TrimSpace(agent.SessionIDFromContext(ctx))
	agentID := resolveACPAgentID(ctx, sessionStore, sessionID, "")
	if !out.MemoryScope.Valid() {
		if inherited.MemoryScope.Valid() {
			out.MemoryScope = inherited.MemoryScope
		} else if scope, ok := state.ParseAgentMemoryScope(agent.MemoryScopeFromContext(ctx).Scope); ok {
			out.MemoryScope = scope
		}
	}
	if strings.TrimSpace(out.ToolProfile) == "" {
		if profileID := strings.TrimSpace(inherited.ToolProfile); profileID != "" {
			out.ToolProfile = profileID
		} else {
			out.ToolProfile = configuredAgentToolProfile(ctx, cfg, docsRepo, agentID)
		}
	}
	if len(out.EnabledTools) == 0 {
		if len(inherited.EnabledTools) > 0 {
			out.EnabledTools = agent.NormalizeAllowedToolNames(inherited.EnabledTools)
		} else {
			out.EnabledTools = configuredAgentEnabledTools(cfg, agentID)
		}
	} else {
		out.EnabledTools = agent.NormalizeAllowedToolNames(out.EnabledTools)
	}
	if len(out.ContextMessages) == 0 {
		if len(inherited.ContextMessages) > 0 {
			out.ContextMessages = cloneACPContextMessages(inherited.ContextMessages)
		} else if messages := assembleACPContextMessages(ctx, cfg, sessionID, agentID); len(messages) > 0 {
			out.ContextMessages = messages
		}
	}
	if out.ParentContext == nil {
		parentSessionID := sessionID
		parentAgentID := strings.TrimSpace(agentID)
		if parentSessionID == "" && inherited.ParentContext != nil {
			parentSessionID = strings.TrimSpace(inherited.ParentContext.SessionID)
		}
		if parentAgentID == "" && inherited.ParentContext != nil {
			parentAgentID = strings.TrimSpace(inherited.ParentContext.AgentID)
		}
		if parentAgentID != "" {
			parentAgentID = defaultAgentID(parentAgentID)
		}
		if parentSessionID != "" || parentAgentID != "" {
			out.ParentContext = &acppkg.ParentContext{SessionID: parentSessionID, AgentID: parentAgentID}
		}
	}
	return out
}
