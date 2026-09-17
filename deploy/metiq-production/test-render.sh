#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

export METIQ_IMAGE='registry.example/metiq@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
export METIQ_SOURCE_COMMIT="$(git -C "$repo_root" rev-parse HEAD)"
export METIQ_SIGNER_URL='bunker://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb?relay=wss%3A%2F%2Frelay.example'
export METIQ_RELAYS_JSON='["wss://relay.example"]'
export SOULFACTORY_CONTROLLER_PUBKEYS_JSON='["cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"]'

"$repo_root/deploy/metiq-production/render.sh" "$out" >/dev/null
grep -Fx 'Environment=METIQ_AGENT_PROVIDER=echo' "$out/metiq-production.container" >/dev/null
grep -Fx 'Environment=METIQ_AGENT_ALLOW_ECHO=true' "$out/metiq-production.container" >/dev/null
jq -e '.agent.default_model == "echo" and .dm.policy == "disabled" and .heartbeat.enabled == false' \
  "$out/config.json" >/dev/null
jq -e '.control.admins | length == 2 and .[0].pubkey == "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" and .[1].pubkey == "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" and all(.[]; .methods == ["soulfactory.provision", "soulfactory.suspend"])' \
  "$out/config.json" >/dev/null

unset SOULFACTORY_CONTROLLER_PUBKEYS_JSON
export SOULFACTORY_CONTROLLER_PUBKEY='eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee'
legacy_out="$(mktemp -d)"
"$repo_root/deploy/metiq-production/render.sh" "$legacy_out" >/dev/null
jq -e '.control.admins == [{"pubkey":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","methods":["soulfactory.provision","soulfactory.suspend"]}]' \
  "$legacy_out/config.json" >/dev/null
rm -rf "$legacy_out"
