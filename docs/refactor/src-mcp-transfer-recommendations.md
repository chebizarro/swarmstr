# Investigation: `src` MCP feature transfer recommendations for `swarmstr`

## Summary

`src` has a much more complete MCP surface than `swarmstr` today.

The biggest gaps are not in basic tool invocation. `swarmstr` already connects to external MCP servers and registers discovered tools through `internal/mcp/manager.go` and `cmd/metiqd/main.go`. The real gaps are everything around that core:

- layered MCP config resolution and policy/trust handling
- connection-state modeling and reconnect behavior
- OAuth and credential lifecycle for remote MCP servers
- generic external MCP resources and prompts support
- operator-facing MCP management commands and health inspection
- lifecycle/telemetry surfaces for MCP server state changes

`swarmstr` should keep its Go-native runtime shape and existing `extra.mcp` support, but it should adapt the higher-value MCP patterns from `src` into native `swarmstr` seams rather than continuing with a tools-only integration.

## Current `src` MCP surface

### 1. Config layering and resolution are first-class

`src/services/mcp/types.ts:10-26` and `src/services/mcp/types.ts:163-257` define scoped server config and explicit MCP connection states.

`src/services/mcp/config.ts:619-759` and `src/services/mcp/config.ts:1033-1252` show that `src` does much more than parse a single config block:

- supports multiple config scopes (`project`, `user`, `local`, `enterprise`, plus dynamic/plugin-like sources)
- merges scopes with precedence
- filters project servers through approval state
- deduplicates plugin/manual servers by launch signature
- applies allow/deny policy before connection
- treats enterprise MCP config as an exclusive override

### 2. Connection lifecycle is stateful, not binary

`src/services/mcp/types.ts:180-226` models connected, failed, needs-auth, pending, and disabled server states.

`src/services/mcp/client.ts:595-764` and `src/services/mcp/client.ts:2130-2249` show a richer lifecycle than `swarmstr` has today:

- transport-specific connection setup
- capability discovery during connect
- reconnect helpers
- cache invalidation on reconnect/close
- per-server health inspection
- batched connection startup with bounded concurrency

### 3. OAuth and credential lifecycle are built into MCP handling

`src/services/mcp/auth.ts:325-620`, `src/services/mcp/auth.ts:847-1354`, and `src/services/mcp/auth.ts:2362-2445` implement:

- OAuth discovery and authorization flow
- token persistence and refresh
- revocation and credential clearing
- per-server client secret handling
- step-up detection and retry support

`src/tools/McpAuthTool/McpAuthTool.ts:28-215` then exposes an auth pseudo-tool when a server exists but cannot yet expose real tools.

### 4. `src` treats generic MCP resources and prompts as product features

`src/services/mcp/client.ts:1998-2092` and `src/services/mcp/client.ts:2130-2249` fetch:

- `resources/list`
- `resources/read`
- `prompts/list`
- `prompts/get`

`src/tools/ListMcpResourcesTool/ListMcpResourcesTool.ts:1-123` and `src/tools/ReadMcpResourceTool/ReadMcpResourceTool.ts:1-158` surface generic resource access to the model.

`src/services/mcp/client.ts:2035-2088` also projects MCP prompts into command-like surfaces.

### 5. Operator-facing MCP management exists outside the runtime

`src/cli/handlers/mcp.tsx:26-360` adds a complete operational surface:

- `mcp list`
- `mcp get`
- `mcp add-json`
- `mcp remove`
- `mcp add-from-claude-desktop`
- project approval reset
- connection health output
- scope-aware removal and inspection

## Current `swarmstr` MCP surface

### What exists now

`swarmstr/internal/mcp/manager.go:26-48` defines a single config shape rooted in `extra.mcp`.

`swarmstr/internal/mcp/manager.go:73-115` and `swarmstr/internal/mcp/manager.go:121-229` connect enabled stdio/SSE/HTTP servers, discover tools, and keep open sessions.

`swarmstr/internal/mcp/manager.go:231-272` can call tools and return discovered tools.

`swarmstr/internal/mcp/manager.go:321-412` parses config from `state.ConfigDoc.Extra["mcp"]`.

`swarmstr/cmd/metiqd/main.go:1001-1085` loads that config at daemon startup and registers each discovered MCP tool into the shared tool registry.

### What is still missing

Compared with `src`, `swarmstr`'s external MCP support is currently missing all of the following:

- no scoped MCP config registry or precedence rules
- no policy/trust/approval model for externally sourced MCP servers
- no per-server state model beyond “present in manager” vs “failed during startup”
- no reconnect or capability refresh path after startup
- no OAuth or credential lifecycle for remote servers
- no generic external `resources/*` or `prompts/*` support
- no auth pseudo-tool for `needs-auth` servers
- no operator CLI/control-plane for MCP add/list/get/remove/test/reconnect
- no explicit MCP events/telemetry for health, auth, or refresh behavior

## Important distinction: ContextVM is not external MCP parity

`swarmstr/internal/agent/toolbuiltin/contextvm.go:77-122` and `swarmstr/internal/agent/toolbuiltin/contextvm.go:293-396` already expose resource and prompt operations for ContextVM-over-Nostr.

That is valuable, but it does **not** close the external MCP gap.

ContextVM gives `swarmstr` a strong Nostr-native MCP surface. It does not provide parity with `src`'s generic external MCP client lifecycle for configured stdio/SSE/HTTP servers.

## Decision matrix

### MCPCFG-01 — Add an MCP config registry and resolution layer
**Decision:** Adapt

Port the idea of layered MCP config resolution into `swarmstr`, but implement it through `state.ConfigDoc`, config mutation methods, and Go-native registries instead of copying `src`'s file layout.

Primary source anchors:
- `src/services/mcp/types.ts:10-26`
- `src/services/mcp/config.ts:619-759`
- `src/services/mcp/config.ts:1033-1252`

Primary target touchpoints:
- `swarmstr/internal/mcp/manager.go`
- `swarmstr/internal/gateway/methods/config_mutation.go`
- `swarmstr/cmd/metiqd/main.go`

### MCPCONN-01 — Add explicit MCP connection states, reconnect, and capability refresh
**Decision:** Adapt

`swarmstr` should move from startup-only discovery to a stateful connection manager that can represent pending/connected/failed/needs-auth/disabled states and refresh capabilities after reconnect.

Primary source anchors:
- `src/services/mcp/types.ts:180-226`
- `src/services/mcp/client.ts:595-764`
- `src/services/mcp/client.ts:2130-2249`

Primary target touchpoints:
- `swarmstr/internal/mcp/manager.go`
- `swarmstr/cmd/metiqd/main.go`
- `swarmstr/internal/gateway/ws/event_bus.go`

### MCPAUTH-01 — Add OAuth and credential lifecycle for remote MCP servers
**Decision:** Adapt

Remote SSE/HTTP MCP servers in `swarmstr` should not rely on static headers only. They need auth discovery, token persistence, refresh/revoke support, and a user-triggerable auth path.

Primary source anchors:
- `src/services/mcp/auth.ts:325-620`
- `src/services/mcp/auth.ts:847-1354`
- `src/services/mcp/auth.ts:2362-2445`
- `src/tools/McpAuthTool/McpAuthTool.ts:28-215`

Primary target touchpoints:
- `swarmstr/internal/mcp/manager.go`
- `swarmstr/internal/secrets/secrets.go`
- `swarmstr/cmd/metiqd/main.go`
- control/CLI surfaces

### MCPCAP-01 — Add generic external MCP resources support
**Decision:** Adapt

External MCP resources should be visible in `swarmstr` through generic resource list/read tooling, not only through ContextVM-specific tools.

Primary source anchors:
- `src/services/mcp/client.ts:1998-2027`
- `src/tools/ListMcpResourcesTool/ListMcpResourcesTool.ts:1-123`
- `src/tools/ReadMcpResourceTool/ReadMcpResourceTool.ts:1-158`

Primary target touchpoints:
- `swarmstr/internal/mcp/manager.go`
- `swarmstr/internal/agent/toolbuiltin/`
- `swarmstr/internal/agent/tools.go`

### MCPCAP-02 — Add generic external MCP prompts support
**Decision:** Adapt

External MCP prompts should be fetched and projected into a usable runtime surface, similar to how `src` turns them into command-like affordances.

Primary source anchors:
- `src/services/mcp/client.ts:2035-2088`

Primary target touchpoints:
- `swarmstr/internal/mcp/manager.go`
- `swarmstr/cmd/metiqd/main.go`
- command/skills surfaces as appropriate

### MCPUX-01 — Add operator-facing MCP management methods and CLI
**Decision:** Adapt

`swarmstr` needs a real MCP operator surface for add/list/get/remove/test/reconnect/import rather than requiring raw `extra.mcp` editing and daemon restart.

Primary source anchors:
- `src/cli/handlers/mcp.tsx:26-360`

Primary target touchpoints:
- `swarmstr/cmd/metiq/cli_cmds.go`
- `swarmstr/internal/gateway/methods/`
- `swarmstr/internal/gateway/ws/event_bus.go`
- `swarmstr/internal/gateway/protocol/`

### MCPPOL-01 — Add MCP trust, approval, and policy controls
**Decision:** Adapt

`swarmstr` should adopt the idea of managed/project trust gates and allow/deny policy without copying `src`'s exact policy source model.

Primary source anchors:
- `src/services/mcp/config.ts:337-536`
- `src/services/mcp/config.ts:1164-1252`
- `src/cli/handlers/mcp.tsx:351-360`

Primary target touchpoints:
- `swarmstr/internal/gateway/methods/config_mutation.go`
- `swarmstr/internal/mcp/manager.go`
- `swarmstr/cmd/metiqd/main.go`

### MCPOBS-01 — Add MCP lifecycle events and health telemetry
**Decision:** Adapt

MCP connection health, auth-required states, reconnects, and capability refreshes should be emitted as structured runtime events instead of living only in logs.

Primary source anchors:
- `src/cli/handlers/mcp.tsx:26-33`
- `src/services/mcp/client.ts:2130-2249`

Primary target touchpoints:
- `swarmstr/internal/gateway/ws/event_bus.go`
- `swarmstr/cmd/metiqd/main.go`
- `swarmstr/internal/store/state/`

## Non-portable or lower-priority `src` assumptions

- Do not port React/Ink MCP UI components directly.
- Do not copy `src`'s exact config-file topology (`.mcp.json`, desktop import UX, managed settings plumbing) without adapting it to `swarmstr`'s control/config model.
- Do not import Claude.ai-specific connector behavior unless `swarmstr` explicitly wants that product surface.
- Do not couple external MCP runtime choices to ContextVM; keep external MCP and Nostr-native ContextVM as separate capability families.

## Recommended implementation order

1. `MCPCFG-01` — config registry and resolution
2. `MCPCONN-01` — stateful connection manager and reconnect behavior
3. `MCPAUTH-01` — OAuth/credential lifecycle and auth-needed state
4. `MCPCAP-01` — generic resources support
5. `MCPCAP-02` — generic prompts support
6. `MCPUX-01` — operator-facing management CLI/control methods
7. `MCPPOL-01` — trust/approval/policy controls
8. `MCPOBS-01` — events and health telemetry

## Bead seed list

- `MCPCFG-01` Add an MCP config registry and resolution layer
- `MCPCONN-01` Add explicit MCP connection states, reconnect, and capability refresh
- `MCPAUTH-01` Add OAuth and credential lifecycle for remote MCP servers
- `MCPCAP-01` Add generic external MCP resources support
- `MCPCAP-02` Add generic external MCP prompts support
- `MCPUX-01` Add operator-facing MCP management methods and CLI
- `MCPPOL-01` Add MCP trust, approval, and policy controls
- `MCPOBS-01` Add MCP lifecycle events and health telemetry
