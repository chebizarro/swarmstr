#!/usr/bin/env bash
set -euo pipefail

cd /repo

export HOME="/tmp/metiq-smoke-home"
export METIQ_AGENT_PROVIDER="echo"
export METIQ_AGENT_ALLOW_ECHO="true"
STATE_DIR="${HOME}/.metiq"
BOOTSTRAP_PATH="${STATE_DIR}/bootstrap.json"
PID_FILE="${STATE_DIR}/metiqd.pid"
LOG_FILE="${STATE_DIR}/metiqd.log"
ADMIN_ADDR="127.0.0.1:18788"
PRIVATE_KEY="1111111111111111111111111111111111111111111111111111111111111111"

cleanup() {
  if [[ -f "${PID_FILE}" ]]; then
    metiq daemon --pid-file "${PID_FILE}" stop >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

mkdir -p "${STATE_DIR}"
cat >"${BOOTSTRAP_PATH}" <<EOF
{
  "private_key": "${PRIVATE_KEY}",
  "relays": ["wss://relay.snort.social"],
  "admin_listen_addr": "${ADMIN_ADDR}"
}
EOF

echo "==> Bootstrap check"
metiq --bootstrap "${BOOTSTRAP_PATH}" bootstrap-check

echo "==> Initial daemon status"
initial_status="$(metiq daemon --pid-file "${PID_FILE}" --bootstrap "${BOOTSTRAP_PATH}" status 2>&1)"
printf '%s\n' "${initial_status}"
grep -q "status=stopped" <<<"${initial_status}"

echo "==> Start daemon"
metiq daemon \
  --pid-file "${PID_FILE}" \
  --log-file "${LOG_FILE}" \
  --bootstrap "${BOOTSTRAP_PATH}" \
  start -- --admin-addr "${ADMIN_ADDR}"

echo "==> Wait for running status"
running_status=""
for _ in {1..20}; do
  running_status="$(metiq daemon --pid-file "${PID_FILE}" --bootstrap "${BOOTSTRAP_PATH}" status 2>&1 || true)"
  if grep -q "status=running" <<<"${running_status}"; then
    break
  fi
  sleep 1
done
printf '%s\n' "${running_status}"
grep -q "status=running" <<<"${running_status}"
test -f "${PID_FILE}"
test -f "${LOG_FILE}"

echo "==> Stop daemon"
metiq daemon --pid-file "${PID_FILE}" stop

echo "==> Final daemon status"
final_status="$(metiq daemon --pid-file "${PID_FILE}" --bootstrap "${BOOTSTRAP_PATH}" status 2>&1 || true)"
printf '%s\n' "${final_status}"
grep -q "status=stopped" <<<"${final_status}"

echo "OK"
