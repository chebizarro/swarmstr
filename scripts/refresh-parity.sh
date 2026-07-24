#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OPENCLAW_ROOT="${OPENCLAW_ROOT:-}"

if [[ -z "$OPENCLAW_ROOT" ]]; then
  for candidate in \
    "$ROOT_DIR/../openclaw" \
    "$ROOT_DIR/../../Dev/openclaw"; do
    if [[ -f "$candidate/src/gateway/methods/core-descriptors.ts" ]]; then
      OPENCLAW_ROOT="$candidate"
      break
    fi
  done
fi

if [[ -z "$OPENCLAW_ROOT" ]]; then
  echo "OpenClaw checkout not found; set OPENCLAW_ROOT" >&2
  exit 1
fi

exec go run ./cmd/paritygen \
  --repo-root "$ROOT_DIR" \
  --openclaw-root "$OPENCLAW_ROOT" \
  "$@"
