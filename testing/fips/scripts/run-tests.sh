#!/usr/bin/env bash
# run-tests.sh — Run FIPS integration tests
#
# This script runs both the in-process Go integration tests (loopback,
# no Docker needed) and optionally the Docker-based E2E tests.
#
# Usage:
#   ./scripts/run-tests.sh              # Go tests only (default)
#   ./scripts/run-tests.sh --docker     # Go tests + Docker E2E
#   ./scripts/run-tests.sh --docker-only # Docker E2E only
#
# Requirements:
#   - Go 1.22+
#   - Docker + Docker Compose (for --docker/--docker-only)
#   - FIPS daemon image (for Docker E2E)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
COMPOSE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }

MODE="go"
if [[ "${1:-}" == "--docker" ]]; then
    MODE="all"
elif [[ "${1:-}" == "--docker-only" ]]; then
    MODE="docker"
fi

# ── Go integration tests (in-process, loopback) ──────────────────────────

run_go_tests() {
    info "Running Go integration tests (build tag: experimental_fips)..."
    cd "$PROJECT_ROOT"

    go test -tags experimental_fips \
        -run 'TestIntegration_' \
        -v -count=1 -timeout 60s \
        ./internal/nostr/runtime/

    local exit_code=$?
    if [[ $exit_code -eq 0 ]]; then
        info "Go integration tests PASSED ✓"
    else
        fail "Go integration tests FAILED (exit $exit_code)"
    fi
}

# ── Docker E2E tests ──────────────────────────────────────────────────────

run_docker_tests() {
    info "Running opt-in real-daemon Docker interoperability suite..."
    "$COMPOSE_DIR/real-daemon/run.sh"
}

# ── Main ──────────────────────────────────────────────────────────────────

info "FIPS Integration Test Harness"
info "Mode: $MODE"
echo

case "$MODE" in
    go)
        run_go_tests
        ;;
    docker)
        run_docker_tests
        ;;
    all)
        run_go_tests
        echo
        run_docker_tests
        ;;
esac

echo
info "Done."
