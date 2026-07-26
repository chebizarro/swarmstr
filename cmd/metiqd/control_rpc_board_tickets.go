package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	boardpkg "metiq/internal/gateway/board"
	"metiq/internal/gateway/mcpapp"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	pluginsurface "metiq/internal/plugins/surface"
)

// Ticket-authorized board view methods (defer-bucket slice swarmstr-fokh.5).
// board.get mints short-lived view tickets (internal/gateway/board/ticket.go)
// for renderable html widgets; widget code presents that ticket here to ask
// about prompt confirmation (board.prompt.authorize), read granted data
// bindings (board.data.read), and run granted action verbs (board.action).
// Every capability is gated on the byte-frozen granted declaration resolved
// from current store state, so a re-put or rejected grant revokes access
// immediately even inside the ticket TTL.

const maxBoardCapabilityParamBytes = 8 * 1024

// boardCoreDataBindings is the closed allowlist of read-only gateway methods
// a widget may be granted as data bindings, mirroring OpenClaw
// CORE_BOARD_DATA_BINDING_IDS. Plugin dashboard data bindings extend this set
// at runtime through the plugin-surface registry (swarmstr-qmxu.3): a granted
// binding id owned by a loaded plugin dispatches read-only into that plugin's
// sandboxed runtime instead of a host method.
var boardCoreDataBindings = map[string]string{
	methods.MethodSessionsList: methods.MethodSessionsList,
	methods.MethodUsageStatus:  methods.MethodUsageStatus,
	methods.MethodUsageCost:    methods.MethodUsageCost,
	methods.MethodCronList:     methods.MethodCronList,
	methods.MethodCronStatus:   methods.MethodCronStatus,
	methods.MethodAgentsList:   methods.MethodAgentsList,
	methods.MethodHealth:       methods.MethodHealth,
}

// boardCoreDataBindingIDs returns the reserved core board-binding ids that no
// plugin contribution may claim (they dispatch to host methods, not into a
// plugin runtime). Fed into the plugin-surface registry so a plugin whose id
// is a prefix of a core binding cannot alias it.
func boardCoreDataBindingIDs() []string {
	ids := make([]string, 0, len(boardCoreDataBindings))
	for id := range boardCoreDataBindings {
		ids = append(ids, id)
	}
	return ids
}

// dispatchBoardHostMethod re-enters the control RPC dispatch for a
// board-hosted capability call. The synthetic inbound is marked Internal so
// policy evaluation is skipped: authorization already happened via the view
// ticket plus the granted capability declaration.
func (h controlRPCHandler) dispatchBoardHostMethod(ctx context.Context, method string, params any) (any, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("board capability params must be JSON serializable")
	}
	res, err := h.Handle(ctx, nostruntime.ControlRPCInbound{
		Method:   method,
		Params:   raw,
		Internal: true,
	})
	if err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	return res.Result, nil
}

func assertBoardCapabilityParamsSize(params map[string]any, capability string) error {
	if len(params) == 0 {
		return nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("board widget %s params must be JSON serializable", capability)
	}
	if len(raw) > maxBoardCapabilityParamBytes {
		return fmt.Errorf("board widget %s params exceed %d UTF-8 bytes", capability, maxBoardCapabilityParamBytes)
	}
	return nil
}

// handleBoardWidgetAppView re-mints an MCP-App view from a pinned mcp-app
// widget. Interactivity requires both the pinned view to have been
// interactive and the operator grant to be granted; anything else mints a
// read-only view. The revision+instance check ensures a concurrent re-put
// can never be viewed under a stale authorization.
func (h controlRPCHandler) handleBoardWidgetAppView(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardWidgetAppViewParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	store, err := h.boardStore()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	registry, err := h.mcpAppViews()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	snapshot := store.GetSnapshot(req.SessionKey)
	var widget *boardpkg.Widget
	for i := range snapshot.Widgets {
		if snapshot.Widgets[i].Name == req.Name {
			widget = &snapshot.Widgets[i]
			break
		}
	}
	doc, ok := store.ReadWidgetMcpApp(req.SessionKey, req.Name)
	if widget == nil || widget.ContentKind != boardpkg.ContentKindMcpApp ||
		widget.Revision != req.Revision || widget.InstanceID != req.InstanceID ||
		!ok || doc.Revision != req.Revision || doc.InstanceID != req.InstanceID {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board MCP App widget not found: %s", req.Name)
	}
	interactive := doc.Interactive && doc.GrantState == boardpkg.GrantGranted
	view := registry.Mint(mcpapp.View{
		SessionKey:    snapshot.SessionKey,
		ServerName:    doc.Descriptor.ServerName,
		ToolName:      doc.Descriptor.ToolName,
		ToolCallID:    doc.Descriptor.ToolCallID,
		UIResourceURI: doc.Descriptor.UIResourceURI,
		ReadOnly:      !interactive,
	})
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"viewId":      view.ViewID,
		"expiresAtMs": view.ExpiresAt.UnixMilli(),
	}}, true, nil
}

func (h controlRPCHandler) handleBoardPromptAuthorize(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardPromptAuthorizeParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	store, err := h.boardStore()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := store.ResolveViewTicket(req.Ticket)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"confirmationRequired": !view.HasGrantedTool("prompt"),
	}}, true, nil
}

func (h controlRPCHandler) handleBoardDataRead(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardDataReadParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if err := assertBoardCapabilityParamsSize(req.Params, "data binding"); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	store, err := h.boardStore()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := store.ResolveViewTicket(req.Ticket)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	// Grant gate first: the binding must be declared by the widget AND granted
	// by the operator, whether it resolves to a core method or a plugin.
	if !view.HasGrantedTool(req.BindingID) {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget tool is not granted: %s", req.BindingID)
	}
	params := req.Params
	if params == nil {
		params = map[string]any{}
	}
	if method, ok := boardCoreDataBindings[req.BindingID]; ok {
		result, err := h.dispatchBoardHostMethod(ctx, method, params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	}
	// Plugin data-binding extension (swarmstr-qmxu.3). The registry resolves the
	// granted binding id to its owning plugin (fail-closed on unknown/colliding
	// ids); dispatch runs read-only inside the plugin's own sandboxed VM.
	if binding, ok := h.pluginSurfaceBinding(req.BindingID); ok {
		result, err := h.deps.surfaceDispatch.InvokeSurface(ctx, binding.PluginID, binding.ID, params, map[string]any{
			"sessionKey":   view.SessionKey,
			"widget":       view.Name,
			"surface_kind": "data_binding",
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"pluginId":  binding.PluginID,
			"bindingId": binding.ID,
			"result":    result,
		}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget data binding is not allowed: %s", req.BindingID)
}

func (h controlRPCHandler) handleBoardAction(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardActionParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	store, err := h.boardStore()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := store.ResolveViewTicket(req.Ticket)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if req.JobID != "" {
		capability := "cron.trigger:" + req.JobID
		if !view.HasGrantedTool(capability) {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget tool is not granted: %s", capability)
		}
		result, err := h.dispatchBoardHostMethod(ctx, methods.MethodCronRun, map[string]any{"id": req.JobID})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	}
	if !view.HasGrantedTool(req.Action) {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget tool is not granted: %s", req.Action)
	}
	if err := assertBoardCapabilityParamsSize(req.Params, "action"); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	// Plugin action-verb extension (swarmstr-qmxu.3). The registry resolves the
	// granted verb id to its owning plugin (fail-closed on unknown/colliding
	// ids); dispatch runs inside the plugin's own sandboxed VM and may have
	// plugin-scoped side effects.
	if verb, ok := h.pluginSurfaceActionVerb(req.Action); ok {
		params := req.Params
		if params == nil {
			params = map[string]any{}
		}
		result, err := h.deps.surfaceDispatch.InvokeSurface(ctx, verb.PluginID, verb.ID, params, map[string]any{
			"sessionKey":   view.SessionKey,
			"widget":       view.Name,
			"surface_kind": "action_verb",
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"pluginId": verb.PluginID,
			"action":   verb.ID,
			"result":   result,
		}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget action verb is not allowed: %s", req.Action)
}

// pluginSurfaceDispatcher invokes a resolved plugin surface verb inside the
// owning plugin's sandboxed runtime. *pluginmanager.GojaPluginManager
// satisfies it in production; tests inject a fake.
type pluginSurfaceDispatcher interface {
	InvokeSurface(ctx context.Context, pluginID, verb string, args, meta map[string]any) (any, error)
}

// pluginSurfaceBinding resolves a granted board data-binding id to its owning
// plugin via the plugin-surface registry. Returns false (fail-closed) when the
// registry or dispatcher is unavailable, or the id does not resolve.
func (h controlRPCHandler) pluginSurfaceBinding(id string) (pluginsurface.Binding, bool) {
	if h.deps.pluginSurface == nil || h.deps.surfaceDispatch == nil {
		return pluginsurface.Binding{}, false
	}
	return h.deps.pluginSurface.LookupBinding(id)
}

// pluginSurfaceActionVerb resolves a granted board action-verb id to its owning
// plugin via the plugin-surface registry. Fail-closed like pluginSurfaceBinding.
func (h controlRPCHandler) pluginSurfaceActionVerb(id string) (pluginsurface.Verb, bool) {
	if h.deps.pluginSurface == nil || h.deps.surfaceDispatch == nil {
		return pluginsurface.Verb{}, false
	}
	return h.deps.pluginSurface.LookupActionVerb(id)
}
