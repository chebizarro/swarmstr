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
// CORE_BOARD_DATA_BINDING_IDS. Plugin dashboard data bindings are a Metiq
// deviation: not supported until a plugin dashboard registry exists.
var boardCoreDataBindings = map[string]string{
	methods.MethodSessionsList: methods.MethodSessionsList,
	methods.MethodUsageStatus:  methods.MethodUsageStatus,
	methods.MethodUsageCost:    methods.MethodUsageCost,
	methods.MethodCronList:     methods.MethodCronList,
	methods.MethodCronStatus:   methods.MethodCronStatus,
	methods.MethodAgentsList:   methods.MethodAgentsList,
	methods.MethodHealth:       methods.MethodHealth,
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
	if !view.HasGrantedTool(req.BindingID) {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget tool is not granted: %s", req.BindingID)
	}
	method, ok := boardCoreDataBindings[req.BindingID]
	if !ok {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget data binding is not allowed: %s", req.BindingID)
	}
	params := req.Params
	if params == nil {
		params = map[string]any{}
	}
	result, err := h.dispatchBoardHostMethod(ctx, method, params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
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
	// Metiq deviation: plugin dashboard action verbs require a plugin verb
	// registry that has not landed; only cron.trigger actions are executable.
	return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget action verb is not allowed: %s", req.Action)
}
