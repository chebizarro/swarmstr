# Investigation: swarmstr parity with post-follow-up agent group-chat discipline

**Date:** 2026-08-30  
**Repository:** `swarmstr` (`metiq` Go daemon)  
**Audited commit:** `a1e49a3ea62278e04b8c8a00c4dc565aec8fe30d`  
**Reference:** `openclaw-nostr` recommendation report plus compliance follow-ups landed in `5126df41248bcb799d61e870e84052afaa3f1aca`  
**Scope:** Read-only source/history audit. No daemon code was changed.

## Executive summary

swarmstr implements the original mechanism family for every recommendation, but **none is fully compliant with the stricter post-`5126df4` openclaw-nostr contract**.

- **IMPLEMENTED:** none.
- **PARTIAL:** R1-R7.
- **MISSING as a whole recommendation:** none. Several required subfeatures are missing or are present only as unwired seams.

The most consequential findings are:

1. R1's heuristic gate is wired, but ambiguity always receives a `nil` Tier-2 hook in production.
2. R2's election and visible takeover are wired, but unrelated same-room traffic from the elected pubkey still cancels takeover.
3. R3 converts generated/inbound targeted ACK replies, but the direct `channels.send` RPC has no target fields and bypasses the classifier via `Channel.Send`.
4. R4 has wire enforcement and reusable persistence/noticing primitives, but it accepts a count of successful actions instead of resolving real task/flow handles, and the durable lifecycle package is not wired into `metiqd`.
5. R5 is scheduled, silent by default, and includes the core pair guard, but its review/throttle state is volatile, commitments are not collected, and moderators do not rotate.
6. R6 has durable kind-30900 FleetTask state plus generated-reply shadow suppression, but no managed TaskFlow lifecycle bridge or explicit-announcement path through the shared throttle was found.
7. R7 exposes useful counts and a `1/n` / `2/n` share alarm, but lacks ACK/echo denominators and rates, commitment open/completed lifecycle metrics, roster-derived `n`, and MAST annotation. `swarmstr-z4ub` is still open.

## Baseline and method

The openclaw-nostr compliance investigation records all R1-R7 recommendations as **Implemented** after `5126df4`, including the seven concrete follow-ups: bounded/wired R1 Tier 2, event-related-only R2 cancellation, state-resolved durable R4 commitments, durable/rotating R5 review, typed/throttled R6 TaskFlow announcements, R7 rates/roster lifecycle metrics, and R7 MAST annotations (`openclaw-nostr/docs/investigations/group-chat-discipline-compliance-2026-08-29.md:102-130`).

This audit:

- read `internal/gateway/channels/parity_matrix_test.go` first as the repository's stated acceptance contract;
- inspected the named channel, commitment, metric, task-bridge, and daemon wiring modules and focused tests;
- searched for production call sites, persistence integration, TaskFlow paths, scorecard denominators, roster inputs, and MAST integration;
- checked recent parity history and `bd show swarmstr-z4ub`;
- treated a package-level seam as insufficient when no production path constructed or invoked it.

Relevant swarmstr history includes:

| Commit | Landed mechanism |
|---|---|
| `5913e46` | R1 ambient should-reply heuristic gate |
| `4a032eb` | R2 deterministic responder election |
| `142395d` | R3 pure-ACK conversion |
| `73ac220` | R4 taskflow commitment enforcement |
| `9d4680a` | R5 scheduled progress ledger |
| `82792a2` | R6 kind-30900 chat-shadow suppression |
| `d169feb` | R7 per-room scorecard |

No later swarmstr history inspected supplied equivalents of the post-`5126df4` R1/R2/R4/R5/R6/R7 remediations.

## Parity matrix

| Item | openclaw-nostr after `5126df4` | swarmstr | swarmstr evidence | Gap against post-follow-up contract | Bead |
|---|---|---|---|---|---|
| R1 should-reply gate | IMPLEMENTED | **PARTIAL** | Tier-1 addressing/question/capability heuristics: `internal/gateway/channels/should_reply_gate.go:187-244`; ambient preflight call: `internal/gateway/channels/nostr_preflight.go:438-461` | Tier-2 interface says wiring is deferred and the only production call passes `nil`, so ambiguity always fails quiet without trying a configured bounded cheap model: `should_reply_gate.go:62-90,246-269`; `nostr_preflight.go:445-455` | `swarmstr-h4qm` |
| R2 responder election | IMPLEMENTED | **PARTIAL** | capability scoring, FNV-1a hash, equal-score rotation: `internal/gateway/channels/responder_election.go:82-132,140-190`; visible successor machinery is wired in `cmd/metiqd/nostr_group_loop_control.go:93-154,313-358` | `ObserveRoomMessage` cancels merely because sender equals elected pubkey, even with no contested-event relation: `responder_election.go:536-555`; the old behavior is asserted at `responder_election_test.go:434-446` and `parity_matrix_test.go:378-385` | `swarmstr-452k` |
| R3 ACK-to-reaction | IMPLEMENTED | **PARTIAL** | classifier: `internal/gateway/channels/outbound.go:20-70`; inbound targeted conversion: `internal/gateway/channels/channels.go:517-532,754-763`; generated room replies call `msg.Reply`: `cmd/metiqd/main.go:2485-2490`, `cmd/metiqd/control_rpc_channels.go:291-296`; default-on policy: `nostr_room_policy.go:40-44,108-109,150-152` | Direct `channels.send` has only `channel_id`/`text` and calls `ch.Send`, which has no target and does not classify ACKs: `internal/gateway/methods/schema_channels.go:57-62`; `cmd/metiqd/control_rpc_channels.go:401-419`; `channels.go:484-516` | `swarmstr-439p` |
| R4 commitment guard | IMPLEMENTED | **PARTIAL** | pre-send rewrite: `internal/gateway/channels/channels.go:484-495`; multiple file-backed records and lifecycle primitives: `internal/commitments/commitments.go:14-59,186-251`; durable save/load: `internal/commitments/store_persistence.go:9-49`; dropped notice: `internal/commitments/heartbeat.go:104-174` | Enforcement is explicit opt-in, backing is only `SuccessfulTaskFlowActions > 0`, no live handle/state lookup exists, records lack task/flow reference fields, and no production caller constructs the durable commitment runtime/store: `commitment_enforcement.go:13-16,35-49`; `nostr_room_policy.go:60-65,180-182`; `commitments.go:34-59` | `swarmstr-hkse` |
| R5 progress ledger | IMPLEMENTED | **PARTIAL** | deterministic findings and silence: `internal/gateway/channels/progress_ledger.go:100-151,516-546`; core guard snapshot collection: `cmd/metiqd/nostr_progress_ledger.go:107-111`; scheduled loop: `nostr_progress_ledger.go:55-93` | recorder and scheduler maps are process-local: `progress_ledger.go:382-427,441-455`; commitments are explicitly deferred: `cmd/metiqd/nostr_progress_ledger.go:96-105`; only fixed/self moderator policy exists: `nostr_room_policy.go:80-99,191-196` | `swarmstr-9wiv` |
| R6 typed task lifecycle | IMPLEMENTED | **PARTIAL** | complete validated kind-30900 events: `internal/tasks/task_events_v2.go:59-78,112-140`; bridge publishes/subscribes and reports effective transitions: `internal/tasks/fleet_task_bridge.go:268-279,345-365,486-492`; daemon feeds the shared corpus: `cmd/metiqd/main.go:5569-5583`; generated reply paths throttle shadows: `main.go:2461-2489`, `control_rpc_channels.go:273-295` | No `internal/` or `cmd/` TaskFlow implementation/path was found. Therefore TaskFlow mutations cannot enter the typed corpus and explicit `announce:true` messages cannot use the shared throttle. Current checks cover model-generated `replyText`, not a managed TaskFlow announcement path. | `swarmstr-r6ik` |
| R7 scorecard | IMPLEMENTED | **PARTIAL** | fixed signal counts: `internal/metrics/scorecard.go:29-75`; unanswered-mention gauge: `scorecard.go:217-229`; observed-sender `1/n` and `2/n` alarm: `scorecard.go:254-279,306-347` | `ack_conversion`/echo are count-only with no opportunity denominator/rate; commitments expose blocked/dropped but not open/opened/completed; `n` is distinct observed senders, not the agent roster; file header explicitly says MAST is not implemented and tracks `swarmstr-z4ub`: `scorecard.go:18-23,29-75,254-279,306-347` | `swarmstr-mjy2`; MAST: existing `swarmstr-z4ub` |

## Detailed findings

### 1. R1 — dedicated should-reply gate: PARTIAL

#### Implemented

The deterministic tier is substantive rather than a name-only gate:

- address detection distinguishes direct address from third-person discussion (`internal/gateway/channels/should_reply_gate.go:116-151`);
- question/request and capability matching feed a deterministic score (`should_reply_gate.go:187-244`);
- it runs only after trust/command/mention decisions and only for otherwise-admitted ambient traffic (`internal/gateway/channels/nostr_preflight.go:438-461`).

#### Gap

`ShouldReplyModelHook` exists, but its comment explicitly says runtime wiring is deferred (`internal/gateway/channels/should_reply_gate.go:62-90`). `ResolveShouldReplyGate` can invoke it, but without a hook converts ambiguity to `drop / ambiguous_no_model` (`should_reply_gate.go:246-269`). The ambient preflight call always passes `nil` (`nostr_preflight.go:445-455`).

There is no production resolver/provider registration, model selection, context cancellation, timeout/error result, or integration test proving ambiguity reaches a bounded cheap model. This fails the checklist's explicit requirement that Tier 2 be wired and ambiguity not always fail quiet when one is available.

### 2. R2 — deterministic election and successor takeover: PARTIAL

#### Implemented

The election is deterministic across instances:

- capabilities earn weighted distinct phrase/token hits (`internal/gateway/channels/responder_election.go:82-116`);
- FNV-1a of event ID supplies stable rotation (`responder_election.go:118-132`);
- matched candidates rank by score and equal-score bands rotate by the hash (`responder_election.go:140-190`);
- daemon wiring defers non-winners, arms the immediate successor, re-delivers on timeout, and publishes a visible 🙋 claim before answering (`cmd/metiqd/nostr_group_loop_control.go:93-154,313-358`).

Event-targeted reactions cancel the pending takeover (`responder_election.go:558-576`).

#### Gap: obsolete cancellation semantics

`ObserveRoomMessage` computes `electedAnswered := sender == entry.pending.ElectedPubkey` and cancels on `electedAnswered || threadAnswered` (`internal/gateway/channels/responder_election.go:536-555`). The elected agent can therefore post unrelated traffic in the room and strand the contested event by suppressing its successor.

The focused and parity tests preserve this obsolete assumption without reply/thread evidence (`internal/gateway/channels/responder_election_test.go:434-446`; `parity_matrix_test.go:378-385`). Post-`5126df4` parity requires all text cancellation, including elected-agent text, to relate to the contested event by reply/thread root. Event-targeted claims/reactions remain valid cancellation evidence.

### 3. R3 — pure-ACK outbound conversion: PARTIAL

#### Implemented

`ClassifyPureACK` recognizes a conservative closed phrase/emoji set after optional mentions and punctuation, while rejecting additional substantive content (`internal/gateway/channels/outbound.go:20-70`). `sendReply` converts a targeted pure ACK into a NIP-25/NIP-29 reaction and returns without posting chat (`internal/gateway/channels/channels.go:517-532`). Every inbound NIP-29 message exposes that targeted reply closure (`channels.go:754-763`), and generated room replies invoke `msg.Reply` in both auto-joined and manually joined room handlers (`cmd/metiqd/main.go:2485-2490`; `cmd/metiqd/control_rpc_channels.go:291-296`).

The generated-reply feature defaults on and has an explicit room opt-out (`internal/gateway/channels/nostr_room_policy.go:40-44,108-109,150-152`). Focused and parity tests cover pure vs substantive replies and opt-out (`internal/gateway/channels/outbound_test.go:65-89,118-164`; `parity_matrix_test.go:255-284`).

#### Gap

The direct outbound RPC does not share this path. `ChannelsSendRequest` contains only `channel_id` and `text` (`internal/gateway/methods/schema_channels.go:57-62`); its handler calls `ch.Send(ctx, req.Text)` (`cmd/metiqd/control_rpc_channels.go:401-419`). `NIP29GroupChannel.Send` always builds kind-9 chat and never runs `ClassifyPureACK` (`internal/gateway/channels/channels.go:484-516`).

Thus generated/inbound targeted replies satisfy R3, but a direct sender cannot supply `replyToId`/`threadId` and cannot structurally convert a targeted pure ACK. Under the checklist's explicit “generated and direct paths” requirement, R3 is partial.

### 4. R4 — commitment enforcement and lifecycle: PARTIAL

#### Implemented primitives

`NIP29GroupChannel.Send` runs enforcement immediately before event construction/publication and records a blocked signal when rewritten (`internal/gateway/channels/channels.go:484-495`). The commitments package can hold multiple records keyed by stable ID, write the complete set to disk, schedule a one-line dropped notice, and acknowledge delivery persistently (`internal/commitments/commitments.go:186-251`; `store_persistence.go:9-49`; `heartbeat.go:104-174`).

#### Gaps

- Taskflow-room enforcement is not inferred/defaulted; the code says the room lacks a taskflow signal and requires explicit `commitmentEnforcement` (`internal/gateway/channels/commitment_enforcement.go:35-39`; `nostr_room_policy.go:60-65,180-182`).
- Backing is only an integer projection. Any successful recognized task/flow action permits the text; the outbound guard carries no handle and performs no live task/flow state resolution (`commitment_enforcement.go:13-16,44-49`; `internal/agent/commitment_guard.go:218-250`). Fabricated, stale, terminal, or wrong-room references therefore cannot be rejected by this seam.
- Commitment records have no backing type, handle, revision, or task/flow lifecycle correlation fields (`internal/commitments/commitments.go:34-59`). Their generic statuses are pending/fulfilled/broken/expired (`commitments.go:14-22`), not a correlated task/flow open→completed/dropped lifecycle.
- A production search excluding tests found no `commitments.NewFileStore`, `commitments.NewRuntime`, `CheckSessionHistory`, or heartbeat `MarkDelivered` caller outside the package. The durable/multi-record/dropped-notice machinery is reusable but not part of the running daemon's Nostr room guard.

### 5. R5 — scheduled progress ledger: PARTIAL

#### Implemented

A 30-second driver feeds a scheduler that enforces the configured review interval and post throttle (`cmd/metiqd/nostr_progress_ledger.go:18-26,55-93`). Evaluation is deterministic across stale mentions, unbacked commitments, duplicate claims, and loop cooldowns; a blank finding set produces no post (`internal/gateway/channels/progress_ledger.go:100-151,516-546`).

Crucially, this audit found the core pair guard is included: the collector calls `opts.guard.Guard().Snapshot()` (`cmd/metiqd/nostr_progress_ledger.go:107-111`), and the pair guard exposes the snapshot (`internal/gateway/channels/bot_loop_guard.go:171-182,323-324`). This is stronger than a legacy-breaker-only integration.

#### Gaps

- The recent-event ring is an in-memory `rooms` map (`internal/gateway/channels/progress_ledger.go:382-427`).
- `lastRun`/`lastPost` throttle state is another in-memory map (`progress_ledger.go:441-455,516-558`). Restart loses both review context and posting cadence; no hydrate/persist API exists.
- Although the evaluator supports commitments and a store adapter exists (`progress_ledger.go:112-124,356-375`), the daemon collector explicitly leaves commitments for future wiring and never populates them (`cmd/metiqd/nostr_progress_ledger.go:96-105`).
- Moderator policy is only a fixed pubkey or `true` for self (`internal/gateway/channels/nostr_room_policy.go:80-99,191-196`). There is no deterministic roster/day rotation.

### 6. R6 — typed task lifecycle and chat-shadow suppression: PARTIAL

#### Implemented

`BuildTaskStateEvent` emits complete kind-30900 state with `d`, domain, schema, status, priority, assignee, dependency, and repository data; validation enforces the kind, cryptographic envelope, trust, and mirror tags (`internal/tasks/task_events_v2.go:59-140`).

The FleetTask bridge publishes local state, subscribes using event-driven Nostr semantics, merges valid effective heads into its ledger, and invokes `OnTaskTransition` after an accepted transition (`internal/tasks/fleet_task_bridge.go:268-279,345-365,486-492`). The daemon feeds those transitions into the shared echo suppressor (`cmd/metiqd/main.go:5569-5583`; `cmd/metiqd/nostr_group_loop_control.go:187-205`).

The suppressor keys the one-announcement allowance by room/author/task and suppresses later same-author shadows (`internal/gateway/channels/echo_suppressor.go:404-445`). Both normal generated reply paths apply this check before `msg.Reply` (`cmd/metiqd/main.go:2461-2489`; `control_rpc_channels.go:273-295`).

#### Gap

A path search found no `*taskflow*` implementation under `internal/` or `cmd/`, and content inspection found no equivalent of openclaw-nostr's managed TaskFlow create/wait/resume/finish/fail/cancel bridge. The only concrete typed corpus integration is FleetTask kind-30900 state.

Consequently there is no TaskFlow mutation path that:

- emits a durable typed transition into the shared corpus;
- correlates commitment terminals;
- keeps `announce:false` typed-only; or
- routes `announce:true` through the same room/author/task throttle.

The present throttle applies to model-generated `replyText`. R6 is therefore partial even though its FleetTask half is substantial.

### 7. R7 — closed-loop scorecard: PARTIAL

#### Implemented

The per-room scorecard has a closed signal set covering ACK conversions, gate pass/drop, echo/task-echo drops, pair-guard trips, elections, blocked/dropped commitments, ledger runs/posts, and status reactions (`internal/metrics/scorecard.go:29-75`). The ledger supplies an oldest-unanswered-mention gauge (`scorecard.go:217-229`; `internal/gateway/channels/progress_ledger.go:535-543`).

The sliding message window computes a per-sender maximum, `1/n` baseline, and `2/n` alarm threshold after a minimum sample (`internal/metrics/scorecard.go:254-279,306-347`). Inbound messages are recorded at the loop-control gate and outbound messages in the channel send path (`cmd/metiqd/nostr_group_loop_control.go:293-296`; `internal/gateway/channels/channels.go:508-510`).

#### Gaps

- Signals expose only an `ack_conversion` count; no ACK-candidate/opportunity count or derived conversion rate exists (`internal/metrics/scorecard.go:29-75,197-215`).
- Echo and task-echo expose drop counts only; no generated-reply/opportunity denominator or derived drop rate exists (`scorecard.go:29-75`; drop call sites `cmd/metiqd/main.go:2465-2481`, `control_rpc_channels.go:275-288`).
- Commitment signals are `commitment_blocked` and `commitment_dropped` only. There are no open/opened/completed counts or open gauge (`scorecard.go:29-75`).
- `sharesLocked` defines `n := len(counts)`, where `counts` is built from every observed sender in `state.msgs` (`scorecard.go:254-279`). Since inbound humans are also recorded, humans can change both the baseline and alarm threshold. There is no configured/NIP-51 roster input to the scorecard.
- The file explicitly documents MAST transcript annotation as deliberately unimplemented and points to `swarmstr-z4ub` (`scorecard.go:18-23`). `bd show swarmstr-z4ub` on 2026-08-30 reports status **open**, priority 3.

## Beads filed

| ID | Scope | Status at audit completion |
|---|---|---|
| `swarmstr-h4qm` | R1 bounded cheap-model Tier-2 wiring | open |
| `swarmstr-452k` | R2 contested-event-only takeover cancellation | open |
| `swarmstr-439p` | R3 direct targeted ACK conversion | open |
| `swarmstr-hkse` | R4 validated durable task/flow commitment lifecycle | open |
| `swarmstr-9wiv` | R5 durable/rebuildable state, commitments, rotation | open |
| `swarmstr-r6ik` | R6 managed TaskFlow typed bridge/shared throttle | open |
| `swarmstr-mjy2` | R7 denominators/rates, lifecycle metrics, roster share | open |
| `swarmstr-z4ub` | R7 MAST LLM-judge annotation (pre-existing) | open |

Audit delivery is tracked by `swarmstr-dizx`.

## Conclusion

swarmstr follows the same broad discipline architecture as openclaw-nostr: mechanical ambient admission, deterministic speaker election, ACK reactions, commitment checks, scheduled supervision, typed task state, chat-shadow suppression, and per-room observability are all recognizable and wired to varying degrees. It does **not** yet match the stronger post-`5126df4` implementation contract.

The parity result is therefore:

- **R1-R7: PARTIAL**.
- **IMPLEMENTED or wholly MISSING recommendations:** none.
- **Overall:** substantial first-generation parity, but seven follow-up implementation tracks plus the existing MAST track remain before full R1-R7 parity can be claimed.
