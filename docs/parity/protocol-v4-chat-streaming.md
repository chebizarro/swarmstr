# Gateway protocol v4 chat streaming

Swarmstr mirrors the OpenClaw `ChatEventSchema` at revision `5ff1690`
(`packages/gateway-protocol/src/schema/logs-chat.ts:143-223`).

## Wire contract

Protocol v4 uses one push event name, `chat`, whose payload is a closed
state union:

- `status`: startup `phase`
- `delta`: incremental `deltaText`; `replace: true` means the text is a
  complete replacement rather than an append
- `final`: successful terminal event
- `aborted`: cancellation terminal event
- `error`: failed terminal event with an optional normalized `errorKind`

Every variant includes non-empty `runId` and `sessionKey` plus a non-negative,
run-local `seq`. Optional routing fields are `agentId` and `spawnedBy`.

The gateway still records internal `chat.chunk` and `turn.result` telemetry
where existing observability code needs it, but those names are not advertised
as client push events. Clients must subscribe to `chat`.

## Ordering and coalescing

`ChatStream` serializes emissions for a logical run and assigns monotonically
increasing payload sequence numbers starting at zero. The first terminal state
wins; later terminal attempts are ignored.

The WebSocket runtime may coalesce adjacent delta events. A coalesced append
concatenates text and carries the newest sequence. A replacement discards
earlier buffered append text; subsequent append deltas extend that replacement
snapshot while retaining `replace: true`.

## Admission and clients

The current protocol version is 4. Invalid ranges and non-overlapping ranges
receive deterministic connect errors containing the requested and supported
ranges. The embedded Web UI requests exactly `[4,4]`, verifies the negotiated
version, checks that `chat` is advertised, and explicitly calls
`events.subscribe` before enabling controls.

The UI deduplicates each `(sessionKey, runId)` by payload sequence, applies
replacement deltas, and handles all five terminal/non-terminal states without
consuming `chat.chunk` or `turn.*`.

## Runtime integration boundary

Gateway producers use the existing `agent.StreamingRuntime` text callback and
do not change provider/runtime interfaces. Provider-neutral structured runtime
events and authoritative replacement deltas remain tracked in the runtime
workstream; the gateway contract is ready to accept them through
`ChatStream.Delta(text, replace)`.
