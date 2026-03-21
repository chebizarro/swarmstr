#!/usr/bin/env bash
# Rebuild and restart metiqd.
# Kills the running daemon, rebuilds the Go binary, and relaunches.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_PATH="${METIQ_RESTART_LOG:-/tmp/metiq-restart.log}"
WAIT_FOR_LOCK=0
LOCK_KEY="$(printf '%s' "${ROOT_DIR}" | shasum -a 256 | cut -c1-8)"
LOCK_DIR="${TMPDIR:-/tmp}/metiq-restart-${LOCK_KEY}"
LOCK_PID_FILE="${LOCK_DIR}/pid"

log()  { printf '%s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

cleanup() {
  if [[ -d "${LOCK_DIR}" ]]; then
    rm -rf "${LOCK_DIR}"
  fi
}

acquire_lock() {
  while true; do
    if mkdir "${LOCK_DIR}" 2>/dev/null; then
      echo "$$" > "${LOCK_PID_FILE}"
      return 0
    fi

    local existing_pid=""
    if [[ -f "${LOCK_PID_FILE}" ]]; then
      existing_pid="$(cat "${LOCK_PID_FILE}" 2>/dev/null || true)"
    fi

    if [[ -n "${existing_pid}" ]] && kill -0 "${existing_pid}" 2>/dev/null; then
      if [[ "${WAIT_FOR_LOCK}" == "1" ]]; then
        log "==> Another restart is running (pid ${existing_pid}); waiting..."
        while kill -0 "${existing_pid}" 2>/dev/null; do
          sleep 1
        done
        continue
      fi
      log "==> Another restart is running (pid ${existing_pid}); re-run with --wait."
      exit 0
    fi

    rm -rf "${LOCK_DIR}"
  done
}

run_step() {
  local label="$1"; shift
  log "==> ${label}"
  if ! "$@"; then
    fail "${label} failed"
  fi
}

trap cleanup EXIT INT TERM

for arg in "$@"; do
  case "${arg}" in
    --wait|-w) WAIT_FOR_LOCK=1 ;;
    --help|-h)
      log "Usage: $(basename "$0") [--wait]"
      log "  --wait    Wait for other restart to complete instead of exiting"
      log ""
      log "Env:"
      log "  METIQ_RESTART_LOG=/tmp/metiq-restart.log  Log path"
      log ""
      log "After restart, metiqd will re-read ~/.metiq/config.json on startup."
      exit 0
      ;;
    *) ;;
  esac
done

mkdir -p "$(dirname "$LOG_PATH")"
rm -f "$LOG_PATH"
exec > >(tee "$LOG_PATH") 2>&1
log "==> Log: ${LOG_PATH}"

acquire_lock

# 1) Stop the running daemon.
log "==> Stopping metiqd"
if systemctl --user is-active metiqd >/dev/null 2>&1; then
  systemctl --user stop metiqd
  log "    systemd unit stopped"
elif pgrep -x metiqd >/dev/null 2>&1; then
  pkill -x metiqd || true
  sleep 0.5
  log "    process killed"
else
  log "    metiqd was not running"
fi

# 2) Rebuild.
run_step "go build" bash -lc "cd '${ROOT_DIR}' && go build -o dist/metiqd ./cmd/metiqd"

# 3) Restart via systemd (or launch directly if no systemd unit).
if systemctl --user cat metiqd >/dev/null 2>&1; then
  run_step "start metiqd (systemd)" systemctl --user start metiqd
  sleep 1
  if systemctl --user is-active metiqd >/dev/null 2>&1; then
    log "OK: metiqd is running (systemd)."
  else
    fail "metiqd failed to start. Check: journalctl --user -u metiqd -n 50"
  fi
else
  log "==> No systemd unit found; launching metiqd directly (background)"
  nohup "${ROOT_DIR}/dist/metiqd" >"${LOG_PATH}.daemon" 2>&1 &
  sleep 1
  if pgrep -x metiqd >/dev/null 2>&1; then
    log "OK: metiqd is running (pid $(pgrep -x metiqd))."
    log "    Logs: ${LOG_PATH}.daemon"
  else
    fail "metiqd exited immediately. Check ${LOG_PATH}.daemon"
  fi
fi
