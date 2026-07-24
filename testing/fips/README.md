# FIPS Integration Test Harness

Automated integration tests for FIPS mesh transport in clawstr agents.

## Quick Start

```bash
# Run Go integration tests (no Docker needed)
./scripts/run-tests.sh

# Run opt-in real-daemon E2E (skips unless a local FIPS image exists)
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
| 11 | `TestIntegration_DelayedDiscovery_EventualFIPSSuccessClearsNegativeCache` | Discovery not ready → relay fallback, later FIPS success clears cache |
| 12 | `TestIntegration_SustainedDMExchange_DuringDaemonRekey` | Sustained bidirectional exchange across a hitless rekey window |
| 13 | `TestIntegration_NegativeCacheTTLExpiry_RetriesFIPS` | Negative cache expires and FIPS is retried |

Manual daemon-side soak coverage is documented in `manual-soak-scenarios.md`, including daemon restart with pooled TCP connections.

Run directly:
```bash
go test -tags experimental_fips -run 'TestIntegration_' -v ./internal/nostr/runtime/
```

### Layer 2: Real-daemon Docker interoperability (opt-in)

`real-daemon/` starts two privileged FIPS daemons with real `fips0` TUN
interfaces and a local event-driven Nostr relay. It validates signed kind-37195
publish/consume discovery, bidirectional production DM/control framing, `.fips`
identity priming, and forged-sender rejection.

The runner uses `FIPS_TEST_IMAGE` (default `fips-test:latest`) and skips cleanly
when that image is absent. Set `FIPS_REAL_DAEMON_REQUIRED=1` to make absence a
failure. Exact image build steps and the known local Docker Desktop blocker are
documented in [`real-daemon/README.md`](real-daemon/README.md).

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
