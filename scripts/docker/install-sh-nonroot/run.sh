#!/usr/bin/env bash
# Non-root install smoke test for metiq.
# Runs as an unprivileged user; the binary should land in $HOME/.local/bin.
set -euo pipefail

INSTALL_URL="${METIQ_INSTALL_URL:-https://raw.githubusercontent.com/swarmstr/swarmstr/main/scripts/install.sh}"
SKIP_DOWNLOAD="${METIQ_INSTALL_SKIP_DOWNLOAD:-0}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# shellcheck source=../install-sh-common/verify.sh
source "$SCRIPT_DIR/../install-sh-common/verify.sh"

echo "==> Confirm running as non-root"
if [[ "$(id -u)" -eq 0 ]]; then
  echo "ERROR: this test must run as a non-root user" >&2
  exit 1
fi
echo "Running as: $(id)"

if [[ "$SKIP_DOWNLOAD" == "1" ]]; then
  echo "==> Skip download (METIQ_INSTALL_SKIP_DOWNLOAD=1)"
  # Binary injected via mount at /tmp/metiqd-linux-amd64 — install manually.
  mkdir -p "$HOME/.local/bin"
  cp /tmp/metiqd-linux-amd64 "$HOME/.local/bin/metiqd"
  chmod +x "$HOME/.local/bin/metiqd"
  ln -sfn metiqd "$HOME/.local/bin/swarmstrd"
  export PATH="$HOME/.local/bin:$PATH"
else
  echo "==> Run install script (non-root)"
  PREFIX="$HOME/.local" curl -fsSL "$INSTALL_URL" | bash
  export PATH="$HOME/.local/bin:$PATH"
fi

echo "==> Verify binary"
verify_binary metiqd
verify_binary swarmstrd

echo "OK"
