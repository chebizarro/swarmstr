---
summary: "Helper scripts, testing workflows, and automation scripts for swarmstr development"
read_when:
  - Using or adding helper scripts in the swarmstr repo
  - Testing swarmstr components
  - Automating swarmstr operations from scripts
title: "Scripts & Testing"
---

# Scripts & Testing

## Go Test Suite

swarmstr uses Go's standard testing framework.

```bash
# Run all tests
go test ./...

# With race detector (always recommended)
go test -race ./...

# Verbose output
go test -v ./...

# Single package
go test ./internal/nostr/...

# Single test
go test -run TestRelayConnect ./...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Helper Scripts

Helper scripts live in `scripts/` in the repo:

### `scripts/test-dm.sh`

Send a test Nostr DM to verify the agent is running:

```bash
#!/bin/bash
# Sends a test DM and waits for the agent to reply
./scripts/test-dm.sh --to npub1abc... --message "ping" --timeout 30
```

### `scripts/relay-check.sh`

Check connectivity to configured relays:

```bash
./scripts/relay-check.sh
# Output:
# ✓ wss://relay.damus.io (42ms)
# ✓ wss://relay.nostr.band (95ms)
# ✗ wss://nostr.example.com (timeout)
```

### `scripts/log-watch.sh`

Watch daemon logs with filtering:

```bash
# All logs
./scripts/log-watch.sh

# Filter by level
./scripts/log-watch.sh --level error

# Filter by keyword
./scripts/log-watch.sh --grep "relay"
```

### `scripts/benchmark.sh`

Run agent turn benchmarks:

```bash
./scripts/benchmark.sh --turns 10 --concurrent 3
```

## Fuzzing

Run Go's built-in fuzzer against key parsing functions:

```bash
# Fuzz event parsing
go test -fuzz=FuzzParseEvent -fuzztime=30s ./internal/nostr/...

# Fuzz config parsing
go test -fuzz=FuzzParseConfig -fuzztime=30s ./internal/config/...
```

## Integration Testing

For end-to-end testing, swarmstr supports a test mode that uses a local relay:

```bash
# Start a local test relay
docker run -d -p 7777:7777 scsibug/nostr-rs-relay

# Run integration tests against local relay
SWARMSTR_TEST_RELAY=ws://localhost:7777 go test -tags=integration ./...
```

## CI/CD

Example GitHub Actions workflow:

```yaml
name: Test
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - run: go build ./...
      - run: go test -race ./...
      - run: govulncheck ./...
```

## Testing Agent Behavior

### Send a DM via CLI

```bash
# Trigger an agent turn with a test message
swarmstr agent --message "ping" --deliver --timeout 30
```

### Test Cron Jobs

```bash
# Trigger a cron job immediately (bypasses schedule)
swarmstr cron run <jobId> --force
```

### Test Heartbeat

```bash
# Trigger a manual heartbeat
swarmstr system event --text "manual heartbeat test" --mode now
```

### Test Webhooks

```bash
# Send a test webhook
curl -X POST http://localhost:18789/hooks/wake \
  -H "x-swarmstr-token: $SWARMSTR_GATEWAY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text": "test webhook"}'
```

## Load Testing

For stress-testing relay connections:

```bash
# Concurrent DM test (10 concurrent senders)
for i in $(seq 1 10); do
  nak event --sec $NSEC -k 4 -c "test message $i" \
    --tag p=$(swarmstr config get channels.nostr.publicKey) \
    wss://relay.damus.io &
done
wait
```

## See Also

- [Debugging](/help/debugging)
- [Security: Formal Verification](/security/formal-verification)
- [Contributing](/CONTRIBUTING.md)
