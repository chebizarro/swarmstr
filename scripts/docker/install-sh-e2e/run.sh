#!/usr/bin/env bash
# End-to-end install + gateway smoke test for swarmstrd.
#
# Steps:
#  1. Install swarmstrd via the install script (or use a pre-built binary)
#  2. Start swarmstrd with a minimal test config
#  3. Hit the /health admin endpoint
#  4. Verify the binary exits cleanly with --version
set -euo pipefail

INSTALL_URL="${SWARMSTR_INSTALL_URL:-https://raw.githubusercontent.com/swarmstr/swarmstr/main/scripts/install.sh}"
INSTALL_TAG="${SWARMSTR_INSTALL_TAG:-latest}"
DRY_RUN="${DRY_RUN:-0}"
SWARMSTR_INSTALL_SKIP_DOWNLOAD="${SWARMSTR_INSTALL_SKIP_DOWNLOAD:-0}"

source /usr/local/lib/swarmstr/verify.sh

# ── Install ────────────────────────────────────────────────────────────────
if [[ "${SWARMSTR_INSTALL_SKIP_DOWNLOAD}" == "1" ]]; then
	echo "==> Skipping download (SWARMSTR_INSTALL_SKIP_DOWNLOAD=1)"
	# Expect the binary already on PATH (e.g. bind-mounted for CI).
else
	if [[ "${DRY_RUN}" == "1" ]]; then
	echo "==> DRY_RUN: would curl ${INSTALL_URL} | sh"
	else
	echo "==> Installing swarmstrd from ${INSTALL_URL} (tag=${INSTALL_TAG})"
	curl -fsSL "${INSTALL_URL}" | SWARMSTR_VERSION="${INSTALL_TAG}" sh
  fi
fi

# ── Binary verification ────────────────────────────────────────────────────
verify_binary swarmstrd

# ── Gateway smoke test ─────────────────────────────────────────────────────
if [[ "${DRY_RUN}" == "1" ]]; then
	echo "==> DRY_RUN: skipping live gateway test"
	echo "PASS (dry-run)"
	exit 0
fi

ADMIN_PORT=18787
BOOTSTRAP_FILE="$(mktemp /tmp/swarmstr-e2e-bootstrap.XXXX.json)"
# Minimal bootstrap with a test private key (not real, just 64 hex chars).
cat > "${BOOTSTRAP_FILE}" <<'JSON'
{
	"private_key": "0000000000000000000000000000000000000000000000000000000000000001",
	"relays": {"read": [], "write": []},
	"admin_listen_addr": "127.0.0.1:18787",
	"admin_token": "e2e-test-token"
}
JSON

echo "==> Starting swarmstrd in background..."
swarmstrd --bootstrap "${BOOTSTRAP_FILE}" &
SWARMSTRD_PID=$!
trap "kill ${SWARMSTRD_PID} 2>/dev/null; rm -f ${BOOTSTRAP_FILE}" EXIT

# Wait for admin API.
for i in $(seq 1 15); do
	if curl -s -f -H "Authorization: Bearer e2e-test-token" "http://127.0.0.1:${ADMIN_PORT}/health" > /dev/null 2>&1; then
	echo "==> Admin API healthy after ${i}s"
	break
  fi
	sleep 1
	if [[ "${i}" == "15" ]]; then
	echo "ERROR: admin API not healthy after 15s"
    exit 1
  fi
done

echo "==> Checking /status..."
curl -s -f -H "Authorization: Bearer e2e-test-token" \
	"http://127.0.0.1:${ADMIN_PORT}/status" | grep -q "pubkey" || {
	echo "ERROR: /status missing pubkey field"
    exit 1
	}

echo "PASS: e2e install smoke test"
