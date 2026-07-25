package main

// control_rpc_introspection.go — control-RPC handlers for the gateway
// introspection long tail (swarmstr-wapc). Each method is backed by a real
// swarmstr subsystem; none returns a fabricated stub:
//
//   - system.info / diagnostics.stability — build-info globals + the Go runtime
//     + the boot crash/restart recovery snapshot + the agent-job registry.
//   - commands.list — the embedded Web UI slash-command catalog (internal/webui).
//   - update.status — the real update.Checker (read-only; never forces a fetch).
//   - tools.effective — the tool catalog filtered by the agent's profile, with a
//     best-effort per-tool overlay from the WS-G permission engine when present.
//   - tools.invoke — the live agent.ToolRegistry.Execute path.
//   - audit.list / audit.activity.list — the WS-G permission engine's Auditor.
//   - agents.workspace.list / .get — the durable agent-docs surface.
//   - approval.history — resolved records from the durable approval ledger.
//   - ui.command — dispatches the gateway method the Web UI maps a slash command to.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/permissions"
	"metiq/internal/store/state"
	"metiq/internal/webui"
)

func (h controlRPCHandler) handleIntrospectionRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	switch method {
	case methods.MethodSystemInfo:
		if _, err := methods.DecodeSystemInfoParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: h.buildSystemInfo()}, true, nil

	case methods.MethodDiagnosticsStability:
		if _, err := methods.DecodeDiagnosticsStabilityParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: h.buildStabilityDiagnostics()}, true, nil

	case methods.MethodCommandsList:
		req, err := methods.DecodeCommandsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		commands := make([]map[string]any, 0)
		for _, c := range webui.CommandCatalog() {
			if req.Source != "" && !strings.EqualFold(c.Source, req.Source) {
				continue
			}
			commands = append(commands, map[string]any{
				"command":     c.Command,
				"text":        c.Text,
				"source":      c.Source,
				"description": c.Desc,
			})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"commands": commands, "count": len(commands)}}, true, nil

	case methods.MethodUpdateStatus:
		if _, err := methods.DecodeUpdateStatusParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: h.buildUpdateStatus()}, true, nil

	case methods.MethodToolsEffective:
		req, err := methods.DecodeToolsEffectiveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.handleToolsEffective(ctx, cfg, req)

	case methods.MethodToolsInvoke:
		req, err := methods.DecodeToolsInvokeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.handleToolsInvoke(ctx, req)

	case methods.MethodAuditList:
		req, err := methods.DecodeAuditListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.handleAuditList(req)

	case methods.MethodAuditActivityList:
		req, err := methods.DecodeAuditActivityListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.handleAuditActivityList(req)

	case methods.MethodAgentsWorkspaceList:
		req, err := methods.DecodeAgentsWorkspaceListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.handleAgentsWorkspaceList(ctx, req)

	case methods.MethodAgentsWorkspaceGet:
		req, err := methods.DecodeAgentsWorkspaceGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.handleAgentsWorkspaceGet(ctx, req)

	case methods.MethodApprovalHistory:
		req, err := methods.DecodeApprovalHistoryParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.handleApprovalHistory(req)

	case methods.MethodUICommand:
		req, err := methods.DecodeUICommandParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return h.handleUICommand(ctx, in, req)
	}
	return nostruntime.ControlRPCResult{}, false, nil
}

// buildSystemInfo assembles the daemon identity/health snapshot from the
// build-info globals, the Go runtime, and the live subsystem presence signals.
func (h controlRPCHandler) buildSystemInfo() map[string]any {
	uptime := time.Since(h.deps.startedAt)
	return map[string]any{
		"version": daemonVersionString(),
		"commit":  commit,
		"pid":     os.Getpid(),
		"platform": map[string]any{
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"go_version": runtime.Version(),
			"num_cpu":    runtime.NumCPU(),
		},
		"uptime_seconds": int(uptime.Seconds()),
		"uptime_ms":      uptime.Milliseconds(),
		"capabilities": map[string]any{
			"mcp":         h.deps.mcpOps != nil,
			"permissions": h.deps.permEngine != nil,
			"channels":    h.deps.channels != nil,
			"node":        h.deps.nodePending != nil,
			"tasks":       h.deps.taskService != nil,
			"sessions":    h.deps.sessionStore != nil,
			"worktrees":   h.deps.worktrees != nil,
		},
	}
}

// buildStabilityDiagnostics assembles a process stability/health snapshot from
// the Go runtime, the boot crash/restart recovery outcomes, and the agent-job
// registry.
func (h controlRPCHandler) buildStabilityDiagnostics() map[string]any {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	uptime := time.Since(h.deps.startedAt)
	out := map[string]any{
		"uptime_seconds": int(uptime.Seconds()),
		"uptime_ms":      uptime.Milliseconds(),
		"goroutines":     runtime.NumGoroutine(),
		"num_cpu":        runtime.NumCPU(),
		"memory": map[string]any{
			"alloc_bytes":      mem.Alloc,
			"sys_bytes":        mem.Sys,
			"heap_alloc":       mem.HeapAlloc,
			"num_gc":           mem.NumGC,
			"gc_pause_last_ns": mem.PauseNs[(mem.NumGC+255)%256],
		},
		"restart_scheduler_ready": h.deps.restartCh != nil,
	}
	if h.deps.agentJobs != nil {
		out["active_runs"] = h.deps.agentJobs.ActiveRuns()
	}
	if recovery := recoveryStatusSnapshot(); len(recovery) > 0 {
		out["recovery"] = recovery
	}
	return out
}

// buildUpdateStatus reports the self-update status without any network I/O: the
// running version plus the last cached release check (if one exists). When no
// updater is wired (unit tests), it honestly reports the running version with no
// available update rather than fabricating one.
func (h controlRPCHandler) buildUpdateStatus() map[string]any {
	current := daemonVersionString()
	out := map[string]any{
		"current_version":  current,
		"update_available": false,
		"checked":          false,
	}
	if h.deps.services == nil || h.deps.services.handlers.updateChecker == nil {
		out["note"] = "update checker not configured; reporting running version only"
		return out
	}
	checker := h.deps.services.handlers.updateChecker
	if c := strings.TrimSpace(checker.Current()); c != "" {
		out["current_version"] = c
	}
	if result, ok := checker.Cached(); ok {
		out["update_available"] = result.Available
		out["latest_version"] = result.Latest
		out["checked"] = true
		out["checked_at_ms"] = result.CheckedAt
		if result.Error != "" {
			out["error"] = result.Error
		}
	} else {
		out["note"] = "no recent release check cached; call update.run to refresh"
	}
	return out
}

func (h controlRPCHandler) handleToolsEffective(ctx context.Context, cfg state.ConfigDoc, req methods.ToolsEffectiveRequest) (nostruntime.ControlRPCResult, bool, error) {
	if err := isKnownAgentID(ctx, h.deps.docsRepo, req.AgentID); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	agentID := defaultAgentID(req.AgentID)

	// Resolve the effective profile: an explicit override, else the agent's
	// stored profile, else the default.
	profileID := agent.DefaultProfile
	if h.deps.docsRepo != nil {
		if doc, err := h.deps.docsRepo.GetAgent(ctx, agentID); err == nil {
			if p, ok := doc.Meta[agent.AgentProfileKey].(string); ok && strings.TrimSpace(p) != "" {
				profileID = p
			}
		}
	}
	if req.Profile != nil && *req.Profile != "" {
		profileID = *req.Profile
	}
	if agent.LookupProfile(profileID) == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown profile %q; valid: %s", profileID, strings.Join(agent.ProfileListSorted(), ", "))
	}

	groups := buildToolCatalogGroups(cfg, h.deps.tools, req.IncludePlugins, h.deps.pluginMgr)
	groups = agent.FilterCatalogByProfile(groups, profileID)

	// Flatten the effective tool ids and, when the permission engine is present,
	// overlay each with the engine's decision behavior.
	toolIDs := make([]string, 0, 16)
	for _, group := range groups {
		toolsAny, _ := group["tools"].([]map[string]any)
		for _, t := range toolsAny {
			if id, ok := t["id"].(string); ok && strings.TrimSpace(id) != "" {
				toolIDs = append(toolIDs, id)
			}
		}
	}
	sort.Strings(toolIDs)

	result := map[string]any{
		"agentId":            agentID,
		"profile":            profileID,
		"groups":             groups,
		"tools":              toolIDs,
		"count":              len(toolIDs),
		"permissionsEnabled": h.deps.permEngine != nil,
	}
	if h.deps.permEngine != nil {
		policy := make([]map[string]any, 0, len(toolIDs))
		for _, id := range toolIDs {
			pr := permissions.NewToolRequest(id, permissionCategoryForTool(h.deps.tools, id)).
				WithContext("", "", agentID, "")
			if origin, originName := permissionOriginForTool(h.deps.tools, id); origin != "" || originName != "" {
				pr = pr.WithOrigin(origin, originName)
			}
			// Preview (non-recording): this inspects every catalog tool, so using the
			// auditing Evaluate would flood the bounded permission audit log with
			// synthetic decisions for tools that were never actually invoked.
			decision := h.deps.permEngine.EvaluatePreview(pr)
			policy = append(policy, map[string]any{
				"tool":     id,
				"behavior": string(decision.Behavior),
				"reason":   decision.Reason,
			})
		}
		result["policy"] = policy
	}
	return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(result)}, true, nil
}

func (h controlRPCHandler) handleToolsInvoke(ctx context.Context, req methods.ToolsInvokeRequest) (nostruntime.ControlRPCResult, bool, error) {
	if h.deps.tools == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("tool registry unavailable")
	}
	if _, ok := h.deps.tools.Descriptor(req.Tool); !ok {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown tool %q", req.Tool)
	}
	// Execute through the live registry path: schema + semantic validation, tool
	// policy, and pre/post hooks all apply exactly as they do for agent turns.
	output, err := h.deps.tools.Execute(ctx, agent.ToolCall{Name: req.Tool, Args: req.Args})
	if err != nil {
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":    false,
			"tool":  req.Tool,
			"error": err.Error(),
		}}, true, nil
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"ok":     true,
		"tool":   req.Tool,
		"result": output,
	}}, true, nil
}

func (h controlRPCHandler) handleAuditList(req methods.AuditListRequest) (nostruntime.ControlRPCResult, bool, error) {
	if h.deps.permEngine == nil || !h.deps.permEngine.AuditEnabled() {
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"entries":   []any{},
			"count":     0,
			"available": false,
			"note":      "permission-engine audit log is not configured",
		}}, true, nil
	}
	opts := permissions.AuditQueryOptions{
		Type:     permissions.AuditEventType(req.Type),
		ToolName: req.Tool,
		Limit:    req.Limit,
	}
	if req.SinceMS > 0 {
		t := time.UnixMilli(req.SinceMS)
		opts.Since = &t
	}
	if req.UntilMS > 0 {
		t := time.UnixMilli(req.UntilMS)
		opts.Until = &t
	}
	events, err := h.deps.permEngine.QueryAudit(opts)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"entries":   events,
		"count":     len(events),
		"available": true,
	}}, true, nil
}

func (h controlRPCHandler) handleAuditActivityList(req methods.AuditActivityListRequest) (nostruntime.ControlRPCResult, bool, error) {
	if h.deps.permEngine == nil || !h.deps.permEngine.AuditEnabled() {
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"activity":  []any{},
			"count":     0,
			"available": false,
			"note":      "permission-engine activity feed is not configured",
		}}, true, nil
	}
	opts := permissions.AuditQueryOptions{}
	if req.SinceMS > 0 {
		t := time.UnixMilli(req.SinceMS)
		opts.Since = &t
	}
	if req.UntilMS > 0 {
		t := time.UnixMilli(req.UntilMS)
		opts.Until = &t
	}
	events, err := h.deps.permEngine.QueryAudit(opts)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	activity := make([]map[string]any, 0, len(events))
	for _, e := range events {
		// The activity feed surfaces agent/session-attributable events.
		if req.AgentID != "" && e.AgentID != req.AgentID {
			continue
		}
		if req.SessionID != "" && e.SessionID != req.SessionID {
			continue
		}
		if e.AgentID == "" && e.SessionID == "" && req.AgentID == "" && req.SessionID == "" {
			// keep un-attributed events only when the caller is not filtering.
		}
		activity = append(activity, map[string]any{
			"id":        e.ID,
			"type":      string(e.Type),
			"timestamp": e.Timestamp.UnixMilli(),
			"agentId":   e.AgentID,
			"sessionId": e.SessionID,
			"tool":      e.ToolName,
			"behavior":  string(e.Behavior),
			"reason":    e.Reason,
		})
		if req.Limit > 0 && len(activity) >= req.Limit {
			break
		}
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"activity":  activity,
		"count":     len(activity),
		"available": true,
	}}, true, nil
}

func (h controlRPCHandler) handleAgentsWorkspaceList(ctx context.Context, req methods.AgentsWorkspaceListRequest) (nostruntime.ControlRPCResult, bool, error) {
	if h.deps.docsRepo == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("docs repository unavailable")
	}
	docs, err := h.deps.docsRepo.ListAgents(ctx, req.Limit)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	agents := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		if doc.Deleted {
			continue
		}
		if req.Workspace != "" && doc.Workspace != req.Workspace {
			continue
		}
		agents = append(agents, workspaceAgentSummary(doc))
	}
	out := map[string]any{"agents": agents, "count": len(agents)}
	if req.Workspace != "" {
		out["workspace"] = req.Workspace
	}
	return nostruntime.ControlRPCResult{Result: out}, true, nil
}

func (h controlRPCHandler) handleAgentsWorkspaceGet(ctx context.Context, req methods.AgentsWorkspaceGetRequest) (nostruntime.ControlRPCResult, bool, error) {
	if h.deps.docsRepo == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("docs repository unavailable")
	}
	doc, err := h.deps.docsRepo.GetAgent(ctx, req.AgentID)
	if err != nil {
		if err == state.ErrNotFound {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown agent id %q", req.AgentID)
		}
		return nostruntime.ControlRPCResult{}, true, err
	}
	if doc.Deleted {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown agent id %q", req.AgentID)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"agent": workspaceAgentSummary(doc)}}, true, nil
}

func workspaceAgentSummary(doc state.AgentDoc) map[string]any {
	out := map[string]any{
		"agentId":   doc.AgentID,
		"agent_id":  doc.AgentID,
		"name":      doc.Name,
		"workspace": doc.Workspace,
		"model":     doc.Model,
		"version":   doc.Version,
	}
	if len(doc.Meta) > 0 {
		out["meta"] = doc.Meta
	}
	return out
}

func (h controlRPCHandler) handleApprovalHistory(req methods.ApprovalHistoryRequest) (nostruntime.ControlRPCResult, bool, error) {
	if h.deps.execApprovals == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("approvals runtime not configured")
	}
	records := h.deps.execApprovals.ListApprovals(req.Kind, "resolved")
	// Newest-resolved first.
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].ResolvedAt > records[j].ResolvedAt
	})
	if req.Limit > 0 && len(records) > req.Limit {
		records = records[:req.Limit]
	}
	out := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		out = append(out, approvalPendingSnapshot(rec))
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"approvals": out, "count": len(out)}}, true, nil
}

// uiCommandRoutes maps a Web UI slash command to the gateway method it invokes.
var uiCommandRoutes = map[string]string{
	"help":     methods.MethodCommandsList,
	"status":   methods.MethodStatus,
	"agents":   methods.MethodAgentsList,
	"channels": methods.MethodChannelsList,
	"compact":  methods.MethodMemoryCompact,
}

func (h controlRPCHandler) handleUICommand(ctx context.Context, in nostruntime.ControlRPCInbound, req methods.UICommandRequest) (nostruntime.ControlRPCResult, bool, error) {
	name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Command), "/"))
	if idx := strings.IndexByte(name, ' '); idx >= 0 {
		name = name[:idx]
	}
	target, ok := uiCommandRoutes[name]
	if !ok {
		known := make([]string, 0, len(uiCommandRoutes))
		for k := range uiCommandRoutes {
			known = append(known, "/"+k)
		}
		sort.Strings(known)
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown ui command %q; known: %s", req.Command, strings.Join(known, ", "))
	}
	params, err := json.Marshal(req.Args)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	// Re-dispatch the resolved method through the normal handler. Internal=true
	// bypasses a redundant control-policy re-check: ui.command has already been
	// authorized at operator.admin, which is at least as strict as any target.
	inner := nostruntime.ControlRPCInbound{
		Method:        target,
		Params:        params,
		FromPubKey:    in.FromPubKey,
		Authenticated: in.Authenticated,
		Internal:      true,
	}
	result, err := h.Handle(ctx, inner)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"command": req.Command,
		"method":  target,
		"result":  result.Result,
	}}, true, nil
}

// daemonVersionString returns the running daemon version, preferring the
// build-info global (ldflags) and falling back to the compiled default.
func daemonVersionString() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	return "metiqd"
}
