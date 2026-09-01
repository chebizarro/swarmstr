#!/usr/bin/env bash
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
openclaw_nostr="${OPENCLAW_NOSTR_INTEROP_DIR:-$(cd "$repo/.." && pwd)/openclaw-nostr}"

if [[ ! -f "$openclaw_nostr/src/fleet-tasks.ts" ]]; then
  echo "openclaw-nostr checkout not found at $openclaw_nostr" >&2
  echo "set OPENCLAW_NOSTR_INTEROP_DIR to its path" >&2
  exit 1
fi
if [[ ! -d "$openclaw_nostr/node_modules/nostr-tools" ]] || \
   [[ ! -f "$openclaw_nostr/node_modules/typescript/bin/tsc" ]]; then
  echo "openclaw-nostr dependencies are not installed at $openclaw_nostr/node_modules" >&2
  exit 1
fi

export FLEET_TASK_CANARY=1
export OPENCLAW_NOSTR_INTEROP_DIR="$openclaw_nostr"

cd "$repo"
exec go test -tags=integration ./testing/fleettaskcanary \
  -run '^TestCrossRuntimeFleetTaskAgentToolCanary$' -count=1 -v
