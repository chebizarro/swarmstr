#!/usr/bin/env bash
# migrate-from-swarmstr.sh — Migrate a swarmstr installation to metiq.
#
# What this script does:
#   1. Copies ~/.swarmstr → ~/.metiq (preserving the original as backup)
#   2. Renames SWARMSTR_* env vars to METIQ_* inside ~/.metiq/.env
#   3. Updates references in config files (bootstrap.json, config.json)
#   4. Migrates systemd user service if installed
#   5. Checks for metiqd/metiq on PATH and suggests symlinks if only old binaries found
#
# Usage:
#   bash scripts/migrate-from-swarmstr.sh            # dry-run by default
#   bash scripts/migrate-from-swarmstr.sh --apply     # actually perform changes
#
# Safe to run multiple times — skips steps that are already done.

set -euo pipefail

# ── Colours ───────────────────────────────────────────────────────────────────
GREEN='\033[38;2;0;229;180m'
YELLOW='\033[38;2;255;176;32m'
RED='\033[38;2;230;57;70m'
DIM='\033[38;2;90;100;128m'
NC='\033[0m'

info()    { printf "${DIM}·${NC} %s\n" "$*"; }
ok()      { printf "${GREEN}✓${NC} %s\n" "$*"; }
warn()    { printf "${YELLOW}!${NC} %s\n" "$*" >&2; }
err()     { printf "${RED}✗${NC} %s\n" "$*" >&2; }
skip()    { printf "${DIM}  skip:${NC} %s\n" "$*"; }

DRY_RUN=true
if [[ "${1:-}" == "--apply" ]]; then
  DRY_RUN=false
fi

OLD_HOME="$HOME/.swarmstr"
NEW_HOME="$HOME/.metiq"
CHANGES=0

banner() {
  echo ""
  echo "  ⚡ swarmstr → metiq migration"
  if $DRY_RUN; then
    echo "  (dry run — pass --apply to make changes)"
  else
    echo "  (applying changes)"
  fi
  echo ""
}

run() {
  if $DRY_RUN; then
    info "[dry-run] $*"
  else
    "$@"
  fi
}

# ── Step 1: Copy state directory ──────────────────────────────────────────────
migrate_state_dir() {
  echo "── Step 1: State directory"

  if [[ ! -d "$OLD_HOME" ]]; then
    skip "~/.swarmstr does not exist, nothing to migrate"
    return
  fi

  if [[ -d "$NEW_HOME" ]]; then
    skip "~/.metiq already exists"
    # Still proceed to check contents for env var renames.
    return
  fi

  CHANGES=$((CHANGES + 1))
  info "Copying ~/.swarmstr → ~/.metiq"
  run cp -a "$OLD_HOME" "$NEW_HOME"
  ok "State directory copied"
}

# ── Step 2: Rename env vars in .env file ──────────────────────────────────────
migrate_env_file() {
  echo "── Step 2: Environment variables in .env"

  local env_file="$NEW_HOME/.env"
  if [[ ! -f "$env_file" ]]; then
    # If ~/.metiq exists but .env is missing, copy it from the old location.
    if [[ -f "$OLD_HOME/.env" ]] && [[ -d "$NEW_HOME" ]]; then
      CHANGES=$((CHANGES + 1))
      info "Copying .env from ~/.swarmstr to ~/.metiq"
      run cp "$OLD_HOME/.env" "$env_file"
      # In dry-run, the file won't exist yet — point at old location for grep.
      if $DRY_RUN; then
        env_file="$OLD_HOME/.env"
      fi
    else
      skip "No .env file found"
      return
    fi
  fi

  if ! grep -q 'SWARMSTR_' "$env_file" 2>/dev/null; then
    skip ".env has no SWARMSTR_* variables"
    return
  fi

  CHANGES=$((CHANGES + 1))
  local count
  count=$(grep -c 'SWARMSTR_' "$env_file" || true)
  info "Found $count SWARMSTR_* variable(s) in $env_file"

  if ! $DRY_RUN; then
    # macOS/BSD sed compatibility
    if sed --version 2>/dev/null | grep -q GNU; then
      sed -i 's/SWARMSTR_/METIQ_/g' "$env_file"
    else
      sed -i '' 's/SWARMSTR_/METIQ_/g' "$env_file"
    fi
  else
    info "[dry-run] Would rename: $(grep 'SWARMSTR_' "$env_file" | head -5)"
  fi
  ok "Renamed SWARMSTR_* → METIQ_* in .env"
}

# ── Step 3: Update config references ─────────────────────────────────────────
migrate_config_files() {
  echo "── Step 3: Config file references"

  local found=0
  for f in "$NEW_HOME/bootstrap.json" "$NEW_HOME/config.json" "$NEW_HOME/config.yaml"; do
    [[ -f "$f" ]] || continue

    if grep -qi 'swarmstr' "$f" 2>/dev/null; then
      found=1
      CHANGES=$((CHANGES + 1))
      info "Updating references in $(basename "$f")"
      if ! $DRY_RUN; then
        if sed --version 2>/dev/null | grep -q GNU; then
          sed -i 's/swarmstrd/metiqd/g; s/swarmstr/metiq/g; s/SWARMSTR_/METIQ_/g' "$f"
        else
          sed -i '' 's/swarmstrd/metiqd/g; s/swarmstr/metiq/g; s/SWARMSTR_/METIQ_/g' "$f"
        fi
      fi
      ok "Updated $(basename "$f")"
    fi
  done

  if [[ $found -eq 0 ]]; then
    skip "No config files with swarmstr references found"
  fi
}

# ── Step 4: Systemd service ──────────────────────────────────────────────────
migrate_systemd() {
  echo "── Step 4: Systemd user service"

  local old_svc="$HOME/.config/systemd/user/swarmstrd.service"
  local new_svc="$HOME/.config/systemd/user/metiqd.service"

  if [[ ! -f "$old_svc" ]]; then
    skip "No swarmstrd.service found"
    return
  fi

  if [[ -f "$new_svc" ]]; then
    skip "metiqd.service already exists"
    return
  fi

  CHANGES=$((CHANGES + 1))
  info "Migrating swarmstrd.service → metiqd.service"

  if ! $DRY_RUN; then
    # Copy and rewrite references
    cp "$old_svc" "$new_svc"
    if sed --version 2>/dev/null | grep -q GNU; then
      sed -i 's/swarmstrd/metiqd/g; s/swarmstr/metiq/g; s/SWARMSTR_/METIQ_/g; s/\.swarmstr/.metiq/g' "$new_svc"
    else
      sed -i '' 's/swarmstrd/metiqd/g; s/swarmstr/metiq/g; s/SWARMSTR_/METIQ_/g; s/\.swarmstr/.metiq/g' "$new_svc"
    fi
    # Reload and suggest switchover
    systemctl --user daemon-reload 2>/dev/null || true
    ok "Created metiqd.service (old service preserved)"
    info "To switch over:"
    info "  systemctl --user disable swarmstrd"
    info "  systemctl --user enable --now metiqd"
  else
    info "[dry-run] Would create $new_svc from $old_svc with references updated"
  fi
}

# ── Step 5: Binary symlinks / PATH check ─────────────────────────────────────
check_binaries() {
  echo "── Step 5: Binary check"

  local old_bin new_bin
  old_bin=$(command -v swarmstrd 2>/dev/null || true)
  new_bin=$(command -v metiqd 2>/dev/null || true)

  if [[ -n "$new_bin" ]]; then
    ok "metiqd found at: $new_bin"
  elif [[ -n "$old_bin" ]]; then
    warn "swarmstrd found at $old_bin but metiqd not on PATH"
    info "Install metiqd or create a symlink:"
    info "  ln -sf $old_bin $(dirname "$old_bin")/metiqd"
  else
    skip "Neither swarmstrd nor metiqd found on PATH"
  fi

  # Same for CLI
  old_bin=$(command -v swarmstr 2>/dev/null || true)
  new_bin=$(command -v metiq 2>/dev/null || true)

  if [[ -n "$new_bin" ]]; then
    ok "metiq CLI found at: $new_bin"
  elif [[ -n "$old_bin" ]]; then
    warn "swarmstr CLI found at $old_bin but metiq not on PATH"
    info "  ln -sf $old_bin $(dirname "$old_bin")/metiq"
  fi
}

# ── Step 6: Check for stale shell env vars ────────────────────────────────────
check_shell_env() {
  echo "── Step 6: Shell environment"

  local stale=0
  while IFS='=' read -r key _; do
    if [[ "$key" == SWARMSTR_* ]]; then
      if [[ $stale -eq 0 ]]; then
        warn "Active SWARMSTR_* environment variables detected:"
      fi
      info "  $key → ${key/SWARMSTR_/METIQ_}"
      stale=$((stale + 1))
    fi
  done < <(env)

  if [[ $stale -gt 0 ]]; then
    warn "Update these in your shell profile (~/.bashrc, ~/.zshrc, etc.)"
  else
    ok "No stale SWARMSTR_* env vars in current shell"
  fi
}

# ── Main ──────────────────────────────────────────────────────────────────────
banner
migrate_state_dir
echo ""
migrate_env_file
echo ""
migrate_config_files
echo ""
migrate_systemd
echo ""
check_binaries
echo ""
check_shell_env
echo ""

if [[ $CHANGES -gt 0 ]]; then
  if $DRY_RUN; then
    echo "Found $CHANGES change(s) to apply. Re-run with --apply to proceed."
  else
    ok "Migration complete ($CHANGES change(s) applied)."
    info "Original ~/.swarmstr preserved as backup."
    info "Remove it when you're confident everything works: rm -rf ~/.swarmstr"
  fi
else
  ok "Nothing to migrate — already on metiq."
fi
