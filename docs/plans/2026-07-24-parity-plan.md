# Swarmstr (Metiq) Parity Plan — OpenClaw / Claude Code / NIPs / FIPS

**Date:** 2026-07-24
**Basis:** 7 parallel investigations comparing swarmstr `805045d` against openclaw HEAD, claude-code, nips repo `cd9f33e` (2026-07-03), and the current FIPS repo.
**Effort key:** S ≤ a few days · M ≈ 1–2 weeks · L = multi-week / cross-cutting.

## Executive summary

Swarmstr's foundations are healthier than expected — the agent loop, hybrid memory retrieval, active recall, and core Nostr crypto (NIP-44/59/17 subset, NIP-42, NIP-65, NIP-98) are solid. The drift is concentrated in five places:

1. **Gateway/protocol surface is severely stale.** OpenClaw moved from ~95 tracked methods to **344 defined / 324 advertised**, and redesigned chat streaming (protocol v4 `chat` state-union replaces `chat.chunk`). The parity fixtures date to 2026-03-04 and internally disagree (95 vs 96).
2. **Streaming is only real for OpenAI + Anthropic.** Registry flags overstate support; there is no provider-neutral structured stream pipeline nor incremental tool-call repair.
3. **FIPS integration has correctness/security bugs** — the FIPS daemon's identity cache is never primed (direct dials can fail), and inbound sender identity trusts plaintext `env.From`.
4. **Nostr payment/zap paths are unsafe as implemented** — NIP-57 receipt validation, NIP-60 state transitions, and NIP-61 P2PK/mint validation are missing; several NIPs (17, 38, 51, 58, 86, 90) have interop-breaking drift.
5. **Security boundaries are advisory** — Node plugin sandbox decisions fail open; registry installs lack SSRF/provenance controls; permissions vs tool-policy are duplicated engines.

Recommended sequencing: fix critical correctness/security first (cheap, high-risk-reduction), then rebuild the parity baseline (fixtures + protocol v4), then the large feature workstreams in parallel.

---

## Phase 0 — Critical correctness & safety (do first)

- [x] **P0.1 (L, Critical)** DONE (commit 6b50945, swarmstr-n6h6 closed; .fips DNS priming) FIPS daemon identity-cache priming: peers derived to `fd00::/8` addrs are unroutable because only swarmstr's Go-side `idCache` is updated (`internal/nostr/runtime/fips_transport.go:260-367`; requirement in `fips/docs/design/fips-ipv6-adapter.md:23-133`). Resolve via FIPS DNS, a daemon identity-insert API, or native FSP service registration.
- [x] **P0.2 (M, Critical)** DONE (commit 6b50945, swarmstr-gxm1 closed) Authenticate inbound FIPS senders; remove the plaintext `env.From` trust fallback (`fips_transport.go:371-394`). Reject unknown senders or bind to the FIPS session identity.
- [x] **P0.3 (L, Critical)** DONE (commit 875a691, swarmstr-fhbt closed) NIP-57 receiver: implement all mandatory receipt checks (event sig, provider `nostrPubkey`, embedded request sig, BOLT-11 description hash, amount, recipient, dedup) — `internal/nostr/zap/zap.go` currently passes every event through.
- [x] **P0.4 (L)** DONE (commit 5b7bea7, swarmstr-em6f closed) NIP-61 nutzap safety: P2PK lock validation, mint allowlist from kind-10019, `"02"` prefix, DLEQ, redemption workflow (`internal/nostr/nip61/nutzap.go`).
- [x] **P0.5 (L)** DONE (commit 5b7bea7, swarmstr-zoh5 closed) NIP-60 wallet: derive live state from `del` transitions + deletions, validate fetched events before decrypting, mandatory history `e` refs, kind-7374 quotes (`internal/nostr/nip60/wallet.go`).
- [x] **P0.6 (L)** DONE (commit 1c6387a, swarmstr-gg8x closed) Node plugin sandbox must fail closed: manager computes a sandbox decision but loads via plain `exec.CommandContext` (`internal/plugins/manager/manager.go:105-137`, `internal/plugins/runtime/node_host.go:98-124`).
- [x] **P0.7 (M–L)** DONE (commit 1c6387a, swarmstr-m0zc closed) SSRF-safe plugin registry/install: private-IP/DNS-rebinding checks, redirect policy, signed metadata/provenance (`internal/plugins/installer/url.go:53-176`).
- [x] **P0.8 (S)** DONE (commit 4d4489d, swarmstr-6oyt closed) Fix Web UI calling nonexistent methods: `nodes.*` plural vs registered `node.*` singular, and `channels.reconnect` (unregistered) — `internal/webui/ui.html:1391-1449` vs `internal/gateway/methods/schema_methods.go:122-138`.

## Phase 1 — Rebaseline parity tracking (foundation for everything else)

- [x] **P1.1 (M)** DONE (`swarmstr-aeyu`; OpenClaw `5ff1690`) Regenerated parity fixtures from the descriptor catalogs via `scripts/refresh-parity.sh`; docs are the gateway source of truth, verifier allowlists are derived, the 4 dropped methods are removed, and CI checks generated classification/triage consistency.
- [x] **P1.2 (M)** DONE (`swarmstr-aeyu`) Triaged all 324 advertised gateway methods into implement / accepted deviation / defer groups in `docs/parity/gateway-triage.json`; generalized the Web UI callsite test to reject every unregistered literal gateway call.
- [x] **P1.3 (S)** DONE (`swarmstr-aeyu`) Refreshed all 65 CLI descriptors, including `claws`, `audit`, `promos`, `exec-approvals`, `users`, `worker`, `fleet`, `worktrees`, and `attach`; classified `gateway` → `gw` as a naming deviation and OpenClaw `node` as a semantic deviation from Metiq node management.

## Phase 2 — Workstreams (parallelizable)

> **Round 11 (2026-07-26): correction — `tts.speak` was never "out of scope."** Earlier summaries mislabeled it after the voice epic (`0tfj`) administratively fenced it out (that epic implemented `talk.speak`). It's actually an unclaimed compat alias for a capability metiq already ships twice (`talk.speak` persona/voice-alias aware; `tts.convert` raw). Registered `tts.speak` as a compat alias routing through the identical `talk.speak` handler (talk dispatch group, operator.write scope) + registration test (`231e837`). Also hardened the memory maintenance gate (`r34j`, `2dd199b`): routed the pre-existing mutating ops (migrations.apply, repair, dedupe, apply-mode remHarness) and the periodic compaction/promotion writers through the shared `maintenanceMu` with documented lock order `maintenanceMu → PromotionManager.mu → b.mu`, atomic candidate-claim (`markPromoted WHERE promoted_at IS NULL`), and schema_version-written-last; closed `s4wl`. **Gateway parity: 306 → 307 implemented, 17 missing.** The 17 remaining are all product/infra-decision-gated (`ngrd`/`xfny.5`/`qmxu`/`uhup`/`ko2f`/`nuqy`) or external (`b1r8`) — no unclaimed implementable method remains.
>
> **Round 10 (2026-07-25): qc53 dream-diary + grounded-short-term subsystem BUILT (product decision → option A).** Answered the 7 design questions and built the subsystem (`665b108`): (1) persisted structured `dream_diary` SQLite store — dated entries {phase, candidatesConsidered, candidate/promoted IDs, counts, narrative} — with the narrative now persisted (was ephemeral) and each entry NIP-44 self-encrypted + mirrored through the existing `memory_events_outbox` seam in one atomic txn; (2) config-gated background `DreamingJob` in metiqd (default off, 6h interval, graceful shutdown); (3) grounded-short-term modeled as a view over the promotion tier (`recall_tracking ⋈ chunks`, recency + source-cited), `resetGroundedShortTerm` **demotes reversibly** (never deletes); (4) the 4 `doctor.memory.*` methods wired real — `backfillDreamDiary` replays consolidation over existing memories bucketed by UTC day for the trailing 30 days (idempotent via partial unique index), `reset*` per-scope + confirmation-token gated. Oracle review caught + fixed 3 real issues (non-atomic diary/outbox write, lock-release-before-persist overlap, unbatched reset UPDATE). **Gateway parity: 302 → 306 implemented, 18 missing.** The remaining 18 are all product/infra-decision-gated (`ngrd` suspend, `xfny.5` provenance, `qmxu` plugin-UI, `uhup` git-forge, `ko2f` message-action, `nuqy` onboarding) or out of scope (`tts.speak`). Follow-ups: `r34j` (extend maintenance gate to pre-existing writers — closes `s4wl`), `dhzo` (durability/token hardening).
>
> **Round 9 (2026-07-25): long tail ground out — gateway parity 261 → 302 implemented, 63 → 22 missing.** Six serial batches (shared method-registry files preclude safe parallelism): **skills discovery+upload** (`xfny.1/.2` — search/detail/securityVerdicts/skillCard + chunked upload with zip-slip-safe lint gate; epic 4/5, `.5` deferred); **voice/talk** (`0tfj` — 22 methods over new `internal/gateway/talk`, session/turn state machine + client sessions live, audio-transport honestly UNAVAILABLE → `j16a`/`kx50`/`6ykn`); **plugin surface** (`zzin` — 8/11, approval ledger; 3 → `qmxu`); **users+gateway-lifecycle+chat** (`ikyn` — 11/14; suspend → `ngrd`); **memory-maintenance** (`wvwk` — migrations.memory.plan/apply + repair/dedupe/remHarness over the real REM PromotionManager; dream-diary/grounded-short-term absent → `qc53`); **models+sessions+node** (`kmhu` — 9/9, Oracle-hardened cleanup/probe SSRF); **misc introspection** (`wapc` — 12/15: system.info/diagnostics.stability/commands.list/update.status/tools.effective+invoke/audit.*/agents.workspace.*/approval.history/ui.command; message.action → `ko2f`, controlUi git-forge → `uhup`); **openclaw** (`i413` — 4 compat aliases delegating to native chat/history/approval/changes; 5 setup.* accepted-deviation → `nuqy`). **The remaining 22 missing are ALL deferred behind product/infra decisions** (`qc53` dream-diary subsystem, `xfny.5` chat.send provenance, `ngrd` suspend machinery, `qmxu` plugin UI-surface registry, `uhup` git-forge, `ko2f` message-action semantics, `nuqy` native onboarding) or out of scope (`tts.speak`). No mechanically-implementable gateway method remains.
>
> **Round 8 (2026-07-25): UI views + skills & voice long tail.** Closed both remaining UI issues — `swarmstr-isxj` (conversations/artifacts/environments management views following the views.js pattern; artifacts.download → Blob download) and `swarmstr-wxcy` (board widget postMessage bridge to ticket-scoped board.prompt.authorize/data.read/action/event, board.widget.grant UX, mcp.app.appView re-minting + viewCreated). Then chewed two full long-tail epics: **`swarmstr-xfny` skills.*** — curator (status/pin/unpin/restore), proposals core (list/inspect/create/update/revise/apply/reject/quarantine), discovery quartet (search/detail/securityVerdicts/skillCard), chunked upload (begin/chunk/commit with zip-slip-safe lint gate); `xfny.5` history methods deferred (human_input_required — needs runId/suppressed-provenance from chat.send). **`swarmstr-0tfj` voice/talk*** — 22 methods over a new `internal/gateway/talk` package: tts.personas/setPersona, talk.catalog/speak, voicewake.routing.get/set, talk.session.* turn state machine, talk.client.* all fully implemented where runtime exists; audio-transport paths return honest UNAVAILABLE/UNSUPPORTED (no fake stubs) tracked by `j16a`/`kx50`/`6ykn` (realtimevoice/stt not daemon-wired, no LiveKit managed rooms). Then the **plugin surface** (`swarmstr-zzin`): plugins.list/search/setEnabled/refresh over the config+GojaPluginManager catalog, plugin.approval.list/request/waitDecision/resolve as a durable ledger modeled on questions.* (waitDecision = pose-and-block); plugins.uiDescriptors/sessionAction/plugin.surface.refresh deferred UNAVAILABLE (no SDK UI-surface backing — `qmxu`, human_input_required, blocks on `5p0v`). **Gateway parity: 212 → 261 implemented, 63 missing.** Follow-ups filed: `4256` (upload-store per-upload mutex), `j16a`/`kx50`/`6ykn` (voice infra), `qmxu` (plugin UI-surface registry). Remaining long tail by prefix: openclaw 9, doctor 7, users 5, gateway 5, chat 4, models/sessions/node 3 each, plugins 3 (deferred), misc singles.
>
> **Round 7 (2026-07-25): UI + loopback follow-ups closed.** `swarmstr-78jt`: Web UI for all new surfaces — terminal panel (attach take-over with seq-reconciled replay, dependency-free renderer, drag-drop upload), question wizard mirroring the approval UX, tasks/attach-grants view, boards tab embedding the widget frame host. `swarmstr-kysn`: MCP loopback runtime wired (`admin.Start` publishes the bound runtime; `/mcp` accepts attach-grant bearer tokens with per-request re-resolution, forced sessionKey scoping, constant-time compares, fail-closed 401s). `swarmstr-j5dq`: question/task-suggestion events registered in `AllPushEvents` (live subscriptions now flow). Remaining backlog (5): `q8vt` (human: SHAREGAP_TOKEN secret), `b1r8` (upstream), `5p0v` (board depth), `isxj` (conversations/artifacts/environments views), `wxcy` (board interactivity bridge + mcp-app views), plus the 112-method triage=implement long tail.
>
> **Round 6 (2026-07-25): `swarmstr-fokh` defer bucket CLOSED.** Four parallel agents landed the entire deferred workspace surface: terminal.attach/list/text/upload + attach.grant/revoke; question.* durable ledger with waitAnswer agent integration; taskSuggestions.* lifecycle; artifacts.* CAS store; environments.* over the fail-closed sandbox; board view-tickets (HMAC, 2-min TTL) + sandboxed widget frame host + board.widget.appView; full mcp.app.* view registry. **Gateway parity: 212/324 implemented, 0 partial** (was 101 at rebaseline). Remaining 112 missing methods are ordinary triage=implement long tail (biggest prefixes: skills 22, talk 18, openclaw 9, doctor 7). Follow-ups: `swarmstr-78jt` (Web UI for new surfaces), `swarmstr-kysn` (MCP loopback runtime), `swarmstr-5p0v` (board data/action depth), plus `q8vt`/`b1r8` carryovers.
>
> **FINAL STATUS — Round 5 (2026-07-24): ALL SEVEN WORKSTREAM EPICS CLOSED.** Round 5 lifted the A7 deferral and completed the plan: terminal PTY surface (connection-owned, race-clean), fs.listDir (os.Root-contained), worktrees.* (git-backed registry), board.* (tabs/widgets with grant flow + board.event steering), conversations.* (conv-ref registry with reply correlation, reusing the outbound media dispatch), outbound sdk.MediaHandle dispatch (cnmb), and Web UI modularization (75hp: ui.html assembled from internal/webui/src fragments via go generate, byte-drift gate + v4 event-contract tests). Epics mp8k and yiyj audited & closed as superseded. Gateway parity matrix now at 179 implemented. Remaining open backlog (3): `q8vt` (needs SHAREGAP_TOKEN repo secret — human), `b1r8` (upstream nostr serializer fix not yet released), `swarmstr-fokh` (fresh defer bucket: terminal.attach/list/text/upload, board tickets, question.*, mcp.app.*, artifacts.*, environments.*, taskSuggestions.*, attach.*). Full build/test/parity green.
>
> **Status update — Round 4 (2026-07-24, Claude-backed agents):** A2.5 collaboration primitives complete — visibility/membership with role-authorized sharing (`0e995c8`), suggestions + connection-scoped typing (`824a021`), discussion provider + observer ask (`0624506`); `vog0.2.5` and parent `vog0.2` closed. Follow-ups landed (`aba4b56`): wiki claim freshness/contradiction health (fe6c), shared outbound media wiring (irwf), agent hot-reload propagation (v2uq); b1r8 annotated (upstream fix not yet released). **WS-A is 8/9 — every implementable child is done; only A7 (explicit defer bucket: terminal/worktrees/fs/boards/conversations) keeps the epic open.** Remaining backlog: q8vt (needs SHAREGAP_TOKEN secret — human), A7 defer bucket, 75hp webui build modularization, cnmb media-handle dispatch, b1r8 upstream tracking, mp8k deferred-parity epic. Full gates green.
>
> **Status update — Round 3 (2026-07-24):** A2 subsystem trio + pre-existing issues. Landed: A2.3 workspace files/CAS + catalog surface (closed), A2.4 session DAG/snapshots with branch/rewind/fork + dzgx session-history UI fix (closed), A2.5 dispatch/reclaim + session groups (priority slice; collaboration primitives remain as `swarmstr-vog0.2.5.1–.3`), WhatsApp linked-device channel `whatsappweb` via Baileys bridge (user decided YES; `swarmstr-33hd.5.1` closed), sandbox availability validation + doctor guidance (a6ff closed — Docker already default), CI private-module auth wiring (q8vt open: **needs SHAREGAP_TOKEN repo secret added by a human**), text-thrashing detector (fzv3 closed), FIPS v0.3.0 epic audited & closed as superseded (yiyj). WS-A now 7/9 with only A2.5 collab children + A7 defer remaining. Commits `a73e159..7f03968`; full gates green. Note: Codex agent provider hit its usage quota at round end (resets Jul 30) — final delivery of the last four packages was landed by the orchestrator after verification.
>
> **Status update — Round 2 (2026-07-24):** All remaining open workstream children dispatched and completed. **Six of seven epics are now CLOSED**: WS-B (Bedrock/Anthropic-Vertex + adapters + skills-subagent integration), WS-C (wiki pipeline + memory-host SDK), WS-D (SMS + zalouser channels, Discord/Signal/iMessage advanced actions; WhatsApp linked-device decision `swarmstr-33hd.5.1` flagged for human input), WS-E (real-daemon FIPS interop suite + Linux CI workflow `fips-real-daemon.yml`), WS-F (NIP-46 remote signer, NIP-05/13/62/70/87/96, NIP-66 monitoring, NIP-77 negentropy, NIP-34 git), WS-G (SDK security/approval/doctor/provider-auth adapters, Vault backend, exec-approval doctor + skills lint CLIs). **WS-A remains open (7/9)**: A1/A3/A4/A5/A6/A8 closed (v4 chat integration, durable approvals with unified owners, descriptor/admission, cron, nodes, channel lifecycle+pairing); remaining: A2.3 (workspace file/CAS backend), A2.4 (session DAG/snapshots), A2.5 (collaboration/placement services) — each needs a dedicated new subsystem — plus A7 (explicit defer). Commits `82af8a0..7686560`; full build/test/parity green.
>
> **Status 2026-07-24 (Round 1):** First execution round complete across all seven workstreams (commits `3d27cd4..81bffe2`), followed by an adversarial audit of all 38 closed child issues (13 reopened for material gaps, all 13 remediated and re-closed; commits `5be7175`, `81bffe2`). Full `go build`/`go test`/`ci-parity` green. Live per-item state is tracked in beads (`bd epic status`): WS-A 1/9, WS-B 4/6, WS-C 3/5, WS-D 4/6, WS-E 8/9, WS-F 11/14, WS-G 6/9 children closed. Remaining open children (largest: WS-A sessions/approvals/cron/nodes surface, B5/B6 providers, F9 NIP-46, G5–G7 SDK/Vault/lint) are accurately scoped in their issues. Checkbox lists below reflect the original plan; beads is authoritative.

### WS-A: Gateway protocol v4, sessions, and Web UI (owner: gateway)

> **Execution update 2026-07-24:** A1, A4, A5, and A6 are complete. Post-review hardening preserved callback-only streaming while deduplicating structured deltas, made cron mutations persist-before-commit, serialized node progress publication, and fenced removed nodes before invocation/pending cleanup. A2 and A3 have landed bounded vertical slices but remain open for the real workspace file/CAS, session DAG/collaboration, and durable non-exec approval backends. A8 is blocked on real channel account lifecycle and DM-pairing hooks (`swarmstr-3597`); no synthetic state was added. A7 remains the explicit defer. Beads remains authoritative.

- [ ] **A1 (L)** Protocol v4 chat streaming: replace `chat.chunk`/`turn.*` with the `chat` state-union (`status|delta|final|aborted|error`, `runId`/`seq`/`replace` semantics per `packages/gateway-protocol/src/schema/logs-chat.ts:143-223`); admission/version rejection behavior.
- [ ] **A2 (L)** Sessions live surface: `sessions.subscribe/unsubscribe`, `sessions.messages.*`, describe/create/send/abort, files, compaction history, branches/rewind/fork, search/diff, dispatch/reclaim.
- [ ] **A3 (M)** Approval surface: `exec.approval.get/list`, unified `approval.*`; UI reconnect reconciliation (restore pending queue after disconnect, expiry handling).
- [ ] **A4 (L)** Method descriptor metadata: operator scopes, node scope, startup availability, control-plane write classification; broader WS admission (device proof/token, flood guard) per `src/gateway/server/ws-connection/`.
- [ ] **A5 (M)** Cron parity: `cron.get`, `cron.scratch.get/set`, richer edit/trigger semantics in CLI+UI.
- [ ] **A6 (M/L)** Nodes parity: `node.pair.remove`, `device.pair.rename`, `node.pending.*`, `node.invoke.progress`; align UI.
- [ ] **A7 (L, defer-able)** Terminal WS surface (`terminal.open/input/resize/close`, data/exit events); worktrees/fs/board/conversation method groups — triage in P1.2.
- [ ] **A8 (S/M)** Channels lifecycle methods `channels.start/stop`, `channels.pairing.*`.

### WS-B: Agent runtime & streaming (owner: runtime)

- [ ] **B1 (L)** Provider-neutral structured streaming events (text/thinking/tool-call deltas, lifecycle events à la `agent-core/src/agent-loop.ts`); implement `Stream` for Gemini, Mistral, Moonshot, Groq, MiniMax, Vertex, Ollama/local (today only OpenAI `provider.go:460` and Anthropic `chat_anthropic.go:541` stream; registry flags overstate support).
- [ ] **B2 (L)** Port openclaw's incremental tool-call stream normalizer (`packages/tool-call-repair/src/stream-normalizer.ts`): multi-chunk repair, partial JSON/XML suppression, scrubbing. Current repair is terminal-only (`internal/agent/toolrepair/promote.go`).
- [ ] **B3 (L)** Real subagent orchestration on top of the existing registry (`internal/agent/subagent/registry.go`): spawning, nesting/concurrency caps, typed agent definitions, parent/child streaming, cancellation, budgets (see claude-code changelog features).
- [ ] **B4 (M)** Wire declared lifecycle hooks (session start/end, before-agent, agent-end, subagent spawn) into runtime — only before/after tool-call are emitted today (`internal/agent/agentic_loop.go:1078-1237`).
- [ ] **B5 (M/L)** Bedrock + Anthropic-on-Vertex provider runtimes.
- [ ] **B6 (M)** Provider-specific adapters (headers, model discovery, usage/thinking normalization) where OpenAI-compat wrappers are lossy; integrate skills catalog with subagent types.

### WS-C: Memory & context (owner: memory)

- [ ] **C1 (M–L)** Enforce token budgets in context assembly: `WindowedEngine.Assemble` ignores its budget arg (`internal/context/engine.go:307`); unify with `internal/agent/context_budget.go` paths.
- [ ] **C2 (M)** Index lifecycle/self-healing: FTS health state, targeted reindex, file watching, session transcript indexing (ref `extensions/memory-core/src/memory/manager-fts-state.ts` etc.).
- [ ] **C3 (L)** Model-assisted extraction/promotion pipeline (short-term promotion, transcript corpus, dreaming) — current extraction is heuristic sentence/keyword only (`internal/memory/extract.go:23-104`).
- [ ] **C4 (L, defer-able)** Wiki/knowledge-base parity (ingestion, compilation, claim health) — `internal/memory/wiki/wiki.go` is minimal vs `extensions/memory-wiki`.
- [ ] **C5 (L, defer-able)** Memory-host SDK abstraction parity for backend portability.

### WS-D: Channels (owner: channels)

- [ ] **D1 (L)** Account-scoped configuration/routing across channels: gateway methods still require raw credentials per call (`telegram/extension.go:111-121`, `slack/extension.go:104-124`); adopt openclaw-style named/default account resolution.
- [ ] **D2 (L)** Thread + edit-lifecycle parity: Telegram forum topics (`messageThreadId`, not `reply_to_message_id`), Slack thread routing/auto-recovery, Matrix thread bindings, draft/final streaming edits across Telegram/Slack/Matrix.
- [ ] **D3 (M)** Slack typing via Assistant thread status (`channel.ts:212-248`).
- [ ] **D4 (M each)** Remove/gate protocol-invalid polling fallbacks (email SEARCH poll, BlueBubbles 5s REST, Mattermost 3s, Nextcloud, Signal).
- [ ] **D5 (M)** New channels: SMS, zalouser; **(L, decision)** WhatsApp linked-device (Baileys-style) alongside Cloud API; **(L, defer-able)** Tlon/Urbit.
- [ ] **D6 (M–L)** Discord advanced actions (archived-thread pagination, rename, permission-aware reopen, reaction cleanup); Signal reaction workflows; iMessage/BlueBubbles capability surface.

### WS-E: ACP & FIPS (owner: protocol) — after P0.1/P0.2

- [ ] **E1 (M)** Align discovery with current FIPS kind-37195 advert schema (`d=fips-overlay-v1`, structured endpoints) — swarmstr currently emits `fips=true`/`fips_transport=udp:2121` tags (`capability.go:324-395`).
- [ ] **E2 (M/L)** Remote worker cancellation on dispatcher/pipeline timeout — timeouts only remove local pending state (`internal/acp/dispatcher.go:245-273`); fan-out cancel on parallel failure; concurrency limits in `pipeline.go`.
- [ ] **E3 (M)** OpenClaw-style backend failover plan (ordered primary+fallback, `sawOutput` safety — `manager.backend-failover.ts`).
- [ ] **E4 (M/L)** Detached child-task ↔ requester task-registry reconciliation (ref `manager.background-task.ts`).
- [ ] **E5 (M)** Harden timeout/cancel races: typed `TURN_TIMEOUT`, dedup concurrent cancels, bounded disconnected-turn cancel (`manager.go:724-959`).
- [ ] **E6 (M)** Spec hygiene: document/version the swarmstr TCP DM/control framing as an application protocol over FIPS IPv6 (not FIPS wire format); rename or bridge `acp_dispatch` vs openclaw's `reply_dispatch` semantics; publish a pipeline schema/cancellation contract.
- [ ] **E7 (L)** Real-daemon FIPS interop test suite — current "full stack" tests are loopback TCP mocks (`testing/fips/README.md:15-24`).

### WS-F: Nostr NIP compliance (owner: nostr) — after P0.3–P0.5

Interop fixes:
- [ ] **F1 (M)** NIP-17: drop the ~50h historic-age rejection (spec allows backdating); accept kind-15 file messages, wrapped kind-7 reactions, wrapped kind-5 deletions; add group rooms, subject/reply APIs (`internal/nostr/runtime/nip17_bus.go:51,610-655`).
- [ ] **F2 (S)** NIP-38: put status text in `content`, `d` = status type (currently custom `["status", …]` tag, `nip38/status.go:299-305`).
- [ ] **F3 (S–M)** NIP-51: relay sets `["relay",…]` not `["r",…]` (with legacy read); NIP-44 private items; missing standard list kinds.
- [ ] **F4 (S)** NIP-58: enforce mandatory `name`/`description`/`image`; multi-recipient awards.
- [ ] **F5 (M)** NIP-86: add `unbanpubkey`, `unallowpubkey`, role methods, `listeventsneedingmoderation`; drop `listdisallowedkinds`; enforce admin authorization policy on the verified pubkey.
- [ ] **F6 (M)** NIP-90: `request` tag must be the stringified request event (not job ID, `dvm/handler.go:359-365`); all `i` inputs; response relay routing; encrypted params; cancellation; payment flow. Note: NIP-90 is marked `unrecommended` upstream — treat as compat, not the strategic A2A path.
- [ ] **F7 (M)** NIP-47: kind-13194 info discovery + method/encryption negotiation; notification kinds 23196/23197.
- [ ] **F8 (S)** NIP-B7 Blossom server-list discovery (kind 10063).

New capabilities (prioritize by product need):
- [ ] **F9 (L)** NIP-46 remote signer (separate agent from user key) — strategic.
- [ ] **F10 (S–M each)** NIP-05 verification flow, NIP-13 PoW, NIP-62 vanish, NIP-70 protected events, NIP-87 mint discovery, NIP-96 HTTP file storage.
- [ ] **F11 (L each, defer-able)** NIP-66 relay discovery/monitoring, NIP-77 negentropy sync, NIP-34 git collaboration.

### WS-G: Plugins, sandbox, security (owner: platform) — after P0.6/P0.7

- [ ] **G1 (L)** Unify permissions engine + tool-policy evaluator (two rule models today: `internal/permissions/engine.go`, `internal/policy/tool_policy.go`).
- [ ] **G2 (M–L)** Expose full `sandbox.Config` through `sandbox.run` (resource caps, rootfs, capabilities, network/egress) — schema exposes 5 fields (`schema_system.go:10-24`); make domain/CIDR policy enforceable (proxy/firewall boundary).
- [ ] **G3 (M–L)** Immutable managed settings layer (MDM/admin precedence, no runtime weakening) + sandboxed hook execution (hooks run unsandboxed `sh -c` today, `hooks/invoker.go:86-157`).
- [ ] **G4 (M)** Plugin package contract parity: `pluginApi` range, host/SDK version negotiation (`packages/plugin-package-contract`).
- [ ] **G5 (L, incremental)** Plugin SDK surface: security/exec-approval/runtime-doctor/provider-auth runtimes.
- [ ] **G6 (M)** Vault secrets backend; lockfile-quality install provenance; plugin trusted-policy contracts.
- [ ] **G7 (M)** Exec-approval maturity (policy doctor), skills validation/linting tooling.

## Explicitly fine / not gaps

- Email (IMAP+SMTP) channel: swarmstr **exceeds** openclaw (no equivalent there).
- Core agent loop (batching, loop detection, checkpoints, compaction): mature; deficits are integration, not the loop.
- Hybrid retrieval/active recall: near-parity (S–M polish only).
- Provider breadth (xAI, Together, OpenRouter, Fireworks, DeepInfra, Perplexity, Azure, Vertex): already beyond the headline list.
- ACP pipelines (sequential/parallel): swarmstr extension with no openclaw equivalent — needs a spec, not a port.

## Dependency notes

- P1 (fixtures) gates the scope of WS-A; do P1 before committing to A-item counts.
- A1 (protocol v4 chat) gates Web UI chat work and any operator-client compat.
- B1 (structured streaming) should land before/with A1 so gateway events have a real runtime source.
- E1–E7 depend on P0.1/P0.2; F-workstream payment items depend on P0.3–P0.5.

## Beads issue mapping

Epics: P1 rebaseline `swarmstr-aeyu` · WS-A `swarmstr-vog0` · WS-B `swarmstr-213m` · WS-C `swarmstr-frp2` · WS-D `swarmstr-33hd` · WS-E `swarmstr-hqgv` · WS-F `swarmstr-hcff` · WS-G `swarmstr-7oop`.
P0 items (children of their epics): P0.1 `swarmstr-n6h6` · P0.2 `swarmstr-gxm1` · P0.3 `swarmstr-fhbt` · P0.4 `swarmstr-em6f` · P0.5 `swarmstr-zoh5` · P0.6 `swarmstr-gg8x` · P0.7 `swarmstr-m0zc` · P0.8 `swarmstr-6oyt`. WS-A additionally depends on Epic P1.

## Sizing rollup (rough)

| Workstream | Scale |
|---|---|
| Phase 0 | ~2 engineer-months, mostly L items but independent |
| P1 rebaseline | 2–3 weeks |
| WS-A gateway/UI | Largest: multi-month if full parity; triage via P1.2 |
| WS-B runtime/streaming | ~2 months |
| WS-C memory | ~1–1.5 months (C1–C3), C4/C5 defer-able |
| WS-D channels | ~2 months across many M items |
| WS-E ACP/FIPS | ~1–1.5 months after P0 |
| WS-F NIPs | ~2 months (interop fixes ~3 weeks; strategic items long tail) |
| WS-G security/plugins | ~2 months after P0 |
