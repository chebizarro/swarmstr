// ui.html is generated from the ordered fragments under src/.  Run
// `go generate ./internal/webui` (or `go run metiq/scripts/build-webui`)
// after editing any fragment; TestUIHTMLIsAssembledFromSources enforces
// that the committed artifact never drifts from its sources.
//
//go:generate go run metiq/scripts/build-webui

package webui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// UISourceFiles is the ordered manifest of fragments, relative to the src/
// directory, that are concatenated verbatim to produce ui.html.
//
// The JavaScript fragments are slices of a single IIFE scope: state.js opens
// it, app.js closes it, and everything in between contributes hoisted
// function declarations (plus the module's own top-level statements in their
// original execution order), so concatenation order is load-bearing for the
// non-declaration statements in state.js, navigation.js and app.js.
var UISourceFiles = []string{
	"page-head.html",       // doctype, <head>, CDN stylesheet, <style>
	"styles.css",           // cyberwave theme CSS
	"layout.html",          // </style>, body markup, component slots, CDN scripts, <script>
	"js/state.js",          // template consts, IIFE open, DOM refs, state, I18N
	"js/chat.js",           // composer, markdown, messages, tool cards, history rendering
	"js/views.js",          // management views (dashboard/agents/channels/files/sharing/…)
	"js/controllers.js",    // run controls, sidebar toggle, thinking/typing, streaming bubble
	"js/gateway-client.js", // sendFrame/callMethod, WS event dispatcher, connect/reconnect
	"js/sidebar.js",        // grouped sidebar loaders, session history, session switching
	"js/approvals.js",      // exec/plugin approval queue, reconciliation, resolution
	"js/navigation.js",     // tab switching, new-session, abort/clear/compact actions
	"js/app.js",            // sendMessage, event listener wiring, boot
	"page-foot.html",       // </script></body></html>
}

// AssembleUI concatenates the manifest fragments found in srcDir into the
// full ui.html document and returns it.
func AssembleUI(srcDir string) ([]byte, error) {
	var buf bytes.Buffer
	for _, name := range UISourceFiles {
		data, err := os.ReadFile(filepath.Join(srcDir, filepath.FromSlash(name)))
		if err != nil {
			return nil, fmt.Errorf("assemble ui: %w", err)
		}
		buf.Write(data)
	}
	return buf.Bytes(), nil
}
