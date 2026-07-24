# Web UI architecture

The Web UI is served as one embedded `ui.html` file, which keeps local
deployment a single artifact. As of the build-system modularization
(swarmstr-75hp), `ui.html` is **generated**: the sources live under
`internal/webui/src/` as ordered fragments and a pure-Go build step
concatenates them, so CI stays Go-only (no Node toolchain) and the committed
artifact never drifts from its sources.

## Build + embed pipeline

```
internal/webui/src/*  --(go generate ./internal/webui)-->  internal/webui/ui.html  --(go:embed)-->  binary
```

- `assemble.go` holds the ordered fragment manifest (`UISourceFiles`) and the
  `AssembleUI` concatenator shared by the builder and the tests.
- `scripts/build-webui` (also wired as `go generate ./internal/webui`)
  rewrites `ui.html`. The generated file is committed.
- `TestUIHTMLIsAssembledFromSources` fails when `ui.html` is edited directly
  or the fragments change without regeneration.
- `{{component "…"}}` slots and the `{{.WSPath}}`/`{{.Token}}` template
  variables pass through the build verbatim; `webui.go` still parses the
  embedded artifact as a Go html/template at init.

## Module boundaries under `src/`

All JS fragments share a single IIFE scope: `js/state.js` opens it,
`js/app.js` closes it. Fragments between them contribute hoisted function
declarations plus their own top-level statements in original execution order
(concatenation order is load-bearing — see the manifest in `assemble.go`).

- `page-head.html` / `styles.css` / `layout.html` / `page-foot.html`: document
  shell, cyberwave theme, body markup and component slots.
- `js/state.js`: DOM refs, mutable state, I18N tables.
- `js/chat.js`: Chat view — transcript rendering, markdown, streaming
  messages, `ToolCard` activity rendering, history-entry reconstruction,
  composer/attachment behavior.
- `js/views.js`: management views (dashboard, agents, channels, sessions,
  cron, nodes, MCP, skills, config, usage) plus the session files and sharing
  panels.
- `js/controllers.js`: run controls, mobile sidebar/drawer, thinking/typing
  indicators, connection state, streaming-bubble lifecycle.
- `js/gateway-client.js`: Gateway client — WebSocket framing, request/response
  tracking, protocol-v4 connect handshake, and the `onEvent` dispatcher for
  the `chat` state-union stream, tool.*, approval, typing and node events.
- `js/sidebar.js`: grouped sidebar loaders (sessions/channels/agents), session
  history loading and session switching.
- `js/approvals.js`: exec and plugin approval queue, reconciliation
  (`approval.list`/`approval.resolve`) and the approval modal.
- `js/navigation.js`: tab switching, new-session flow, abort/clear/compact
  session actions.
- `js/app.js`: `sendMessage`, event-listener wiring, boot.

Avoid adding unrelated gateway calls directly inside rendering helpers. Prefer
a small data-loading function plus a render function per view.

## Contracts enforced by tests

- `TestGatewayMethodCallsitesAreRegistered`: every `callMethod`/`callSafe`
  callsite in the built artifact must be a registered gateway method.
- `TestUIEventContractMatchesGatewayCatalog`: every subscribed and dispatched
  event must exist in the gateway push-event catalog (protocol v4).
- `TestProtocolV4ChatStreamingContract`: the chat state-union handling
  (status/delta/final/aborted/error, runId/seq/replace) stays wired and no
  legacy chat.chunk/turn.* dispatch reappears.

The embedded handler contract should remain stable: serve `/` and connect to
the configured gateway WebSocket path.
