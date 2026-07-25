package main

import (
	"context"
	"fmt"
	"time"

	"metiq/internal/autoreply"
	boardpkg "metiq/internal/gateway/board"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
)

// Board RPC handlers (WS-A/A7.4 + the view-ticket slice). The board store is
// process-local, keyed by sessionKey; every mutating call broadcasts
// board.changed so clients refetch via board.get, which mints short-lived
// view tickets for renderable html widgets. Ticket-authorized methods live in
// control_rpc_board_tickets.go; the mcp-app appView flow remains deferred.

func (h controlRPCHandler) boardStore() (*boardpkg.Store, error) {
	if h.deps.boardStore == nil {
		return nil, fmt.Errorf("board surface is not available")
	}
	return h.deps.boardStore, nil
}

func emitBoardChanged(snapshot boardpkg.Snapshot, widget string) {
	emitControlWSEvent(gatewayws.EventBoardChanged, gatewayws.BoardChangedPayload{
		SessionKey: snapshot.SessionKey,
		Revision:   snapshot.Revision,
		Widget:     widget,
	})
}

func (h controlRPCHandler) handleBoardGet(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardGetParams(in.Params)
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
	return nostruntime.ControlRPCResult{Result: store.GetSnapshotWithTickets(req.SessionKey)}, true, nil
}

func boardOpFromParam(op methods.BoardOpParam) boardpkg.Op {
	return boardpkg.Op{
		Kind:       op.Kind,
		TabID:      op.TabID,
		Title:      op.Title,
		ChatDock:   op.ChatDock,
		Position:   op.Position,
		TabIDs:     op.TabIDs,
		Name:       op.Name,
		After:      op.After,
		SizeW:      op.SizeW,
		SizeH:      op.SizeH,
		HeightMode: op.HeightMode,
	}
}

func (h controlRPCHandler) handleBoardUpdate(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardUpdateParams(in.Params)
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
	ops := make([]boardpkg.Op, 0, len(req.Ops))
	for _, op := range req.Ops {
		ops = append(ops, boardOpFromParam(op))
	}
	snapshot, err := store.ApplyOps(req.SessionKey, ops)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if len(ops) > 0 {
		emitBoardChanged(snapshot, "")
	}
	return nostruntime.ControlRPCResult{Result: snapshot}, true, nil
}

func (h controlRPCHandler) handleBoardWidgetPut(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardWidgetPutParams(in.Params)
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
	params := boardpkg.PutParams{
		SessionKey: req.SessionKey,
		Name:       req.Name,
		Title:      req.Title,
		Content: boardpkg.PutContent{
			Kind:       req.Content.Kind,
			HTML:       req.Content.HTML,
			PluginKind: req.Content.PluginKind,
			Props:      req.Content.Props,
		},
		Presentation: req.Presentation,
		HeightMode:   req.HeightMode,
	}
	if req.Placement != nil {
		params.Placement = &boardpkg.PutPlacement{
			TabID: req.Placement.TabID,
			Size:  req.Placement.Size,
			After: req.Placement.After,
		}
	}
	if req.Declared != nil {
		params.Declared = &boardpkg.Declared{
			NetOrigins: req.Declared.NetOrigins,
			Tools:      req.Declared.Tools,
		}
	}
	if req.Content.Kind == boardpkg.ContentKindMcpApp {
		// Pin the descriptor from the active MCP-App view. Interactivity is
		// derived from the source view and gated behind the standard grant
		// flow via the mcp.app.interact capability declaration.
		registry, err := h.mcpAppViews()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		view, err := registry.Resolve(req.SessionKey, req.Content.ViewID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		params.Content.McpApp = &boardpkg.McpAppDescriptor{
			ServerName:    view.ServerName,
			ToolName:      view.ToolName,
			UIResourceURI: view.UIResourceURI,
			ToolCallID:    view.ToolCallID,
		}
		params.Content.Interactive = !view.ReadOnly
		if params.Content.Interactive {
			params.Declared = &boardpkg.Declared{Tools: []string{boardpkg.McpAppInteractCapability}}
		} else {
			params.Declared = nil
		}
	}
	snapshot, err := store.PutWidget(params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	emitBoardChanged(snapshot, req.Name)
	return nostruntime.ControlRPCResult{Result: snapshot}, true, nil
}

func (h controlRPCHandler) handleBoardWidgetGrant(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardWidgetGrantParams(in.Params)
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
	snapshot, err := store.Grant(req.SessionKey, req.Name, req.Decision, req.Revision, req.InstanceID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	emitBoardChanged(snapshot, "")
	return nostruntime.ControlRPCResult{Result: snapshot}, true, nil
}

func (h controlRPCHandler) handleBoardEvent(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeBoardEventParams(in.Params)
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
	if h.deps.boardNotices == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board surface is not available")
	}
	sessionKey, widget := req.SessionKey, req.Widget
	if req.Ticket != "" {
		// Ticket variant: the view ticket alone proves the widget identity.
		view, err := store.ResolveViewTicket(req.Ticket)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		sessionKey, widget = view.SessionKey, view.Name
	} else if !store.HasWidget(sessionKey, widget) {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("board widget not found: %s", widget)
	}
	notice, ok, err := h.deps.boardNotices.Render(sessionKey, widget, req.Payload, time.Now())
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	appended := false
	if ok {
		// Metiq deviation: notices reach the agent as active-run steering at
		// the next model boundary. When no run is active for the session the
		// event is acknowledged but not appended.
		if h.deps.steeringMailboxes != nil {
			if mailbox := h.deps.steeringMailboxes.GetIfExists(sessionKey); mailbox != nil {
				appended = mailbox.Enqueue(autoreply.SteeringMessage{
					Text:      notice,
					SenderID:  "board:" + widget,
					CreatedAt: time.Now().Unix(),
					Source:    "board",
					Priority:  autoreply.SteeringPriorityNormal,
				})
			}
		}
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "appended": appended}}, true, nil
}
