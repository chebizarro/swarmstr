package main

// control_rpc_plugin_surface.go — control-RPC handlers for the plugin-surface
// long tail (swarmstr-zzin, WS-G): plugins.list / plugins.search /
// plugins.setEnabled / plugins.refresh and the durable plugin.approval.*
// request/waitDecision/resolve/list lifecycle. Served over the control-RPC
// tooling surface only, mirroring the skills discovery wiring
// (control_rpc_skills_surface.go).
//
// Listing is derived on demand from the merged plugin catalog: config
// (plugins.installs install records + plugins.entries enabled flags) unioned
// with the live GojaPluginManager (loaded manifests + tool counts). The
// approval ledger mirrors internal/gateway/questions: pending approvals persist
// durably and plugin.approval.waitDecision parks a caller on the operator's
// decision exactly like question.waitAnswer.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"metiq/internal/gateway/methods"
	pluginapprovalpkg "metiq/internal/gateway/pluginapproval"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) pluginApprovalManager() (*pluginapprovalpkg.Manager, error) {
	if h.deps.pluginApprovals == nil {
		return nil, fmt.Errorf("plugin approval surface is not available")
	}
	return h.deps.pluginApprovals, nil
}

// pluginExtensions returns the plugins.* extensions config subtree.
func pluginExtensions(cfg state.ConfigDoc) map[string]any {
	if cfg.Extra == nil {
		return nil
	}
	rawExt, _ := cfg.Extra["extensions"].(map[string]any)
	return rawExt
}

// buildPluginList derives the merged plugin catalog from config + the live
// plugin manager. Each entry reports enabled/loaded state, source metadata, and
// (when loaded) the plugin's tool surface.
func (h controlRPCHandler) buildPluginList(cfg state.ConfigDoc) []map[string]any {
	rawExt := pluginExtensions(cfg)
	entries, _ := rawExt["entries"].(map[string]any)
	installs, _ := rawExt["installs"].(map[string]any)

	loaded := map[string]struct{}{}
	if h.deps.pluginMgr != nil {
		for _, id := range h.deps.pluginMgr.PluginIDs() {
			loaded[id] = struct{}{}
		}
	}

	ids := map[string]struct{}{}
	for id := range entries {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = struct{}{}
		}
	}
	for id := range installs {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = struct{}{}
		}
	}
	for id := range loaded {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = struct{}{}
		}
	}

	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)

	out := make([]map[string]any, 0, len(sorted))
	for _, id := range sorted {
		entry, hasEntry := entries[id].(map[string]any)
		install, _ := installs[id].(map[string]any)
		_, isLoaded := loaded[id]

		// enabled mirrors the manager's entryEnabled: true unless the entry
		// explicitly sets enabled:false. Plugins without an entry are not
		// eligible for loading, so they read as disabled.
		enabled := false
		if hasEntry {
			enabled = true
			if b, ok := entry["enabled"].(bool); ok {
				enabled = b
			}
		}

		rec := map[string]any{
			"id":       id,
			"enabled":  enabled,
			"loaded":   isLoaded,
			"hasEntry": hasEntry,
		}
		if install != nil {
			if v := stringField(install, "source"); v != "" {
				rec["source"] = v
			}
			if v := stringField(install, "installPath"); v != "" {
				rec["installPath"] = v
			}
			if v := stringField(install, "spec"); v != "" {
				rec["spec"] = v
			}
			if v := stringField(install, "version"); v != "" {
				rec["version"] = v
			}
		}
		if entry != nil {
			if v := stringField(entry, "plugin_type"); v != "" {
				rec["type"] = v
			}
		}
		if isLoaded && h.deps.pluginMgr != nil {
			if mf, err := h.deps.pluginMgr.PluginManifest(id); err == nil {
				if strings.TrimSpace(mf.ID) != "" {
					rec["name"] = mf.ID
				}
				if strings.TrimSpace(mf.Version) != "" {
					rec["version"] = mf.Version
				}
				if strings.TrimSpace(mf.Description) != "" {
					rec["description"] = mf.Description
				}
				tools := make([]map[string]any, 0, len(mf.Tools))
				for _, t := range mf.Tools {
					tools = append(tools, map[string]any{
						"name":        t.Name,
						"description": t.Description,
					})
				}
				rec["tools"] = tools
				rec["toolCount"] = len(tools)
			}
		}
		if _, ok := rec["name"]; !ok {
			rec["name"] = id
		}
		out = append(out, rec)
	}
	return out
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func pluginListMatchesEnabled(rec map[string]any, want *bool) bool {
	if want == nil {
		return true
	}
	enabled, _ := rec["enabled"].(bool)
	return enabled == *want
}

func pluginSearchScore(rec map[string]any, query string) int {
	if query == "" {
		return 1
	}
	fields := []string{
		stringField(rec, "id"),
		stringField(rec, "name"),
		stringField(rec, "description"),
		stringField(rec, "spec"),
		stringField(rec, "source"),
	}
	score := 0
	for _, f := range fields {
		lf := strings.ToLower(f)
		if lf == query {
			score += 100
		} else if strings.Contains(lf, query) {
			score += 10
		}
	}
	return score
}

func (h controlRPCHandler) handlePluginSurfaceRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	switch method {
	case methods.MethodPluginsList:
		req, err := methods.DecodePluginsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		all := h.buildPluginList(cfg)
		plugins := make([]map[string]any, 0, len(all))
		for _, rec := range all {
			if pluginListMatchesEnabled(rec, req.Enabled) {
				plugins = append(plugins, rec)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"plugins": plugins,
			"count":   len(plugins),
		}}, true, nil

	case methods.MethodPluginsSearch:
		req, err := methods.DecodePluginsSearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		query := strings.ToLower(req.Query)
		type scored struct {
			rec   map[string]any
			score int
		}
		ranked := make([]scored, 0)
		for _, rec := range h.buildPluginList(cfg) {
			if !pluginListMatchesEnabled(rec, req.Enabled) {
				continue
			}
			s := pluginSearchScore(rec, query)
			if s <= 0 {
				continue
			}
			ranked = append(ranked, scored{rec: rec, score: s})
		}
		sort.SliceStable(ranked, func(i, j int) bool {
			if ranked[i].score != ranked[j].score {
				return ranked[i].score > ranked[j].score
			}
			return stringField(ranked[i].rec, "id") < stringField(ranked[j].rec, "id")
		})
		results := make([]map[string]any, 0, len(ranked))
		for i, item := range ranked {
			if i >= req.Limit {
				break
			}
			rec := item.rec
			rec["score"] = item.score
			results = append(results, rec)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"query":   req.Query,
			"results": results,
			"count":   len(results),
		}}, true, nil

	case methods.MethodPluginsSetEnabled:
		req, err := methods.DecodePluginsSetEnabledParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		commit, err := commitRuntimeConfigMutation(ctx, h.deps.docsRepo, h.deps.configState, configMutationCommitRequest{
			BaseHash: req.BaseHash,
			BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
				return methods.ApplyConfigSet(current, "plugins.entries."+req.PluginID+".enabled", req.Enabled)
			},
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":              true,
			"pluginId":        req.PluginID,
			"enabled":         req.Enabled,
			"hash":            commit.Next.Hash(),
			"restart_pending": commit.RestartPending,
		}}, true, nil

	case methods.MethodPluginsRefresh:
		req, err := methods.DecodePluginsRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		reloaded := false
		if h.deps.pluginMgr != nil {
			// Load is documented idempotent: it recompiles the enabled plugin
			// set from current config, replacing the prior set. This picks up
			// setEnabled/install/uninstall changes without a daemon restart.
			if err := h.deps.pluginMgr.Load(ctx, cfg); err != nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin refresh failed: %w", err)
			}
			reloaded = true
		}
		all := h.buildPluginList(cfg)
		plugins := make([]map[string]any, 0, len(all))
		for _, rec := range all {
			if pluginListMatchesEnabled(rec, req.Enabled) {
				plugins = append(plugins, rec)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":       true,
			"reloaded": reloaded,
			"plugins":  plugins,
			"count":    len(plugins),
		}}, true, nil

	case methods.MethodPluginsUIDescriptors:
		req, err := methods.DecodePluginsUIDescriptorsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		descriptors := []map[string]any{}
		if h.deps.pluginSurface != nil {
			descriptors = h.deps.pluginSurface.Descriptors()
		}
		if req.PluginID != "" {
			filtered := make([]map[string]any, 0, len(descriptors))
			for _, d := range descriptors {
				if d["pluginId"] == req.PluginID {
					filtered = append(filtered, d)
				}
			}
			descriptors = filtered
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"descriptors": descriptors,
			"count":       len(descriptors),
		}}, true, nil

	case methods.MethodPluginsSessionAction:
		req, err := methods.DecodePluginsSessionActionParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.pluginSurface == nil || h.deps.surfaceDispatch == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin surface is not available")
		}
		store, err := h.boardStore()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		// Grant gate: the board view ticket must resolve to a granted widget
		// whose declared tools include the verb (board.widget.grant). This routes
		// the session action through the same operator-approval boundary as the
		// board surface — fail-closed for untrusted plugins.
		view, err := store.ResolveViewTicket(req.Ticket)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if !view.HasGrantedTool(req.Verb) {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin session action verb is not granted: %s", req.Verb)
		}
		verb, ok := h.deps.pluginSurface.LookupSessionAction(req.Verb)
		if !ok {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin session action verb is not allowed: %s", req.Verb)
		}
		params := req.Params
		if params == nil {
			params = map[string]any{}
		}
		result, err := h.deps.surfaceDispatch.InvokeSurface(ctx, verb.PluginID, verb.ID, params, map[string]any{
			"sessionKey":   view.SessionKey,
			"session_id":   view.SessionKey,
			"widget":       view.Name,
			"surface_kind": "session_action",
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":             true,
			"pluginId":       verb.PluginID,
			"verb":           verb.ID,
			"sessionKey":     view.SessionKey,
			"mutatesSession": verb.MutatesSession,
			"result":         result,
		}}, true, nil

	case methods.MethodPluginSurfaceRefresh:
		req, err := methods.DecodePluginSurfaceRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.pluginSurface == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin surface is not available")
		}
		scope := "all"
		reloaded := false
		if req.PluginID != "" {
			// Single-plugin scope: re-aggregate only; validate the plugin exists.
			scope = req.PluginID
			if !h.deps.pluginSurface.RefreshScope(req.PluginID) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin %q is not loaded", req.PluginID)
			}
		} else {
			// All-plugins scope: reload plugin code then re-aggregate.
			if h.deps.pluginMgr != nil {
				if err := h.deps.pluginMgr.Load(ctx, cfg); err != nil {
					return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin surface refresh failed: %w", err)
				}
				reloaded = true
			}
			h.deps.pluginSurface.Refresh()
		}
		widgets, bindings, verbs, sessions := h.deps.pluginSurface.Counts()
		emitControlWSEvent(gatewayws.EventPluginSurfaceChanged, map[string]any{
			"scope":          scope,
			"widgets":        widgets,
			"bindings":       bindings,
			"verbs":          verbs,
			"sessionActions": sessions,
		})
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":             true,
			"scope":          scope,
			"reloaded":       reloaded,
			"descriptors":    h.deps.pluginSurface.Descriptors(),
			"widgets":        widgets,
			"bindings":       bindings,
			"verbs":          verbs,
			"sessionActions": sessions,
		}}, true, nil

	case methods.MethodPluginApprovalList:
		if _, err := methods.DecodePluginApprovalListParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		mgr, err := h.pluginApprovalManager()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"approvals": mgr.List()}}, true, nil

	case methods.MethodPluginApprovalRequest:
		req, err := methods.DecodePluginApprovalRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		mgr, err := h.pluginApprovalManager()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		rec, err := mgr.Request(pluginapprovalpkg.RequestParams{
			ID:         req.ID,
			PluginID:   req.PluginID,
			Action:     req.Action,
			Reason:     req.Reason,
			SessionKey: req.SessionKey,
			AgentID:    req.AgentID,
			Detail:     req.Detail,
			TimeoutMS:  req.TimeoutMS,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"id":          rec.ID,
			"status":      rec.Status,
			"expiresAtMs": rec.ExpiresAtMs,
		}}, true, nil

	case methods.MethodPluginApprovalWaitDecision:
		req, err := methods.DecodePluginApprovalWaitDecisionParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		mgr, err := h.pluginApprovalManager()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := mgr.WaitDecision(ctx, req.ID, req.TimeoutMS)
		if err != nil {
			if perr := (*pluginapprovalpkg.Error)(nil); errors.As(err, &perr) && perr.Code == pluginapprovalpkg.ErrCodeNotFound {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin approval %q not found", req.ID)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodPluginApprovalResolve:
		req, err := methods.DecodePluginApprovalResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		mgr, err := h.pluginApprovalManager()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		rec, err := mgr.Resolve(req.ID, req.Decision, req.DecidedBy, req.Note)
		if err != nil {
			if perr := (*pluginapprovalpkg.Error)(nil); errors.As(err, &perr) && perr.Code == pluginapprovalpkg.ErrCodeNotFound {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("plugin approval %q not found", req.ID)
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"id":       rec.ID,
			"status":   rec.Status,
			"decision": rec.Decision,
		}}, true, nil

	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
