#!/usr/bin/env bash
set -euo pipefail
CONFIG=${1:-security/opengrep/metiq-nostr.yml}
BASE=${BASE_REF:-origin/main}
if ! command -v opengrep >/dev/null 2>&1; then
  echo "opengrep not installed; install OpenGrep/Semgrep-compatible scanner to enforce $CONFIG" >&2
  exit 127
fi
mapfile -t files < <(git diff --name-only --diff-filter=ACMRTUXB "$BASE"...HEAD -- '*.go' '*.js' '*.ts' '*.py' 2>/dev/null || git diff --name-only --diff-filter=ACMRTUXB -- '*.go' '*.js' '*.ts' '*.py')
if [[ ${#files[@]} -eq 0 ]]; then
  echo "No changed source files to scan."
  exit 0
fi
opengrep scan --config "$CONFIG" "${files[@]}"
