package webui

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	gatewayws "metiq/internal/gateway/ws"
)

// TestUIHTMLIsAssembledFromSources is the drift gate for the build+embed
// step: the committed ui.html must be byte-identical to the concatenation of
// the src/ fragments.  It keeps CI Go-only (no Node toolchain) while making
// direct edits to the generated artifact fail loudly.
func TestUIHTMLIsAssembledFromSources(t *testing.T) {
	built, err := AssembleUI("src")
	if err != nil {
		t.Fatalf("assemble ui from src/: %v", err)
	}
	disk, err := os.ReadFile("ui.html")
	if err != nil {
		t.Fatalf("read ui.html: %v", err)
	}
	if !bytes.Equal(built, disk) {
		t.Fatal("ui.html is stale: edit internal/webui/src/* and run `go generate ./internal/webui` (never edit ui.html directly)")
	}
}

// TestUISourceManifestFragmentsExist ensures every manifest entry resolves and
// that the load-bearing IIFE boundaries live where the manifest says they do.
func TestUISourceManifestFragmentsExist(t *testing.T) {
	for _, name := range UISourceFiles {
		if _, err := os.Stat("src/" + name); err != nil {
			t.Errorf("manifest fragment missing: %v", err)
		}
	}
	state, err := os.ReadFile("src/js/state.js")
	if err != nil {
		t.Fatalf("read state.js: %v", err)
	}
	if !strings.Contains(string(state), "(function () {") {
		t.Error("state.js must open the shared IIFE scope")
	}
	app, err := os.ReadFile("src/js/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	if !strings.Contains(string(app), "})();") {
		t.Error("app.js must close the shared IIFE scope")
	}
}

func TestUIOrchestrationSurfacesAreCatalogDriven(t *testing.T) {
	views, err := os.ReadFile("src/js/views.js")
	if err != nil {
		t.Fatalf("read views.js: %v", err)
	}
	surfaces, err := os.ReadFile("src/js/surfaces.js")
	if err != nil {
		t.Fatalf("read surfaces.js: %v", err)
	}
	gateway, err := os.ReadFile("src/js/gateway-client.js")
	if err != nil {
		t.Fatalf("read gateway-client.js: %v", err)
	}
	viewJS, surfaceJS, gatewayJS := string(views), string(surfaces), string(gateway)
	for _, method := range []string{
		"sessions.compaction.list", "sessions.compaction.get", "sessions.compaction.branch", "sessions.compaction.restore",
		"sessions.branches.list", "sessions.branches.switch", "sessions.rewind",
		"sessions.dispatch", "sessions.reclaim", "sessions.recover", "sessions.resolve",
	} {
		if !strings.Contains(viewJS, "gatewayMethodAdvertised('"+method+"')") {
			t.Errorf("session orchestration method %q is not feature-detected from hello descriptors", method)
		}
	}
	for _, confirmation := range []string{"Restore this checkpoint?", "Rewind before this user message?"} {
		if !strings.Contains(viewJS, confirmation) {
			t.Errorf("missing destructive history confirmation %q", confirmation)
		}
	}
	if !strings.Contains(viewJS, "Revision conflict:") || !strings.Contains(viewJS, "await showSessionHistoryPanel(grid, sid)") {
		t.Error("history mutations must surface revision conflicts and reload transcript/branch state")
	}
	for _, method := range []string{"tasks.list", "tasks.get", "tasks.doctor", "tasks.trace"} {
		if !strings.Contains(surfaceJS, "gatewayMethodAdvertised('"+method+"')") {
			t.Errorf("task inventory/detail method %q is not feature-detected", method)
		}
	}
	if !strings.Contains(surfaceJS, "handleTaskLifecycleEvent") || !strings.Contains(surfaceJS, "queueTaskInventoryRefresh") {
		t.Error("task activity panel is not wired for event-driven inventory refresh")
	}
	for _, event := range []string{"session.placement", "sessions.changed", "session.operation", "session.tool"} {
		if !strings.Contains(gatewayJS, "'"+event+"'") {
			t.Errorf("gateway client does not subscribe/dispatch orchestration event %q", event)
		}
	}
}

// pluginApprovalEvents are dispatched by the webui but not (yet) part of the
// gateway push-event catalog: the UI only subscribes to advertised events
// (wanted.filter(advertised.has)), so these subscriptions are inert until the
// gateway starts advertising them.  Keep them allowlisted rather than silently
// dropping the handlers.
var pluginApprovalEvents = map[string]struct{}{
	"plugin.approval.requested": {},
	"plugin.approval.resolved":  {},
}

// TestUIEventContractMatchesGatewayCatalog is the protocol-v4 event-handling
// parity gate: every event the UI subscribes to and every event the onEvent
// dispatcher handles must exist in the gateway's canonical push-event catalog
// (gatewayws.AllPushEvents), modulo the explicit plugin-approval allowlist.
func TestUIEventContractMatchesGatewayCatalog(t *testing.T) {
	raw, err := os.ReadFile("ui.html")
	if err != nil {
		t.Fatalf("read ui.html: %v", err)
	}
	html := string(raw)

	catalog := map[string]struct{}{}
	for _, name := range gatewayws.AllPushEvents {
		catalog[name] = struct{}{}
	}

	// Subscription list: the `wanted` array passed through the advertised
	// filter before events.subscribe.
	wantedRe := regexp.MustCompile(`const wanted = \[([^\]]+)\]`)
	m := wantedRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("ui.html no longer declares the `wanted` event subscription list")
	}
	var subscribed []string
	for _, quoted := range regexp.MustCompile(`'([a-z][a-z0-9._-]*)'`).FindAllStringSubmatch(m[1], -1) {
		subscribed = append(subscribed, quoted[1])
	}
	if len(subscribed) == 0 {
		t.Fatal("parsed no subscribed events from the wanted list")
	}
	for _, name := range subscribed {
		if _, ok := catalog[name]; ok {
			continue
		}
		if _, ok := pluginApprovalEvents[name]; ok {
			continue
		}
		t.Errorf("UI subscribes to %q which the gateway catalog does not push", name)
	}

	// The subscription must remain gated on the hello-advertised event set so
	// unknown names never reach events.subscribe.
	if !strings.Contains(html, "wanted.filter(name => advertised.has(name))") {
		t.Error("UI must filter the wanted subscription list by hello-advertised events")
	}

	// Dispatcher: every case label in the onEvent switch (all string case
	// labels in ui.html belong to it) must be a known push event.
	dispatched := map[string]struct{}{}
	for _, match := range regexp.MustCompile(`case '([a-z][a-z0-9._-]*)':`).FindAllStringSubmatch(html, -1) {
		dispatched[match[1]] = struct{}{}
	}
	if len(dispatched) == 0 {
		t.Fatal("parsed no dispatched events from the onEvent switch")
	}
	for name := range dispatched {
		if _, ok := catalog[name]; ok {
			continue
		}
		if _, ok := pluginApprovalEvents[name]; ok {
			continue
		}
		t.Errorf("onEvent dispatches %q which the gateway catalog does not push", name)
	}

	// Current-protocol essentials must stay wired: the chat state-union
	// stream, collaborator typing, and exec-approval reconciliation pair.
	for _, name := range []string{"chat", "agent.status", "session.typing", "chat.message", "tool.start", "tool.progress", "tool.result", "tool.error", "exec.approval.requested", "exec.approval.resolved", "node.invoke.progress", "config.updated"} {
		if _, ok := dispatched[name]; !ok {
			t.Errorf("onEvent no longer handles required event %q", name)
		}
	}
	for _, name := range []string{"chat", "exec.approval.requested", "exec.approval.resolved", "session.typing"} {
		found := false
		for _, sub := range subscribed {
			if sub == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("UI no longer subscribes to required event %q", name)
		}
	}
}
