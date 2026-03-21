#!/usr/bin/env bash
set -euo pipefail

cd /repo

export METIQ_STATE_DIR="/tmp/metiq-test"
export METIQ_CONFIG_PATH="${METIQ_STATE_DIR}/config.json"

echo "==> Build"
pnpm build

echo "==> Seed state"
mkdir -p "${METIQ_STATE_DIR}/credentials"
mkdir -p "${METIQ_STATE_DIR}/agents/main/sessions"
echo '{}' >"${METIQ_CONFIG_PATH}"
echo 'creds' >"${METIQ_STATE_DIR}/credentials/marker.txt"
echo 'session' >"${METIQ_STATE_DIR}/agents/main/sessions/sessions.json"

echo "==> Reset (config+creds+sessions)"
metiq reset --scope config+creds+sessions --yes --non-interactive

test ! -f "${METIQ_CONFIG_PATH}"
test ! -d "${METIQ_STATE_DIR}/credentials"
test ! -d "${METIQ_STATE_DIR}/agents/main/sessions"

echo "==> Recreate minimal config"
mkdir -p "${METIQ_STATE_DIR}/credentials"
echo '{}' >"${METIQ_CONFIG_PATH}"

echo "==> Uninstall (state only)"
metiq uninstall --state --yes --non-interactive

test ! -d "${METIQ_STATE_DIR}"

echo "OK"
