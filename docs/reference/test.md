---
summary: "How to run tests locally and when to use coverage modes"
read_when:
  - Running or fixing tests
  - Verifying a change before merging
title: "Tests"
---

# Tests

swarmstr uses the standard Go test toolchain.

## Running Tests

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run tests for a specific package
go test ./internal/agent/...

# Run a specific test
go test -run TestSessionStore ./internal/store/state/...

# Run with race detector
go test -race ./...
```

## Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...

# View coverage in browser
go tool cover -html=coverage.out

# Coverage for a specific package
go test -coverprofile=coverage.out ./internal/store/state/...
go tool cover -func=coverage.out
```

## Local PR Gate

Before merging, verify:

```bash
go build ./...
go test ./...
```

Zero failures expected. If a test is flaky on a loaded host, rerun once before treating it as a regression.

## Integration / Live Tests

Some tests require a running daemon or external credentials. These are skipped by default
and enabled via environment variables:

```bash
# Run with live provider keys
ANTHROPIC_API_KEY=sk-ant-... go test ./internal/agent/...
```

## Model Latency Benchmarks

```bash
# Basic benchmark
go test -bench=. -benchtime=10x ./internal/...

# With count
go test -bench=BenchmarkSession -benchcount=5 ./...
```

## CI

Tests run automatically on every push and pull request via
[`.github/workflows/ci.yml`](/.github/workflows/ci.yml):

- `go build ./...`
- `go test ./...`
- Race detector enabled

## See Also

- [Release Checklist](/reference/RELEASING)
- [CI workflow](/.github/workflows/ci.yml)
