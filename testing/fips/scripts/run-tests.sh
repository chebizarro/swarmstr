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
    info "Running Docker E2E tests..."

    # Check prerequisites.
    command -v docker >/dev/null 2>&1 || fail "docker not found"
    command -v docker compose >/dev/null 2>&1 && COMPOSE="docker compose" || {
        command -v docker-compose >/dev/null 2>&1 && COMPOSE="docker-compose" || {
            fail "docker compose not found"
        }
    }

    cd "$COMPOSE_DIR"

    # Build images.
    info "Building test images..."
    $COMPOSE build --quiet 2>/dev/null || warn "Image build failed (FIPS daemon image may not be available)"

    # Start the mesh.
    info "Starting 3-node mesh..."
    $COMPOSE up -d

    # Wait for health checks.
    info "Waiting for agents to become healthy..."
    local max_wait=60
    local waited=0
    while [[ $waited -lt $max_wait ]]; do
        local healthy
        healthy=$($COMPOSE ps --format json 2>/dev/null | grep -c '"healthy"' || true)
        if [[ $healthy -ge 3 ]]; then
            info "All nodes healthy ($healthy/3)"
            break
        fi
        sleep 2
        waited=$((waited + 2))
    done
    if [[ $waited -ge $max_wait ]]; then
        warn "Timeout waiting for healthy nodes — running tests anyway"
    fi

    # Test 1: DM over FIPS
    info "Test: DM over FIPS (Agent-A → Agent-B)..."
    # This would use the metiqd control API to trigger a DM send.
    # Placeholder — requires running agents with API access.
    warn "Docker DM test: requires FIPS daemon — skipping (run manually)"

    # Test 2: Control RPC over FIPS
    info "Test: Control RPC (Agent-A → Agent-B status.get)..."
    warn "Docker control test: requires FIPS daemon — skipping (run manually)"

    # Test 3: FIPS health status
    info "Test: fips_status tool output..."
    warn "Docker health test: requires FIPS daemon — skipping (run manually)"

    # Cleanup.
    info "Tearing down mesh..."
    $COMPOSE down -v --remove-orphans 2>/dev/null

    info "Docker E2E tests complete (manual verification steps noted above)"
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
