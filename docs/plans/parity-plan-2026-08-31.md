# Swarmstr (Metiq) Parity Plan — 2026-08-31

Synthesized from a 7-agent audit comparing swarmstr against current openclaw, claude-code,
nostr-protocol/nips, and fips checkouts. Each item carries file:line evidence from the audit.
This document is the shared work plan: orchestrator updates checkboxes as items complete.
Sub-agents: treat this file as read-only context; the orchestrator owns checkbox state.

## Overall verdict

Swarmstr is not a stale port — most subsystems are substantially implemented, and some
(Nostr NIP coverage, checkpoint DAG, task diagnostics, ACP) are ahead of upstream.
Drift concentrates in five places:

1. **Security wiring** — policy surfaces accept config that is not enforced (exec approvals worst).
2. **Lifecycle durability** — subagent registry, checkpoints, session maintenance are
   in-memory/boot-only where upstream is durable and continuous.
3. **Trust/provenance semantics** — memory and plugin trust lack immutable origin/taint boundaries.
4. **Contract projection** — gateway methods/events, channel capabilities, CLI names diverge
   from upstream catalogs.
5. **Operational integration** — FIPS wire format is current, but daemon status/probe/lifecycle
   integration and the CI pin are behind.

---

## Phase 0 — Correctness & security fixes (CRITICAL, first)

Enforcement bugs, not features.

- [x] **0.1 Exec approval policy enforcement.** Legacy `mode/tools/timeout` fields are accepted
  but only `allow_always_signatures` is enforced (`internal/permissions/doctor.go:43-61`).
  Build one authoritative effective-policy evaluator (mode, ask, fallback, allowlist,
  caller + execution-host merge) per `openclaw/docs/tools/exec-approvals.md:146-214`.
- [x] **0.2 Bind approvals to full execution context.** Sign and revalidate canonical cwd, argv,
  sanitized env, executable identity, script/interpreter file content immediately before
  execution (`internal/security/commandanalysis/analysis.go:20-34`; upstream
  `exec-approvals.md:35-50`).
- [x] **0.3 Plugin trust hardening.** Local/path installs and install-record strings can elevate
  trust (`internal/plugins/trust/trust.go:22-55`). Derive trust from immutable source identity
  + operator-owned policy; never accept plugin-authored metadata as a trust grant.
- [x] **0.4 Memory provenance/taint.** Add closed `originClass`/`sessionKind`/taint/recall-origin
  fields to `MemoryRecord` (`internal/memory/record.go:46-88`, `extract.go:23-76`,
  `cmd/metiqd/main.go:4567-4573,5031-5037`); enforce before storage, promotion, or automatic
  injection. This is the memory-poisoning vector.
- [x] **0.5 NIP-17 sender copy relay routing.** Sender backup copy uses runtime relays instead of
  the sender's own kind 10050 list (`internal/nostr/runtime/nip17_bus.go:392-406` vs
  `nips/17.md:71-88`). Resolve sender's own DM relay list; fail rather than substitute.
- [x] **0.6 NIP-17 deletion `k` tag.** Hard-coded to "14" (`nip17_bus.go:310-323`); derive per-rumor
  kind (14/15/7), split deletion events when kinds differ.
- [x] **0.7 Silent pre-compaction history loss.** `WindowedEngine.Ingest` retains only most recent
  50 messages independent of token pressure (`internal/context/engine.go:285-318`). Never apply
  the count cap until history has been summarized/checkpointed; use token-derived pressure.
- [x] **0.8 Immutable creator-role sandbox requirement.** Persist a creator-derived sandbox
  requirement on session creation; fail closed when the backend is unavailable
  (`internal/sandbox/sandbox.go:58-110`; upstream `openclaw/docs/gateway/sandboxing.md:40-64`).
- [x] **0.9 FIPS CI pin update.** `.github/workflows/fips-real-daemon.yml:19-22` pins a July 2026
  `0.5.0-dev` commit; move to v0.5.0 or reviewed current SHA, run the mandatory real-daemon suite
  (`testing/fips/real-daemon/README.md:100-113`).
- [x] **0.10 Gate NIP-04 behind legacy option.** `runtime/dm_bus.go:45-48`; NIP-17 default
  everywhere (NIP-04 is deprecated per `nips/04.md:1-12`).

## Phase 1 — Durability & lifecycle parity

- [ ] **1.1 Durable checkpoints.** In-memory, caller-persisted store
  (`internal/session/checkpoint/checkpoint.go:154-166,277-321`) → durable repository owning
  atomic persistence, branch/restore, expected-revision CAS, byte caps (not just count=25),
  snapshot artifact cleanup. Upstream: `openclaw/src/gateway/session-compaction-checkpoints.ts`.
- [ ] **1.2 Durable subagent registry.** In-memory mutex map
  (`internal/agent/subagent/registry.go:49-224`) → persist child-run ownership/outcome/
  completion-delivery state (SQLite), restart reconciliation, retryable completion announcement.
  Upstream: `openclaw/src/agents/subagents/registry/*`.
- [ ] **1.3 Continuous session maintenance.** Boot-only age pruning
  (`cmd/metiqd/main_session_ops.go:26-135`, `internal/store/state/models_config.go:339-349`) →
  continuous warn/enforce maintenance: entry-count cap, disk budget/high-water, archive-before-
  delete, short model-run retention, protected active set, artifact sweep.
  Upstream: `openclaw/src/config/sessions/store-maintenance.ts:18-205`.
- [ ] **1.4 Wire credential key rotation.** `KeyRing.Pick`/`MarkFailed` have zero production
  callers (`internal/agent/keyring.go:41-117`). Integrate at request time; rotate only on
  classified rate-limit/quota failures.
- [ ] **1.5 Compaction planner parity.** Fixed 10K/40K thresholds + linear cut
  (`internal/context/session_memory_compact.go:30-71,96-180`) → sanitized projection, adaptive
  chunking, multi-stage summaries, oversized-message placeholders, model-window-derived budgets,
  bounded overflow recovery. Upstream: `openclaw/src/agents/compaction-planning.ts:56-387`.
- [ ] **1.6 MEMORY.md write-time rejection.** Silent truncation + injected warning
  (`internal/memory/file_memory.go:92-156`) → reject writes that leave loaded index over budget
  (current claude-code behavior, `CHANGELOG.md:1084-1086,1124-1127`).

## Phase 2 — Contract/protocol parity (client compatibility)

- [ ] **2.1 Gateway method/event catalog.** 84 upstream core methods and 29 event names absent
  (upstream catalogs: `openclaw/src/gateway/methods/core-descriptors.ts:66-780`,
  `server-methods-list.ts:42-97`; swarmstr: `internal/gateway/methods/method_registry.go`,
  `internal/gateway/ws/event_bus.go:20-221`). Priority: node invoke events + runner inventory →
  `sessions.get/resolve/recover/steer` + owner/viewer/group contracts → pairing scope-upgrade/
  bootstrap-token lanes → tasks.retry/dismiss + exec.approval.grants admin. Add compatibility
  projections for renamed events; keep Metiq natives as extensions.
- [ ] **2.2 Channel capability honesty + contract tests.** WhatsApp/ZaloUser advertise threads
  upstream doesn't (`internal/extensions/whatsapp/extension.go:111-124`,
  `zalouser/extension.go:53-55`); Matrix implements media but doesn't declare it
  (`matrix/extension.go:83-90,497-578`); Feishu no-op typing (`feishu/extension.go:482-485`).
  Add contract tests requiring every advertised capability to map to a working handle; split
  `Reply` from `Threads` in `internal/plugins/sdk/api.go:542-565`.
- [ ] **2.3 Channel media parity.** Missing/undeclared outbound media for iMessage/BlueBubbles,
  Feishu, Google Chat, IRC, LINE, Mattermost, Signal, MS Teams, Nextcloud, SMS, Synology,
  Zalo, ZaloUser. Implement `MediaHandle` consistently; set `Media` only after conformance tests.
- [ ] **2.4 CLI parity refresh.** `docs/parity/cli-parity.json` is stale (65 vs current 70 upstream
  commands; `cmd/metiq/cli_parity_test.go:41-108`). Regenerate from live descriptors; add aliases
  (`gateway`, `automations`, `exec-approvals`, `triage`); fix automation-visible flag drift
  (`gw --timeout` seconds vs ms, `--json` default true vs false; `cmd/metiq/gw_cmd.go:14-75`).
- [ ] **2.5 NIP-34 permissive parsing.** Over-strict parsers reject spec-valid events
  (`internal/nostr/nip34/collaboration.go:157-226,374-430` vs `nips/34.md:87-105,128-177`).
  MUST-only validation; recommended fields become warnings.

## Phase 3 — Runtime & provider parity

- [ ] **3.1 OpenAI/Azure Responses streaming.** Buffered, `SupportsStreaming:false`
  (`internal/agent/provider_registry.go:290`, `chat_openai_responses.go:25-62`,
  `chat_azure_responses.go:19-26`). Implement typed SSE behind `EventStreamingProvider` first;
  then WS/auto-fallback + `previous_response_id` continuation
  (upstream `openclaw/packages/ai/src/transports/openai-responses-client.ts:277-488`).
- [ ] **3.2 Per-model capability metadata.** Provider-wide flags
  (`internal/agent/provider_registry.go:284-335`) + 5-row static catalog
  (`model_catalog.go:21-27`) → per-model context/modality/tool/reasoning/cache/transport rows;
  prefer provider usage/tokenizer data over 4-chars/token estimates
  (`context_preflight.go:180-229`).
- [ ] **3.3 Provider breadth.** Add priority plugin-owned routes: DeepSeek, Z.AI/GLM, Qwen,
  Cerebras, Cohere, Vercel AI Gateway (upstream table
  `openclaw/docs/concepts/model-providers.md:280-310`).
- [ ] **3.4 Memory subsystem parity.** Rename fake `lancedb` backend (JSON + brute-force cosine,
  `internal/memory/lancedb/lancedb.go:1-9,36-74,116-157`) to `json-vector` or implement real
  LanceDB; trigger-first recall with intent-aware escalation
  (`active_recall.go:132-262` vs `openclaw/extensions/active-memory/index.ts:394-489`);
  generation-safe index publication; isolated append-only pre-compaction memory-flush run;
  event-driven wiki sync replacing 2s polling walk (`internal/memory/wiki/wiki.go:25,256-270`).
- [ ] **3.5 Secrets lifecycle.** Structured `SecretRef` (env/file/exec/store), atomic owner-scoped
  snapshots, cold/stale isolation, egress-time sentinels
  (`internal/secrets/secrets.go:76-253` vs `openclaw/docs/gateway/secrets.md:24-195`).

## Phase 4 — FIPS integration & UX surfaces

- [ ] **4.1 FIPS daemon integration.** Local control-socket adapter for status/probe (diagnosis
  only, never ACP completion; `fips/docs/reference/control-socket.md:51-120`); consume
  `Degraded/Failed/Draining` lifecycle states in transport selection; subscribe to NIP-09 advert
  deletions (`internal/nostr/runtime/capability.go:1028-1033`); fix "AgentPort is the FSP port"
  doc drift (`fips_transport.go:64-69`). Evaluate — do not adopt yet — the native
  pubkey-datagram API: ACP requires TCP reliability until a versioned reliability layer exists.
- [ ] **4.2 Web UI orchestration surfaces.** Checkpoint/rewind timeline over the existing DAG
  (backend ahead of UI; `internal/gateway/methods/schema_methods.go:442-473`); live
  subagent/task activity panel (`/tasks`-style); session ownership/recovery UX; then routed
  shell, approvals history, push notifications (`internal/webui/src/js/*` vs
  `openclaw/ui/src/app-routes.ts:27-113`).
- [ ] **4.3 New NIP surfaces + migrations.** NIP-43 relay access, NIP-67 EOSE completeness hints,
  NIP-37 drafts, kind 21059 ephemeral gift wraps; plan NIP-90→use-case microstandards and
  NIP-96→Blossom default migrations.

---

## Coordination notes

- Phase 0 lands first; Phases 1–4 are parallelizable by subsystem with these overlap warnings:
  - 0.4/0.7 touch `internal/memory` + `internal/context` before 1.5/1.6/3.4 build on them.
  - 0.1/0.2 touch `internal/permissions`/`internal/security` before any Phase 2 approval-grant
    RPC work.
- bd issue tracker holds one epic per phase with child tasks mirroring the checkboxes here.
- Audit evidence lives in the seven agent session transcripts (2026-08-31 parity audit session).
