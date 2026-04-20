# FIPS Integration Test Harness

Automated integration tests for FIPS mesh transport in clawstr agents.

## Quick Start

```bash
# Run Go integration tests (no Docker needed)
./scripts/run-tests.sh

# Run with Docker E2E (requires FIPS daemon image)
./scripts/run-tests.sh --docker
```

## Test Layers

### Layer 1: Go Integration Tests (in-process)

Located in `internal/nostr/runtime/fips_integration_test.go`, these tests
exercise the full FIPS stack using loopback TCP connections. No Docker or
FIPS daemon required.

**Tests:**

| # | Test | What it validates |
|---|------|-------------------|
| 1 | `TestIntegration_DM_Over_FIPS` | Agent A sends DM to Agent B via FIPSTransport |
| 2 | `TestIntegration_Bidirectional_DM` | A↔B exchange — both directions work |
| 3 | `TestIntegration_Control_RPC_Over_FIPS` | Control request/response via FIPSControlChannel |
| 4 | `TestIntegration_TransportSelector_Fallback` | FIPS fails → falls back to relay |
| 5 | `TestIntegration_TransportSelector_FIPSOnly_NoFallback` | fips-only mode refuses relay fallback |
| 6 | `TestIntegration_MultiBurst` | 20-message burst — ordering and completeness |
| 7 | `TestIntegration_HealthAccessors` | ConnectionCount, IdentityCacheSize, ListenerAddr |
| 8 | `TestIntegration_DualPort_DM_And_Control` | DM + control on same agent (separate ports) |
| 9 | `TestIntegration_IdentityDerivation_Consistency` | 3 agents → 3 unique fd00::/8 addresses |
| 10 | `TestIntegration_ConnectionPool_Eviction` | Pool cap behaviour under load |

Run directly:
```bash
go test -tags experimental_fips -run 'TestIntegration_' -v ./internal/nostr/runtime/
```

### Layer 2: Docker E2E Tests

Uses `docker-compose.yml` to stand up a 3-node mesh:

```
┌──────────┐     ┌──────────────┐     ┌──────────┐
│ Agent-A   │◄───►│  FIPS Relay  │◄───►│ Agent-B   │
│ metiqd    │     │ (pure mesh)  │     │ metiqd    │
│ + fips    │     │              │     │ + fips    │
└──────────┘     └──────────────┘     └──────────┘
  172.28.0.11      172.28.0.10         172.28.0.12
```

**Requires:**
- FIPS daemon image (`ghcr.io/jmcorgan/fips:latest`)
- Docker with IPv6 support
- metiqd built with `-tags experimental_fips`

**Test scenarios** (run via `scripts/run-tests.sh --docker`):
1. DM over FIPS mesh (A → B)
2. ACP task dispatch over FIPS
3. Transport fallback (kill FIPS daemon → relay)
4. Fleet discovery shows FIPS peers
5. Control RPC round-trip
6. Latency comparison (FIPS vs relay)
7. Mesh healing after relay node failure

## Fixtures

### `fixtures/keys.json`

Pre-generated secp256k1 keypairs (well-known test vectors) for deterministic
node identities. Each agent derives a unique `fd00::/8` IPv6 address from its
pubkey via `SHA-256(pubkey)[0..15]`.

**⚠️ These keys are public test vectors — never use them in production.**

## CI Integration

Add to your CI pipeline (only runs when `experimental_fips` tag is set):

```yaml
- name: FIPS integration tests
  run: |
    go test -tags experimental_fips \
      -run 'TestIntegration_' \
      -v -count=1 -timeout 60s \
      ./internal/nostr/runtime/
```

The Docker E2E layer can be added as a separate CI job when the FIPS daemon
image is available in the CI environment.
