#!/usr/bin/env bash
# Smoke test for swarmstr install.sh.
#
# When SWARMSTR_INSTALL_SKIP_DOWNLOAD=1 the binary is expected to already be
# present at /usr/local/bin/swarmstrd (mounted by the CI job).
set -euo pipefail

INSTALL_URL="${SWARMSTR_INSTALL_URL:-https://raw.githubusercontent.com/swarmstr/swarmstr/main/scripts/install.sh}"
SKIP_DOWNLOAD="${SWARMSTR_INSTALL_SKIP_DOWNLOAD:-0}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# shellcheck source=../install-sh-common/verify.sh
source "$SCRIPT_DIR/../install-sh-common/verify.sh"

echo "==> Validate install.sh syntax"
if command -v shellcheck &>/dev/null; then
  curl -fsSL "$INSTALL_URL" -o /tmp/install.sh
  shellcheck -S warning /tmp/install.sh
  echo "shellcheck passed"
else
  curl -fsSL "$INSTALL_URL" | bash -n
  echo "bash -n syntax check passed"
fi

if [[ "$SKIP_DOWNLOAD" == "1" ]]; then
  echo "==> Skip download (SWARMSTR_INSTALL_SKIP_DOWNLOAD=1)"
  echo "==> Verify pre-installed binary"
  verify_binary swarmstrd
else
  echo "==> Run install script"
  curl -fsSL "$INSTALL_URL" | bash

  echo "==> Verify installed binary"
  verify_binary swarmstrd
fi

echo "OK"
