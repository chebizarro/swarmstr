# Investigation: Room-channel planning-only continuation + config surface

## Summary
Resolved. Room-channel turns now run the same bounded planning-only continuation as DMs, gated by a
new per-room `planningOnlyContinuation` policy flag (OR the assigned agent's `planning_only_continuation`),
and the flag is documented in the config reference and deploy example.

## Resolution (implemented)
- `swarmstr-495e.1` — `NostrRoomPolicy.PlanningOnlyContinuation` (`nostr_room_policy.go:82,243-246`) +
  shared `runTurnWithPlanningContinuation` (`cmd/metiqd/planning_continuation.go:12`) + tests.
- `swarmstr-495e.2` — wired into NIP29 (`main.go:2585`), NIP28 (`:2816`), Chat (`:2914`), Control-RPC
  (`control_rpc_channels.go:269`), before persistence/commitment/reply.
- `swarmstr-495e.3` — docs (`commitment-guard.md`, `nostr.md`, `groups.md`, `CONFIGURATION_GUIDE.md`)
  and `deploy/metiq-production/config.json.in` example.
- `swarmstr-495e.4` — verified; `go build ./...`, `go vet ./...`, and package tests pass.

## Symptoms
- DM agents that only acknowledge ("I'll do X") and call no tools are now re-prompted once
  (swarmstr-8jl2). The same announce-and-forget behaviour is reported/expected on room channels.
- The new `AgentConfig.PlanningOnlyContinuation` flag is undocumented with no config example and
  an implicit `false` default.

## Background / Prior Research
<!-- Phase 1.5 explore agents: git archaeology / external docs. -->
- (prior turn) openclaw-nostr commitment guard is **room-only** (`commitmentGuard` default false,
  `commitmentEnforcement` default true when taskFlows) and converts pure acks to reactions.
- (prior turn) OpenClaw host continues the loop on tool results / `endTurn:false` / steering queue.
- (prior turn) swarmstr room paths already have `CommitmentGuard`/`CommitmentEnforcement` via
  `internal/gateway/channels/commitment_enforcement.go` + `nostr_room_policy.go`, but only append a
  reminder note / rewrite outbound text — no retry.

## Investigation Log

### Phase 1 - Triage
**Hypothesis:** Room turns dispatch through several distinct paths (NIP29/Communikey/Concord auto-join,
chat/relay-filter, control RPC), and the continuation must be factored into a shared helper rather
than duplicated per path. The config flag likely needs a room/channel-level surface in addition to
the per-agent one.
**Findings:** pending.
**Evidence:** pending.
**Conclusion:** pending.

## Root Cause

Room-channel turns never invoke the planning-only continuation. The detection primitives
(`internal/agent/commitment_guard.go`) and the continuation helper (`internal/agent/continuation.go`)
are generic, but the only caller is the DM path `runInboundTurn` (`cmd/metiqd/main.go` ~5048-5066).
Room turns run in four separate sites with no shared turn-execution seam:

- NIP29 auto-join: `buildAutoJoinTurn` at `cmd/metiqd/main.go:2583`, reply at `:2652` (commitment ctx).
- NIP28: `main.go:2808`, reply at `:2832` (bare `turnCtx`).
- Chat kind:9: `main.go:2894`, reply at `:2918` (bare `turnCtx`).
- Control-RPC channel: `cmd/metiqd/control_rpc_channels.go:251`, reply at `:305` (commitment ctx).

(Relay-filter/NIP34 at `main.go:3032` delegates to `dmRunAgentTurnRef` → `runInboundTurn`, so it already
inherits DM continuation.)

There is also no room-level config flag: `PlanningOnlyContinuation` exists only on `AgentConfig`
(`internal/store/state/models_config.go:714`); `NostrRoomPolicy`
(`internal/gateway/channels/nostr_room_policy.go`) has `CommitmentGuard`/`CommitmentEnforcement` but
no continuation flag. Finally, the flag appears in no deploy template or config example
(`deploy/metiq-production/bootstrap.json.in`, `config.json.in`, `docs/reference/CONFIGURATION_GUIDE.md`).

## Recommendations

1. Config seam — add `PlanningOnlyContinuation bool` to `NostrRoomPolicy`
   (`internal/gateway/channels/nostr_room_policy.go`), parsed in `ResolveNostrRoomPolicy` from
   `config["planningOnlyContinuation"]` (and `planning_only_continuation`), default `false` (opt-in).
2. Shared execution seam — extract `runTurnWithPlanningContinuation(enabled, baseTurn, run)` in
   `cmd/metiqd` (bounded to one retry via `agent.ShouldRetryPlanningOnly(...,0,1)` and
   `agent.BuildPlanningOnlyContinuation`) and call it in the four room sites above, immediately after
   `ProcessTurn` and **before** `ContextWithCommitmentBacking`/`msg.Reply`, so the continuation's
   tool traces feed the commitment check (ordering already correct in the DM path).
3. Config surface/docs — document the flag and add examples: `docs/concepts/commitment-guard.md`,
   `docs/channels/nostr.md` / `docs/channels/groups.md`, `docs/reference/CONFIGURATION_GUIDE.md`, and
   commented entries in `deploy/metiq-production/bootstrap.json.in` / `config.json.in`.
4. Tests — unit-test the shared helper (planning-only => one retry; tool-using => none; disabled =>
   no-op; continuation error => fallback) and the room-policy parse default.

## Preventive Measures

- Keep turn-execution side effects (continuation, commitment backing, reply) behind one shared helper
  rather than per-channel closures; new channel kinds should call the seam.
- When adding an agent/runtime behaviour flag, add it to the config reference and a deploy template in
  the same change so `omitempty` opt-ins do not become undocumented.


## Investigator Findings

### 1. Dispatch Sites — Every Room-Channel Agent-Turn Dispatch Point

There are **5 distinct turn-dispatch paths** (not counting the DM path):

#### A. NIP29 Room (auto-join) — `main.go` ~2520–2680
- **Dispatch**: `nostrLoopControl.enqueue(sessionID, msg.EventID, func() { ... })` at line 2580
- **Turn**: `buildAutoJoinTurn(turnCtx, sessionID, decision.BodyForAgent, turnTools, turnExecutor)` at line 2583
- **Reply**: `msg.Reply(commitmentCtx, replyText)` at line 2652 — uses `commitmentCtx` (wraps `turnCtx` with `channels.ContextWithCommitmentBacking`), built from `agent.BuildCommitmentStateFromTraces(result.ToolTraces)` at line 2639

#### B. NIP28 Public Channel — `main.go` ~2795–2845
- **Dispatch**: goroutine inside `OnMessage` at line 2808
- **Turn**: `buildAutoJoinTurn(turnCtx, sessionID, msg.Text, turnTools, turnExecutor)` at line 2808
- **Reply**: `msg.Reply(turnCtx, replyText)` at line 2832 — uses raw `turnCtx`, **no commitment backing context**

#### C. Chat Channel (NIP-C7, kind:9) — `main.go` ~2885–2930
- **Dispatch**: goroutine inside `OnMessage` at line 2894
- **Turn**: `buildAutoJoinTurn(turnCtx, sessionID, msg.Text, turnTools, turnExecutor)` at line 2894
- **Reply**: `msg.Reply(turnCtx, replyText)` at line 2918 — uses raw `turnCtx`, **no commitment backing context**

#### D. Relay-Filter / NIP34 Inbox — `main.go` ~2940–3100
- **Dispatch**: goroutine deferred from `OnEvent` at line ~3020 (calls `dmRunAgentTurnRef(...)`)
- **Turn**: `dmRunAgentTurnRef(...) → runInboundTurn(...)` at line 5309 — goes through the **full DM turn path**, NOT `buildAutoJoinTurn`
- **Reply**: `replyFn` inside `runInboundTurn` at line 5185 — goes through `replyFn` closure, which is `nil` for this path (no DM reply, line 3022 passes `nil` as replyFn)

#### E. Control RPC Channel — `control_rpc_channels.go` ~240–310
- **Dispatch**: `controlNostrLoopControl.enqueue(sessionID, msg.EventID, func() { ... })` at line 251
- **Turn**: `filteredRuntime.ProcessTurn(turnCtx, agent.Turn{...})` at line 251 — builds `agent.Turn{}` directly, **does NOT use `buildAutoJoinTurn`**; no context engine, no history, no memory scope beyond `resolveMemoryScopeContext`
- **Reply**: `msg.Reply(commitmentCtx, replyText)` at line 305 — uses `commitmentCtx` with `channels.ContextWithCommitmentBacking` at line 295

### 2. Shared Turn-Dispatch Seam

**There is NO single shared seam.** The paths diverge:

| Path | Turn construction | Reply point | Uses `buildAutoJoinTurn`? |
|------|------------------|-------------|--------------------------|
| NIP29 (main.go) | `buildAutoJoinTurn` | `msg.Reply(commitmentCtx, ...)` line 2652 | YES |
| NIP28 (main.go) | `buildAutoJoinTurn` | `msg.Reply(turnCtx, ...)` line 2832 | YES |
| Chat (main.go) | `buildAutoJoinTurn` | `msg.Reply(turnCtx, ...)` line 2918 | YES |
| Relay-filter (main.go) | `runInboundTurn` (DM path) | `replyFn` line 5185 | NO |
| Ctrl-RPC (control_rpc_channels.go) | raw `agent.Turn{}` | `msg.Reply(commitmentCtx, ...)` line 305 | NO |
| DM (main.go) | raw `agent.Turn{}` in `runInboundTurn` | `replyFn` line 5185 | NO |

**Recommended single insertion seam**: `buildAutoJoinTurn` at `main.go:2287`.

- 3 of 5 room paths (NIP29, NIP28, Chat) already converge here.
- The control RPC path and the relay-filter path are outliers. The control RPC path at `control_rpc_channels.go:251` builds its own `agent.Turn{}` directly and would need separate wiring.
- The relay-filter path delegates to `runInboundTurn` (the DM path, line 5309) which already has planning-only continuation wired at lines 5048-5060.

**To cover 3 of 5 room paths with one change**: add planning-only continuation logic inside `buildAutoJoinTurn` (after `filteredRuntime.ProcessTurn`, before `msg.Reply`). This would cover NIP29, NIP28, and Chat in one shot.

### 3. Room/Channel Config Flags

#### `NostrRoomPolicy` — `internal/gateway/channels/nostr_room_policy.go:25–51`
Struct with 26 fields (see full field list in Phase 1.5 findings above). Key fields for this investigation:
- `CommitmentGuard bool` (default `false`) — line 46
- `TaskFlows bool` (default `false`) — line 47
- `CommitmentEnforcement bool` (default follows `TaskFlows`) — line 48

#### `ResolveNostrRoomPolicy` — `nostr_room_policy.go:155`
Parses from `config map[string]any`. All config keys (31 total parsed): `requireMention`, `allowBots`, `ambientPolicy`, `unmentionedInbound`, `shouldReplyGate`, `shouldReplyModel`, `shouldReplyModelTimeoutMs`, `responderElection`, `responderElectionTakeoverSeconds`, `ackAsReaction`, `echoSuppression`, `echoSimilarityThreshold`, `taskEchoSuppression`, `taskEchoSimilarityThreshold`, `commitmentGuard`, `taskFlows`, `commitmentEnforcement`, `progressLedger`, `progressLedgerIntervalSeconds`, `progressLedgerPostIntervalSeconds`, `progressLedgerStaleMentionSeconds`, `progressLedgerModerator`, `progressLedgerModeratorRotation`, `botLoopProtection`.

#### `boolFromAny` — TWO distinct definitions:
- **Strict** (used by `ResolveNostrRoomPolicy`): `nostr_room_policy.go:275` — returns `(value, ok)`, only accepts `bool`
- **Lenient** (used by `cmd/metiqd/memory_maintenance.go:104`): accepts `bool`, `string` ("true"/"1"/"yes"), `float64`/`int` (nonzero)

#### `parseNIP34AutoReviewConfig` — `cmd/metiqd/nip34_auto_review.go:101`
Parses `chanCfg.Config["auto_review"]` — can be `bool` or `map[string]any`. Defaults: `FollowedOnly: true`, `TriggerTypes: {pr, pr_update}`.

#### `AgentConfig.PlanningOnlyContinuation` — `internal/store/state/models_config.go:714`
```go
PlanningOnlyContinuation bool `json:"planning_only_continuation,omitempty"`
```
Comment: "enables automatic re-invocation when a DM turn produces only a planning-only response (promises without tool actions). Default false."

There is **no room/channel-level `planning_only_continuation` flag** — it only exists on the per-agent `AgentConfig` struct. A room-channel implementation would need a new config flag in either `NostrRoomPolicy` or `NostrChannelConfig.Config` map.

### 4. Config Examples/Docs

**Docs that reference channel config or `planning_only_continuation`:**
- `docs/concepts/commitment-guard.md` — lines 49, 146, 173, 195 — describes DM continuation, config table with `planning_only_continuation: bool / false`
- `docs/investigations/room-planning-only-continuation-2026-09-13.md` — this file
- `docs/channels/nostr.md` — general Nostr channel config reference (bootstrap.json, runtime ConfigDoc)
- `docs/channels/groups.md` — likely room/group config examples
- `docs/reference/CONFIGURATION_GUIDE.md` — configuration guide
- `.beads/issues.jsonl` — closed issue `swarmstr-8jl2.2` (line 95) and `swarmstr-8jl2.1` (line 96)

**Deploy config templates** (no `planning_only_continuation` in any):
- `deploy/metiq-production/bootstrap.json.in`
- `deploy/metiq-production/config.json.in`

**`planning_only_continuation` appears nowhere outside:**
- Go source files (`internal/store/state/models_config.go:714`, `internal/agent/continuation.go:7`, `internal/agent/runtime.go:193`, `cmd/metiqd/main.go:4910/5049`, `cmd/metiqd/main_telemetry.go:177`)
- Markdown docs (the three files above)
- Issue tracker (`.beads/issues.jsonl`)
- **No YAML/JSON/TOML/example/config template files contain it.**

### 5. `msg.Reply` and Commitment Enforcement Interaction

**`msg.Reply` is a closure** defined per-message in `InboundMessage` (`internal/gateway/channels/channels.go:42–60`, field `Reply func(ctx context.Context, text string) error`). It is bound at channel construction time by the NIP29/NIP28/Chat channel implementation (e.g., `NIP29GroupChannel.sendReply` at `channels.go:614`).

**Commitment enforcement flow per path:**

| Path | Reply context | Commitment check |
|------|--------------|-----------------|
| NIP29 (main.go:2652) | `commitmentCtx` — wraps `turnCtx` with `ContextWithCommitmentBacking` | YES — commitment state built from tool traces at 2639, injected into context before `msg.Reply` at 2652 |
| NIP28 (main.go:2832) | `turnCtx` (bare) | NO — no commitment backing |
| Chat (main.go:2918) | `turnCtx` (bare) | NO — no commitment backing |
| Ctrl-RPC (control_rpc_channels.go:305) | `commitmentCtx` — wraps with `ContextWithCommitmentBacking` | YES — built at 295 |
| DM (main.go:5185) | `replyFn` channel back | N/A — DM, no commitment context |

**The NIP29 commitment enforcement** (`channels.commitment_enforcement.go`) checks context for commitment backing data and may:
- Append a "task commitments not backed by tool results" notice
- Rewrite the reply text to include dropped-commitment warnings

**Importantly**: for NIP29, `msg.Reply(commitmentCtx, ...)` fires AFTER `filteredRuntime.ProcessTurn` returns. If planning-only continuation were added between ProcessTurn and msg.Reply, it must be added **before** `ctx := channels.ContextWithCommitmentBacking(turnCtx, ...)` so the continuation result's tool traces flow into the commitment check. The ordering is:

```
ProcessTurn → [continuation would go here] → BuildCommitmentStateFromTraces → ContextWithCommitmentBacking → msg.Reply(commitmentCtx, replyText)
```

The DM path already has this ordering right: `planningOnlyContinuation` check is at lines 5048–5060 (between `ProcessTurn` and the `replyFn` call at 5185), and the continuation result replaces `turnResult`, so the subsequent `BuildCommitmentStateFromTraces(turnResult.ToolTraces)` reads from the final result.

**For room channels**, the same ordering constraint applies. In `buildAutoJoinTurn` callers, the flow is:
```
result := ProcessTurn(prepared.TurnCtx, prepared.Turn)
[continuation would go here, replacing result]
ctx := BuildCommitmentStateFromTraces(result.ToolTraces)
msg.Reply(commitmentCtx, replyText)
```
