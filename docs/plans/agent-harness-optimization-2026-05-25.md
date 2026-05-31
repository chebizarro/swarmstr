# Agent Harness Optimization: Comprehensive Analysis Plan

## Goal

Identify and prioritize optimization opportunities across seven dimensions of the Metiq/Swarmstr AI agent harness — prompt cache efficiency, response latency, system footprint, environment fit (Docker + bare-metal), growth control, multi-tasking, and self-healing — with actionable work items for each.

## Background

### Prompt Cache Efficiency

The prompt assembly pipeline (`internal/agent/llm.go:112-231`) explicitly separates static and dynamic system prompt content into typed lanes (`PromptLaneSystemStatic`, `PromptLaneDynamicContext`, `PromptLaneCurrentUser` — `llm.go:14-22`). Static system prompt gets `CacheControl{Type: "ephemeral"}` for Anthropic cache boundaries; dynamic additions receive no cache control (`llm.go:219-231`).

**Provider-specific caching:**
- **Anthropic**: per-block `cache_control` on system parts and last tool definition (`chat_anthropic.go:118-133, 416-430`). Enabled by default (`prompt_cache.go:134-143`).
- **Gemini**: explicit `CachedContent` resources with 1h TTL, keyed by model+system text, min 2000 chars (`chat_gemini_cache.go:40-61, 193-233`). System+tools omitted from request when cache hit (`chat_gemini.go:131-147`).
- **OpenAI-compatible**: no per-block metadata; relies on message ordering for prefix caching. `DynamicContextPlacementLateUser` moves volatile context after history to preserve prefix stability (`llm.go:138-143, 180-191`). llama-server sends `"cache_prompt": true` (`chat_openai.go:145-152`); vLLM is layout-only.

**Context engines** produce `SystemPromptAddition` as non-cacheable material ("treated as non-cacheable prompt material so per-turn context churn does not bust the reusable system-prompt cache prefix" — `engine.go:69-74`). Both windowed and small-window engines inject summaries/recall into this addition. Cache telemetry tracks changes and marks `CacheBroken = true` with `BreakReason` when the addition changes (`lifecycle.go:436-479`).

### Response Latency — Hot Path Analysis

**Pre-LLM work** (per turn): mutation tracking setup, micro-compaction of old tool results, deferred-tool setup, context pruning, steering drain, tool-result guarding, context budget enforcement, tool schema normalization (`agentic_loop.go:146-249`).

**Turn checkpoint persistence** serializes 4 JSON blobs synchronously before each tool execution (`agentic_loop.go:528-551`). This is repeated every tool iteration.

**Tool execution** is batch-based: consecutive concurrency-safe tools run in parallel (goroutine + semaphore capped by `CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY`, default 10 — `agentic_loop.go:1236-1241`); unsafe tools run serially. Each tool call has pre/post hooks, mutation tracking, loop detection, and policy evaluation (`agentic_loop.go:925-1149`).

**Nostr DM → agent dispatch pipeline** (`main.go:4320-4404`): DM allow policy, rate limiting, session auto-rotation, ACP fast-path, then `runInboundTurn` which: acquires per-session turn slot (queue/steer/interrupt on busy — `main.go:3574-3651`), persists inbound transcript, constructs turn context, resolves memory scope, **synchronously** ingests message into context engine, **synchronously** builds dynamic memory recall (SQLite FTS queries — `sqlite.go:341-420`), assembles context-engine history (may trigger auto-compaction), resolves runtime/tool surface, builds prompt envelope, then calls provider.

**Post-LLM work**: persist tool traces, ingest turn history, distill memory (async), update task state, persist telemetry, send reply via Nostr, persist assistant message, record token accounting — mostly synchronous (`main.go:4138-4236`).

**SessionStore** is journaled JSON with mutex + fsync on every mutation (`session_store.go:822-953`). Multiple synchronous writes happen in the hot path: session update, telemetry, memory recall artifacts, token accounting.

### System Footprint — File Locations

**Primary home**: `~/.metiq/` (Docker: `/data/.metiq/`). Contents:
- `config.json`, `bootstrap.json`, `.env` — config/secrets
- `sessions.json` + `.journal` — session state
- `tasks.json` — task queue
- `memory.sqlite` — memory DB (WAL mode)
- `memory-index.json` — JSON FTS fallback index
- `memory-backups/` — weekly SQLite backups (4-week retention)
- `logs/commands.log` — command logger hook output
- `hooks/` — managed hooks
- `skills/` — managed skills
- `plugins/`, `plugins/storage/` — user plugins
- `mcp-auth.json` — MCP auth tokens
- `metiqd.pid`, `metiqd.log` — daemon mode files

**Workspace-local** (per project):
- `.metiq/agent-memory`, `.metiq/agent-memory-local`, `.metiq/agent-memory-snapshots` — durable memory
- `.metiq/session-memory` — session memory
- `.metiq/team-memory`, `.metiq/team-memory-sync` — team memory
- `.metiq/skills` — workspace skill mirrors
- `.metiq/plugins` — project plugins

**System temp**: plugin downloads, Node shims, skill fallback use OS temp dir (`/tmp` or systemd private tmp).

**Docker image**: multi-stage Debian bookworm, static Go binaries + `/app/skills`. Optional layers: Python+uv, Node.js 24, Chromium+Xvfb (~300MB), Docker CLI (~50MB). Volume at `/data`, non-root `metiq` user, healthcheck on `:7423/health`.

**Systemd**: user service, `PrivateTmp=yes`, `ProtectSystem=strict`, writable: `~/.metiq`, `~/.local/share/metiq`, private `/tmp`.

### Growth Vectors — Gaps and Tuning

**Unbounded / high-risk:**
1. `~/.metiq/logs/commands.log` — append-only, no rotation/size cap (`handler_command_logger.go:46-52`)
2. `~/.metiq/tasks.json` — no TTL, no automatic pruning of completed tasks (`taskqueue.go:38-87, 196-229`)
3. `acp_tasks.json` — no auto-retention (`acp/task_store.go:300-423`)
4. `memory.sqlite` — no global row limit, no VACUUM/auto-vacuum policy; WAL mode only (`sqlite.go:124-144, 160-240`). Provenance and conflict tables append-only.
5. Session cardinality — in-memory maps (`sessions`, `summaries`, `promptCacheLast`, autocompact state, session-memory compact state) grow per unique session ID with no global cap; boot pruning is opt-in (`main.go:6721-6732`)
6. `sessions.json` — unbounded session count; per-session arrays are capped but total sessions are not

**Conditionally bounded:**
- Memory outbox: successful publishes deleted, failed expire after 7d/compact after 30d; pending depth unbounded while publishing broken (`nostr_outbox.go:96-342`)
- `agentJobRegistry`: finished jobs cleaned after 5min delay goroutine; stuck pending jobs have no cap (`runtime_semantics.go:463-514`)
- `sessionTurnHandoffRegistry`, `eventInFlightRegistry`: rely on callers to consume/end; no TTL (`main_persistence.go:446-525`)

**Well-bounded:**
- In-memory ring buffers: 2K entries (`runtime_semantics.go:112-137`)
- Embedding cache: MaxEntries eviction (`embedding_cache.go:326-346`)
- DM rate-limiter: bucket pruning every 10min (`main.go:2453-2463`)
- Node/cron/approval/wizard caps: 1000/500/200/100 with 15min cleanup (`runtime_semantics.go:610-620`)

### Multi-tasking

**Sub-agent lifecycle**: depth-limited (max 5 — `runtime_semantics.go:276`), not count-limited. Spawn creates goroutine via `launchManagedRun` (`agent_run_orchestrator.go:103-186`). Cleanup is opportunistic (runs before each spawn, not background — `agent_run_orchestrator.go:109`). Stale after 2h, recently-ended relevant for 30min (`subagent_liveness.go:13-24`). Reactivation replaces ended runs atomically (`subagent/reactivate.go:37-113`).

**Session concurrency**: one active turn per session, cross-session parallel. Busy sessions queue/steer/interrupt per queue mode (`main.go:3569-3722`). Queue drain spawns goroutine for next pending turn.

**Tool concurrency**: semaphore-bounded parallel batches (default 10), serial for unsafe tools. No general worker pool.

**Goroutine management**: per-run `runWG` + timeout context + panic recovery (`agent_run_orchestrator.go:196-411`). Job cleanup uses per-job 5min sleep goroutine — can accumulate sleepers under high churn.

### Self-Healing

**Crash recovery**: fresh in-memory job/subagent registries on boot (active runs lost — `main.go:1465-1505`). Cron jobs restored from state store. Session metadata survives via journaled `sessions.json`. Turn-level checkpoints exist (`agentic_loop.go:88-93, 220-231`) but depend on caller wiring.

**SQLite recovery**: weekly backups (4-week retention), integrity check on startup, quarantine+restore+rebuild chain (`sqlite_recovery.go:83-177, 232-347`). Backups use `PRAGMA wal_checkpoint(FULL)` + `VACUUM INTO` (`sqlite_recovery.go:302-323`).

**Error recovery**: LLM context overflow retries once after compaction (`agentic_loop.go:430-456, 600-609`). Tool failures become error tool results, critical errors set `LoopBlocked`. Runtime-level fallback retries for 429/rate-limit/model-not-found (`agent_run_orchestrator.go:247-259, 340-354`).

**Upgrades**: check-only (`update/checker.go:1-79`), no self-update. Manual curl-based install (`docs/install/updating.md`, `scripts/install.sh`).

**Backup/restore**: CLI `backup` command writes zip, `restore` extracts to `~/.metiq`. Config writes are atomic (temp+fsync+rename — `file.go:103-127`). Session store is journaled with rollback.

### Prior Art

- `docs/analysis/novel-features-assessment.md` — recommends performance benchmarking harness (`scripts/perf/`, 1 week estimate)
- `docs/reviews/agent1-runtime-streaming-providers.md` — identifies streaming delta throttling gap, praises prompt caching/model routing
- `docs/reviews/agent4-multi-agent-acp-review.md` — gaps in task persistence, event ledger/replay, turn latency stats
- `docs/reviews/memory-context-review.md` — memory/context "impressively mature"; gaps in prompt-cache awareness/telemetry, subagent lifecycle hooks
- `docs/concepts/architecture.md` — single long-lived `metiqd`, Nostr primary transport
- `docs/concepts/queue.md` — one active turn per session, cross-session parallel, bounded queues
- `docs/plan/active-run-steering-architecture.md` — event-driven steering shipped, no polling/sleeps
- Existing benchmarks: `scripts/perf/bench-cli-startup.sh`, `scripts/bench-cli-startup.ts`, `scripts/bench-model.ts`, Go benchmarks for ack fast path, canvas handler, memory promotion/cosine/in-memory search
- No production pprof integration found; `skills/perf-profile/SKILL.md` documents manual profiling

## Overall Strategy

**Size scale:** S = 1–2 engineering days · M = 3–5 engineering days · L = 1–2 engineering weeks · XL = multi-week / cross-subsystem

### Guiding Principles

1. **Instrument first, then optimize.** All hot-path changes are gated by measured baselines using existing scripts (`scripts/perf/`, `scripts/bench-cli-startup.ts`, `scripts/bench-model.ts`) extended to cover the daemon turn path.

2. **Preserve cache-stable prefixes before reducing provider latency.** Prompt cache efficiency changes come before provider/routing work because Anthropic/Gemini/OpenAI-compatible caching already exists and is the cheapest latency win.

3. **Split durability into crash-critical vs deferrable work.** Synchronous writes remain for state that affects correctness on crash/restart; telemetry, derived memory artifacts, and similar non-critical writes move to end-of-turn batching or async flush.

4. **Bound every growth surface locally.** Reuse existing bounded-state patterns (e.g. `EmbeddingCache.Prune()`, ring buffers in `runtime_semantics.go`). Do not add a new central "retention manager."

5. **Self-update remains out of scope.** `internal/update/checker.go` stays check-only unless the product decision changes (see Open Question 4).

### Key Invariants (apply across all work items)

- Do not replace `RunAgenticLoop`, `ChatProvider`, `SessionStore`, or the queue model wholesale — the code already separates concerns well enough for incremental optimization
- Maintain one active turn per session, same-run steering semantics, and tool-result ordering guarantees per `docs/plan/active-run-steering-architecture.md` and `docs/concepts/queue.md`
- Runtime semantics stay identical across Docker and bare metal; only packaging/ops defaults may differ

### Turn-Path Reference

The full inbound-turn call sequence (critical context for Work Items 1–4):

```
dmOnMessage → tracker.AlreadyProcessed → policy.EvaluateIncomingDM
  → dmRateLimiter.Allow → session auto-rotation → ACP/RPC fast path
  → dmRunAgentTurn → runInboundTurn
    → sessionTurns.TryAcquire (queue/steer/interrupt on busy)
    → persist inbound transcript
    → context-engine ingest
    → buildDynamicMemoryRecallContext (sync SQLite FTS)
    → context-engine assemble (may trigger auto-compaction)
    → resolve runtime/tool surface → build prompt envelope
    → RunAgenticLoop → provider.Chat
    → [tool loop: checkpoint → execute batch → append results → re-prune → provider.Chat]
    → POST-LLM: persistToolTraces → persistAndIngestTurnHistory
      → observeSessionMemoryRuntime → distillAndPersistMemory (async)
      → updateSessionTaskState → commitMemoryRecallArtifacts
      → persistTurnTelemetry → replyFn (Nostr send)
      → persistAssistant → sessionStore.AddTokens → mark processed
```

---

## Sequenced Work Items

### Work Item 1 — Baseline the Harness

**Goal:** Establish reproducible latency, cache, and durability baselines for the real turn path so later work can be justified and regression-checked.

**Done when:**
- A daemon-turn benchmark exists under `scripts/perf/` that exercises the full `runInboundTurn` path with: a synthetic inbound message (direct API or test harness, not live Nostr), a pre-seeded session with representative depth (~20 turns), and the default tool surface
- Measured spans exist for: session-store journal write time, memory recall query time (including whether duplicate per-turn queries exist — gates WI-4 scope), context-engine assemble time, provider call time, tool execution batch time, post-turn persistence time
- Baselines are captured in both Docker (`/data` named volume) and bare-metal/systemd (`~/.metiq` local FS) to quantify fsync cost difference (gates WI-3 priority)
- Baseline numbers are recorded and used to gate Work Items 2–8

**Key files:** `scripts/perf/bench-cli-startup.sh`, `scripts/bench-cli-startup.ts`, `scripts/bench-model.ts`, `cmd/metiqd/main.go`, `internal/agent/agentic_loop.go`, `internal/context/lifecycle.go`, `internal/memory/sqlite.go`, `internal/store/state/session_store.go`

**Dependencies:** None

**Size:** M

---

### Work Item 2 — Stabilize Prompt-Cache Inputs and Telemetry

**Goal:** Reduce avoidable cache misses by making dynamic context deterministic and by surfacing provider-specific cache-break causes already tracked by `PromptCacheInfo`.

**Done when:**
- `internal/context/lifecycle.go` remains the single cache-break attribution point
- Anthropic and Gemini keep using system-prompt cache boundaries; OpenAI-compatible providers default to `DynamicContextPlacementLateUser` where supported by `prompt_cache.go`
- `SystemPromptAddition` content is normalized to reduce non-semantic churn — the specific normalization strategy depends on what the two engine variants (`WindowedEngine`, `SmallWindowEngine`) inject: summary text diffs, recall fragment reordering, and whitespace variation are the likely churn sources to investigate in `engine.go:247-263` and `smallwindow_engine.go:172-203`
- Cache-break output is attributable to: summary changes, recall changes, dynamic-context placement changes, provider-profile changes
- The `SystemPromptAddition` contract in `engine.go:69-74` is explicitly preserved

**Key files:** `internal/agent/llm.go`, `internal/agent/prompt_cache.go`, `internal/context/engine.go`, `internal/context/lifecycle.go`, `internal/context/smallwindow_engine.go`

**Dependencies:** Work Item 1

**Size:** M

---

### Work Item 3 — Split Hot-Path Durability from Deferrable Persistence

**Goal:** Remove avoidable synchronous persistence from the reply-critical path without weakening crash correctness for active-turn state.

**Done when:**
- The post-LLM write sequence (see Turn-Path Reference) is classified into crash-critical vs deferrable:
  - **Crash-critical** (remain synchronous): `persistAndIngestTurnHistory`, `replyFn`, `persistAssistant`, `mark processed` — data needed to not replay or lose a completed turn
  - **Deferrable** (batch to end-of-turn or async): `persistToolTraces`, `commitMemoryRecallArtifacts`, `persistTurnTelemetry`, `sessionStore.AddTokens` — derived/analytics data that can be reconstructed
  - **Requires investigation**: `updateSessionTaskState`, `observeSessionMemoryRuntime` — classification depends on whether downstream logic reads these synchronously before next turn
- `SessionStore` writes are grouped so a turn does not call journal append repeatedly for deferrable fields
- `persistTurnCheckpoint(...)` in `agentic_loop.go:528-551` is no longer unconditional per tool iteration: it only writes when resume support is enabled for the turn, and skips writes when checkpoint state is unchanged
- Cache-telemetry writes in `lifecycle.go:436-479` remain synchronous (they feed WI-2's cache-break attribution and must not be deferred)
- Baseline re-runs show separate Docker vs bare-metal improvements

**Key files:** `internal/agent/agentic_loop.go`, `cmd/metiqd/main.go:4138-4236`, `internal/store/state/session_store.go:822-953`, `cmd/metiqd/main_persistence.go`

**Dependencies:** Work Item 1 (baselines gate the durability/latency tradeoff), coordinate with Work Item 2 (cache-telemetry writes in `lifecycle.go` must remain synchronous even after this change)

**Size:** L

**Risk:** Changing `SessionStore` write timing changes crash windows. Ship any deferred/batched mode behind config first; keep synchronous journaling as the default until baselines prove the benefit. *(Gates on Open Question 1.)*

---

### Work Item 4 — Reduce Synchronous Recall and Context-Assembly Latency

**Goal:** Shorten pre-provider turn setup by reusing existing cacheable recall/search behavior and removing duplicate same-turn recall work.

**Done when:**
- The synchronous recall call chain in `cmd/metiqd/main.go:3794-3839` is explicitly timed
- Same-turn duplicate queries are eliminated at the caller level (SQLite FTS cache in `sqlite.go:341-420` handles backend caching, but caller-side memoization may be needed)
- Context-engine ingest and assemble costs are measured separately
- The optimization does not move steering earlier than the existing model-boundary drains
- The plan records whether additional caller-side recall memoization is needed after `SQLiteBackend` cache hits are measured

**Key files:** `cmd/metiqd/main.go`, `internal/memory/sqlite.go:330-445`, `internal/context/engine.go`, `internal/context/lifecycle.go`, `internal/context/smallwindow_engine.go`

**Dependencies:** Work Items 1, 2 (cache stability before recall optimization)

**Size:** M

---

### Work Item 5 — Put Hard Bounds on All Growth Surfaces

**Goal:** Apply local retention/cap policies to every currently unbounded or weakly bounded store/log/registry.

**Done when:**
- Every growth surface identified in Background → "Growth Vectors" is bounded. Target bounds and retention policies are determined by WI-1 baselines; specific mechanisms are implementation choices. The surfaces to bound:
  - `commands.log` (`handler_command_logger.go:46-52`) — currently append-only, no rotation
  - Task queue (`taskqueue.go`) — completed/cancelled tasks retained indefinitely
  - ACP task store (`acp/task_store.go`) — terminal records retained indefinitely; use existing `EndedAt`/`CleanupAfter` fields
  - Nostr outbox pending depth (`nostr_outbox.go`) — must not silently discard fresh pending events
  - `sessionTurnHandoffRegistry` and `eventInFlightRegistry` (`main_persistence.go:446-525`) — no TTL cleanup
  - Global session count (`main.go:6721-6732`) — opt-in boot pruning only; needs runtime enforcement with dry-run metrics first
- `memory.sqlite` gets a retention policy for append-only tables (`memory_nostr_provenance`, conflict candidates — `nostr_sync.go:259-267, 293-323`) AND a periodic `VACUUM` to reclaim freed space (WAL-only at `sqlite.go:124-144` doesn't reclaim deletes)
- `embedding_cache.go:326-346` is cited as the reusable local boundedness pattern

**Key files:** `internal/hooks/handler_command_logger.go`, `internal/agent/toolbuiltin/taskqueue.go`, `internal/acp/task_store.go`, `internal/memory/nostr_outbox.go`, `cmd/metiqd/main_persistence.go`, `cmd/metiqd/runtime_semantics.go`, `cmd/metiqd/main.go`, `internal/memory/sqlite.go`, `internal/memory/embedding_cache.go`

**Dependencies:** Work Item 1 (baselines reveal which surfaces are actually large)

**Size:** L

**Risk:** Destructive cleanup requires additive terminal metadata for tasks rather than reinterpreting old fields. Sessions need dry-run metrics and operator visibility before enforcing hard caps. *(Gates on Open Question 2.)*

---

### Work Item 6 — Improve Multi-Tasking Controls

**Goal:** Raise safe concurrency by controlling background churn and unbounded subagent/job proliferation, not by allowing concurrent turns in a session.

**Done when:**
- `agent_run_orchestrator.go` enforces a live subagent count cap in addition to the existing depth limit (max 5 — `runtime_semantics.go:276`)
- Per-job delayed cleanup goroutines in `agentJobRegistry.Finish(...)` (`runtime_semantics.go:492-516`) are eliminated — the invariant is "no sleeper goroutine accumulation under churn"; the replacement mechanism (tick-based reaper, lazy cleanup, timestamped sweep) is an implementation choice
- `SubagentRegistry` gets periodic stale cleanup, not only opportunistic cleanup before spawn
- Queue pressure becomes observable (metrics/status), but `steer`, `interrupt`, and sequential queue behavior remain unchanged per `docs/concepts/queue.md`
- Tool concurrency remains semaphore-based via `getMaxToolUseConcurrency()`; no general worker-pool abstraction is added

**Key files:** `cmd/metiqd/agent_run_orchestrator.go:103-245`, `cmd/metiqd/runtime_semantics.go:250-535`, `cmd/metiqd/main.go`, `internal/agent/agentic_loop.go:1236-1241`

**Dependencies:** Work Items 1, 5 (growth bounds before concurrency expansion)

**Size:** M

---

### Work Item 7 — Formalize Docker and Bare-Metal Runtime Profiles

**Goal:** Make footprint and operational behavior predictable by declaring supported profiles instead of relying on implicit packaging combinations.

**Done when:**
- `Dockerfile` profiles are documented as: minimal runtime, full runtime, browser-enabled runtime — with approximate image sizes
- `docker-compose.yml` health behavior is aligned with the Dockerfile healthcheck fallback (the compose file always curls `127.0.0.1:7423/health` while the Dockerfile tolerates admin API disablement — resolve this mismatch)
- `metiqd.service` documents bare-metal assumptions: binary location, env file location, writable paths, optional dependency expectations
- The plan explicitly calls out that Docker image layering changes footprint and startup time, while systemd changes filesystem and sandbox behavior
- No feature behavior diverges between environments beyond packaging and supervision

**Key files:** `Dockerfile`, `docker-compose.yml`, `scripts/systemd/metiqd.service`, `docs/install/updating.md`

**Dependencies:** Work Item 1 (startup baselines inform profile documentation)

**Size:** S

**Note:** The `docker-compose.yml` healthcheck mismatch (compose always curls `127.0.0.1:7423/health` while Dockerfile tolerates admin API disablement) is a concrete bug to fix, not just documentation.

---

### Work Item 8 — Strengthen Self-Healing and Restart Recovery

**Goal:** Make crash/restart outcomes explicit and operator-visible, while avoiding unsafe automatic replay of in-flight mutating work.

**Done when:**
- Boot-time recovery in `main.go:1435-1505` distinguishes: recoverable turns, lost turns, lost background jobs/subagents/tasks — and reports each category
- Turn checkpoints from `agentic_loop.go:88-93, 220-231` are only used for explicit resume-safe cases; mutating tool calls are not auto-replayed by default (default restart outcome: "lost but visible")
- `sqlite_recovery.go` outcomes surface into status/health reporting rather than only logs
- ACP task state and subagent state degrade to explicit terminal statuses on restart instead of disappearing silently
- Docker restart behavior (`restart: unless-stopped`) and systemd `Restart=on-failure` are both documented as part of the same recovery model

**Key files:** `cmd/metiqd/main.go:1420-1525`, `internal/agent/agentic_loop.go:220-231, 430-456`, `internal/memory/sqlite_recovery.go:83-177`, `internal/acp/task_store.go`, `cmd/metiqd/runtime_semantics.go`

**Dependencies:** Work Items 3 (durability split), 5 (growth bounds), 6 (multi-tasking controls)

**Size:** L

**Risk:** Auto-resume is dangerous for mutating tool calls. Limit to non-mutating, explicitly resume-safe turns. Default restart outcome must be "lost but visible," not "blindly resumed."

---

## Implementation Order

```
WI-1  Baseline ──────────────────────────────┐
  │                                           │
  ├── WI-2  Prompt cache stabilization        │
  │     │                                     │
  │     └── WI-4  Recall/context latency      │
  │                                           │
  ├── WI-3  Hot-path durability split         │
  │                                           │
  ├── WI-5  Growth bounds ───┐                │
  │                          │                │
  │                    WI-6  Multi-tasking     │
  │                                           │
  ├── WI-7  Docker/bare-metal profiles        │
  │                                           │
  └─────────────────── WI-8  Self-healing ────┘
                     (depends on 3, 5, 6)
```

---

## Open Questions

1. **SessionStore fsync cost** — How much latency does synchronous journaling (`session_store.go:942-953`) add per mutation? Is batched/async journaling feasible without compromising crash safety? *(Gates Work Items 1, 3)*
2. **Production session cardinality** — Dozens or thousands? Determines urgency of session-count bounding and in-memory map growth. *(Gates Work Items 5, 6)*
3. **Docker image split** — Should the image be split into a minimal base + optional sidecar layers, or is the current optional-layer approach sufficient? *(Gates Work Item 7)*
4. **Self-update appetite** — Is the manual upgrade path intentional for stability? Keep excluded from this plan unless product direction changes. *(Out of scope; revisit after Work Item 8 only if needed)*

## References

- `internal/agent/llm.go` — prompt assembly and cache boundaries
- `internal/agent/agentic_loop.go` — agentic loop, tool batching, checkpoints
- `internal/agent/prompt_cache.go` — provider cache profiles
- `internal/context/engine.go`, `smallwindow_engine.go` — context engines
- `internal/context/lifecycle.go` — cache telemetry, context projection
- `internal/memory/sqlite.go`, `sqlite_recovery.go` — memory DB and recovery
- `internal/store/state/session_store.go` — session state persistence
- `cmd/metiqd/main.go` — daemon startup, DM pipeline, persistence
- `cmd/metiqd/runtime_semantics.go` — subagent registry, job registry, caps
- `cmd/metiqd/agent_run_orchestrator.go` — managed agent run lifecycle
- `Dockerfile`, `docker-compose.yml` — container deployment
- `scripts/systemd/metiqd.service` — bare-metal deployment
