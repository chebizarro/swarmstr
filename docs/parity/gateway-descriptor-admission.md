# Gateway method descriptors and WebSocket admission

The protocol-v4 `hello-ok.features.methodDescriptors` array is the canonical public policy catalog for the methods advertised by a Metiq gateway. Each descriptor includes:

- `name` and `scope` (`operator.read`, `operator.write`, `operator.admin`, `operator.approvals`, `operator.pairing`, `node`, or `dynamic`)
- optional `startup: "unavailable-until-sidecars"`
- optional `controlPlaneWrite: true`
- the compatibility `since` marker

`internal/gateway/methods.MethodDescriptors` covers every core registry method. Extension-owned method names fail closed to `operator.admin` until they provide narrower metadata. Runtime startup rejects an incomplete, duplicate, or invalid descriptor set, so method listing and admission cannot silently drift.

## Admission behavior

Before dispatch, the WebSocket runtime enforces descriptor role and scope metadata. Node-scoped methods require an authenticated `role: "node"` connection; node clients cannot call operator methods. Explicit operator scopes are least-privilege, with `operator.write` satisfying reads. Host-sensitive methods that would require parameter-aware policy in OpenClaw are conservatively classified as `operator.admin` until Metiq has an equivalent resolver.

Paired device and node tokens are validated against the persisted pairing catalog. Device tokens are bound to approved roles and scopes; removed records, rotated tokens, role mismatches, and scope escalation are rejected with deterministic `DEVICE_AUTH_*` codes. The signed connect proof still binds the device key, nonce, role, scopes, timestamp, and token.

Existing protocol-range, origin, payload-size, handshake-rate, unauthorized-burst, and slow-client guards remain active. Methods marked startup-unavailable return retryable `UNAVAILABLE` errors while sidecars are pending. Methods marked as control-plane writes use a separate per-connection write budget and return a retryable flood error when exceeded.
