package main

import (
	"strings"
	"testing"

	"metiq/internal/autoreply"
	boardpkg "metiq/internal/gateway/board"
	mcpapppkg "metiq/internal/gateway/mcpapp"
	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newMcpAppTestHandler() (controlRPCHandler, *mcpapppkg.Registry, *autoreply.SteeringMailboxRegistry) {
	registry := mcpapppkg.NewRegistry()
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	h := newControlRPCHandler(controlRPCDeps{
		boardStore:        boardpkg.NewStore(),
		boardNotices:      boardpkg.NewNoticeDeduper(),
		mcpAppViews:       registry,
		steeringMailboxes: mailboxes,
		configState:       newRuntimeConfigStore(state.ConfigDoc{}),
	})
	return h, registry, mailboxes
}

func mintTestView(registry *mcpapppkg.Registry, sessionKey string) mcpapppkg.View {
	view, _ := registry.ObserveToolResult(sessionKey, "srv", "show_chart", "call1", map[string]any{"q": "x"}, &mcpsdk.CallToolResult{Content: []mcpsdk.Content{
		&mcpsdk.TextContent{Text: "rendered"},
		&mcpsdk.EmbeddedResource{Resource: &mcpsdk.ResourceContents{URI: "ui://srv/app", MIMEType: "text/html", Text: "<html>app</html>"}},
	}})
	return view
}

func TestMcpAppViewLifecycle(t *testing.T) {
	h, registry, _ := newMcpAppTestHandler()
	view := mintTestView(registry, "sess")

	result, err := workspaceSurfaceCall(t, h, methods.MethodMcpAppView, `{"sessionKey":"sess","viewId":"`+view.ViewID+`"}`)
	if err != nil {
		t.Fatalf("mcp.app.view: %v", err)
	}
	payload := result.Result.(map[string]any)
	if payload["html"] != "<html>app</html>" || payload["messageSupported"] != true {
		t.Fatalf("unexpected view payload: %#v", payload)
	}
	if payload["toolResult"] != "rendered" || payload["uiResourceUri"] != "ui://srv/app" {
		t.Fatalf("unexpected view metadata: %#v", payload)
	}

	// Unknown/expired/foreign views resolve identically.
	for _, params := range []string{
		`{"sessionKey":"sess","viewId":"view_nope"}`,
		`{"sessionKey":"other","viewId":"` + view.ViewID + `"}`,
	} {
		if _, err := workspaceSurfaceCall(t, h, methods.MethodMcpAppView, params); err == nil || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("expected expired error for %s, got %v", params, err)
		}
	}

	// Manager-backed operations fail cleanly without an MCP manager.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodMcpAppListTools, `{"sessionKey":"sess","viewId":"`+view.ViewID+`"}`); err == nil || !strings.Contains(err.Error(), "manager is not available") {
		t.Fatalf("expected manager unavailable, got %v", err)
	}
	if _, err := workspaceSurfaceCall(t, h, methods.MethodMcpAppCallTool, `{"sessionKey":"sess","viewId":"`+view.ViewID+`","toolName":"echo"}`); err == nil || !strings.Contains(err.Error(), "manager is not available") {
		t.Fatalf("expected manager unavailable, got %v", err)
	}

	// Params validation.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodMcpAppCallTool, `{"sessionKey":"sess","viewId":"`+view.ViewID+`"}`); err == nil || !strings.Contains(err.Error(), "toolName") {
		t.Fatalf("expected toolName error, got %v", err)
	}
	if _, err := workspaceSurfaceCall(t, h, methods.MethodMcpAppReadResource, `{"sessionKey":"sess","viewId":"`+view.ViewID+`"}`); err == nil || !strings.Contains(err.Error(), "uri") {
		t.Fatalf("expected uri error, got %v", err)
	}
}

func TestBoardMcpAppWidgetLifecycle(t *testing.T) {
	h, registry, _ := newMcpAppTestHandler()
	source := mintTestView(registry, "sess")

	// Pin the view as a board widget: interactivity is declared and pending.
	result, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetPut, `{"sessionKey":"sess","name":"app","content":{"kind":"mcp-app","viewId":"`+source.ViewID+`"}}`)
	if err != nil {
		t.Fatalf("board.widget.put mcp-app: %v", err)
	}
	snap := result.Result.(boardpkg.Snapshot)
	widget := snap.Widgets[0]
	if widget.ContentKind != boardpkg.ContentKindMcpApp || widget.GrantState != boardpkg.GrantPending {
		t.Fatalf("unexpected mcp-app widget: %+v", widget)
	}
	if widget.Declared == nil || len(widget.Declared.Tools) != 1 || widget.Declared.Tools[0] != boardpkg.McpAppInteractCapability {
		t.Fatalf("unexpected declared capabilities: %+v", widget.Declared)
	}

	appViewParams := `{"sessionKey":"sess","name":"app","revision":` + jsonInt(widget.Revision) + `,"instanceId":"` + widget.InstanceID + `"}`

	// Before the grant resolves, minted views are read-only.
	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardWidgetAppView, appViewParams)
	if err != nil {
		t.Fatalf("board.widget.appView pending: %v", err)
	}
	viewID := result.Result.(map[string]any)["viewId"].(string)
	if minted, err := registry.Resolve("sess", viewID); err != nil || !minted.ReadOnly {
		t.Fatalf("expected read-only pending view: %+v err=%v", minted, err)
	}

	// Granting the interact capability makes re-minted views interactive.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetGrant, `{"sessionKey":"sess","name":"app","decision":"granted","revision":`+jsonInt(widget.Revision)+`,"instanceId":"`+widget.InstanceID+`"}`); err != nil {
		t.Fatalf("grant: %v", err)
	}
	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardWidgetAppView, appViewParams)
	if err != nil {
		t.Fatalf("board.widget.appView granted: %v", err)
	}
	viewID = result.Result.(map[string]any)["viewId"].(string)
	minted, err := registry.Resolve("sess", viewID)
	if err != nil || minted.ReadOnly {
		t.Fatalf("expected interactive view: %+v err=%v", minted, err)
	}
	if minted.ServerName != "srv" || minted.ToolName != "show_chart" || minted.UIResourceURI != "ui://srv/app" {
		t.Fatalf("descriptor not pinned: %+v", minted)
	}

	// Stale revision or instance is rejected.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetAppView, `{"sessionKey":"sess","name":"app","revision":99,"instanceId":"`+widget.InstanceID+`"}`); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected stale revision rejection, got %v", err)
	}

	// Pinning an unknown or expired source view fails.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetPut, `{"sessionKey":"sess","name":"app2","content":{"kind":"mcp-app","viewId":"view_nope"}}`); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired source view, got %v", err)
	}

	// canvas-doc remains a rejected deferred source.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetPut, `{"sessionKey":"sess","name":"doc","content":{"kind":"canvas-doc","docId":"d1"}}`); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected canvas-doc rejection, got %v", err)
	}
}

func TestMcpAppUpdateModelContextSteering(t *testing.T) {
	h, registry, mailboxes := newMcpAppTestHandler()
	view := mintTestView(registry, "sess")

	// Without an active-run mailbox the update is acknowledged, not appended.
	result, err := workspaceSurfaceCall(t, h, methods.MethodMcpAppUpdateModelContext, `{"sessionKey":"sess","viewId":"`+view.ViewID+`","content":{"selection":"row-3"}}`)
	if err != nil {
		t.Fatalf("updateModelContext: %v", err)
	}
	if payload := result.Result.(map[string]any); payload["ok"] != true || payload["appended"] != false {
		t.Fatalf("unexpected result: %#v", payload)
	}

	// With a live mailbox the widget context lands as steering.
	mailbox := mailboxes.Get("sess")
	result, err = workspaceSurfaceCall(t, h, methods.MethodMcpAppUpdateModelContext, `{"sessionKey":"sess","viewId":"`+view.ViewID+`","content":{"selection":"row-4"}}`)
	if err != nil {
		t.Fatalf("updateModelContext live: %v", err)
	}
	if payload := result.Result.(map[string]any); payload["appended"] != true {
		t.Fatalf("expected appended=true: %#v", payload)
	}
	queued := mailbox.Drain()
	if len(queued) != 1 || !strings.Contains(queued[0].Text, "row-4") || !strings.Contains(queued[0].Text, "mcp-app srv/show_chart") {
		t.Fatalf("unexpected steering: %#v", queued)
	}
}
