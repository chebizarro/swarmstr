package main

import (
	"context"
	"fmt"
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

// newBoardTicketTestHandler mirrors newBoardTestHandler but adds a config
// store: board.data.read re-enters the full control RPC dispatch, which reads
// runtime config at the top of Handle.
func newBoardTicketTestHandler() controlRPCHandler {
	return newControlRPCHandler(controlRPCDeps{
		boardStore:        boardpkg.NewStore(),
		boardNotices:      boardpkg.NewNoticeDeduper(),
		steeringMailboxes: autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize),
		configState:       newRuntimeConfigStore(state.ConfigDoc{}),
	})
}

// mintGrantedTicket puts an html widget declaring tools, grants it, and
// returns the view ticket minted by board.get.
func mintGrantedTicket(t *testing.T, h controlRPCHandler, name string, declaredJSON string) string {
	t.Helper()
	put := `{"sessionKey":"sess","name":"` + name + `","content":{"kind":"html","html":"<p>w</p>"}`
	if declaredJSON != "" {
		put += `,"declared":` + declaredJSON
	}
	put += `}`
	result, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetPut, put)
	if err != nil {
		t.Fatalf("board.widget.put: %v", err)
	}
	snap := result.Result.(boardpkg.Snapshot)
	var widget boardpkg.Widget
	for _, w := range snap.Widgets {
		if w.Name == name {
			widget = w
		}
	}
	if declaredJSON != "" {
		if widget.GrantState != boardpkg.GrantPending {
			t.Fatalf("expected pending widget: %+v", widget)
		}
		if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetGrant,
			`{"sessionKey":"sess","name":"`+name+`","decision":"granted","revision":`+jsonInt(widget.Revision)+`,"instanceId":"`+widget.InstanceID+`"}`); err != nil {
			t.Fatalf("board.widget.grant: %v", err)
		}
	}
	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardGet, `{"sessionKey":"sess"}`)
	if err != nil {
		t.Fatalf("board.get: %v", err)
	}
	for _, w := range result.Result.(boardpkg.Snapshot).Widgets {
		if w.Name == name {
			if w.ViewTicket == "" {
				t.Fatalf("board.get minted no ticket for %s: %+v", name, w)
			}
			return w.ViewTicket
		}
	}
	t.Fatalf("widget %s missing from board.get", name)
	return ""
}

func TestBoardTicketLifecycle(t *testing.T) {
	h := newBoardTicketTestHandler()
	ticket := mintGrantedTicket(t, h, "chart", `{"tools":["prompt","health"]}`)

	// prompt granted -> no confirmation required.
	result, err := workspaceSurfaceCall(t, h, methods.MethodBoardPromptAuthorize, `{"ticket":"`+ticket+`"}`)
	if err != nil {
		t.Fatalf("board.prompt.authorize: %v", err)
	}
	if confirmed := result.Result.(map[string]any)["confirmationRequired"]; confirmed != false {
		t.Fatalf("expected confirmationRequired=false: %#v", result.Result)
	}

	// Granted core data binding dispatches internally.
	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardDataRead, `{"ticket":"`+ticket+`","bindingId":"health"}`)
	if err != nil {
		t.Fatalf("board.data.read health: %v", err)
	}
	if ok := result.Result.(map[string]any)["ok"]; ok != true {
		t.Fatalf("unexpected health result: %#v", result.Result)
	}

	// Ungranted binding fails closed even though it is a core binding.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardDataRead, `{"ticket":"`+ticket+`","bindingId":"sessions.list"}`); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("expected not-granted error, got %v", err)
	}

	// Ungranted cron trigger fails closed with the composed capability id.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardAction, `{"ticket":"`+ticket+`","action":"cron.trigger","jobId":"job1"}`); err == nil || !strings.Contains(err.Error(), "cron.trigger:job1") {
		t.Fatalf("expected cron capability error, got %v", err)
	}

	// Ticket variant of board.event resolves the widget identity.
	result, err = workspaceSurfaceCall(t, h, methods.MethodBoardEvent, `{"ticket":"`+ticket+`","payload":{"clicked":true}}`)
	if err != nil {
		t.Fatalf("board.event ticket variant: %v", err)
	}
	if ok := result.Result.(map[string]any)["ok"]; ok != true {
		t.Fatalf("unexpected board.event result: %#v", result.Result)
	}
	// Ticket plus explicit identity is rejected.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardEvent, `{"ticket":"`+ticket+`","sessionKey":"sess","widget":"chart","payload":1}`); err == nil {
		t.Fatal("expected mutually-exclusive board.event params error")
	}
}

func TestBoardTicketUngatedWidgetRequiresConfirmation(t *testing.T) {
	h := newBoardTicketTestHandler()
	ticket := mintGrantedTicket(t, h, "plain", "")

	result, err := workspaceSurfaceCall(t, h, methods.MethodBoardPromptAuthorize, `{"ticket":"`+ticket+`"}`)
	if err != nil {
		t.Fatalf("board.prompt.authorize: %v", err)
	}
	if confirmed := result.Result.(map[string]any)["confirmationRequired"]; confirmed != true {
		t.Fatalf("expected confirmationRequired=true: %#v", result.Result)
	}
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardDataRead, `{"ticket":"`+ticket+`","bindingId":"health"}`); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("expected not-granted error, got %v", err)
	}
}

func TestBoardTicketStaleAfterRePut(t *testing.T) {
	h := newBoardTicketTestHandler()
	ticket := mintGrantedTicket(t, h, "chart", `{"tools":["prompt"]}`)

	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardWidgetPut, `{"sessionKey":"sess","name":"chart","content":{"kind":"html","html":"<p>new bytes</p>"}}`); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardPromptAuthorize, `{"ticket":"`+ticket+`"}`); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale ticket error, got %v", err)
	}
}

func TestBoardActionPluginVerbNotAllowed(t *testing.T) {
	h := newBoardTicketTestHandler()
	ticket := mintGrantedTicket(t, h, "chart", `{"tools":["custom.verb"]}`)

	// Granted, but no plugin dashboard verb registry exists (Metiq deviation).
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardAction, `{"ticket":"`+ticket+`","action":"custom.verb"}`); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected not-allowed error, got %v", err)
	}
	// Forged tickets are rejected before any capability logic runs.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardAction, `{"ticket":"v1.bogus.bogus","action":"custom.verb"}`); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected invalid ticket error, got %v", err)
	}
	// jobId without cron.trigger action is a params error.
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardAction, `{"ticket":"`+ticket+`","action":"custom.verb","jobId":"x"}`); err == nil {
		t.Fatal("expected jobId/action mismatch error")
	}
}

// ── Plugin data-binding / action-verb board extension (swarmstr-qmxu.3) ──────

type fakeManifestSource struct {
	manifests map[string]sdk.Manifest
}

func (f *fakeManifestSource) PluginIDs() []string {
	ids := make([]string, 0, len(f.manifests))
	for id := range f.manifests {
		ids = append(ids, id)
	}
	return ids
}

func (f *fakeManifestSource) PluginManifest(id string) (sdk.Manifest, error) {
	mf, ok := f.manifests[id]
	if !ok {
		return sdk.Manifest{}, fmt.Errorf("not found: %s", id)
	}
	return mf, nil
}

// recordingDispatcher captures InvokeSurface calls and returns a canned value.
type recordingDispatcher struct {
	calls  []dispatchCall
	result any
	err    error
}

type dispatchCall struct {
	pluginID string
	verb     string
	args     map[string]any
	meta     map[string]any
}

func (d *recordingDispatcher) InvokeSurface(_ context.Context, pluginID, verb string, args, meta map[string]any) (any, error) {
	d.calls = append(d.calls, dispatchCall{pluginID: pluginID, verb: verb, args: args, meta: meta})
	if d.err != nil {
		return nil, d.err
	}
	return d.result, nil
}

// newBoardPluginSurfaceHandler builds a board ticket handler wired with a
// plugin-surface registry (one plugin "dash" declaring a data binding and an
// action verb) and a recording dispatcher.
func newBoardPluginSurfaceHandler(t *testing.T) (controlRPCHandler, *recordingDispatcher) {
	t.Helper()
	src := &fakeManifestSource{manifests: map[string]sdk.Manifest{
		"dash": {ID: "dash", Version: "1.0.0", Surfaces: &pluginmanifest.SurfaceContributions{
			DataBindings: []pluginmanifest.DataBindingSurface{{ID: "dash.stats"}},
			ActionVerbs:  []pluginmanifest.ActionVerbSurface{{ID: "dash.refresh"}},
		}},
	}}
	registry := pluginsurface.New(src)
	dispatch := &recordingDispatcher{result: map[string]any{"value": 42}}
	h := newControlRPCHandler(controlRPCDeps{
		boardStore:        boardpkg.NewStore(),
		boardNotices:      boardpkg.NewNoticeDeduper(),
		steeringMailboxes: autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize),
		configState:       newRuntimeConfigStore(state.ConfigDoc{}),
		pluginSurface:     registry,
		surfaceDispatch:   dispatch,
	})
	return h, dispatch
}

// TestBoardDataReadPluginBindingGranted proves a granted plugin data binding
// dispatches into the owning plugin runtime and returns its result.
func TestBoardDataReadPluginBindingGranted(t *testing.T) {
	h, dispatch := newBoardPluginSurfaceHandler(t)
	ticket := mintGrantedTicket(t, h, "chart", `{"tools":["dash.stats"]}`)

	result, err := workspaceSurfaceCall(t, h, methods.MethodBoardDataRead, `{"ticket":"`+ticket+`","bindingId":"dash.stats","params":{"range":"7d"}}`)
	if err != nil {
		t.Fatalf("board.data.read plugin binding: %v", err)
	}
	payload := result.Result.(map[string]any)
	if payload["pluginId"] != "dash" || payload["bindingId"] != "dash.stats" {
		t.Fatalf("unexpected binding result: %#v", payload)
	}
	if len(dispatch.calls) != 1 || dispatch.calls[0].pluginID != "dash" || dispatch.calls[0].verb != "dash.stats" {
		t.Fatalf("dispatch not routed to plugin: %#v", dispatch.calls)
	}
	if dispatch.calls[0].args["range"] != "7d" {
		t.Fatalf("params not forwarded: %#v", dispatch.calls[0].args)
	}
}

// TestBoardActionPluginVerbGranted proves a granted plugin action verb
// dispatches into the plugin runtime (may mutate).
func TestBoardActionPluginVerbGranted(t *testing.T) {
	h, dispatch := newBoardPluginSurfaceHandler(t)
	ticket := mintGrantedTicket(t, h, "chart", `{"tools":["dash.refresh"]}`)

	result, err := workspaceSurfaceCall(t, h, methods.MethodBoardAction, `{"ticket":"`+ticket+`","action":"dash.refresh"}`)
	if err != nil {
		t.Fatalf("board.action plugin verb: %v", err)
	}
	if payload := result.Result.(map[string]any); payload["pluginId"] != "dash" || payload["action"] != "dash.refresh" {
		t.Fatalf("unexpected action result: %#v", payload)
	}
	if len(dispatch.calls) != 1 || dispatch.calls[0].verb != "dash.refresh" {
		t.Fatalf("dispatch not routed to plugin: %#v", dispatch.calls)
	}
}

// TestBoardPluginBindingUngrantedFailsClosed proves a plugin binding that the
// widget did NOT declare/grant is rejected before any dispatch.
func TestBoardPluginBindingUngrantedFailsClosed(t *testing.T) {
	h, dispatch := newBoardPluginSurfaceHandler(t)
	// Widget grants a different tool, not the plugin binding.
	ticket := mintGrantedTicket(t, h, "chart", `{"tools":["health"]}`)

	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardDataRead, `{"ticket":"`+ticket+`","bindingId":"dash.stats"}`); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("expected not-granted for ungranted plugin binding, got %v", err)
	}
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardAction, `{"ticket":"`+ticket+`","action":"dash.refresh"}`); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("expected not-granted for ungranted plugin verb, got %v", err)
	}
	if len(dispatch.calls) != 0 {
		t.Fatalf("dispatch must not run for ungranted ids: %#v", dispatch.calls)
	}
}

// TestBoardPluginUnknownIDFailsClosed proves that even a GRANTED id that no
// plugin declares never dispatches (registry fails closed).
func TestBoardPluginUnknownIDFailsClosed(t *testing.T) {
	h, dispatch := newBoardPluginSurfaceHandler(t)
	// Widget declares+grants an id no plugin owns.
	ticket := mintGrantedTicket(t, h, "chart", `{"tools":["ghost.binding","ghost.verb"]}`)

	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardDataRead, `{"ticket":"`+ticket+`","bindingId":"ghost.binding"}`); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected not-allowed for unknown granted binding, got %v", err)
	}
	if _, err := workspaceSurfaceCall(t, h, methods.MethodBoardAction, `{"ticket":"`+ticket+`","action":"ghost.verb"}`); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected not-allowed for unknown granted verb, got %v", err)
	}
	if len(dispatch.calls) != 0 {
		t.Fatalf("dispatch must not run for unknown ids: %#v", dispatch.calls)
	}
}
