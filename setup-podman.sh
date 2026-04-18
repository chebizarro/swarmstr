#!/usr/bin/env bash
# metiq — One-time host setup for rootless Podman.
#
# Creates a dedicated metiq user, builds the image, loads it into the user's
# Podman store, and installs the launch script. Run from repo root with sudo.
#
# Usage:
#   ./setup-podman.sh                  # user + image + launch script
#   ./setup-podman.sh --quadlet        # also install systemd Quadlet service
#
# After setup, start the daemon:
#   ./scripts/run-metiq-podman.sh launch
#   ./scripts/run-metiq-podman.sh launch setup   # onboarding wizard
#
# Or as the metiq user:
#   sudo -u metiq /home/metiq/run-metiq-podman.sh
#
# Environment variables:
#   METIQ_PODMAN_USER    — system user to create (default: metiq)
#   METIQ_REPO_PATH      — path to repo root (default: script directory)
#   METIQ_PODMAN_QUADLET — "1" to install Quadlet (same as --quadlet)
set -euo pipefail

METIQ_USER="${METIQ_PODMAN_USER:-metiq}"
REPO_PATH="${METIQ_REPO_PATH:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
RUN_SCRIPT_SRC="$REPO_PATH/scripts/run-metiq-podman.sh"
QUADLET_TEMPLATE="$REPO_PATH/scripts/podman/metiq.container.in"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing dependency: $1" >&2
    exit 1
  fi
}

is_writable_dir() {
  local dir="$1"
  [[ -n "$dir" && -d "$dir" && ! -L "$dir" && -w "$dir" && -x "$dir" ]]
}

is_safe_tmp_base() {
  local dir="$1"
  local mode=""
  local owner=""
  is_writable_dir "$dir" || return 1
  mode="$(stat -Lc '%a' "$dir" 2>/dev/null || true)"
  if [[ -n "$mode" ]]; then
    local perm=$((8#$mode))
    if (( (perm & 0022) != 0 && (perm & 01000) == 0 )); then
      return 1
    fi
  fi
  if is_root; then
    owner="$(stat -Lc '%u' "$dir" 2>/dev/null || true)"
    if [[ -n "$owner" && "$owner" != "0" ]]; then
      return 1
    fi
  fi
  return 0
}

resolve_image_tmp_dir() {
  if ! is_root && is_safe_tmp_base "${TMPDIR:-}"; then
    printf '%s' "$TMPDIR"
    return 0
  fi
  for d in /var/tmp /tmp; do
    if is_safe_tmp_base "$d"; then
      printf '%s' "$d"
      return 0
    fi
  done
  printf '%s' "/tmp"
}

is_root() { [[ "$(id -u)" -eq 0 ]]; }

run_root() {
  if is_root; then
    "$@"
  else
    sudo "$@"
  fi
}

run_as_user() {
  local user="$1"
  shift
  if command -v sudo >/dev/null 2>&1; then
    ( cd /tmp 2>/dev/null || cd /; sudo -u "$user" "$@" )
  elif is_root && command -v runuser >/dev/null 2>&1; then
    ( cd /tmp 2>/dev/null || cd /; runuser -u "$user" -- "$@" )
  else
    echo "Need sudo (or root+runuser) to run commands as $user." >&2
    exit 1
  fi
}

run_as_metiq() {
  run_as_user "$METIQ_USER" env HOME="$METIQ_HOME" "$@"
}

escape_sed_replacement_pipe_delim() {
  printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'
}

generate_token_hex_32() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
    return 0
  fi
  if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import secrets; print(secrets.token_hex(32))'
    return 0
  fi
  if command -v od >/dev/null 2>&1; then
    od -An -N32 -tx1 /dev/urandom | tr -d " \n"
    echo
    return 0
  fi
  echo "Missing dependency: need openssl, python3, or od to generate tokens." >&2
  exit 1
}

user_exists() {
  local user="$1"
  if command -v getent >/dev/null 2>&1; then
    getent passwd "$user" >/dev/null 2>&1 && return 0
  fi
  id -u "$user" >/dev/null 2>&1
}

resolve_user_home() {
  local user="$1"
  local home=""
  if command -v getent >/dev/null 2>&1; then
    home="$(getent passwd "$user" 2>/dev/null | cut -d: -f6 || true)"
  fi
  if [[ -z "$home" && -f /etc/passwd ]]; then
    home="$(awk -F: -v u="$user" '$1==u {print $6}' /etc/passwd 2>/dev/null || true)"
  fi
  if [[ -z "$home" ]]; then
    home="/home/$user"
  fi
  printf '%s' "$home"
}

resolve_nologin_shell() {
  for cand in /usr/sbin/nologin /sbin/nologin /usr/bin/nologin /bin/false; do
    if [[ -x "$cand" ]]; then
      printf '%s' "$cand"
      return 0
    fi
  done
  printf '%s' "/usr/sbin/nologin"
}

# ── Parse flags ─────────────────────────────────────────────────────────────
INSTALL_QUADLET=false
for arg in "$@"; do
  case "$arg" in
    --quadlet)   INSTALL_QUADLET=true ;;
    --container) INSTALL_QUADLET=false ;;
  esac
done
if [[ -n "${METIQ_PODMAN_QUADLET:-}" ]]; then
  case "${METIQ_PODMAN_QUADLET,,}" in
    1|yes|true)  INSTALL_QUADLET=true ;;
    0|no|false)  INSTALL_QUADLET=false ;;
  esac
fi

# ── Prerequisites ───────────────────────────────────────────────────────────
require_cmd podman
if ! is_root; then
  require_cmd sudo
fi
if [[ ! -f "$REPO_PATH/Dockerfile" ]]; then
  echo "Dockerfile not found at $REPO_PATH. Set METIQ_REPO_PATH to the repo root." >&2
  exit 1
fi
if [[ ! -f "$RUN_SCRIPT_SRC" ]]; then
  echo "Launch script not found at $RUN_SCRIPT_SRC." >&2
  exit 1
fi

# ── Create metiq user ──────────────────────────────────────────────────────
if ! user_exists "$METIQ_USER"; then
  NOLOGIN_SHELL="$(resolve_nologin_shell)"
  echo "Creating user $METIQ_USER ($NOLOGIN_SHELL, with home)..."
  if command -v useradd >/dev/null 2>&1; then
    run_root useradd -m -s "$NOLOGIN_SHELL" "$METIQ_USER"
  elif command -v adduser >/dev/null 2>&1; then
    run_root adduser --disabled-password --gecos "" --shell "$NOLOGIN_SHELL" "$METIQ_USER"
  else
    echo "Neither useradd nor adduser found, cannot create user $METIQ_USER." >&2
    exit 1
  fi
else
  echo "User $METIQ_USER already exists."
fi

METIQ_HOME="$(resolve_user_home "$METIQ_USER")"
METIQ_UID="$(id -u "$METIQ_USER" 2>/dev/null || true)"
METIQ_CONFIG="$METIQ_HOME/.metiq"
LAUNCH_SCRIPT_DST="$METIQ_HOME/run-metiq-podman.sh"

# Enable lingering for rootless Podman to survive logout.
if command -v loginctl &>/dev/null; then
  run_root loginctl enable-linger "$METIQ_USER" 2>/dev/null || true
fi
if [[ -n "${METIQ_UID:-}" && -d /run/user ]] && command -v systemctl &>/dev/null; then
  run_root systemctl start "user@${METIQ_UID}.service" 2>/dev/null || true
fi

# Rootless Podman needs subuid/subgid ranges.
if ! grep -q "^${METIQ_USER}:" /etc/subuid 2>/dev/null; then
  echo "Warning: $METIQ_USER has no subuid range. Rootless Podman may fail." >&2
  echo "  Add a line to /etc/subuid and /etc/subgid, e.g.: $METIQ_USER:100000:65536" >&2
fi

echo "Creating $METIQ_CONFIG and workspace..."
run_as_metiq mkdir -p "$METIQ_CONFIG/workspace"
run_as_metiq chmod 700 "$METIQ_CONFIG" "$METIQ_CONFIG/workspace" 2>/dev/null || true

# ── Seed .env with gateway token ────────────────────────────────────────────
ENV_FILE="$METIQ_CONFIG/.env"
if run_as_metiq test -f "$ENV_FILE"; then
  if ! run_as_metiq grep -q '^METIQ_GATEWAY_TOKEN=' "$ENV_FILE" 2>/dev/null; then
    TOKEN="$(generate_token_hex_32)"
    printf 'METIQ_GATEWAY_TOKEN=%s\n' "$TOKEN" | run_as_metiq tee -a "$ENV_FILE" >/dev/null
    echo "Added METIQ_GATEWAY_TOKEN to $ENV_FILE."
  fi
  run_as_metiq chmod 600 "$ENV_FILE" 2>/dev/null || true
else
  TOKEN="$(generate_token_hex_32)"
  printf 'METIQ_GATEWAY_TOKEN=%s\n' "$TOKEN" | run_as_metiq tee "$ENV_FILE" >/dev/null
  run_as_metiq chmod 600 "$ENV_FILE" 2>/dev/null || true
  echo "Created $ENV_FILE with new token."
fi

# ── Build image ─────────────────────────────────────────────────────────────
echo "Building image from $REPO_PATH..."
BUILD_ARGS=()
[[ -n "${METIQ_APT_PACKAGES:-}" ]] && BUILD_ARGS+=(--build-arg "METIQ_APT_PACKAGES=${METIQ_APT_PACKAGES}")
[[ -n "${METIQ_INSTALL_PYTHON:-}" ]] && BUILD_ARGS+=(--build-arg "METIQ_INSTALL_PYTHON=${METIQ_INSTALL_PYTHON}")
[[ -n "${METIQ_INSTALL_NODE:-}" ]] && BUILD_ARGS+=(--build-arg "METIQ_INSTALL_NODE=${METIQ_INSTALL_NODE}")
[[ -n "${METIQ_INSTALL_BROWSER:-}" ]] && BUILD_ARGS+=(--build-arg "METIQ_INSTALL_BROWSER=${METIQ_INSTALL_BROWSER}")
[[ -n "${METIQ_INSTALL_DOCKER_CLI:-}" ]] && BUILD_ARGS+=(--build-arg "METIQ_INSTALL_DOCKER_CLI=${METIQ_INSTALL_DOCKER_CLI}")
podman build ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} -t metiqd:local -f "$REPO_PATH/Dockerfile" "$REPO_PATH"

# ── Load image into metiq user's Podman store ──────────────────────────────
echo "Loading image into $METIQ_USER's Podman store..."
TMP_IMAGE_DIR="$(resolve_image_tmp_dir)"
echo "Using temporary image dir: $TMP_IMAGE_DIR"
TMP_STAGE_DIR="$(mktemp -d -p "$TMP_IMAGE_DIR" metiq-image.XXXXXX)"
TMP_IMAGE="$TMP_STAGE_DIR/image.tar"
chmod 700 "$TMP_STAGE_DIR"
trap 'rm -rf "$TMP_STAGE_DIR"' EXIT
podman save metiqd:local -o "$TMP_IMAGE"
chmod 600 "$TMP_IMAGE"
cat "$TMP_IMAGE" | run_as_user "$METIQ_USER" env HOME="$METIQ_HOME" podman load
rm -rf "$TMP_STAGE_DIR"
trap - EXIT

# ── Install launch script ──────────────────────────────────────────────────
echo "Copying launch script to $LAUNCH_SCRIPT_DST..."
run_root cat "$RUN_SCRIPT_SRC" | run_as_metiq tee "$LAUNCH_SCRIPT_DST" >/dev/null
run_as_metiq chmod 755 "$LAUNCH_SCRIPT_DST"

# ── Quadlet (systemd user service) ─────────────────────────────────────────
QUADLET_DIR="$METIQ_HOME/.config/containers/systemd"
if [[ "$INSTALL_QUADLET" == true && -f "$QUADLET_TEMPLATE" ]]; then
  echo "Installing systemd Quadlet for $METIQ_USER..."
  run_as_metiq mkdir -p "$QUADLET_DIR"
  METIQ_HOME_SED="$(escape_sed_replacement_pipe_delim "$METIQ_HOME")"
  sed "s|{{METIQ_HOME}}|$METIQ_HOME_SED|g" "$QUADLET_TEMPLATE" | run_as_metiq tee "$QUADLET_DIR/metiq.container" >/dev/null
  run_as_metiq chmod 700 "$METIQ_HOME/.config" "$METIQ_HOME/.config/containers" "$QUADLET_DIR" 2>/dev/null || true
  run_as_metiq chmod 600 "$QUADLET_DIR/metiq.container" 2>/dev/null || true
  if command -v systemctl &>/dev/null; then
    run_root systemctl --machine "${METIQ_USER}@" --user daemon-reload 2>/dev/null || true
    run_root systemctl --machine "${METIQ_USER}@" --user enable metiq.service 2>/dev/null || true
    run_root systemctl --machine "${METIQ_USER}@" --user start metiq.service 2>/dev/null || true
  fi
fi

echo ""
echo "Setup complete. Start the daemon:"
echo "  $RUN_SCRIPT_SRC launch"
echo "  $RUN_SCRIPT_SRC launch setup   # onboarding wizard"
echo "Or as $METIQ_USER (e.g. from cron):"
echo "  sudo -u $METIQ_USER $LAUNCH_SCRIPT_DST"
if [[ "$INSTALL_QUADLET" == true ]]; then
  echo "Or use systemd (Quadlet):"
  echo "  sudo systemctl --machine ${METIQ_USER}@ --user start metiq.service"
  echo "  sudo systemctl --machine ${METIQ_USER}@ --user status metiq.service"
else
  echo "To install systemd Quadlet later: $0 --quadlet"
fi
