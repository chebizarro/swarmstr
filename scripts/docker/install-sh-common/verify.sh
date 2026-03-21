#!/usr/bin/env bash
# Shared helper: verify a daemon binary is installed and functional.

verify_binary() {
  local binary="${1:-metiqd}"
  local cmd_path=""

  # Search PATH and common install locations.
  cmd_path="$(command -v "$binary" 2>/dev/null || true)"
  if [[ -z "$cmd_path" ]]; then
    for dir in /usr/local/bin "$HOME/.local/bin" "$HOME/.metiq/bin"; do
      if [[ -x "$dir/$binary" ]]; then
        cmd_path="$dir/$binary"
        break
      fi
    done
  fi

  if [[ -z "$cmd_path" ]]; then
    echo "ERROR: $binary not found on PATH or in common install dirs" >&2
    return 1
  fi

  echo "Found: $cmd_path"

  # Verify it runs and prints a version.
  local ver=""
  ver="$("$cmd_path" --version 2>/dev/null || "$cmd_path" version 2>/dev/null || echo "unknown")"
  echo "Version: $ver"

  # Sanity: --help must exit 0 (or 2 for flag-only binaries).
  if "$cmd_path" --help >/dev/null 2>&1 || "$cmd_path" --help 2>&1 | grep -q usage; then
    echo "==> $binary --help: OK"
  else
    echo "WARN: $binary --help returned non-zero (may be normal for flag-only CLIs)"
  fi
}
