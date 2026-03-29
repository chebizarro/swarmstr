#!/bin/sh
set -eu

BOOTSTRAP_PATH="${METIQ_BOOTSTRAP_PATH:-/data/.metiq/bootstrap.json}"
BOOTSTRAP_DIR="$(dirname "${BOOTSTRAP_PATH}")"

if [ ! -f "${BOOTSTRAP_PATH}" ]; then
  if [ -z "${METIQ_NOSTR_KEY:-}" ] && [ -z "${METIQ_SIGNER_URL:-}" ]; then
    echo "ERROR: missing bootstrap config at ${BOOTSTRAP_PATH}." >&2
    echo "Set METIQ_NOSTR_KEY (hex or nsec...) or METIQ_SIGNER_URL, or mount an existing bootstrap.json into /data/.metiq/." >&2
    exit 1
  fi

  mkdir -p "${BOOTSTRAP_DIR}"

  relays_csv="${METIQ_NOSTR_RELAYS:-wss://nos.lol,wss://relay.primal.net,wss://relay.damus.io}"

  relays_json="$(printf '%s' "${relays_csv}" | jq -Rn 'split(",") | map(gsub("^\\s+|\\s+$"; "")) | map(select(length>0))')"

  tmp_path="${BOOTSTRAP_PATH}.tmp"

  if [ -n "${METIQ_SIGNER_URL:-}" ]; then
    jq -n --arg signer_url "${METIQ_SIGNER_URL}" --argjson relays "${relays_json}" '{signer_url:$signer_url, relays:$relays}' > "${tmp_path}"
  else
    jq -n --arg private_key "${METIQ_NOSTR_KEY}" --argjson relays "${relays_json}" '{private_key:$private_key, relays:$relays}' > "${tmp_path}"
  fi

  chmod 600 "${tmp_path}" || true
  mv "${tmp_path}" "${BOOTSTRAP_PATH}"

  echo "Wrote bootstrap config to ${BOOTSTRAP_PATH}" >&2
fi

exec /usr/local/bin/metiqd "$@"
