#!/bin/sh
set -eu

# Fix /data ownership if running as root (Docker volume mount scenario)
if [ "$(id -u)" = "0" ]; then
  # Create /data and /data/.metiq directories if they don't exist
  mkdir -p /data/.metiq
  
  # Fix ownership recursively
  chown -R metiq:metiq /data
  
  # Ensure directories are writable
  chmod 755 /data /data/.metiq
  
  # Re-exec this script as the metiq user
  exec su-exec metiq "$0" "$@"
fi

BOOTSTRAP_PATH="${METIQ_BOOTSTRAP_PATH:-/data/.metiq/bootstrap.json}"
BOOTSTRAP_DIR="$(dirname "${BOOTSTRAP_PATH}")"

if [ ! -f "${BOOTSTRAP_PATH}" ]; then
  if [ -z "${METIQ_NOSTR_KEY:-}" ] && [ -z "${METIQ_SIGNER_URL:-}" ]; then
    echo "ERROR: missing bootstrap config at ${BOOTSTRAP_PATH}." >&2
    echo "Set METIQ_NOSTR_KEY (hex or nsec...) or METIQ_SIGNER_URL, or mount an existing bootstrap.json into /data/.metiq/." >&2
    exit 1
  fi

  if [ -z "${METIQ_NOSTR_RELAYS:-}" ]; then
    echo "ERROR: missing relay configuration for ${BOOTSTRAP_PATH}." >&2
    echo "Set METIQ_NOSTR_RELAYS to a comma-separated list of wss:// relay URLs, or mount an existing bootstrap.json into /data/.metiq/." >&2
    exit 1
  fi

  mkdir -p "${BOOTSTRAP_DIR}"

  relays_csv="${METIQ_NOSTR_RELAYS}"

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
