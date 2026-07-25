package main

import (
	"context"
	"fmt"
	"time"

	"metiq/internal/autoreply"
	"metiq/internal/gateway/mcpapp"
	"metiq/internal/gateway/methods"
	mcppkg "metiq/internal/mcp"
	nostruntime "metiq/internal/nostr/runtime"
)

// mcp.app.* RPC handlers (defer-bucket slice swarmstr-fokh.7). Views are
// minted by the internal/gateway/mcpapp registry when an agent MCP tool call
// returns a ui:// resource (wired via mcppkg.ToolCallObserver in main.go and
// announced with the mcp.app.viewCreated WS event). Every method resolves
// {sessionKey, viewId} against the registry first; operations are scoped to
// the view's originating server. Metiq deviations: callTool is limited to
// tools currently advertised by the owning server (OpenClaw additionally
// narrows to app-declared tool names); updateModelContext delivers widget
// context as active-run steering at the next model boundary.

func (h controlRPCHandler) mcpAppViews() (*mcpapp.Registry, error) {
	if h.deps.mcpAppViews == nil {
		return nil, fmt.Errorf("mcp app surface is not available")
	}
	return h.deps.mcpAppViews, nil
}

func (h controlRPCHandler) mcpAppManager() (*mcppkg.Manager, error) {
	if h.deps.mcpOps == nil || h.deps.mcpOps.manager == nil || *h.deps.mcpOps.manager == nil {
		return nil, fmt.Errorf("mcp manager is not available")
	}
	return *h.deps.mcpOps.manager, nil
}

func (h controlRPCHandler) resolveMcpAppView(sessionKey, viewID string) (mcpapp.View, error) {
	registry, err := h.mcpAppViews()
	if err != nil {
		return mcpapp.View{}, err
	}
	return registry.Resolve(sessionKey, viewID)
}

func (h controlRPCHandler) handleMcpAppView(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeMcpAppViewParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := h.resolveMcpAppView(req.SessionKey, req.ViewID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	html := view.HTML
	if html == "" {
		// The tool result linked the app document instead of embedding it;
		// fetch and cache it on first render.
		manager, err := h.mcpAppManager()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		resource, err := manager.ReadResource(ctx, view.ServerName, view.UIResourceURI)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		for _, contents := range resource.Contents {
			if contents != nil && contents.Text != "" {
				html = contents.Text
				break
			}
		}
		if html == "" {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp app resource has no renderable content: %s", view.UIResourceURI)
		}
		if registry, err := h.mcpAppViews(); err == nil {
			registry.SetHTML(view.ViewID, html)
		}
	}
	interactive := !view.ReadOnly
	result := map[string]any{
		"html":                        html,
		"uiResourceUri":               view.UIResourceURI,
		"toolInput":                   view.ToolInput,
		"toolResult":                  view.ToolResult,
		"expiresAtMs":                 view.ExpiresAt.UnixMilli(),
		"messageSupported":            interactive,
		"updateModelContextSupported": interactive,
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleMcpAppListTools(_ context.Context, in nostruntime.ControlRPCInbound, method string) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeMcpAppListParams(in.Params, method)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := h.resolveMcpAppView(req.SessionKey, req.ViewID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	manager, err := h.mcpAppManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	tools := make([]map[string]any, 0)
	for _, tool := range manager.GetAllTools()[view.ServerName] {
		if tool == nil {
			continue
		}
		entry := map[string]any{"name": tool.Name}
		if tool.Description != "" {
			entry["description"] = tool.Description
		}
		if tool.InputSchema != nil {
			entry["inputSchema"] = mcppkg.ToolInputSchemaToMap(tool.InputSchema)
		}
		tools = append(tools, entry)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"tools": tools}}, true, nil
}

func (h controlRPCHandler) handleMcpAppListResources(ctx context.Context, in nostruntime.ControlRPCInbound, method string, templates bool) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeMcpAppListParams(in.Params, method)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := h.resolveMcpAppView(req.SessionKey, req.ViewID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	manager, err := h.mcpAppManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if templates {
		listing, err := manager.ListResourceTemplates(ctx, view.ServerName)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: listing}, true, nil
	}
	listing, err := manager.ListResources(ctx, view.ServerName)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: listing}, true, nil
}

func (h controlRPCHandler) handleMcpAppReadResource(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeMcpAppReadResourceParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := h.resolveMcpAppView(req.SessionKey, req.ViewID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	manager, err := h.mcpAppManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	resource, err := manager.ReadResource(ctx, view.ServerName, req.URI)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: resource}, true, nil
}

func (h controlRPCHandler) handleMcpAppCallTool(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeMcpAppCallToolParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := h.resolveMcpAppView(req.SessionKey, req.ViewID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if view.ReadOnly {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp app view is read-only")
	}
	manager, err := h.mcpAppManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	allowed := false
	for _, tool := range manager.GetAllTools()[view.ServerName] {
		if tool != nil && tool.Name == req.ToolName {
			allowed = true
			break
		}
	}
	if !allowed {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp app tool is not available on server %s: %s", view.ServerName, req.ToolName)
	}
	result, err := manager.CallTool(ctx, view.ServerName, req.ToolName, req.Arguments)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleMcpAppUpdateModelContext(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeMcpAppUpdateModelContextParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	view, err := h.resolveMcpAppView(req.SessionKey, req.ViewID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if view.ReadOnly {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp app view is read-only")
	}
	// Metiq deviation: widget model context reaches the agent as active-run
	// steering at the next model boundary, matching board.event delivery.
	appended := false
	if len(req.Content) > 0 && h.deps.steeringMailboxes != nil {
		if mailbox := h.deps.steeringMailboxes.GetIfExists(view.SessionKey); mailbox != nil {
			text := fmt.Sprintf("[mcp-app %s/%s] model context update: %s", view.ServerName, view.ToolName, string(req.Content))
			appended = mailbox.Enqueue(autoreply.SteeringMessage{
				Text:      text,
				SenderID:  "mcp-app:" + view.ViewID,
				CreatedAt: time.Now().Unix(),
				Source:    "mcp-app",
				Priority:  autoreply.SteeringPriorityNormal,
			})
		}
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "appended": appended}}, true, nil
}
