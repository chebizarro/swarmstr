# Area: Plugin System + CLI + Web UI

**Agent 6 Review — metiq vs openclaw vs claude-code**
**Date:** 2026-05-25

---

## 1. Scope and files examined

### metiq (`swarmstr/`)

**Plugin System:**
- `internal/plugins/runtime/goja_host.go` — Goja in-process JS runtime
- `internal/plugins/runtime/node_host.go` — Node.js subprocess runtime
- `internal/plugins/runtime/openclaw_host.go` — OpenClaw compatibility runtime
- `internal/plugins/runtime/node_shim.js`, `openclaw_shim.js`, `openclaw_api.js`
- `internal/plugins/manifest/schema.go`, `registry.go` — manifest schema v2
- `internal/plugins/installer/installer.go`, `npm.go`, `url.go`, `clawhub.go`, `manifest.go`
- `internal/plugins/lifecycle/lifecycle.go` — install/enable/disable/update state machine
- `internal/plugins/hooks/invoker.go`, `payloads.go`, `results.go` — hook system
- `internal/plugins/registry/unified.go`, `capabilities.go`, `registry.go` — unified capability registry
- `internal/plugins/sdk/api.go` — host API surface for plugins
- `internal/plugins/manager/manager.go` — Goja/Node plugin manager
- `internal/plugins/channels/bridge.go` — channel plugin bridge
- `internal/plugins/providers/bridge.go` — provider plugin bridge

**CLI:**
- `cmd/metiq/main.go`, `registry.go` — command registration & dispatch
- `cmd/metiq/cli_cmds.go`, `cli_helpers.go`, `cli_output.go` — core commands & output formatting
- All 21 command files: `admin_ops_cmd.go`, `agents_plugins_cmd.go`, `approvals_cmd.go`, `channels_cmd.go`, `cli_admin.go`, `config_cmd.go`, `cron_cmd.go`, `daemon_cmd.go`, `doctor_cmd.go`, `gw_cmd.go`, `hooks_secrets_cmd.go`, `init.go`, `keygen_cmd.go`, `lists_cmd.go`, `mcp_cmd.go`, `memory_cmd.go`, `migrate_cmd.go`, `misc_cmd.go`, `nodes_cmd.go`, `observe_cmd.go`, `sessions_cmd.go`, `skills_cmd.go`, `tasks_cmd.go`

**Web UI:**
- `internal/webui/webui.go` — embedded HTTP handler
- `internal/webui/ui.html` — single-page application (embedded)
- `internal/webui/CYBERWAVE_THEME.md`, `THEME_UPDATES.md`

### openclaw (`openclaw/`)

**Plugin System:**
- `src/plugins/manifest.ts`, `manifest-types.ts` — manifest schema
- `src/plugins/loader.ts` — plugin loading
- `src/plugins/install.ts`, `install-paths.ts` — installation
- `src/plugins/git-install.ts` — git-based install
- `src/plugins/clawhub.ts` — ClawHub registry
- `src/plugins/marketplace.ts` — marketplace manifests
- `src/plugins/hooks.ts`, `hook-types.ts` — hook system
- `src/plugins/registry.ts` — capability registration API
- `src/plugins/runtime/index.ts` — runtime object
- `src/plugins/discovery.ts` — plugin discovery
- `src/plugins/plugin-lifecycle-trace.ts` — lifecycle tracing
- `src/plugins/schema-validator.ts` — AJV schema validation
- `src/plugins/cli.ts` — CLI integration
- `src/plugins/contracts/` — 60+ contract test files

**CLI:**
- `src/cli/program/build-program.ts` — Commander-based program construction
- `src/cli/program/command-group-descriptors.ts`, `core-command-descriptors.ts`
- `src/cli/program/register-command-groups.ts`, `json-mode.ts`, `help.ts`
- `src/cli/plugins-cli.ts`, `config-cli.ts`, `daemon-cli.ts`, `gateway-cli.ts`
- `src/cli/channels-cli.ts`, `skills-cli.ts`, `secrets-cli.ts`, `security-cli.ts`
- 200+ CLI files across subfolders

**Web UI:**
- `ui/src/main.ts`, `ui/src/ui/app.ts`, `ui/src/ui/app-render.ts`
- `ui/src/ui/views/chat.ts`, `sessions.ts`, `agents.ts`, `channels.ts`, `exec-approval.ts`
- `ui/src/ui/chat/tool-cards.ts`, `realtime-talk.ts`, `slash-commands.ts`
- `ui/src/ui/navigation.ts`, `ui/src/ui/app-gateway.ts`, `ui/src/ui/theme.ts`
- `ui/src/styles/layout.mobile.css`
- `ui/src/i18n/` — 19 locale files

### claude-code (`claude-code/`)

**Plugin System:**
- `plugins/README.md` — plugin documentation
- `.claude-plugin/marketplace.json` — marketplace manifest
- `plugins/code-review/`, `plugins/feature-dev/`, `plugins/hookify/`, `plugins/security-guidance/`, `plugins/plugin-dev/` — example plugins
- `examples/hooks/bash_command_validator_example.py`
- `examples/settings/` — settings/permissions examples
- `.claude/commands/` — built-in slash commands

---

## 2. Current-state comparison

### Submatrix 1: Plugin System

| Capability | metiq | openclaw | claude-code | Notes |
|---|---|---|---|---|
| **Runtimes** | Goja (in-process JS), Node.js (subprocess), OpenClaw compat (subprocess) | Native Node.js (in-process `register()`) | Shell commands + Markdown prompts | metiq has 3 runtimes vs openclaw's 1 native; claude-code uses no JS runtime |
| **Manifest schema** | JSON, schema v2, 7 capability types, permissions block, config schema | JSON, rich manifest with 30+ fields, model catalog/pricing, provider endpoints, activation rules | Minimal JSON (`plugin.json` with name/version/description) | openclaw's manifest is significantly richer |
| **Manifest validation** | Go-based: ID format, semver, runtime enum, duplicate checks | AJV-based: all-errors, defaults, allowed-value hints, LRU cache | None (convention-based) | openclaw has schema-first validation |
| **Capability types** | tools, channels, hooks, mcp_servers, skills, gateway_methods, providers | tools, hooks, providers, channels, services, commands, 12+ media/AI provider types, context engines, compaction providers, session extensions | commands, agents, skills, hooks | openclaw has ~30 registerable capability types |
| **Permissions model** | Manifest-declared: network, filesystem, exec, secrets, nostr, agent, storage | Path safety, install security scans, registration guards, capability gating, prompt injection gates | Settings-level allow/deny/ask per tool, managed hooks, sandbox | metiq declares but doesn't enforce; openclaw gate at API level |
| **Install: npm** | ✅ `--ignore-scripts`, audit, integrity, rollback | ✅ Managed npm root, peer linking, integrity drift, min-host checks, security scans | ❌ | Both strong; openclaw slightly deeper |
| **Install: URL/archive** | ✅ HTTPS only, 50MiB limit, tar.gz/zip/js | ✅ Archive, file, directory, path | ✅ `--plugin-dir` for local dirs and .zip | metiq and openclaw comparable |
| **Install: git** | ❌ | ✅ Full git support (GitHub shorthand, SSH, refs, hardened git env) | ❌ | openclaw-only |
| **Install: registry** | ✅ ClawHub (`registry.clawhub.ai`) + Nostr registry | ✅ ClawHub + marketplace manifests + known marketplaces | ✅ Marketplace with `/plugin install name@marketplace` | All three have registry concepts |
| **Install: version pinning** | Partial (npm semver, ClawHub version param) | ✅ npm semver, git refs, ClawHub version/channel, integrity | ❌ (no versioning visible) | openclaw strongest |
| **Lifecycle states** | installed → enabled/disabled/error/updating | Loaded → registered (guarded close) | Loaded at startup | metiq has richer state machine |
| **Lifecycle scopes** | user (~/.metiq), project (.metiq), local (dev) | Config dir + managed npm/git subdirs | Per-project `.claude-plugin/` or `--plugin-dir` | metiq's scoping is well-designed |
| **Hook system** | Priority-sorted, timeout per handler, stop-on-mutation/reject/error, native + node hooks, 30+ event types | Same 30+ event types, timeout support, prompt injection gates, conversation access gates | 9 event types, shell command or prompt type, exit-code protocol | metiq and openclaw share hook events; claude-code is simpler but has unique `Stop` hook |
| **Plugin debugging** | slog component logging, stderr forwarding (Node) | `OPENCLAW_PLUGIN_LIFECYCLE_TRACE=1`, startup trace metrics, profiling wrapper, schema diagnostics | `--debug`, hook test scripts, debug log files, `init.plugin_errors` | openclaw has richest debugging |
| **Sandboxing: Goja** | ✅ No Node APIs, host stubs, per-call timeouts, mutex serialization, VM interrupt | N/A | N/A | metiq-unique strength |
| **Sandboxing: Node** | Process isolation only; no OS-level sandbox | No hard sandbox; layered guardrails (path safety, registration close, install scans) | Settings-level `sandbox` for Bash only | Neither has true Node sandbox |
| **OpenClaw compat** | ✅ Dedicated runtime, shim layer, full capability map | N/A (is the native format) | N/A | metiq's compat layer is a differentiator |
| **SDK API surface** | Nostr, Config, HTTP, Storage, Log, Agent (6 host APIs) | ~20 runtime surfaces (config, agent, subagent, nodes, system, media, webSearch, channel, events, tasks, etc.) | No runtime API (plugins are markdown/scripts) | openclaw's runtime API is far richer |
| **Plugin update** | ✅ npm update, ClawHub update | ✅ npm update, version resolution, plugin convergence | ❌ (no update command visible) | Both metiq and openclaw support updates |
| **Plugin uninstall** | ✅ via lifecycle manager | ✅ `plugins uninstall` with confirmation | ❌ | Both support uninstall |
| **Plugin search/publish** | ✅ ClawHub search, Nostr publish | ✅ ClawHub + marketplace search | ✅ marketplace-based | metiq adds Nostr publishing |
| **Contract tests** | Unit tests per subsystem | 60+ contract test files covering registration, boundaries, runtime, schemas | No plugin-specific tests | openclaw has exceptional test coverage |

### Submatrix 2: CLI

| Capability | metiq | openclaw | claude-code | Notes |
|---|---|---|---|---|
| **Command count** | 43 top-level, 114 total paths | 54+ top-level, 64+ subcommand nodes (estimated 150+ total paths) | ~15 slash commands + CLI flags | openclaw is broadest |
| **Framework** | stdlib `flag`, hand-rolled registry | Commander.js with lazy group loading | Not applicable (single binary) | openclaw's Commander gives richer UX |
| **Command hierarchy** | Flat top-level + `switch args[0]` subcommands | Deep tree: groups → commands → subcommands, lazy loaded | Flat slash commands | openclaw has best hierarchy |
| **JSON output** | Per-command `--json`, `printJSON` helper, `gw` defaults to JSON | Per-command `--json` with metadata-driven behavior (output vs parse-only) | `--output-format json` for some modes | Both strong; openclaw more systematic |
| **Table/CSV output** | ❌ No generic table renderer | Human-formatted rich text, no CSV | ❌ | Neither has CSV |
| **NDJSON/streaming output** | ❌ | `--raw-stream-path` on gateway run | JSON streaming in headless mode | openclaw has real stream output |
| **Shell-script friendliness** | Good: stderr errors, exit codes, stdin support, `--json` | Strong: exit codes, `--json` metadata, `--no-color`, profile support | Strong: `--print`, `--output-format`, piping | All decent; openclaw slightly ahead |
| **Interactive prompts** | Minimal: `setup`/`onboard`/`configure` via `ReadString` | Rich: config guided setup, channel add wizard, plugin confirm prompts, commander-based | N/A (conversational) | openclaw significantly richer |
| **Shell completions** | ✅ bash, zsh, fish | ✅ bash, fish completions | ❌ | Both metiq and openclaw support completions |
| **Streaming commands** | ❌ `observe --wait` is long-poll; `logs` is fixed count | ✅ `gateway run --raw-stream`, event streaming, progress UI | ✅ Inherent streaming in chat | metiq lacks true streaming CLI |
| **Config management** | `config get/set/unset/patch/list/schema/validate/path/import/export` (10 subcommands) | `config get/set/patch/unset/file/schema/validate` (8 subcommands) | Settings files only | metiq has more config subcommands |
| **Plugin CLI** | `plugins list/info/capabilities/install/search/publish` (6) | `plugins list/search/inspect/enable/disable/uninstall/install/update/registry/doctor/build/validate/init/marketplace list` (16) | `/plugin install` | openclaw has 2.5x more plugin commands |
| **Memory CLI** | `memory search/import/stats/compact/health/repair/eval/list/backends/sync` (11) | Spread across config/agent/sessions | N/A | metiq has dedicated memory CLI — a strength |
| **MCP management** | `mcp list/get/put/add/remove/test/reconnect/auth start/auth refresh/auth clear` (10) | `mcp` CLI | MCP via `.mcp.json` | metiq has rich MCP CLI |
| **Daemon management** | `daemon start/stop/restart/status` | `daemon status/install/uninstall/start/stop/restart` + `gateway run/status/install/...` (16 gateway commands) | N/A | openclaw has richer gateway/daemon split |
| **Task management** | `tasks list/show/audit/cancel/resume/runs` (6) | `tasks` CLI | N/A | Comparable |
| **Approval CLI** | `approvals list/approve/deny` | Exec approvals via CLI + gateway | N/A | Both support approval CLI |
| **Node management** | `nodes list/add/status/send/pending/approve/reject/describe/invoke/rename` (10) | `nodes` CLI with camera/screen/location/push/invoke | N/A | Both rich; openclaw adds device-specific |
| **Session management** | `sessions list/get/export/delete/reset/prune` (6) | `sessions` with search/sort/pagination/bulk/archive/compaction | N/A | openclaw richer |
| **Doctor/diagnostics** | `doctor` (top-level) | `doctor` + `gateway diagnostics export` (zip) | N/A | openclaw adds export |
| **Update command** | `update` (self-update) | `update` with plugin convergence, restart helper | N/A | openclaw more robust |
| **Version/help** | `version`, `--version`, per-command help | `-V`, `--version`, root help, examples, docs link | `--version` | openclaw has richer help |
| **Themed output** | ✅ Cyberwave-themed colored output (printSuccess/Error/Accent etc.) | ✅ Rich terminal styling with `--no-color` | ✅ Minimal | Both metiq and openclaw have themed output |
| **Lazy loading** | ❌ All commands registered at startup | ✅ Lazy command group loading for performance | N/A | openclaw advantage for large CLIs |
| **Admin workflows** | `security audit`, `keygen`, `qr`, `health`, `observe`, `migrate` | `security`, `secrets`, `sandbox`, `exec-approvals`, `exec-policy`, `backup`, `migrate` | Settings-based | openclaw has broader admin surface |

### Submatrix 3: Web UI

| Capability | metiq | openclaw | claude-code | Notes |
|---|---|---|---|---|
| **Architecture** | Single embedded HTML file, served from Go handler | Lit-based SPA with Vite build, component architecture | Terminal-only (no web UI) | openclaw is a full modern web app |
| **Framework** | Vanilla JS, inline CSS | Lit (Web Components), TypeScript, Vite | N/A | Major architecture gap |
| **Session sidebar** | 3 tabs: Sessions / Channels / Agents | Collapsible nav rail with grouped sections (Chat, Control, Agent, Settings) | N/A | openclaw has richer navigation |
| **Session switching** | Click to load, localStorage persistence | Session selector + agent/model selector, search, sort, pagination, bulk operations, compaction restore | N/A | openclaw far richer |
| **Streaming text** | ✅ `chat.chunk` events → temporary bubble → finalize with Markdown | ✅ Streaming groups, reading indicator, "new messages" floating button | N/A | Both work; openclaw more polished |
| **Markdown rendering** | ✅ marked + DOMPurify + highlight.js (CDN) | ✅ Full markdown with task lists, syntax highlighting | N/A | Comparable rendering |
| **Code blocks** | ✅ Syntax highlighting + copy button | ✅ Syntax highlighting + copy + canvas preview | N/A | openclaw adds canvas |
| **Streaming cursor** | ✅ Blinking `▌` cursor | ✅ Streaming indicator | N/A | Both have streaming UX |
| **Tool visibility** | ❌ No tool call/result display | ✅ Collapsible tool cards with input/output, raw toggle, side panel, canvas iframe | N/A | Major gap in metiq |
| **Exec approval modal** | ✅ Queue-based, countdown timer, command preview with highlighting, Allow once/Deny/Always allow | ✅ Queue-based, countdown, command highlighting, host/agent/session/cwd/security metadata, Allow once/Deny/Always allow + plugin approvals | N/A | Both strong; openclaw adds plugin approvals and richer metadata |
| **Channels tab** | Display-only: channel id + type + green dot | Full channel cards per platform (WhatsApp/Telegram/Discord/Slack/Signal/iMessage/Nostr/Google Chat), status, login/logout, health, config | N/A | Major depth gap |
| **Agents tab** | Display-only: agent id + model, active highlight | Full agent management: overview, files, tools, skills, channels, cron, tool allow/deny, config save | N/A | Major depth gap |
| **Reconnect/resume** | ✅ Exponential backoff (1.5s → 15s), streaming finalization, pending clear | ✅ Exponential backoff, seq-gap detection, approval pruning, chat queue resume, subscription resync | N/A | openclaw handles more edge cases |
| **Dark theme** | ✅ Cyberwave: electric purple/neon cyan, glows, gradients — unique aesthetic | ✅ Multiple theme families (claw/knot/dash/custom) × light/dark, system mode, custom import | N/A | metiq has unique identity; openclaw has flexibility |
| **Light theme** | ❌ Dark-only | ✅ Full light/dark + system detection | N/A | Gap |
| **Mobile responsive** | Partial: `100dvh`, fluid widths, no media queries, fixed 220px sidebar | ✅ Full responsive: drawer sidebar ≤1100px, mobile controls ≤768px, progressive column hiding, safe-area, 44px tap targets | N/A | Significant gap |
| **i18n** | ❌ English-only | ✅ 19 locales with `t()` throughout, glossaries, locale selector | N/A | Major gap |
| **Voice/realtime** | ❌ | ✅ Realtime Talk with OpenAI/Google providers, WebRTC/gateway relay, VAD controls | N/A | Not in scope for minimum parity |
| **Search** | ❌ | ✅ Cmd/Ctrl+F search in chat | N/A | Nice-to-have gap |
| **Attachments** | ❌ | ✅ File picker, paste, drag-drop | N/A | Gap |
| **Slash commands** | Mentioned in placeholder hint only | ✅ Slash command menu with keyboard nav and argument completion | N/A | Gap |
| **Chat export** | ❌ | ✅ Export as Markdown | N/A | Gap |
| **Input history** | ❌ | ✅ Up/down arrow history | N/A | Gap |
| **Token estimate** | ❌ | ✅ Token count in composer | N/A | Gap |
| **Overview/dashboard** | ❌ | ✅ Connection, uptime, usage, sessions, skills, cron, attention items, event log, log tail | N/A | Major gap |
| **Usage metrics** | ❌ | ✅ Usage tab with cost/token metrics | N/A | Gap |
| **Cron management** | ❌ | ✅ Cron view and quick-create | N/A | Gap |
| **Config UI** | ❌ | ✅ Full config form with schema-driven rendering, presets, search | N/A | Gap |
| **Run controls** | ❌ | ✅ Abort, clear history, compact, new session via UI | N/A | Gap |
| **Auth/login** | Token-based, loopback-only when token set | Token + password + pairing + device auth, auth failure hints | N/A | openclaw more robust |
| **Service worker** | ❌ | ✅ Versioned SW, web push subscription | N/A | Gap |
| **Thinking/reasoning** | ❌ | ✅ Thinking tags, per-session reasoning/verbose/fast toggles | N/A | Gap |

---

## 3. Gaps

### Submatrix 1: Plugin System Gaps

| Gap ID | Capability | Severity | metiq status | Evidence | User impact | Recommended metiq change |
|---|---|---|---|---|---|---|
| P1-01 | Permission enforcement at runtime | P1 High | Partial — declared but not enforced | Manifest has `permissions` block; no runtime check found that gates host API calls against declared permissions | Plugins can access capabilities they didn't declare; weakens security trust model | `internal/plugins/runtime/goja_host.go`: check manifest permissions before enabling host namespaces; `node_host.go`: similar gating |
| P1-02 | Git-based plugin install | P2 Medium | Absent | openclaw has `git-install.ts` with GitHub shorthand, SSH, refs, hardened env; metiq has npm/URL/ClawHub only | Cannot install plugins from git repos or use ref-based versioning | `internal/plugins/installer/git.go`: new git install flow with ref support and safe git env |
| P1-03 | Plugin SDK runtime API breadth | P1 High | Weaker — 6 host APIs | openclaw runtime has ~20 surfaces (subagent, nodes, media, webSearch, tasks, etc.); metiq has nostr/config/http/storage/log/agent | Plugin authors limited in what they can build; cannot access subagent, task, media, or web search from plugins | `internal/plugins/sdk/api.go`: expand `Host` with Session, Task, Memory, and WebSearch interfaces |
| P1-04 | Plugin contract/integration tests | P2 Medium | Partial — unit tests per file | openclaw has 60+ dedicated contract tests validating registration boundaries, runtime invariants, loader behavior | Less confidence in plugin system correctness during refactors | Add contract-style tests under `internal/plugins/contracts/` |
| P1-05 | Plugin lifecycle tracing/profiling | P2 Medium | Absent | openclaw has `OPENCLAW_PLUGIN_LIFECYCLE_TRACE`, startup metrics, profiling wrapper | Hard to debug slow plugin loading or registration failures in production | `internal/plugins/lifecycle/lifecycle.go`: add trace mode with timing logs, env var toggle |
| P1-06 | Schema validation with AJV-like diagnostics | P3 Low | Partial — Go struct validation | openclaw uses AJV with all-errors, allowed-value hints, LRU cache; metiq validates via Go struct checks | Less helpful error messages for plugin authors with invalid manifests | `internal/plugins/manifest/schema.go`: improve validation error messages with field paths and allowed values |
| P1-07 | Registration guard (close after sync) | P2 Medium | Absent | openclaw's `createGuardedPluginRegistrationApi` blocks late registration calls | Late/async plugin registration could cause race conditions | `internal/plugins/registry/unified.go`: add registration window close after sync load |
| P1-08 | Plugin enable/disable via CLI | P2 Medium | Absent from CLI | openclaw has `plugins enable/disable`; metiq lifecycle supports it but no CLI command found | Users must manually edit config to enable/disable plugins | `cmd/metiq/agents_plugins_cmd.go`: add `plugins enable/disable` subcommands |
| P1-09 | Plugin doctor/validate CLI | P3 Low | Absent | openclaw has `plugins doctor`, `plugins validate`, `plugins build` | Plugin authors lack diagnostic tools | `cmd/metiq/agents_plugins_cmd.go`: add `plugins doctor/validate` |
| P1-10 | Marketplace browsing experience | P2 Medium | Basic search only | openclaw has marketplace list, category, known marketplace management; claude-code has `/plugin install name@marketplace` | Less discoverable plugin ecosystem | `internal/plugins/installer/clawhub.go` + `cmd/metiq/agents_plugins_cmd.go`: richer marketplace browsing |

### Submatrix 2: CLI Gaps

| Gap ID | Capability | Severity | metiq status | Evidence | User impact | Recommended metiq change |
|---|---|---|---|---|---|---|
| C2-01 | Streaming/follow CLI commands | P1 High | Absent | `observe --wait` is long-poll; `logs` is fixed count; no `--follow` | Operators cannot tail live logs or stream events from CLI | `cmd/metiq/observe_cmd.go`: add `--stream`/`--follow` flag with SSE/WebSocket event subscription; `cmd/metiq/cli_cmds.go`: add `logs --follow` |
| C2-02 | Lazy command loading | P3 Low | Absent | All 43 commands registered at startup; openclaw uses lazy group loading | Minor startup overhead; no user-visible impact with current command count | Future consideration if command count grows |
| C2-03 | Commander-level UX features | P2 Medium | Absent | metiq uses hand-rolled `switch` dispatch; no automatic help generation, no option parsing framework | Inconsistent help formatting, no automatic `--help` on subcommands, no option validation | `cmd/metiq/`: consider adopting cobra or similar Go CLI framework |
| C2-04 | Interactive guided workflows | P2 Medium | Minimal — ReadString prompts | openclaw has config wizard, channel add wizard, plugin confirmation prompts | Setup and onboarding feel bare compared to guided flows | `cmd/metiq/config_cmd.go`: add interactive config wizard; `channels_cmd.go`: add guided channel setup |
| C2-05 | Plugin enable/disable/doctor/build/validate CLI | P2 Medium | Absent | openclaw has 16 plugin subcommands vs metiq's 6 | Plugin management workflows incomplete from CLI | `cmd/metiq/agents_plugins_cmd.go`: add `enable`, `disable`, `update`, `uninstall`, `doctor`, `validate` |
| C2-06 | Gateway diagnostic export | P2 Medium | Absent | openclaw has `gateway diagnostics export` producing a zip bundle | Harder to collect debug information for support cases | `cmd/metiq/gw_cmd.go` or new `diagnostics_cmd.go`: add diagnostic export |
| C2-07 | Global `--json` / `--no-color` flags | P2 Medium | Per-command `--json`; no `--no-color` | openclaw has metadata-driven JSON and global `--no-color` | Inconsistent script integration; colored output can break piped workflows | `cmd/metiq/registry.go`: add global `--json` and `--no-color` flags |
| C2-08 | Backup/restore CLI | P3 Low | Absent | openclaw has `backup` command | No easy backup workflow for metiq state | `cmd/metiq/admin_ops_cmd.go`: add `backup` subcommand |
| C2-09 | Security/exec-policy CLI | P2 Medium | Partial — `security audit` only | openclaw has `exec-approvals`, `exec-policy`, `sandbox` CLI commands | Cannot manage exec policies or approval rules from CLI | `cmd/metiq/admin_ops_cmd.go`: add exec-policy management commands |
| C2-10 | Logs CLI with follow/filtering | P1 High | Absent — fixed recent lines only | openclaw has `logs` CLI with runtime filtering | Cannot monitor running daemon behavior from CLI in real-time | `cmd/metiq/cli_cmds.go`: add `logs --follow --filter` |

### Submatrix 3: Web UI Gaps

| Gap ID | Capability | Severity | metiq status | Evidence | User impact | Recommended metiq change |
|---|---|---|---|---|---|---|
| W3-01 | Tool call/result visibility | P0 Critical | Absent | No tool card rendering; openclaw shows collapsible cards with input/output, raw toggle, side panel | Users cannot see what tools the agent is invoking or their results — fundamental observability gap | `internal/webui/ui.html`: add tool card rendering from `chat.message` tool content |
| W3-02 | Mobile responsive layout | P1 High | Partial — fluid sizing only, fixed 220px sidebar | No `@media` queries; openclaw has drawer ≤1100px, mobile controls ≤768px, safe-area | UI is unusable on mobile devices | `internal/webui/ui.html`: add responsive breakpoints, collapsible sidebar, mobile-optimized controls |
| W3-03 | Agent management in UI | P1 High | Display-only (id + model) | openclaw shows agent overview, files, tools, skills, channels, cron tabs, tool allow/deny | Cannot configure or inspect agents from UI | `internal/webui/ui.html`: expand agents tab with agent details panel |
| W3-04 | Channel management in UI | P1 High | Display-only (id + type) | openclaw shows per-platform cards with login/logout, health, config | Cannot manage channels from UI | `internal/webui/ui.html`: expand channels tab with status, config, health |
| W3-05 | Light theme / theme switching | P2 Medium | Dark-only Cyberwave theme | openclaw has 4 theme families × light/dark + system mode | Users who prefer light themes cannot use the UI comfortably | `internal/webui/ui.html`: add light theme variant and toggle |
| W3-06 | i18n / localization | P2 Medium | English-only | openclaw has 19 locales with glossaries and locale selector | Non-English speakers have degraded experience | Not immediate priority; plan for extraction of strings |
| W3-07 | Overview/dashboard page | P2 Medium | Absent | openclaw has uptime, usage, sessions, skills, cron, attention items, event log, log tail | No at-a-glance operational view | `internal/webui/ui.html`: add dashboard/overview section |
| W3-08 | Slash commands in chat | P2 Medium | Mentioned in placeholder only | openclaw has full slash command menu with keyboard nav and argument completion | Users cannot access commands from the chat input | `internal/webui/ui.html`: implement slash command menu |
| W3-09 | Chat search | P3 Low | Absent | openclaw has Cmd/Ctrl+F in-chat search | Cannot search conversation history in UI | `internal/webui/ui.html`: add search overlay |
| W3-10 | File attachments | P2 Medium | Absent | openclaw supports file picker, paste, drag-drop | Cannot send files/images to the agent via UI | `internal/webui/ui.html`: add attachment support |
| W3-11 | Chat export | P3 Low | Absent | openclaw has export-as-Markdown | Cannot save conversations | `internal/webui/ui.html`: add export button |
| W3-12 | Input history (up/down) | P3 Low | Absent | openclaw has up/down arrow input history | Minor UX convenience gap | `internal/webui/ui.html`: add input history |
| W3-13 | Run controls (abort, compact) | P1 High | Absent | openclaw has abort, clear history, compact buttons | Cannot stop a runaway agent or manage context from UI | `internal/webui/ui.html`: add abort/stop button and clear history action |
| W3-14 | Session resume on reconnect | P2 Medium | Partial — stores session ID but doesn't auto-load history | Session ID persisted in localStorage but chat.history not called on reconnect/page load | Refreshing the page shows blank chat until user clicks session | `internal/webui/ui.html`: auto-load history for stored session after connect |
| W3-15 | Component architecture | P1 High | Monolithic single HTML file | openclaw uses Lit components with TypeScript, Vite build, tests | Hard to maintain, test, or extend the UI as features grow | Plan migration to component-based SPA (Lit, Svelte, or similar) |
| W3-16 | Plugin approval modal | P2 Medium | Exec approvals only | openclaw also handles plugin installation approvals | Cannot approve/deny plugin actions from UI | `internal/webui/ui.html`: add plugin approval event handling |
| W3-17 | Config UI | P2 Medium | Absent | openclaw has schema-driven config form with search, presets, validation | Must use CLI for all configuration | `internal/webui/ui.html`: add config view |
| W3-18 | Usage metrics display | P3 Low | Absent | openclaw shows cost/token usage metrics | No visibility into resource consumption from UI | Future enhancement |

---

## 4. Parity target

### What parity means for this area

Parity means metiq's plugin system enables the same breadth of plugin development as openclaw, the CLI covers all common operational workflows without hidden manual steps, and the Web UI provides clear operational visibility including tool calls, streaming, approvals, and basic agent/channel management.

### Minimum target

**Plugin System:**
- Permission enforcement at runtime (P1-01)
- SDK API expansion to cover subagent, task, and web search (P1-03)
- Plugin enable/disable from CLI (P1-08, C2-05)
- Registration guard to prevent late/async registration (P1-07)
- Lifecycle tracing for debugging (P1-05)

**CLI:**
- Streaming/follow commands for logs and events (C2-01, C2-10)
- Global `--json` and `--no-color` flags (C2-07)
- Plugin management parity (enable/disable/update) (C2-05)
- Exec-policy CLI commands (C2-09)

**Web UI:**
- Tool call/result visibility (W3-01)
- Mobile responsive layout (W3-02)
- Run controls: abort/stop (W3-13)
- Session auto-resume on reconnect (W3-14)
- Agent and channel detail views (W3-03, W3-04)

### Stretch target

**Plugin System:**
- Git-based plugin install (P1-02)
- AJV-quality schema validation (P1-06)
- 60+ contract tests (P1-04)
- Richer marketplace UX (P1-10)
- claude-code-inspired declarative hook system with markdown rule files

**CLI:**
- Commander-level framework (cobra) for consistent UX (C2-03)
- Interactive guided workflows for setup/channels (C2-04)
- Diagnostic export bundle (C2-06)
- Backup/restore CLI (C2-08)

**Web UI:**
- Component-based architecture (W3-15)
- Light theme + theme switching (W3-05)
- i18n framework (W3-06)
- Slash commands, search, attachments, export (W3-08-11)
- Dashboard/overview (W3-07)
- Config UI (W3-17)

---

## 5. Implementation plan for metiq

### Phase 1: Critical and High-priority (P0/P1)

#### 5.1 Tool visibility in Web UI (W3-01) — P0
- **Package:** `internal/webui/ui.html`
- **Change:** Parse `chat.message` events for tool call/result content. Render collapsible tool cards below agent bubbles showing tool name, input JSON, and output. Add expand/collapse toggle.
- **Rationale:** Fundamental observability gap; users cannot see agent actions.
- **Dependencies:** Gateway must already emit tool content in chat messages (verify `chat.message` payload structure).

#### 5.2 Run controls — abort/stop (W3-13) — P1
- **Package:** `internal/webui/ui.html`
- **Change:** Add "Stop" button visible during active agent runs. Wire to gateway RPC method for run cancellation.
- **Dependencies:** Gateway must support run cancellation RPC.

#### 5.3 Permission enforcement (P1-01) — P1
- **Packages:** `internal/plugins/runtime/goja_host.go`, `internal/plugins/sdk/api.go`
- **Change:** Before wiring host namespaces in Goja, check manifest `permissions` field. Only wire namespaces the plugin declared. For Node runtime, pass allowed permissions to shim for enforcement.
- **Rationale:** Permissions are declared but unenforced; this weakens the security model.
- **Dependencies:** None.

#### 5.4 SDK API expansion (P1-03) — P1
- **Packages:** `internal/plugins/sdk/api.go`, `internal/plugins/runtime/goja_host.go`
- **Change:** Add `Session`, `Task`, `Memory`, and `WebSearch` host interfaces. Wire into Goja VM globals. For Node, extend shim protocol with new RPC methods.
- **Rationale:** Plugin authors are limited to 6 host APIs vs openclaw's ~20.
- **Dependencies:** Internal APIs for session/task/memory must be accessible from plugin manager.

#### 5.5 Streaming CLI commands (C2-01, C2-10) — P1
- **Packages:** `cmd/metiq/observe_cmd.go`, `cmd/metiq/cli_cmds.go`
- **Change:** Add `observe --stream` (or `--follow`) that opens a WebSocket/SSE connection to the gateway and streams events to stdout. Add `logs --follow` that tails daemon logs continuously.
- **Dependencies:** Gateway event subscription API.

#### 5.6 Mobile responsive Web UI (W3-02) — P1
- **Package:** `internal/webui/ui.html`
- **Change:** Add CSS media queries: sidebar becomes drawer at ≤768px with hamburger toggle. Adjust message widths, button sizes (44px tap targets), and safe-area bottom for mobile browsers.
- **Dependencies:** None.

#### 5.7 Agent/Channel detail views (W3-03, W3-04) — P1
- **Package:** `internal/webui/ui.html`
- **Change:** Expand Agents tab to show agent config details (model, tools, skills) when clicked. Expand Channels tab to show per-channel status, type details, and account info.
- **Dependencies:** Gateway RPC must return sufficient agent/channel metadata (likely already available via `agents.list` and `channels.list`).

### Phase 2: Medium-priority (P2)

#### 5.8 Global CLI flags (C2-07)
- **Package:** `cmd/metiq/registry.go`, `cmd/metiq/cli_output.go`
- **Change:** Add `--json` and `--no-color` as global flags parsed before command dispatch. Thread through to output helpers.

#### 5.9 Plugin enable/disable CLI + management commands (C2-05, P1-08)
- **Package:** `cmd/metiq/agents_plugins_cmd.go`
- **Change:** Add `plugins enable <id>`, `plugins disable <id>`, `plugins update <id>`, `plugins uninstall <id>`, `plugins doctor`.
- **Dependencies:** `internal/plugins/lifecycle/lifecycle.go` already supports these operations.

#### 5.10 Registration guard (P1-07)
- **Package:** `internal/plugins/registry/unified.go`
- **Change:** Add a `Close()` method that locks the registry after initial sync registration. Late calls return error instead of silently succeeding.

#### 5.11 Lifecycle tracing (P1-05)
- **Package:** `internal/plugins/lifecycle/lifecycle.go`, `internal/plugins/manager/manager.go`
- **Change:** Add `METIQ_PLUGIN_TRACE=1` env var. When enabled, log phase timing (load, register, init) per plugin to stderr.

#### 5.12 Git-based plugin install (P1-02)
- **Package:** `internal/plugins/installer/git.go` (new file)
- **Change:** Implement git clone with ref support, GitHub shorthand parsing, hardened git env (no terminal prompts, no editor, etc.). Managed git dir under `~/.metiq/git/`.

#### 5.13 Session auto-resume on reconnect (W3-14)
- **Package:** `internal/webui/ui.html`
- **Change:** After successful WebSocket connect, if `localStorage["metiq_session"]` is set, auto-call `chat.history` and render.

#### 5.14 Interactive CLI workflows (C2-04)
- **Package:** `cmd/metiq/config_cmd.go`, `cmd/metiq/channels_cmd.go`
- **Change:** Add prompted config wizard for first-time setup. Add guided channel add with platform-specific prompts.

#### 5.15 Light theme (W3-05)
- **Package:** `internal/webui/ui.html`
- **Change:** Define `:root.light` CSS variables with light backgrounds/dark text. Add theme toggle button in header.

#### 5.16 Exec-policy CLI (C2-09)
- **Package:** `cmd/metiq/admin_ops_cmd.go`
- **Change:** Add `exec-policy list/set/remove` subcommands for managing tool/command execution policies.

#### 5.17 Slash commands in UI (W3-08)
- **Package:** `internal/webui/ui.html`
- **Change:** Detect `/` prefix in input, show dropdown menu of available commands, handle selection and argument entry.

#### 5.18 Attachments in UI (W3-10)
- **Package:** `internal/webui/ui.html`
- **Change:** Add file input button, paste handler, and drag-drop zone. Send attachments as base64 or multipart with chat message.

#### 5.19 Plugin approval modal (W3-16)
- **Package:** `internal/webui/ui.html`
- **Change:** Handle `plugin.approval.requested` events alongside exec approvals. Render plugin-specific approval details.

#### 5.20 Config UI (W3-17)
- **Package:** `internal/webui/ui.html`
- **Change:** Add Config tab/view. Fetch config schema from gateway, render form fields, support get/set/validate.

### Phase 3: Low-priority polish (P3)

#### 5.21 Contract tests for plugin system (P1-04)
- **Package:** `internal/plugins/contracts/` (new directory)
- **Change:** Add integration tests for registration boundaries, runtime invariants, loader behavior.

#### 5.22 Schema validation improvements (P1-06)
- **Package:** `internal/plugins/manifest/schema.go`
- **Change:** Improve error messages with field paths and allowed values.

#### 5.23 Diagnostic export (C2-06)
- **Package:** `cmd/metiq/` (new `diagnostics_cmd.go`)
- **Change:** Collect logs, config, plugin state, session stats into a zip bundle.

#### 5.24 Chat search, export, input history (W3-09, W3-11, W3-12)
- **Package:** `internal/webui/ui.html`
- **Change:** Add Ctrl+F search overlay, export-as-Markdown button, up/down arrow input history.

#### 5.25 Dashboard/overview (W3-07)
- **Package:** `internal/webui/ui.html`
- **Change:** Add overview section showing connection status, uptime, session count, active channels, recent events.

#### 5.26 Web UI component architecture (W3-15)
- **Package:** New `internal/webui/` restructure
- **Change:** Migrate from single HTML file to a component-based build (Lit/Svelte with Vite). Embed built assets instead of raw HTML.
- **Rationale:** Current monolithic HTML will become unmaintainable as features grow.
- **Risk:** Significant refactor; best done as a dedicated project.

---

## 6. Risks / unknowns

### R1: Web UI monolith scalability
The single-file HTML architecture makes implementing Phase 2-3 UI features progressively harder. Each addition increases the complexity of a file that's already substantial. A component architecture migration (W3-15) should be considered before or during Phase 2 to avoid compounding technical debt.

### R2: Gateway RPC surface assumptions
Many UI improvements assume the gateway already exposes sufficient RPC methods (tool content in messages, agent details, channel config, run cancellation). If these methods don't exist, each UI gap has a hidden backend dependency. **Mitigation:** Verify gateway RPC surface before estimating UI work.

### R3: Permission enforcement may break existing plugins
Enforcing permissions (P1-01) on plugins that previously ran without declaring permissions could break existing plugin installations. **Mitigation:** Add a grace period with warnings before hard enforcement; allow a `permissions: ["*"]` escape hatch for development.

### R4: SDK API expansion requires internal API stability
Expanding the plugin SDK (P1-03) exposes internal APIs (session, task, memory) to plugin authors. These APIs need to be stable or versioned. **Mitigation:** Start with a narrow, explicitly versioned SDK surface.

### R5: OpenClaw compat runtime untested with complex plugins
The OpenClaw compatibility runtime supports tools, hooks, providers, channels, services, and commands, but test coverage for complex multi-capability plugins is unclear. Real-world OpenClaw plugins may exercise edge cases not yet handled.

### R6: CLI framework migration risk
Moving to cobra or another framework (C2-03) would improve consistency but requires touching all 43+ commands. **Mitigation:** Can be done incrementally by wrapping the existing registry pattern.

### R7: Theme consistency during migration
Adding a light theme (W3-05) while maintaining the Cyberwave dark aesthetic requires careful CSS variable design. The current theme is deeply integrated into specific color values rather than semantic tokens.

---

## 7. Validation plan

### Plugin System

| Test type | Coverage | How |
|---|---|---|
| Unit tests | Permission enforcement blocks unauthorized host API access | Mock manifest with restricted permissions, assert namespace stubs |
| Unit tests | Registration guard rejects late registration | Register, close, attempt late register, assert error |
| Unit tests | Lifecycle tracing emits timing logs | Enable trace env var, load plugin, assert structured log output |
| Integration tests | Git install clones, checks out ref, loads plugin | Integration test with local git repo fixture |
| Integration tests | SDK expanded APIs are accessible from Goja plugin | Write test plugin using new Session/Task/Memory/WebSearch APIs |
| Contract tests | All registered capability types survive roundtrip | Register each type, serialize, deserialize, verify |
| Manual | Install plugin from ClawHub, enable, disable, uninstall via CLI | End-to-end walkthrough |

### CLI

| Test type | Coverage | How |
|---|---|---|
| Unit tests | `--json` global flag threads to all commands | Parse global flags, verify JSON output mode |
| Unit tests | `--no-color` disables themed output | Parse flag, verify plain text output |
| Integration tests | `observe --stream` receives and prints events | Start daemon, trigger event, verify CLI stdout |
| Integration tests | `logs --follow` tails new log lines | Start daemon, write log, verify CLI stdout |
| Integration tests | `plugins enable/disable` toggles plugin state | Install test plugin, enable, disable, verify lifecycle state |
| Manual | Guided config wizard produces valid configuration | Run `metiq setup`, follow prompts, verify config |

### Web UI

| Test type | Coverage | How |
|---|---|---|
| Manual | Tool cards render for tool-calling sessions | Start agent run with tools, verify cards appear in UI |
| Manual | Abort button stops active run | Start long run, click Stop, verify run cancels |
| Manual | Mobile layout works at 375px width | Use browser DevTools responsive mode |
| Manual | Session auto-resumes on page refresh | Open session, refresh page, verify history loads |
| Manual | Light/dark theme toggle works | Click toggle, verify all elements render correctly |
| Manual | Agent detail panel shows config | Click agent in sidebar, verify details panel |
| Manual | Channel cards show per-platform info | Open channels tab, verify platform-specific rendering |
| Browser test | Responsive breakpoints trigger at correct widths | Automated viewport-width tests |
| Browser test | Streaming text renders and finalizes correctly | Mock WebSocket, send chunks, verify DOM |
| Browser test | Approval modal queues and processes multiple approvals | Mock multiple approval events, verify queue behavior |

### Regression

- All existing plugin unit tests pass after permission enforcement changes
- All existing CLI tests pass after global flag additions
- Existing Cyberwave theme unchanged when dark mode is active after light theme addition
- WebSocket reconnect behavior unchanged after session auto-resume addition
- OpenClaw compatibility runtime still loads test plugin after registration guard addition

---

## Appendix: metiq Strengths

While this report focuses on gaps, several metiq capabilities are **comparable or stronger** than peers:

1. **Three-runtime plugin architecture** — Goja (safe in-process), Node.js (subprocess), and OpenClaw compat in one system is unique. Neither openclaw nor claude-code offers this flexibility.

2. **Goja sandboxing** — The in-process Goja runtime with explicit host API stubs, per-call timeouts, mutex serialization, and VM interrupt is the strongest plugin sandbox of the three systems.

3. **Nostr-based plugin distribution** — Publishing plugins as Nostr events with signature verification is a novel decentralized distribution mechanism not present in either peer.

4. **Scoped plugin lifecycle** — User/project/local scopes with resolution order is a clean design that provides good separation of concerns.

5. **Cyberwave UI aesthetic** — The distinctive visual identity differentiates metiq from generic dark themes. This is a brand asset worth preserving.

6. **Memory CLI depth** — 11 memory subcommands (search, import, stats, compact, health, repair, eval, list, backends, sync) is the deepest memory management CLI of the three systems.

7. **MCP CLI management** — 10 MCP subcommands with auth lifecycle is more comprehensive than either peer's MCP management surface.

8. **CLI command breadth** — 114 total command paths is substantial and covers most operational needs, even if some areas lag behind openclaw's depth.
