package main

import (
	"strings"
	"testing"

	"metiq/internal/autoreply"
	boardpkg "metiq/internal/gateway/board"
	"metiq/internal/gateway/methods"
	pluginmanifest "metiq/internal/plugins/manifest"
	"metiq/internal/plugins/sdk"
	pluginsurface "metiq/internal/plugins/surface"
	"metiq/internal/store/state"
)

// newPluginSurfaceV4Handler wires a handler with a plugin-surface registry
// (plugin "dash" declaring a board widget + a session-action verb) plus a
// board store and recording dispatcher, for the qmxu.4 gateway methods.
func newPluginSurfaceV4Handler(t *testing.T) (controlRPCHandler, *recordingDispatcher) {
	t.Helper()
	src := &fakeManifestSource{manifests: map[string]sdk.Manifest{
		"dash": {ID: "dash", Version: "1.0.0", Surfaces: &pluginmanifest.SurfaceContributions{
			BoardWidgets: []pluginmanifest.BoardWidgetSurface{
				{ID: "dash.overview", Title: "Overview", Presentation: "card"},
			},
			SessionActions: []pluginmanifest.SessionActionSurface{
				{ID: "dash.pin", MutatesSession: true},
			},
		}},
	}}
	dispatch := &recordingDispatcher{result: map[string]any{"pinned": true}}
	h := newControlRPCHandler(controlRPCDeps{
		boardStore:        boardpkg.NewStore(),
		boardNotices:      boardpkg.NewNoticeDeduper(),
		steeringMailboxes: autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize),
		configState:       newRuntimeConfigStore(state.ConfigDoc{}),
		pluginSurface:     pluginsurface.New(src),
		surfaceDispatch:   dispatch,
	})
	return h, dispatch
}

func TestPluginsUIDescriptorsHandler(t *testing.T) {
	h, _ := newPluginSurfaceV4Handler(t)
	res, err := pluginSurfaceCall(t, h, methods.MethodPluginsUIDescriptors, `{}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("plugins.uiDescriptors: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["count"].(int) != 1 {
		t.Fatalf("expected 1 descriptor: %#v", payload)
	}
	descs := payload["descriptors"].([]map[string]any)
	if descs[0]["pluginId"] != "dash" || descs[0]["id"] != "dash.overview" {
		t.Fatalf("unexpected descriptor: %#v", descs[0])
	}
	// pluginId filter that matches nothing yields an empty set.
	res, err = pluginSurfaceCall(t, h, methods.MethodPluginsUIDescriptors, `{"pluginId":"ghost"}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("filtered uiDescriptors: %v", err)
	}
	if res.Result.(map[string]any)["count"].(int) != 0 {
		t.Fatalf("expected 0 descriptors for unknown plugin")
	}
}

func TestPluginsSessionActionGrantedDispatch(t *testing.T) {
	h, dispatch := newPluginSurfaceV4Handler(t)
	ticket := mintGrantedTicket(t, h, "panel", `{"tools":["dash.pin"]}`)

	res, err := pluginSurfaceCall(t, h, methods.MethodPluginsSessionAction, `{"ticket":"`+ticket+`","verb":"dash.pin","params":{"item":"row-3"}}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("plugins.sessionAction: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != true || payload["pluginId"] != "dash" || payload["verb"] != "dash.pin" || payload["mutatesSession"] != true {
		t.Fatalf("unexpected sessionAction result: %#v", payload)
	}
	if len(dispatch.calls) != 1 {
		t.Fatalf("expected one dispatch, got %#v", dispatch.calls)
	}
	call := dispatch.calls[0]
	if call.pluginID != "dash" || call.verb != "dash.pin" {
		t.Fatalf("dispatch not routed to plugin: %#v", call)
	}
	if call.args["item"] != "row-3" {
		t.Fatalf("params not forwarded: %#v", call.args)
	}
	if call.meta["session_id"] != "sess" || call.meta["surface_kind"] != "session_action" {
		t.Fatalf("session context not passed: %#v", call.meta)
	}
}

func TestPluginsSessionActionUngrantedFailsClosed(t *testing.T) {
	h, dispatch := newPluginSurfaceV4Handler(t)
	// Widget grants a different tool, not the session verb.
	ticket := mintGrantedTicket(t, h, "panel", `{"tools":["health"]}`)
	if _, err := pluginSurfaceCall(t, h, methods.MethodPluginsSessionAction, `{"ticket":"`+ticket+`","verb":"dash.pin"}`, state.ConfigDoc{}); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("expected not-granted, got %v", err)
	}
	// Granted id that no plugin declares as a session action → not allowed.
	ticket2 := mintGrantedTicket(t, h, "panel2", `{"tools":["ghost.session"]}`)
	if _, err := pluginSurfaceCall(t, h, methods.MethodPluginsSessionAction, `{"ticket":"`+ticket2+`","verb":"ghost.session"}`, state.ConfigDoc{}); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected not-allowed, got %v", err)
	}
	// A forged ticket is rejected before any registry logic.
	if _, err := pluginSurfaceCall(t, h, methods.MethodPluginsSessionAction, `{"ticket":"v1.bogus.bogus","verb":"dash.pin"}`, state.ConfigDoc{}); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected invalid ticket, got %v", err)
	}
	if len(dispatch.calls) != 0 {
		t.Fatalf("dispatch must not run when fail-closed: %#v", dispatch.calls)
	}
}

func TestPluginSurfaceRefreshHandler(t *testing.T) {
	h, _ := newPluginSurfaceV4Handler(t)
	// All-plugins scope (no plugin manager wired in test → reloaded=false).
	res, err := pluginSurfaceCall(t, h, methods.MethodPluginSurfaceRefresh, `{}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("plugin.surface.refresh: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["ok"] != true || payload["scope"] != "all" || payload["widgets"].(int) != 1 {
		t.Fatalf("unexpected refresh result: %#v", payload)
	}
	// Single-plugin scope for a loaded plugin.
	res, err = pluginSurfaceCall(t, h, methods.MethodPluginSurfaceRefresh, `{"pluginId":"dash"}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("scoped refresh: %v", err)
	}
	if res.Result.(map[string]any)["scope"] != "dash" {
		t.Fatalf("expected scope=dash: %#v", res.Result)
	}
	// Single-plugin scope for an unknown plugin fails.
	if _, err := pluginSurfaceCall(t, h, methods.MethodPluginSurfaceRefresh, `{"pluginId":"ghost"}`, state.ConfigDoc{}); err == nil || !strings.Contains(err.Error(), "not loaded") {
		t.Fatalf("expected not-loaded error, got %v", err)
	}
}
