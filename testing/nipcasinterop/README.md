# NIP-CAS-0006 Go ↔ TypeScript interoperability

This harness verifies the production wire contract between swarmstr and
openclaw-nostr against a real in-process WebSocket relay.

The scenario publishes an open task and a competing Go claim, lets the
TypeScript implementation publish an earlier winning claim and a continuation,
then starts a fresh Go merge from the relay's retained addressable heads. Both
implementations must select the same winning claim and effective continuation.

Run it from the swarmstr repository root:

```sh
OPENCLAW_NOSTR_INTEROP_DIR=/path/to/openclaw-nostr \
  go test ./internal/tasks \
    -run TestNIPCAS0006OpenClawNostrLiveRelayInterop \
    -count=1 \
    -v
```

The openclaw-nostr checkout must have dependencies installed. If its
`node_modules` lives elsewhere, set `OPENCLAW_NOSTR_NODE_MODULES` to that
directory. Without `OPENCLAW_NOSTR_INTEROP_DIR`, the test skips so the normal
swarmstr unit suite remains self-contained.
