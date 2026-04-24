# Feature Parity Plan

**Path:** `swarmstr/docs/plan/FEATURE_PARITY_PLAN.md`
**Generated:** 2026-04-24
**Source Analysis:** openclaw, src (Claude Code), swarmstr/metiq

This plan focuses on the highest-value parity gaps identified for `swarmstr/metiq`, with emphasis on:

- Multi-agent orchestration
- Memory and context management
- Tool ecosystem
- Developer/plugin experience
- Security and permissions

---

## Summary

| Priority | Feature | Category | Source | Why it matters | Dependencies | Effort |
|---|---|---|---|---|---|---|
| 1 | Generalized tool permission engine | Security / Permissions | openclaw + src | Safely expands tools, plugins, MCP, and remote execution | None | XL |
| 2 | Background task ledger | Multi-agent / Automation | openclaw + src | Makes ACP, cron, webhook, and detached work inspectable and operable | None | L |
| 3 | Durable workflow / taskflow substrate | Multi-agent / Automation | openclaw + src | Enables auditable long-running multi-step orchestration beyond ACP pipeline | #2 | XL |
| 4 | Typed plugin SDK and manifest contract | Plugins / DevEx | openclaw + src | Creates a stable extension platform for channels, tools, hooks, MCP, and providers | #1 recommended | XL |
| 5 | Broader MCP developer surface | MCP / Tool Ecosystem | src | Turns strong MCP core into a developer-facing capability layer | #1, #4 optional | L |
| 6 | Deeper developer tool suite | Tools / Coding Agent | openclaw + src | Improves coding-agent usefulness for file editing, browser work, and project navigation | #1, #5 helpful | XL |
| 7 | Advanced memory curation layer | Memory / Context | openclaw | Improves long-term recall quality, reviewability, and knowledge durability | #2 helpful, #3 optional | XL |
| 8 | Plugin lifecycle scopes + plugin/skill integration | Plugins / Skills / Ops | src + openclaw | Makes plugins safer to operate and easier to adopt per user/project/workspace | #4 | L |

---

## Planning Principles

1. **Preserve Nostr-first architecture.** New capability layers should fit the existing Nostr-native daemon model rather than copy peer implementations literally.
2. **Build foundations before breadth.** Permissioning, task tracking, and extension contracts should land before large new tool/plugin surfaces.
3. **Prefer inspectable state.** New orchestration and memory features should be queryable via CLI/API and auditable in stored state.
4. **Keep explicit operator control.** Especially for plugins, skills, MCP, and approvals.
5. **Reuse existing strong subsystems.** Metiq already has good MCP runtime, memory primitives, cron/webhooks, and ACP transport.

---

# 1) Generalized Tool Permission Engine

## Overview

A unified permission engine would govern all tool execution in one place: built-in tools, plugin tools, MCP tools, sandbox/exec calls, network fetches, file writes, and remote agent actions.

This matters because metiq is a remotely addressable daemon. As tool breadth grows, ad hoc approvals become a scaling and security bottleneck.

## Current State

Metiq already has several security controls, but they are fragmented:

- exec approval flow and approvals CLI are surfaced in CLI/docs
- sandbox backends exist (`nop`, `docker`) with resource controls
- DM access control exists (`pairing`, `allowlist`, `open`, `disabled`)
- webhook auth and agent allowlists exist
- Nostr control RPC requires signed caller identity
- MCP has its own allow/deny and remote-approval policy model in `internal/mcp/config.go`

What is missing is a **single typed permission model** that spans all tools and all sources.

## Target State

A central permission subsystem should provide:

- rule behaviors: `allow`, `ask`, `deny`
- layered scopes: global/user, project/workspace, agent, session, ephemeral override
- tool metadata classification:
  - read-only
  - destructive
  - networked
  - filesystem-writing
  - secret-touching
  - remote-control / delegation
- evaluator support for:
  - exact tool names
  - tool groups
  - host/domain/path matchers
  - working-directory or workspace scope
  - MCP server / plugin source matchers
- interactive approvals plus non-interactive policies
- audit trail for why a decision was made

## Key Implementation Steps

1. **Define permission model**
   - Add a typed policy schema for rules, scopes, and decision reasons.
   - Model tool metadata/capabilities so evaluation is not string-only.

2. **Create a central evaluator**
   - Build one permission evaluation path used by built-in tools, plugin tools, MCP tools, and gateway-triggered actions.
   - Support ordered rule precedence and explicit decision reasons.

3. **Annotate existing tools**
   - Classify `exec`, file I/O, web fetch/search, Nostr tools, sandbox, ACP delegation, and future plugin tools.
   - Introduce tool groups for policy convenience.

4. **Add approval queue and persistence**
   - Normalize approval requests/resolutions into one system.
   - Persist approval records for CLI/API inspection.

5. **Integrate with runtime entry points**
   - Agent tool runner
   - gateway method execution
   - webhook-triggered isolated runs
   - ACP worker execution
   - MCP tool/resource access

6. **Expose operator UX**
   - Add `metiq permissions ...` CLI surface
   - Add gateway methods for list/get/update permissions
   - Surface decision reasons in logs/events

7. **Document policy model**
   - Config examples
   - safe defaults
   - non-interactive behavior

## Files/Packages to Touch

- **Existing**
  - `cmd/metiq/main.go`
  - `internal/policy`
  - `internal/mcp/config.go`
  - `docs/gateway/sandboxing.md`
  - `docs/channels/index.md`
  - `docs/concepts/features.md`
- **Likely packages to add/extend**
  - `internal/permissions`
  - `internal/agent`
  - `internal/gateway`
  - `internal/plugins`
  - `internal/store/state`

## Dependencies

- None required
- Recommended before expanding plugin, MCP, and developer tool surfaces

## Estimated Effort

**XL**

---

# 2) Background Task Ledger

## Overview

A background task ledger is a first-class record of detached work: ACP runs, cron executions, webhook-triggered turns, approvals, sandbox jobs, and future workflow steps.

This matters because metiq already does detached work, but operators lack a unified, queryable ledger for status, history, and troubleshooting.

## Current State

Metiq has:

- ACP dispatch/pipeline behavior
- cron scheduling and persistence
- webhook-triggered isolated runs
- session transcripts and session store
- approvals CLI
- logs/observe/status CLI surfaces

But it does **not** have a canonical task object model with lifecycle states, lineage, and auditable status.

## Target State

A task ledger should provide:

- task records with:
  - `id`
  - `type`
  - `source`
  - `status`
  - `agent_id`
  - `session_key`
  - `parent_task_id`
  - `created_at/started_at/finished_at`
  - `result/error`
- status model such as:
  - `pending`
  - `running`
  - `blocked`
  - `waiting_approval`
  - `completed`
  - `failed`
  - `cancelled`
- task lineage for ACP delegation and future workflows
- operator surfaces:
  - list
  - show
  - audit/history
  - cancel/retry where applicable

## Key Implementation Steps

1. **Define task schema**
   - Create canonical task types and status enums.
   - Include parent/child relationships and source metadata.

2. **Choose persistence model**
   - Store in local daemon state, ideally aligned with existing session/state patterns.
   - Decide retention/pruning policy.

3. **Instrument detached work producers**
   - ACP dispatch/pipeline
   - cron job execution
   - webhook `/hooks/agent`
   - sandbox executions that outlive a single turn
   - approval requests/resolutions

4. **Emit events**
   - State change events for UI/CLI observers.
   - Structured audit events for logs/observe.

5. **Add CLI/API surface**
   - `metiq tasks list`
   - `metiq tasks show <id>`
   - `metiq tasks audit`
   - optional `cancel` / `retry`

6. **Link tasks to sessions**
   - Record session key / transcript association so operators can pivot between task and conversation history.

## Files/Packages to Touch

- **Existing**
  - `cmd/metiq/main.go`
  - `docs/automation/cron-jobs.md`
  - `docs/automation/webhook.md`
  - `docs/concepts/multi-agent.md`
  - `docs/reference/session-management-compaction.md`
- **Likely packages to add/extend**
  - `internal/store/state`
  - `internal/acp`
  - `internal/gateway`
  - `internal/agent`
  - `internal/cron`
  - `internal/approvals`

## Dependencies

- None required
- Should precede durable workflow/taskflow work

## Estimated Effort

**L**

---

# 3) Durable Workflow / Taskflow Substrate

## Overview

This is the layer above individual ACP dispatches and cron jobs: durable, inspectable, revisioned multi-step workflows with retries, cancellation, and run history.

This matters because ACP pipeline already gives metiq basic sequential/parallel delegation, but high-value Nostr-native use cases need long-running, restart-safe orchestration.

## Current State

Metiq currently supports:

- `/spawn` and agent routing
- `acp_delegate`
- `acp.dispatch`
- `acp.pipeline` sequential and parallel execution
- cron calling messages or gateway methods
- webhook-triggered isolated runs

What is missing is a **durable workflow runtime** with persisted definitions, run state, and step-level inspection.

## Target State

A workflow subsystem should provide:

- named workflow definitions
- versioned revisions
- workflow runs with persisted state
- step graph support:
  - sequential
  - parallel
  - dependency-based
- step actions:
  - agent turn
  - ACP delegation
  - gateway method
  - wait/timer
  - approval gate
- controls:
  - list/show/cancel/retry
  - pause/resume
  - inspect step outputs and failures

## Key Implementation Steps

1. **Define workflow model**
   - Workflow definition schema
   - Run schema
   - Step schema
   - Revisioning/version metadata

2. **Implement orchestrator**
   - Load definition
   - Create run
   - Schedule ready steps
   - Persist transitions
   - Handle retry/cancel semantics

3. **Reuse task ledger**
   - Represent workflow steps as tasks or task-backed step records.
   - Capture step lineage and audit trails.

4. **Support action types**
   - local agent turn
   - ACP dispatch
   - gateway method call
   - cron/webhook entry
   - approval/wait nodes

5. **Add operator surfaces**
   - CLI: `metiq workflows ...` or `metiq tasks flow ...`
   - gateway methods for list/show/cancel/run
   - event streaming for run progress

6. **Integrate automation entry points**
   - cron should be able to trigger workflows
   - webhooks should be able to trigger workflows
   - future standing-order / autonomous behavior can target workflows

7. **Document authoring format**
   - JSON/YAML examples
   - Nostr-native remote-agent workflow examples

## Files/Packages to Touch

- **Existing**
  - `cmd/metiq/main.go`
  - `docs/automation/cron-jobs.md`
  - `docs/automation/webhook.md`
  - `docs/concepts/multi-agent.md`
  - `README.md`
- **Likely packages to add/extend**
  - `internal/workflow`
  - `internal/acp`
  - `internal/store/state`
  - `internal/gateway`
  - `internal/agent`
  - `internal/cron`

## Dependencies

- **Required:** #2 Background task ledger
- **Recommended:** #1 Permission engine for safe step execution

## Estimated Effort

**XL**

---

# 4) Typed Plugin SDK and Manifest Contract

## Overview

A typed plugin SDK and manifest contract would turn metiq's current plugin runtime/install system into a real extension platform.

This matters because today metiq can install and run plugins, but long-term ecosystem growth needs stable declarations, validation, capability registration, and better developer ergonomics.

## Current State

Metiq already has:

- Goja and Node.js plugin runtimes
- plugin install/search/publish flows
- Nostr registry support
- channel plugins as a major extension mechanism
- skills intentionally separated from plugin discovery

What is missing is a **documented, versioned contract** for what plugins may declare and how they register capabilities.

## Target State

A plugin platform should provide:

- a versioned manifest schema
- explicit capability declarations for:
  - tools
  - channels
  - hooks
  - MCP servers
  - skills
  - settings/config
  - providers (later if staged)
- runtime registration APIs for JS/Node plugins
- manifest validation at install/publish/load time
- plugin capability metadata usable by:
  - permission engine
  - CLI help
  - plugin registry UI
  - operator policy

## Key Implementation Steps

1. **Design manifest schema**
   - plugin identity/version/source
   - capability sections
   - permissions/capability declarations
   - compatibility/runtime constraints

2. **Version the contract**
   - Add manifest versioning and runtime compatibility checks.

3. **Implement plugin host registration API**
   - Define a stable JS/Node host bridge.
   - Start with tools, channels, hooks, MCP servers, and skills/settings.

4. **Validate at lifecycle boundaries**
   - validate on publish
   - validate on install
   - validate on load/enable

5. **Surface plugin metadata in CLI/API**
   - list declared capabilities
   - show required permissions
   - show provided channels/tools/MCP servers

6. **Add examples/templates**
   - minimal plugin
   - channel plugin
   - tool plugin
   - MCP provider plugin

7. **Document the SDK**
   - authoring guide
   - manifest reference
   - runtime API reference

## Files/Packages to Touch

- **Existing**
  - `cmd/metiq/main.go`
  - `README.md`
  - `docs/channels/index.md`
  - `docs/tools/skills.md`
  - `docs/index.md`
- **Likely packages to add/extend**
  - `internal/plugins`
  - `internal/plugins/registry`
  - `internal/gateway`
  - `internal/agent`
  - `internal/store/state`
  - `docs/plugins/` (new docs area)

## Dependencies

- **Recommended:** #1 Permission engine so plugin-declared capabilities can be governed safely
- Enables #8 directly

## Estimated Effort

**XL**

---

# 5) Broader MCP Developer Surface

## Overview

Metiq already has one of its strongest core implementations in MCP. The next step is to make that strength more accessible to operators, plugin authors, and agents.

This matters because MCP is the fastest path to growing the tool ecosystem without hardcoding everything into the daemon.

## Current State

Metiq already has:

- resolved MCP config model
- source precedence and suppression logic
- allow/deny and remote-approval policy
- OAuth support for remote servers
- runtime manager with connect/reconnect/refresh/snapshot behavior
- CLI `mcp` surface

Missing or partial areas include:

- broader transport set (notably WebSocket)
- agent-facing MCP resource/tool access surfaces
- richer config scopes/source types
- plugin-declared MCP servers
- better operator auth/approval UX

## Target State

The MCP subsystem should provide:

- broader transport support:
  - stdio
  - SSE
  - HTTP
  - WebSocket
- richer source model:
  - built-in/default
  - user/project
  - plugin-provided
  - dynamic/runtime
- agent-facing tools:
  - list MCP resources
  - read MCP resources
  - inspect MCP prompts/capabilities
- improved operator UX:
  - explicit auth state
  - approve/connect/reconnect flows
  - capability inventory
- plugin SDK hooks for contributed MCP servers

## Key Implementation Steps

1. **Extend config/runtime types**
   - Add WebSocket transport.
   - Add richer source provenance beyond `extra.mcp`.

2. **Broaden manager snapshots**
   - Include prompt/resource inventories where useful.
   - Preserve source/signature/auth state for operator inspection.

3. **Expose MCP inventory through CLI/API**
   - list connected servers
   - show capabilities/resources/prompts
   - reconnect/refresh/auth status

4. **Add agent-facing MCP tools**
   - resource list
   - resource read
   - optional prompt discovery

5. **Integrate with plugin manifests**
   - allow plugins to declare MCP servers
   - annotate source provenance clearly

6. **Tighten approval/auth flows**
   - remote server approval should use the new permission model where possible
   - make auth-needed states actionable from CLI

## Files/Packages to Touch

- **Existing**
  - `internal/mcp/config.go`
  - `internal/mcp/manager.go`
  - `internal/mcp/manager_types.go`
  - `cmd/metiq/main.go`
- **Docs**
  - `docs/index.md`
  - `docs/concepts/features.md`
  - `docs/gateway/nostr-control.md`
- **Likely packages to add/extend**
  - `internal/gateway`
  - `internal/agent`
  - `internal/plugins`

## Dependencies

- **Recommended:** #1 Permission engine
- **Optional:** #4 Plugin SDK/manifest for plugin-provided MCP declarations

## Estimated Effort

**L**

---

# 6) Deeper Developer Tool Suite

## Overview

Metiq has the basics for coding assistance, but it lacks the deeper tool set that makes `src` and OpenClaw more capable as day-to-day coding agents.

This matters because a Nostr-native coding agent becomes much more useful when it can do structured edits, project navigation, browser work, and richer dev-environment operations safely.

## Current State

Metiq currently documents/supports:

- exec
- sandboxed execution
- file I/O
- web fetch
- web search
- canvas
- Nostr-native tools

Missing or partial areas include:

- structured patch application (`apply_patch`)
- browser automation
- LSP/symbol-aware project tooling
- notebook/REPL-style dev tooling
- worktree-oriented flows

## Target State

The near-term target should prioritize the highest-leverage coding tools:

### Phase A
- `apply_patch`
- structured edit/diff tool
- better project search/navigation

### Phase B
- LSP-backed symbol/query/refactor tools
- diagnostics/navigation

### Phase C
- browser automation
- optional notebook/REPL or MCP-backed browser/dev integrations

## Key Implementation Steps

1. **Prioritize tool roadmap**
   - Start with `apply_patch` and symbol/project navigation before broader UI-heavy tools.

2. **Add structured patch/edit tool**
   - Apply multi-hunk patches safely
   - validate patch bounds
   - produce auditable diffs/results

3. **Add richer project intelligence**
   - LSP-backed symbol lookup
   - references/definitions
   - diagnostics readout

4. **Decide browser strategy**
   - native browser automation
   - or MCP/browser integration as first delivery

5. **Integrate with permissions**
   - patch/edit/write tools must participate in unified policy
   - browser/network tools need host/domain controls

6. **Improve operator feedback**
   - tool result summaries
   - persisted artifacts for large outputs
   - better CLI/web UI rendering where needed

## Files/Packages to Touch

- **Existing**
  - `README.md`
  - `docs/concepts/features.md`
  - `docs/gateway/sandboxing.md`
- **Likely packages to add/extend**
  - `internal/agent`
  - `internal/tools`
  - `internal/sandbox`
  - `internal/gateway`
  - `internal/mcp`
  - `go.mod`

## Dependencies

- **Required:** #1 Permission engine
- **Helpful:** #5 MCP developer surface for browser/dev integrations

## Estimated Effort

**XL**

---

# 7) Advanced Memory Curation Layer

## Overview

Metiq already has strong memory primitives. The next high-value step is a curation layer that improves recall quality over time: promotion, review, backfill, and compiled knowledge.

This matters because long-lived agents degrade when memory remains raw, unreviewed, or purely append-only.

## Current State

Metiq already has a strong base:

- file-backed memory with `MEMORY.md` plus typed topic files
- scoped memory surfaces (`user`, `project`, `local`)
- indexed memory with pinned vs stored entries
- JSON-FTS / Qdrant / hybrid backend
- maintained session memory artifact
- compaction-coupled memory refresh
- team-memory foundation
- doctor/health surface

What is missing is a **higher-order curation layer** like OpenClaw's dreaming/backfill/wiki-style knowledge management.

## Target State

A curation layer should provide:

- promotion pipeline from raw/session memories into durable curated memory
- grounded backfill over historical notes
- compiled knowledge surface with:
  - topic pages
  - claims/evidence
  - freshness markers
  - contradiction tracking
- review artifacts for operators
- scheduled maintenance jobs

## Key Implementation Steps

1. **Define memory promotion pipeline**
   - candidate capture from session memory, raw notes, and indexed memory
   - thresholds and ranking rules
   - explicit reviewed promotion into durable memory

2. **Add grounded backfill**
   - scan historical `memory/*.md` and daily notes
   - extract durable candidates
   - store review artifacts rather than auto-promote everything

3. **Create compiled knowledge layer**
   - materialize curated topic pages from durable memory
   - preserve provenance and freshness metadata

4. **Add operator tools/commands**
   - inspect candidates
   - approve/reject promotions
   - rebuild compiled knowledge
   - get specific memory/topic content

5. **Integrate with compaction and automation**
   - pre-compaction flush should feed candidate capture
   - cron/workflow-driven review jobs should be supported

6. **Expose health/debug surfaces**
   - memory candidate counts
   - promotion backlog
   - stale compiled pages
   - curation run status

## Files/Packages to Touch

- **Existing**
  - `docs/concepts/memory.md`
  - `docs/reference/session-management-compaction.md`
  - `docs/automation/cron-jobs.md`
  - `cmd/metiq/main.go`
- **Likely packages to add/extend**
  - `internal/memory`
  - `internal/store/state`
  - `internal/agent`
  - `internal/cron`
  - `internal/gateway`

## Dependencies

- **Helpful:** #2 Background task ledger for background curation runs
- **Optional:** #3 Workflow substrate for multi-phase review/promotion pipelines

## Estimated Effort

**XL**

---

# 8) Plugin Lifecycle Scopes + Plugin/Skill Integration

## Overview

Once metiq has a stronger plugin manifest/SDK, it should improve plugin operations and allow carefully controlled plugin-to-skill integration.

This matters because today plugin adoption is powerful but operationally coarse. Project-scoped install/enablement and opt-in skill integration would make extensions safer and more practical.

## Current State

Metiq currently has:

- plugin install/search/publish
- sources including path/npm/url/Nostr registry
- explicit skill catalog with clear precedence
- intentional separation between plugins and automatic skill discovery

Missing or partial areas include:

- scoped installs (`user`, `project`, `local`)
- enable/disable/update lifecycle controls
- per-scope resolution/precedence
- optional plugin-provided skills/commands with explicit operator control

## Target State

The plugin lifecycle should provide:

- scoped installation:
  - user
  - project/workspace
  - local/session
- explicit states:
  - installed
  - enabled
  - disabled
  - update available
- scope-aware resolution and precedence
- registry refresh/reconciliation
- **opt-in** plugin skill export:
  - explicit manifest declaration
  - source labeling in skills status
  - operator enable/disable control
  - no silent prompt flooding

## Key Implementation Steps

1. **Add scoped install model**
   - define installation records and state files by scope
   - decide precedence rules

2. **Extend CLI lifecycle**
   - `plugins enable`
   - `plugins disable`
   - `plugins update`
   - scope flags for install/list/remove

3. **Resolve plugin loading by scope**
   - merge user/project/local views deterministically
   - support workspace-local plugin policy

4. **Add opt-in plugin skill contract**
   - manifest section for exposed skills
   - explicit enablement path
   - source labeling in `skills status`

5. **Integrate catalog invalidation**
   - plugin lifecycle changes should refresh skills/commands cleanly

6. **Preserve explicitness**
   - plugin skills should remain opt-in, not automatic by default
   - maintain clear operator visibility into what enters prompt context

## Files/Packages to Touch

- **Existing**
  - `cmd/metiq/main.go`
  - `docs/tools/skills.md`
  - `docs/index.md`
  - `README.md`
- **Likely packages to add/extend**
  - `internal/plugins`
  - `internal/plugins/registry`
  - `internal/store/state`
  - `internal/agent`
  - `internal/gateway`

## Dependencies

- **Required:** #4 Typed plugin SDK and manifest contract

## Estimated Effort

**L**

---

## Recommended Delivery Sequence

| Wave | Items | Goal |
|---|---|---|
| Wave 1 | #1, #2 | Establish safety and observability foundations |
| Wave 2 | #3, #4 | Enable durable orchestration and stable extension contracts |
| Wave 3 | #5, #8 | Expand MCP and plugin operations safely |
| Wave 4 | #6, #7 | Add high-value coding tools and long-term memory quality improvements |

---

## Suggested Milestone Framing

| Milestone | Scope |
|---|---|
| M1: Safe Expansion | Permission engine + task ledger |
| M2: Orchestration Core | Workflow substrate + task-backed runs |
| M3: Platform Surface | Plugin SDK + scoped lifecycle + MCP expansion |
| M4: Power-User Capability | Developer tools + memory curation |

---

## Final Notes

Metiq already has strong foundations in:

- Nostr-native transport and identity
- ACP-style remote delegation
- MCP runtime management
- scoped memory and session handling
- cron/webhook automation

The highest-value parity work is therefore **not** "copy everything from peers," but rather:

1. make powerful features **safe**,
2. make detached work **inspectable**,
3. make extensions **stable and operable**,
4. then broaden tools and memory sophistication.

That sequence best fits metiq's Nostr-native architecture and should deliver the most user-visible value with the least platform risk.
