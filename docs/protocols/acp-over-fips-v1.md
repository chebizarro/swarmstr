# Swarmstr ACP over FIPS IPv6 — Application Protocol v1

Status: implemented, experimental (`experimental_fips` build tag)

This document specifies **swarmstr's application protocol** carried by TCP over a FIPS-provided IPv6 interface. It is not the FIPS wire format and does not redefine FIPS routing, Noise handshakes, FSP, discovery, or NAT traversal.

FIPS owns:

- identity-key and overlay routing;
- the `fd00::/8` interface and `.fips` DNS resolution;
- encrypted hop/session transport;
- kind-37195 `d=fips-overlay-v1` endpoint discovery.

Swarmstr owns everything below: TCP ports 1337/1338, the five-byte frame header, frame types, JSON envelopes, ACP messages, control RPC, and pipeline cancellation behavior.

## 1. Version domains

Two independent versions can appear in one delivered task:

1. Outer swarmstr-over-FIPS envelope: JSON field `v`, currently `1` (`FIPSApplicationProtocolVersion`).
2. Inner ACP message: JSON field `acp_version`, currently `1` (`acp.Version`).

Senders MUST emit both applicable versions. Receivers accept an omitted outer `v` (decoded as `0`) only as legacy-read compatibility. Receivers MUST NOT emit `v:0` and MUST reject unknown nonzero outer versions before application dispatch.

The kind-37195 advert's `version` is a third, independent discovery-schema version.

## 2. Addressing and connection setup

Before dialing a peer, swarmstr resolves `<npub>.fips` as IPv6. The query both verifies the expected deterministic FIPS IPv6 address and primes the local daemon's identity/routing cache. Swarmstr then dials the derived address with TCP over the FIPS IPv6 interface.

Default ports:

| Port | Service |
| --- | --- |
| 1337 | DM/ACP application messages |
| 1338 | Control RPC |

These TCP services are swarmstr conventions, not FIPS/FSP service definitions.

## 3. Common TCP framing

Every application frame is:

```text
+----------------------+-----------+-------------------+
| payload_len (u32 BE) | type (u8) | payload (N bytes) |
+----------------------+-----------+-------------------+
```

`payload_len` counts only payload bytes; it excludes the one-byte type. The maximum payload is 262,144 bytes. A receiver closes the connection after an oversized or truncated frame. Multiple frames may use one TCP connection and retain order.

Frame types:

| Hex | Name | Payload |
| --- | --- | --- |
| `0x01` | DM | versioned DM JSON |
| `0x02` | control request | versioned control-request JSON |
| `0x03` | control response | versioned control-response JSON |
| `0x04` | ping | empty |
| `0x05` | pong | empty |

Ping and pong are type-only heartbeat frames and MUST have `payload_len=0`; their shape is fixed by this v1 specification and therefore has no JSON `v` field.

Unknown frame types are ignored. Implementations must fully consume the declared payload before reading the next header.

## 4. DM envelope

A v1 DM payload is UTF-8 JSON:

```json
{
  "v": 1,
  "from": "<64-character hex Nostr pubkey>",
  "text": "<application message>",
  "ts": 1784928000
}
```

`text` may be ordinary plaintext or a serialized ACP `Message`. ACP recognition uses the inner `acp_type` discriminator.

Inbound validation order:

1. decode JSON and reject unknown nonzero `v`;
2. resolve the remote FIPS IPv6 source to an authenticated pubkey;
3. parse both authenticated and claimed `from` pubkeys;
4. require exact canonical pubkey equality;
5. only then deliver the DM.

The plaintext `from` field is a consistency claim, never the identity authority.

## 5. Control RPC envelopes

Request (`0x02`):

```json
{
  "v": 1,
  "req_id": "01J...",
  "from": "<64-character hex Nostr pubkey>",
  "method": "status.get",
  "params": {}
}
```

Response (`0x03`):

```json
{
  "v": 1,
  "req_id": "01J...",
  "result": {},
  "error": "",
  "error_code": 0
}
```

A response carries either `result` or a nonempty `error`; on error, `result` is omitted. Request and response correlate by `req_id` and use the same TCP connection. Every emitted response carries `v:1`, including responses to legacy `v:0` requests and malformed/unsupported requests when a response can be formed. Unsupported versions and invalid sender claims use JSON-RPC invalid-request code `-32600`.

Control listeners require an authenticated remote-address identity resolver and apply the same `from` equality rule as DMs before invoking a method handler. A channel sharing a `FIPSTransport` should use its `ResolveIdentity` method after discovery has populated identities with `RegisterIdentity`. `method` is required. `params` is method-defined JSON.

## 6. ACP task, result, and cancellation

The DM `text` for ACP is the JSON `acp.Message` schema. Relevant discriminators are:

- `acp_type:"task"`: delegated work request;
- `acp_type:"result"`: exactly one terminal worker result;
- `acp_type:"cancel"`: requester cancellation for the same `task_id`;
- `acp_type:"ping"` / `"pong"`: ACP-level health messages, distinct from TCP frame heartbeat types.

All current ACP messages emit `acp_version:1`. Routing and deduplication use `task_id`; sender authorization uses the authenticated DM sender, not an untrusted payload field.

Task lifecycle:

```text
queued -> running -> succeeded | failed | blocked | timed_out | cancelled | lost
```

Terminal transitions are compare-before-update and idempotent. A late result or duplicate cancellation cannot overwrite an existing terminal state.

Cancellation contract:

- requester timeout or cancellation sends at most one remote `acp_type:"cancel"` to the worker owning `task_id`;
- the worker accepts cancellation only from the authenticated original requester;
- local wait state and remote execution are both cancelled;
- cancellation is event-driven; absence of an event after a delay is not completion;
- timeout is represented as `ACP_TURN_FAILED` with `detail_code:"TURN_TIMEOUT"` where manager errors are exposed.

## 7. Pipeline schema and behavior

A pipeline is an ordered `steps` array. Each step has:

```json
{
  "peer_pubkey": "<worker hex pubkey>",
  "instructions": "work description",
  "task": {},
  "context_messages": [],
  "memory_scope": "project",
  "tool_profile": "coding",
  "enabled_tools": [],
  "parent_context": {"session_id": "parent", "agent_id": "main"},
  "timeout_ms": 60000,
  "artifacts": [],
  "label": "optional display name"
}
```

Optional orchestration fields are `flow_id`, `owner_session_key`, `goal`, and `max_concurrency`. `max_concurrency<=0` selects the safe default of 4.

Sequential execution dispatches one step at a time and may feed prior text/artifacts to the next step. Parallel execution never has more than `max_concurrency` remote steps in flight. On the first send/wait/worker failure it cancels the shared pipeline context and fans out one remote cancellation to every other registered in-flight worker. Partial `PipelineResult` entries remain index-correlated and are not treated as an atomic batch.

Historical/realtime completion rules follow the underlying event stream: an ACP result or explicit terminal event completes work; timers only bound execution and trigger cancellation.

## 8. `acp_dispatch` and `reply_dispatch`

The names are intentionally **not aliases**:

- `acp_dispatch` is a workflow/gateway operation that creates and sends delegated ACP work to a worker and correlates its result by `task_id`.
- `reply_dispatch` is an OpenClaw-compatible local plugin hook around delivery of a user-facing reply.

An ACP result may later enter the ordinary reply-delivery pipeline, which can emit `reply_dispatch`, but receiving or sending `reply_dispatch` never creates remote ACP work. Implementations must not put either operation name into the other protocol as a substitute discriminator.

## 9. Compatibility and evolution

- v1 writers always emit outer `v:1`.
- v1 readers accept missing/zero `v` for legacy DM and control JSON only.
- unknown nonzero versions fail closed.
- additive JSON fields may be ignored by v1 readers; changing framing, identity rules, required field meaning, or frame-type semantics requires a new nonzero version.
- the legacy reader does not weaken authenticated sender checks.

Implementation sources: `internal/nostr/runtime/fips_transport.go`, `fips_listener.go`, `fips_control.go`, and `internal/acp`.
