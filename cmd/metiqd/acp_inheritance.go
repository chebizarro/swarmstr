package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	pluginmanager "metiq/internal/plugins/manager"
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
	if controlServices != nil && controlServices.session.sessionRouter != nil && strings.TrimSpace(sessionID) != "" {
		if routed := strings.TrimSpace(controlServices.session.sessionRouter.Get(sessionID)); routed != "" {
			return defaultAgentID(routed)
		}
	}
	return defaultAgentID("")
}

// contextWindowForAgent resolves the model's actual context window size for
// budget allocation. This is distinct from maxContextTokensForAgent — it
// represents the model's native capacity, not an operator-imposed ceiling.
//
// Resolution order:
//  1. AgentConfig.ContextWindow (explicit operator override)
//  2. Model registry lookup (prefix-matched model name)
//  3. Default 200K (standard assumption)
func contextWindowForAgent(cfg state.ConfigDoc, agentID string) int {
	agentID = defaultAgentID(agentID)

	// Check explicit ContextWindow config first.
	for _, ac := range cfg.Agents {
		if strings.TrimSpace(ac.ID) == agentID && ac.ContextWindow > 0 {
			return ac.ContextWindow
		}
	}

	// Resolve from the agent's model via the model registry.
	if model := modelForAgent(cfg, agentID); model != "" {
		profile := agent.ResolveModelContext(model)
		return profile.ContextWindowTokens
	}

	return 200_000
}

func maxContextTokensForAgent(cfg state.ConfigDoc, agentID string) int {
	agentID = defaultAgentID(agentID)

	// Check explicit per-agent MaxContextTokens ceiling.
	for _, ac := range cfg.Agents {
		if strings.TrimSpace(ac.ID) == agentID && ac.MaxContextTokens > 0 {
			return ac.MaxContextTokens
		}
	}

	// Fall back to the context window (model capacity is the ceiling).
	return contextWindowForAgent(cfg, agentID)
}

// modelForAgent returns the trimmed model ID for the given agent, checking
// per-agent config then the default model. Returns "" if none is configured.
func modelForAgent(cfg state.ConfigDoc, agentID string) string {
	agentID = defaultAgentID(agentID)
	for _, ac := range cfg.Agents {
		if defaultAgentID(ac.ID) == agentID && strings.TrimSpace(ac.Model) != "" {
			return strings.TrimSpace(ac.Model)
		}
	}
	if dm := strings.TrimSpace(cfg.Agent.DefaultModel); dm != "" {
		return dm
	}
	return ""
}

func assembleACPContextMessages(ctx context.Context, cfg state.ConfigDoc, sessionID, agentID string) []map[string]any {
	if controlServices == nil || controlServices.session.contextEngine == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	assembled, err := controlServices.session.contextEngine.Assemble(ctx, sessionID, maxContextTokensForAgent(cfg, agentID))
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
	var toolReg *agent.ToolRegistry
	var plugMgr *pluginmanager.GojaPluginManager
	if controlServices != nil {
		toolReg = controlServices.session.toolRegistry
		plugMgr = controlServices.handlers.pluginMgr
	}
	if toolReg == nil {
		return map[string]bool{}
	}
	groups := buildToolCatalogGroups(cfg, toolReg, nil, plugMgr)
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

func cloneTaskMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		out[key] = value
	}
	return out
}

func taskMetaString(task *state.TaskSpec, key string) string {
	if task == nil || task.Meta == nil {
		return ""
	}
	raw, _ := task.Meta[key].(string)
	return strings.TrimSpace(raw)
}

func deriveInheritedACPTask(sessionID string, inherited *state.TaskSpec, instructions string) *state.TaskSpec {
	if inherited == nil {
		return nil
	}
	task := state.TaskSpec{
		GoalID:       strings.TrimSpace(inherited.GoalID),
		ParentTaskID: strings.TrimSpace(inherited.TaskID),
		PlanID:       strings.TrimSpace(inherited.PlanID),
		SessionID:    strings.TrimSpace(sessionID),
		Title:        deriveACPTaskTitle(instructions),
		Instructions: strings.TrimSpace(instructions),
		Meta:         cloneTaskMeta(inherited.Meta),
	}
	if task.Meta == nil {
		task.Meta = map[string]any{}
	}
	if parentRunID := strings.TrimSpace(inherited.CurrentRunID); parentRunID != "" {
		task.Meta["parent_run_id"] = parentRunID
	}
	if sessionID != "" {
		task.Meta["parent_session_id"] = sessionID
	}
	return &task
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

func bindACPTaskID(payload *acppkg.TaskPayload, taskID string) {
	if payload == nil {
		return
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	if payload.Task == nil {
		return
	}
	task := payload.Task.Normalize()
	task.TaskID = taskID
	payload.Task = &task
}

func recordACPDelegatedChild(sessionStore *state.SessionStore, payload acppkg.TaskPayload, taskID string) {
	if sessionStore == nil {
		return
	}
	parent := payload.ParentContext
	if parent == nil || strings.TrimSpace(parent.SessionID) == "" {
		return
	}
	if err := sessionStore.AppendChildTask(parent.SessionID, taskID); err != nil {
		log.Printf("acp delegated child link failed parent_session=%s task_id=%s err=%v", parent.SessionID, taskID, err)
	}
}

func deriveACPTaskTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "task"
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	if len(text) > 96 {
		text = strings.TrimSpace(text[:96])
	}
	if text == "" {
		return "task"
	}
	return text
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
	if out.Task == nil {
		out.Task = deriveInheritedACPTask(sessionID, inherited.Task, out.Instructions)
	}
	if out.Task != nil {
		task := out.Task.Normalize()
		if task.Title == "" {
			task.Title = deriveACPTaskTitle(firstNonEmptyTrimmed(task.Instructions, out.Instructions))
		}
		if task.Instructions == "" {
			task.Instructions = out.Instructions
		}
		if task.SessionID == "" {
			task.SessionID = sessionID
		}
		if task.GoalID == "" && inherited.Task != nil {
			task.GoalID = strings.TrimSpace(inherited.Task.GoalID)
		}
		if task.ParentTaskID == "" && inherited.Task != nil {
			task.ParentTaskID = strings.TrimSpace(inherited.Task.TaskID)
		}
		if task.PlanID == "" && inherited.Task != nil {
			task.PlanID = strings.TrimSpace(inherited.Task.PlanID)
		}
		if task.Meta == nil {
			task.Meta = map[string]any{}
		}
		if inherited.Task != nil {
			if _, ok := task.Meta["parent_run_id"]; !ok {
				if parentRunID := strings.TrimSpace(inherited.Task.CurrentRunID); parentRunID != "" {
					task.Meta["parent_run_id"] = parentRunID
				}
			}
		}
		if _, ok := task.Meta["parent_session_id"]; !ok && sessionID != "" {
			task.Meta["parent_session_id"] = sessionID
		}
		out.Task = &task
	}
	return out
}
