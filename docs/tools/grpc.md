---
summary: "Native gRPC endpoint profiles and generated agent tools in metiq"
read_when:
  - Connecting an agent to approved gRPC services
  - Configuring gRPC reflection or descriptor-set discovery
  - Calling unary or streaming gRPC methods from tools
title: "gRPC Tools"
---

# gRPC Tools

metiq can expose approved gRPC services as native agent tools. The runtime discovers
methods from configured endpoint profiles, generates JSON-schema-backed tools, and
invokes RPCs in-process without MCP/plugin bridges. Runtime reflection and invocation
do not require `protoc`; descriptor-set mode consumes a prebuilt descriptor file.

Agents **cannot choose arbitrary gRPC hosts**. Every callable service must be listed
in the runtime config under `grpc.endpoints[]`.

## What gets generated

Each discovered gRPC method becomes one or more tools:

- Unary methods: one tool
  - `grpc_<profile>_<service>_<method>`
- Server streaming methods: `start`, `receive`, `finish`
- Client streaming methods: `start`, `send`, `finish`
- Bidirectional streaming methods: `start`, `send`, `receive`, `finish`

Tool names are stable snake_case names. If two methods collide after normalization,
metiq appends a short stable hash.

Every generated descriptor uses:

- `Origin.Kind = "grpc"`
- `Origin.ServerName = <endpoint id>`
- `Origin.CanonicalName = </package.Service/Method>`
- `InputJSONSchema` generated from protobuf descriptors
- aliases: `body`, `input`, `args` → `request`; `headers` → `metadata`; `timeout_ms` → `deadline_ms`

Unary tools are concurrency-safe. Stream session tools are not concurrency-safe and
use cancel-on-interrupt behavior.

## Configuration

Add a top-level `grpc` block to the metiq runtime config:

```yaml
grpc:
  endpoints:
    - id: billing
      target: billing.internal:443
      discovery:
        mode: reflection
        refresh_ttl: 10m
      transport:
        tls_mode: system
        server_name: billing.internal
      auth:
        metadata:
          authorization: "Bearer ${BILLING_TOKEN}"
        allow_override_keys: [x-request-id, x-correlation-id]
      defaults:
        dial_timeout_ms: 10000
        reflection_timeout_ms: 5000
        deadline_ms: 15000
        max_deadline_ms: 120000
        max_recv_message_bytes: 4194304
      exposure:
        mode: auto
        deferred_threshold: 25
        namespace: grpc_billing
        include_services: [acme.billing.InvoiceService]
        exclude_methods: [/acme.billing.InvoiceService/DeleteEverything]
```

### Endpoint fields

| Field | Meaning |
|---|---|
| `id` | Stable endpoint profile id. Used in generated tool names and provenance. |
| `target` | gRPC dial target such as `host:443` or `127.0.0.1:50051`. |
| `discovery` | How metiq loads service descriptors. |
| `transport` | TLS, CA, mTLS, or explicit plaintext settings. |
| `auth` | Default outgoing metadata and allowed per-call metadata overrides. |
| `defaults` | Dial/reflection deadlines, call deadline bounds, and max receive size. |
| `exposure` | Tool namespace, service filtering, excluded methods, and inline/deferred policy. |

Defaults if omitted:

- discovery mode: `reflection`
- TLS mode: `system`
- dial timeout: `10000ms`
- reflection timeout: `5000ms`
- call deadline: `15000ms`
- max call deadline: `120000ms`
- max receive message bytes: `4194304`
- exposure mode: `auto`
- auto deferred threshold: `25` generated tools

## Discovery modes

### Reflection (recommended)

Use reflection when the server enables gRPC Server Reflection:

```yaml
grpc:
  endpoints:
    - id: health
      target: health.internal:443
      discovery:
        mode: reflection
      transport:
        tls_mode: system
        server_name: health.internal
      exposure:
        namespace: grpc_health
        include_services: [grpc.health.v1.Health]
```

Reflection discovers services at startup using the endpoint transport/auth policy.
If `descriptor_set` is also present, metiq can fall back to the static descriptor
set when reflection is unavailable.

### Static descriptor set

Use descriptor sets for production systems where reflection is disabled:

```bash
protoc \
  -I proto \
  -I third_party/googleapis \
  --include_imports \
  --descriptor_set_out=dist/billing-api.pb \
  proto/acme/billing/invoice.proto
```

```yaml
grpc:
  endpoints:
    - id: billing_static
      target: billing.internal:443
      discovery:
        mode: descriptor_set
        descriptor_set: dist/billing-api.pb
      transport:
        tls_mode: system
        server_name: billing.internal
      exposure:
        namespace: grpc_billing
        include_services:
          - acme.billing.InvoiceService
```

Descriptor-set mode does not require server reflection. The descriptor file must
include imports for any referenced messages, enums, or well-known wrappers not
already in the process registry.

### Proto files

The config model recognizes `mode: proto_files` with `proto_files` and
`import_paths`, but runtime discovery currently returns a clear error for this
mode. For now, compile source protos to a descriptor set and use
`mode: descriptor_set`.

```yaml
discovery:
  mode: proto_files        # reserved; not currently executable
  proto_files: [proto/acme/billing/invoice.proto]
  import_paths: [proto, third_party/googleapis]
```

## Filtering and exposure

Use `include_services` and `exclude_methods` to keep the generated catalog small
and safe:

```yaml
exposure:
  mode: auto              # auto | inline | deferred
  deferred_threshold: 25
  namespace: grpc_billing
  include_services:
    - acme.billing.InvoiceService
  exclude_methods:
    - /acme.billing.InvoiceService/DeleteInvoice
    - /acme.billing.Admin/DeleteEverything
```

- `namespace` prefixes generated names, for example `grpc_billing_...`.
- `include_services` matches full service names.
- `exclude_methods` accepts full `/package.Service/Method` names,
  `package.Service/Method` names, or bare method names. To exclude a whole
  service, omit it from `include_services` or list each method explicitly.
- `mode: inline` always includes tools in the normal tool list.
- `mode: deferred` always exposes tools through deferred tools.
- `mode: auto` defers the profile when its generated tool count exceeds
  `deferred_threshold`.

## Unary calls

A unary tool accepts:

```json
{
  "request": { "id": "inv-123" },
  "metadata": { "x-request-id": "req-2026-05-08" },
  "deadline_ms": 5000
}
```

Aliases are accepted, so this is equivalent:

```json
{
  "input": { "id": "inv-123" },
  "headers": { "x-request-id": "req-2026-05-08" },
  "timeout_ms": 5000
}
```

Typical result envelope:

```json
{
  "ok": true,
  "profile": "billing",
  "method": "/acme.billing.InvoiceService/GetInvoice",
  "response": { "id": "inv-123", "status": "PAID" },
  "status": { "code": "OK" },
  "duration_ms": 42
}
```

Schema validation failures are reported before dialing. Metadata policy errors are
semantic validation failures. RPC status errors and stream-state problems such as
unknown stream IDs or wrong-direction send/receive calls are runtime execution
failures and include the relevant gRPC or stream status where available.

## Streaming workflow

Streaming RPCs are modeled as short tool calls over a turn-scoped stream session.
Stream IDs are created by `start`, then passed to `send`, `receive`, or `finish`.
They are not persisted across turns.

### Server streaming

1. Start the stream with the request:

```json
{
  "request": { "account_id": "acct_123" },
  "deadline_ms": 30000
}
```

2. Receive one or more messages:

```json
{
  "stream_id": "grpc_stream_...",
  "max_messages": 10
}
```

3. Finish when done, or after a terminal receive:

```json
{ "stream_id": "grpc_stream_..." }
```

### Client streaming

1. Start with optional metadata/deadline:

```json
{ "metadata": { "x-request-id": "upload-1" }, "deadline_ms": 60000 }
```

2. Send messages:

```json
{ "stream_id": "grpc_stream_...", "message": { "chunk": "..." } }
```

3. Finish and read the final response:

```json
{ "stream_id": "grpc_stream_..." }
```

### Bidirectional streaming

1. Start the stream.
2. Alternate `send` and `receive` calls as the protocol requires.
3. Call `finish` to half-close and close the session.

For bidirectional or server-streaming methods, `finish` can drain remaining server
messages:

```json
{
  "stream_id": "grpc_stream_...",
  "drain_remaining": true,
  "max_messages": 64
}
```

Duplicate `finish` calls are idempotent. `send` after close, unknown stream IDs,
and wrong-direction operations return runtime execution errors.

## Auth, metadata, deadlines, and transport

### Metadata auth

Default metadata is configured per endpoint and sent on every call:

```yaml
auth:
  metadata:
    authorization: "Bearer ${BILLING_TOKEN}"
    x-tenant-id: tenant-a
  allow_override_keys:
    - x-request-id
    - x-correlation-id
```

Per-call `metadata` can only set keys listed in `allow_override_keys`. Metadata
keys must be lowercase and cannot be pseudo-headers or `grpc-*` reserved keys.

Secrets are redacted from returned tool results, returned errors, stream lifecycle
error strings, and post-execute hook results when they match sensitive keys or
common bearer/basic/`secret:` forms. Raw tool-call arguments, including per-call
`metadata`, may still be visible to pre-execute hooks or traces, so avoid passing
secrets as per-call overrides. Prefer profile-level metadata and audit custom
hooks before enabling sensitive gRPC profiles.

The metadata values are sent exactly as configured, so resolve any `${...}`
placeholders before they reach the runtime config if your deployment uses
environment or secret-store interpolation.

### Deadlines

Each call gets a deadline. A tool-supplied `deadline_ms` is bounded by
`defaults.max_deadline_ms`: unary calls clamp oversized values down to the max,
while stream start policy rejects values above the max. Set `max_deadline_ms` to
the largest duration the endpoint should allow.

```yaml
defaults:
  deadline_ms: 15000
  max_deadline_ms: 120000
```

### TLS and mTLS

System roots:

```yaml
transport:
  tls_mode: system
  server_name: billing.internal
```

Custom CA:

```yaml
transport:
  tls_mode: custom_ca
  ca_file: /etc/metiq/ca.pem
  server_name: billing.internal
```

mTLS:

```yaml
transport:
  tls_mode: mtls
  ca_file: /etc/metiq/ca.pem
  cert_file: /etc/metiq/client.pem
  key_file: /etc/metiq/client.key
  server_name: billing.internal
```

Local plaintext development only:

```yaml
transport:
  tls_mode: insecure
```

`insecure` cannot be combined with CA, cert, key, or `server_name` settings.

## Example endpoint profiles

### Production billing via reflection

```yaml
grpc:
  endpoints:
    - id: billing
      target: billing.internal:443
      discovery:
        mode: reflection
        refresh_ttl: 10m
      transport:
        tls_mode: system
        server_name: billing.internal
      auth:
        metadata:
          authorization: "Bearer ${BILLING_TOKEN}"
        allow_override_keys: [x-request-id, x-correlation-id]
      defaults:
        deadline_ms: 15000
        max_deadline_ms: 60000
      exposure:
        mode: auto
        deferred_threshold: 20
        namespace: grpc_billing
        include_services: [acme.billing.InvoiceService]
```

### Locked-down reports API via static descriptor

```yaml
grpc:
  endpoints:
    - id: reports
      target: reports.internal:443
      discovery:
        mode: descriptor_set
        descriptor_set: /opt/metiq/descriptors/reports.pb
      transport:
        tls_mode: mtls
        ca_file: /etc/metiq/ca.pem
        cert_file: /etc/metiq/reports-client.pem
        key_file: /etc/metiq/reports-client.key
        server_name: reports.internal
      auth:
        metadata:
          x-api-key: "${REPORTS_API_KEY}"
        allow_override_keys: [x-request-id]
      defaults:
        deadline_ms: 30000
        max_deadline_ms: 120000
        max_recv_message_bytes: 8388608
      exposure:
        mode: deferred
        namespace: grpc_reports
        include_services:
          - acme.reports.ReportService
```

### Local development server

```yaml
grpc:
  endpoints:
    - id: local_echo
      target: 127.0.0.1:50051
      discovery:
        mode: reflection
      transport:
        tls_mode: insecure
      exposure:
        mode: inline
        namespace: grpc_echo
```

## Validation coverage

The current gRPC integration is validated by tests covering these ten items:

1. Native `grpc` origin provenance on generated tools.
2. Endpoint config parsing and validation, including unknown-field rejection.
3. Reflection discovery against an in-process real gRPC server.
4. Reflection-disabled/static descriptor-set discovery and reflection fallback.
5. Protobuf-to-JSON schema generation for enums, maps, oneofs, scalar types, and well-known types.
6. Unary execution with aliases, bounded deadlines, metadata merge, status envelopes, and dynamic `Any` resolution.
7. Server, client, and bidirectional streaming lifecycle with turn cancellation and idempotent finish.
8. Large catalog exposure defers when `mode: auto` exceeds the configured threshold.
9. Auth/TLS/mTLS/custom CA/max receive size policy, including rejection of unsafe metadata overrides.
10. Secret redaction for returned results/errors, post-execute hook results, lifecycle errors, and stream progress events.

Run the focused validation suite with:

```bash
go test ./internal/agent/toolgrpc ./internal/config ./internal/agent
```
