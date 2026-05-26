# Web UI architecture runway

The current Web UI is intentionally served as one embedded `ui.html` file. That keeps local deployment simple, but the parity roadmap now includes management views, dashboard widgets, search/export, localization, and theme variants. Those features should move toward component boundaries before a build tool is introduced.

## Near-term boundaries inside `ui.html`

Keep new code grouped by responsibility:

- Shell/layout: header, sidebar/drawer, responsive behavior, connection state.
- Gateway client: WebSocket framing, request/response tracking, event dispatch.
- Chat view: transcript rendering, streaming messages, tool activity cards, composer behavior.
- Approvals: exec and plugin approval queue/modal.
- Management views: agents, channels, config, dashboard, usage.

Avoid adding unrelated gateway calls directly inside rendering helpers. Prefer a small data-loading function plus a render function per view.

## Migration target

When the UI outgrows the embedded file, migrate incrementally to:

1. `internal/webui/assets/` for source modules and styles.
2. A tiny build step that emits static files embedded by `webui.go`.
3. Component modules for `AppShell`, `ChatView`, `ToolCard`, `ApprovalModal`, `AgentsView`, `ChannelsView`, `ConfigView`, and `DashboardView`.
4. Browser-level tests for event handling and responsive layout.

The embedded handler contract should remain stable: serve `/` and connect to the configured gateway WebSocket path.
