#!/usr/bin/env bash
set -euo pipefail

required=(METIQ_IMAGE METIQ_SOURCE_COMMIT METIQ_SIGNER_URL METIQ_RELAYS_JSON SOULFACTORY_CONTROLLER_PUBKEY)
for name in "${required[@]}"; do
  if [[ -z "${!name:-}" ]]; then
    echo "missing required environment variable: $name" >&2
    exit 2
  fi
done

[[ "$METIQ_IMAGE" =~ @sha256:[0-9a-f]{64}$ ]] || {
  echo "METIQ_IMAGE must be an immutable digest reference" >&2
  exit 2
}
[[ "$METIQ_SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || {
  echo "METIQ_SOURCE_COMMIT must be a full lowercase Git commit" >&2
  exit 2
}
[[ "$SOULFACTORY_CONTROLLER_PUBKEY" =~ ^[0-9a-f]{64}$ ]] || {
  echo "SOULFACTORY_CONTROLLER_PUBKEY must be a lowercase hex public key" >&2
  exit 2
}
[[ "$METIQ_SIGNER_URL" == bunker://* ]] || {
  echo "METIQ_SIGNER_URL must be a bunker URL" >&2
  exit 2
}
[[ "$METIQ_SIGNER_URL" != *"secret="* ]] || {
  echo "METIQ_SIGNER_URL must not contain a one-time connection secret" >&2
  exit 2
}
jq -e 'type == "array" and length > 0 and all(.[]; type == "string" and startswith("wss://"))' \
  <<<"$METIQ_RELAYS_JSON" >/dev/null

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [[ "$(git -C "$repo_root" rev-parse HEAD)" != "$METIQ_SOURCE_COMMIT" ]]; then
  echo "worktree HEAD does not match METIQ_SOURCE_COMMIT" >&2
  exit 2
fi

out="${1:-$repo_root/build/metiq-production}"
mkdir -p "$out"
chmod 700 "$out"

escape_sed() { printf '%s' "$1" | sed 's/[\\&|]/\\&/g'; }
image="$(escape_sed "$METIQ_IMAGE")"
signer="$(escape_sed "$METIQ_SIGNER_URL")"
relays="$(escape_sed "$METIQ_RELAYS_JSON")"
controller="$(escape_sed "$SOULFACTORY_CONTROLLER_PUBKEY")"

sed -e "s|{{METIQ_SIGNER_URL}}|$signer|g" \
    -e "s|{{METIQ_RELAYS_JSON}}|$relays|g" \
    "$repo_root/deploy/metiq-production/bootstrap.json.in" >"$out/bootstrap.json"
sed -e "s|{{METIQ_RELAYS_JSON}}|$relays|g" \
    -e "s|{{SOULFACTORY_CONTROLLER_PUBKEY}}|$controller|g" \
    "$repo_root/deploy/metiq-production/config.json.in" >"$out/config.json"
sed -e "s|{{METIQ_IMAGE}}|$image|g" \
    "$repo_root/deploy/metiq-production/metiq-production.container.in" >"$out/metiq-production.container"
cp "$repo_root/deploy/metiq-production/"*.volume "$out/"
printf '%s\n' "$METIQ_SOURCE_COMMIT" >"$out/SOURCE_COMMIT"
printf '%s\n' "$METIQ_IMAGE" >"$out/IMAGE_DIGEST"
chmod 600 "$out"/*
jq -e . "$out/bootstrap.json" "$out/config.json" >/dev/null
if grep -Eni 'private_key|secret=' "$out/bootstrap.json" "$out/config.json"; then
  echo "rendered configuration contains forbidden identity material" >&2
  exit 2
fi
if grep -En '{{[^}]+}}' "$out"/*; then
  echo "rendered output contains an unresolved placeholder" >&2
  exit 2
fi
echo "rendered production inputs in $out"
