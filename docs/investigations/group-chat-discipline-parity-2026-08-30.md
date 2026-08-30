# Investigation follow-up: swarmstr parity with group-chat discipline

**Date:** 2026-08-30  
**Repository:** `swarmstr` (`metiq` Go daemon)  
**Reference:** `openclaw-nostr` commit `5126df41248bcb799d61e870e84052afaa3f1aca`
**Scope:** implementation follow-up to the original read-only gap analysis

## Updated result

The seven P1/P2 follow-up gaps are implemented. R1-R6 now match the post-`5126df4` behavioral contract, and the deterministic/lifecycle portion of R7 is implemented. The only remaining parity item is the separate P3 MAST LLM-judge annotation bead, `swarmstr-z4ub`.

- **IMPLEMENTED:** R1-R6; R7 deterministic scorecard, rates, commitment lifecycle, and roster share.
- **PARTIAL:** R7 only because MAST transcript annotation remains separate P3 work.
- **MISSING as a whole recommendation:** none.

## Parity matrix

| Item | State | swarmstr implementation evidence | Contract coverage | Bead |
|---|---|---|---|---|
| R1 should-reply gate | **IMPLEMENTED** | Bounded resolver/hook registry and fail-quiet verdicts in `internal/gateway/channels/should_reply_gate.go`; room model/timeout policy in `nostr_room_policy.go`; deployment light-model adapter in `cmd/metiqd/nostr_should_reply_model.go`; production invocation in `nostr_group_loop_control.go` | Only Tier-1 ambiguity invokes Tier 2. No hook, timeout, invalid verdict, and provider error drop quietly. Deterministic pass/drop plus mention/command paths bypass the model. | `swarmstr-h4qm` |
| R2 responder election | **IMPLEMENTED** | `TakeoverCoordinator.ObserveRoomMessage` in `internal/gateway/channels/responder_election.go`; focused and production regressions in `responder_election_test.go` and `cmd/metiqd/nostr_responder_election_test.go` | Unrelated elected-agent traffic leaves takeover armed. A reply/thread relation to the contested event or event-targeted reaction/claim cancels it. | `swarmstr-452k` |
| R3 ACK-to-reaction | **IMPLEMENTED** | Additive `TargetedChannel` contract and shared target-aware send path in `internal/gateway/channels/channels.go`; direct-send target schema in `internal/gateway/methods/schema_channels.go`; RPC routing in `cmd/metiqd/control_rpc_channels.go` | Targeted direct pure ACKs use the same reaction seam as generated replies. Substantive and targetless sends remain chat; room opt-out still applies. | `swarmstr-439p` |
| R4 commitment guard/lifecycle | **IMPLEMENTED** | Concrete-reference extraction in `internal/agent/commitment_guard.go`; live resolver contract in `internal/gateway/channels/commitment_enforcement.go`; v2 atomic multi-record persistence and backing correlation in `internal/commitments`; daemon resolver/heartbeat in `cmd/metiqd/nostr_commitments.go` | `taskFlows:true` enables enforcement by default with explicit opt-out. Fabricated, stale, terminal, and wrong-room flow handles fail. Multiple commitments survive restart. Task/flow terminal transitions correlate completed/dropped lifecycle, and dropped notices are durably acknowledged once. | `swarmstr-hkse` |
| R5 progress ledger | **IMPLEMENTED** | Durable recorder/scheduler state in `internal/gateway/channels/progress_ledger_persistence.go`; open-commitment and pair-guard collection in `cmd/metiqd/nostr_progress_ledger.go`; daily roster rotation in `nostr_room_policy.go` | Review window and last-run/last-post state survive restart; open commitments and the core pair guard are inputs; exactly one sorted roster member moderates each room/day without restart; silence and throttling remain intact. | `swarmstr-9wiv` |
| R6 typed task lifecycle | **IMPLEMENTED** | Persisted `FlowRegistry` transition observers and per-mutation announcement context in `internal/acp/flow.go`; `announce` input in `internal/gateway/methods/schema_acp.go`; shared explicit/generated announcement router in `internal/gateway/channels/echo_suppressor.go`; daemon bridge in `cmd/metiqd/nostr_taskflow_bridge.go` and `main.go` | Managed flow create/start/wait/resume/finish/fail/cancel transitions enter the typed corpus. Mutations are typed-only by default; an explicit `announce:true` invocation sends through the same room/author/task throttle, so explicit and generated shadows suppress one another. Terminal transitions also close commitments. | `swarmstr-r6ik` |
| R7 closed-loop scorecard | **PARTIAL (MAST only)** | Denominators/rates, commitment open/opened/completed/dropped values, observed-sender diagnostics, and roster-only share math in `internal/metrics/scorecard.go`; ACK/echo opportunity call sites in channel/daemon send paths; live NIP-51 roster wiring in `nostr_group_loop_control.go` | ACK conversion and echo/task-echo drop rates are denominator-backed. Commitment lifecycle is visible. Fair share uses `1/n` and `2/n` where `n` is the fleet roster; humans remain diagnostic-only and do not change `n`. MAST annotation remains open. | `swarmstr-mjy2`; MAST: `swarmstr-z4ub` |

## Behavioral notes

### R1 — bounded Tier 2

The preflight remains deterministic. It retains an ambiguous outcome long enough for the daemon to request a deployment hook. The hook is context-aware, has a bounded per-room timeout, uses a no-tools/no-session cheap model runtime, accepts only the closed `RESPOND`/`IGNORE`/`STOP` vocabulary, and fails quiet on every integration failure. Mention, command, trusted, and deterministic heuristic paths never call it.

### R4 — durable validated commitments

Same-turn tool names or success counts are no longer sufficient evidence. Structured results yield concrete `task:`/`flow:` references, which are resolved immediately before send against the live FleetTask bridge or durable ACP FlowRegistry. Flow ownership is room-scoped where available. The commitment store now persists backing references, supports multiple pending records per room, migrates v1 files to an atomic v2 format, and resolves every record correlated with a terminal backing handle.

The daemon owns the file store and dropped-notice heartbeat. Successful notice delivery records `DroppedNoticeAt`; retries do not consume the acknowledgement, and lifecycle counters are protected from double counting when a terminal transition precedes the visible notice.

### R5 — durable supervision and rotation

The bounded event ring and review/post throttle maps are file-backed. The collector combines recent events, the live fleet-task view, pending durable commitments, the live NIP-51 roster, and `PairLoopGuard.Snapshot()`. Explicit fixed moderators still take precedence. With `progressLedgerModeratorRotation:true`, a stable hash of room plus UTC day selects exactly one member from the sorted deduplicated roster on every tick, so rotation changes without restart.

### R6 — managed TaskFlow bridge

`FlowRegistry` emits an observer event only after a mutation is persisted. The bridge maps those events into the existing typed transition corpus. Mutations remain typed-only unless the invoking `acp.pipeline` request carries `announce:true`; that intent travels with the persisted transition rather than becoming room-wide policy. The explicit router consumes the same `(room, author, task)` allowance used by generated-reply suppression, rather than performing a raw send.

### R7 — rates and fleet share

Per-room snapshots retain the all-sender observed window while separately computing agent traffic over the configured/live fleet roster. Silent roster agents remain part of `n`; human traffic does not affect the baseline or alarm threshold. Zero denominators produce zero rates.

## Bead state for this implementation session

| ID | Scope | Final state |
|---|---|---|
| `swarmstr-h4qm` | R1 bounded cheap-model Tier 2 | closed |
| `swarmstr-452k` | R2 contested-event cancellation | closed |
| `swarmstr-439p` | R3 direct targeted ACK conversion | closed |
| `swarmstr-hkse` | R4 validated durable commitment lifecycle | closed |
| `swarmstr-9wiv` | R5 durable state/commitments/rotation | closed |
| `swarmstr-r6ik` | R6 managed TaskFlow bridge/shared throttle | closed |
| `swarmstr-mjy2` | R7 rates/lifecycle/roster share | closed |
| `swarmstr-z4ub` | MAST LLM-judge annotation | open with implementation notes; not required to block R1-R7 core fixes |

## Remaining P3 work

`swarmstr-z4ub` remains the cleanly separated follow-up: choose a judge model/provider, sample bounded room transcripts, apply the MAST taxonomy used by `openclaw-nostr` `5126df4`, persist/schedule annotations, and join the failure-mode counts into `RoomScorecardSnapshot`. This session leaves that larger LLM/scheduling feature open rather than weakening the seven production fixes above.
