#!/usr/bin/env bash
set -euo pipefail

cd /repo

export SWARMSTR_STATE_DIR="/tmp/swarmstr-test"
export SWARMSTR_CONFIG_PATH="${SWARMSTR_STATE_DIR}/config.json"

echo "==> Build"
pnpm build

echo "==> Seed state"
mkdir -p "${SWARMSTR_STATE_DIR}/credentials"
mkdir -p "${SWARMSTR_STATE_DIR}/agents/main/sessions"
echo '{}' >"${SWARMSTR_CONFIG_PATH}"
echo 'creds' >"${SWARMSTR_STATE_DIR}/credentials/marker.txt"
echo 'session' >"${SWARMSTR_STATE_DIR}/agents/main/sessions/sessions.json"

echo "==> Reset (config+creds+sessions)"
swarmstr reset --scope config+creds+sessions --yes --non-interactive

test ! -f "${SWARMSTR_CONFIG_PATH}"
test ! -d "${SWARMSTR_STATE_DIR}/credentials"
test ! -d "${SWARMSTR_STATE_DIR}/agents/main/sessions"

echo "==> Recreate minimal config"
mkdir -p "${SWARMSTR_STATE_DIR}/credentials"
echo '{}' >"${SWARMSTR_CONFIG_PATH}"

echo "==> Uninstall (state only)"
swarmstr uninstall --state --yes --non-interactive

test ! -d "${SWARMSTR_STATE_DIR}"

echo "OK"
