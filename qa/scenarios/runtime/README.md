---
coverage_id: QA-RUNTIME-001
title: Runtime OpenTelemetry diagnostics are optional
domain: runtime
required_features: [otel-diagnostics]
required_plugins: []
parity_tier: P2
lane: deterministic
pstf: []
checks:
  - type: file_exists
    path: internal/diagnostics/otel.go
  - type: grep
    path: internal/diagnostics/otel.go
    pattern: StartToolCall
    must_find: true
---
## Steps
- Construct diagnostics with no OTEL endpoint.
- Construct diagnostics with an endpoint and create agent-turn, tool-call, and provider-request spans.

## Expected
- Diagnostics are no-op when no endpoint is configured.
- When enabled, spans and metric counters can be flushed to the configured OTLP-compatible endpoint.
