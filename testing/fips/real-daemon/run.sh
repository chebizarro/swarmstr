#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
COMPOSE=(docker compose -f "$HERE/docker-compose.yml")
PROBE_BIN="${TMPDIR:-/tmp}/swarmstr-fips-probe-${USER:-user}"
SERVER_A_LOG="${TMPDIR:-/tmp}/swarmstr-e7-a.log"
SERVER_B_LOG="${TMPDIR:-/tmp}/swarmstr-e7-b.log"
FIPS_TEST_IMAGE="${FIPS_TEST_IMAGE:-fips-test:latest}"
FIPS_REAL_DAEMON_REQUIRED="${FIPS_REAL_DAEMON_REQUIRED:-0}"
FIPS_DIAGNOSTICS_DIR="${FIPS_DIAGNOSTICS_DIR:-}"
export FIPS_TEST_IMAGE

NPUB_A="npub1sjlh2c3x9w7kjsqg2ay080n2lff2uvt325vpan33ke34rn8l5jcqawh57m"
NPUB_B="npub1tdwa4vjrjl33pcjdpf2t4p027nl86xrx24g4d3avg4vwvayr3g8qhd84le"
NODE_A="swarmstr-fips-e7-a"
NODE_B="swarmstr-fips-e7-b"
RELAY="swarmstr-fips-e7-relay"
STARTED=false

cleanup() {
  "$STARTED" && "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  rm -f "$PROBE_BIN" "$SERVER_A_LOG" "$SERVER_B_LOG"
}
diagnostics() {
  for container in "$RELAY" "$NODE_A" "$NODE_B"; do
    echo "--- $container logs ---" >&2
    docker logs "$container" 2>&1 | tail -100 >&2 || true
  done
  for container in "$NODE_A" "$NODE_B"; do
    echo "--- $container peers ---" >&2
    docker exec "$container" fipsctl show peers >&2 || true
    echo "--- $container links ---" >&2
    docker exec "$container" fipsctl show links >&2 || true
  done
}
on_exit() {
  status=$?
  trap - EXIT
  if (( status != 0 )) && "$STARTED"; then
    if [[ -n "$FIPS_DIAGNOSTICS_DIR" ]]; then
      mkdir -p "$FIPS_DIAGNOSTICS_DIR"
      diagnostics 2>&1 | tee "$FIPS_DIAGNOSTICS_DIR/diagnostics.log" >&2
      [[ -f "$SERVER_A_LOG" ]] && cp "$SERVER_A_LOG" "$FIPS_DIAGNOSTICS_DIR/server-a.log"
      [[ -f "$SERVER_B_LOG" ]] && cp "$SERVER_B_LOG" "$FIPS_DIAGNOSTICS_DIR/server-b.log"
    else
      diagnostics
    fi
  fi
  cleanup
  exit "$status"
}
trap on_exit EXIT
trap 'exit 130' INT TERM

command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
docker info >/dev/null
if ! docker image inspect "$FIPS_TEST_IMAGE" >/dev/null 2>&1; then
  message="SKIP: real FIPS daemon image '$FIPS_TEST_IMAGE' is unavailable; see testing/fips/real-daemon/README.md"
  if [[ "$FIPS_REAL_DAEMON_REQUIRED" == "1" ]]; then
    echo "$message" >&2
    exit 1
  fi
  echo "$message"
  exit 0
fi

case "$(docker image inspect "$FIPS_TEST_IMAGE" --format '{{.Architecture}}')" in
  amd64|x86_64) GOARCH=amd64 ;;
  arm64|aarch64) GOARCH=arm64 ;;
  *) echo "unsupported FIPS image architecture" >&2; exit 1 ;;
esac
FIPS_TEST_PLATFORM="${FIPS_TEST_PLATFORM:-linux/$GOARCH}"
export FIPS_TEST_PLATFORM

echo "==> Building swarmstr probe for linux/$GOARCH"
(
  cd "$ROOT"
  GOCACHE="${GOCACHE:-/tmp/swarmstr-go-cache}" CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
    go build -tags=experimental_fips -o "$PROBE_BIN" \
    ./testing/fips/real-daemon/cmd/swarmstr-fips-probe
)
chmod 755 "$PROBE_BIN"
export PROBE_BIN

"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
echo "==> Starting relay and two real FIPS daemons from $FIPS_TEST_IMAGE"
STARTED=true
"${COMPOSE[@]}" up -d --wait --wait-timeout 90

# This bounded loop is a daemon/link readiness health check, not message
# completion. Application delivery below completes only from explicit callbacks.
echo "==> Waiting for Nostr-discovered peer links"
for pair in "$NODE_A:$NPUB_B" "$NODE_B:$NPUB_A"; do
  container="${pair%%:*}"
  peer="${pair#*:}"
  ready=false
  for _ in {1..60}; do
    if docker exec "$container" ping -6 -c 1 -W 1 "$peer.fips" >/dev/null 2>&1; then
      ready=true
      break
    fi
    sleep 1
  done
  $ready || { echo "peer link did not converge for $container" >&2; exit 1; }
done

echo "==> Validating signed kind-37195 adverts through REQ/EVENT/EOSE"
docker exec "$RELAY" /usr/local/bin/swarmstr-fips-probe advert \
  --relay ws://127.0.0.1:7777 --authors "$NPUB_A,$NPUB_B"

echo "==> Starting production DM/control listeners"
docker exec "$NODE_A" rm -f /tmp/swarmstr-e7-a.ready
docker exec "$NODE_B" rm -f /tmp/swarmstr-e7-b.ready
docker exec "$NODE_A" /usr/local/bin/swarmstr-fips-probe serve \
  --self "$NPUB_A" --peer "$NPUB_B" --expect-text "B to A" \
  --ready-file /tmp/swarmstr-e7-a.ready >"$SERVER_A_LOG" 2>&1 &
SERVER_A=$!
docker exec "$NODE_B" /usr/local/bin/swarmstr-fips-probe serve \
  --self "$NPUB_B" --peer "$NPUB_A" --expect-text "A to B" \
  --ready-file /tmp/swarmstr-e7-b.ready >"$SERVER_B_LOG" 2>&1 &
SERVER_B=$!

# Listener readiness is a health check. No delivery assertion polls.
for marker in "$NODE_A:/tmp/swarmstr-e7-a.ready" "$NODE_B:/tmp/swarmstr-e7-b.ready"; do
  container="${marker%%:*}"
  path="${marker#*:}"
  ready=false
  for _ in {1..30}; do
    if docker exec "$container" test -f "$path"; then
      ready=true
      break
    fi
    sleep 1
  done
  $ready || { echo "application listener did not become ready in $container" >&2; exit 1; }
done

echo "==> Verifying fail-closed sender authentication"
docker exec "$NODE_A" /usr/local/bin/swarmstr-fips-probe send-raw \
  --self "$NPUB_A" --to "$NPUB_B" --claim "$NPUB_B"
docker exec "$NODE_B" /usr/local/bin/swarmstr-fips-probe send-raw \
  --self "$NPUB_B" --to "$NPUB_A" --claim "$NPUB_A"
docker exec "$NODE_A" /usr/local/bin/swarmstr-fips-probe control \
  --self "$NPUB_A" --to "$NPUB_B" --claim "$NPUB_B" --expect-error
docker exec "$NODE_B" /usr/local/bin/swarmstr-fips-probe control \
  --self "$NPUB_B" --to "$NPUB_A" --claim "$NPUB_A" --expect-error

echo "==> Exercising bidirectional authenticated DM and control"
docker exec "$NODE_A" /usr/local/bin/swarmstr-fips-probe send \
  --self "$NPUB_A" --to "$NPUB_B" --text "A to B"
docker exec "$NODE_B" /usr/local/bin/swarmstr-fips-probe send \
  --self "$NPUB_B" --to "$NPUB_A" --text "B to A"
docker exec "$NODE_A" /usr/local/bin/swarmstr-fips-probe control \
  --self "$NPUB_A" --to "$NPUB_B"
docker exec "$NODE_B" /usr/local/bin/swarmstr-fips-probe control \
  --self "$NPUB_B" --to "$NPUB_A"

wait "$SERVER_A"
wait "$SERVER_B"
cat "$SERVER_A_LOG"
cat "$SERVER_B_LOG"

echo "real FIPS daemon interoperability PASSED"
