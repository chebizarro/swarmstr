#!/usr/bin/env bash
# metiq — Docker Compose setup script.
#
# Builds the image, seeds configuration, fixes permissions, and starts
# the metiqd daemon via docker compose.
#
# Usage:
#   ./docker-setup.sh                # build + start
#   METIQ_VARIANT=slim ./docker-setup.sh   # use slim image
#
# Environment variables (all optional):
#   METIQ_IMAGE          — image name (default: metiq/metiqd:latest)
#   METIQ_VARIANT        — "default" (bookworm) or "slim" (bookworm-slim)
#   METIQ_VERSION        — version tag (default: dev)
#   METIQ_INSTALL_PYTHON — set to "1" to bake in Python 3 + uv
#   METIQ_INSTALL_NODE   — set to "1" to bake in Node.js
#   METIQ_INSTALL_BROWSER— set to "1" to bake in Chromium + Xvfb
#   METIQ_INSTALL_DOCKER_CLI — set to "1" to bake in Docker CLI
#   METIQ_APT_PACKAGES   — extra apt packages to install
#   METIQ_DATA_DIR       — host path for persistent data (default: Docker volume)
#   METIQ_ADMIN_PORT     — admin API host port (default: 7423)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="$ROOT_DIR/docker-compose.yml"
ENV_FILE="$ROOT_DIR/.env"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "Missing dependency: $1"
  fi
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
  fail "Need openssl, python3, or od to generate tokens."
}

upsert_env_var() {
  local file="$1"
  local key="$2"
  local value="$3"
  local tmp
  tmp="$(mktemp)"
  if [[ -f "$file" ]]; then
    awk -v k="$key" -v v="$value" '
      BEGIN { found = 0 }
      $0 ~ ("^" k "=") { print k "=" v; found = 1; next }
      { print }
      END { if (!found) print k "=" v }
    ' "$file" >"$tmp"
  else
    printf '%s=%s\n' "$key" "$value" >"$tmp"
  fi
  mv "$tmp" "$file"
  chmod 600 "$file" 2>/dev/null || true
}

# ── Prerequisites ───────────────────────────────────────────────────────────
require_cmd docker
if ! docker compose version >/dev/null 2>&1; then
  fail "Docker Compose not available (try: docker compose version)"
fi

if [[ ! -f "$COMPOSE_FILE" ]]; then
  fail "docker-compose.yml not found at $COMPOSE_FILE"
fi

# ── Ensure .env exists ──────────────────────────────────────────────────────
if [[ ! -f "$ENV_FILE" ]]; then
  if [[ -f "$ROOT_DIR/.env.example" ]]; then
    cp "$ROOT_DIR/.env.example" "$ENV_FILE"
    echo "Created $ENV_FILE from .env.example — please fill in your keys."
  else
    touch "$ENV_FILE"
    chmod 600 "$ENV_FILE"
  fi
fi

# ── Forward env vars to .env for compose ────────────────────────────────────
[[ -n "${METIQ_VERSION:-}" ]]        && upsert_env_var "$ENV_FILE" "METIQ_VERSION" "$METIQ_VERSION"
[[ -n "${METIQ_VARIANT:-}" ]]        && upsert_env_var "$ENV_FILE" "METIQ_VARIANT" "$METIQ_VARIANT"
[[ -n "${METIQ_INSTALL_PYTHON:-}" ]] && upsert_env_var "$ENV_FILE" "METIQ_INSTALL_PYTHON" "$METIQ_INSTALL_PYTHON"
[[ -n "${METIQ_INSTALL_NODE:-}" ]]   && upsert_env_var "$ENV_FILE" "METIQ_INSTALL_NODE" "$METIQ_INSTALL_NODE"
[[ -n "${METIQ_INSTALL_BROWSER:-}" ]]&& upsert_env_var "$ENV_FILE" "METIQ_INSTALL_BROWSER" "$METIQ_INSTALL_BROWSER"
[[ -n "${METIQ_INSTALL_DOCKER_CLI:-}" ]] && upsert_env_var "$ENV_FILE" "METIQ_INSTALL_DOCKER_CLI" "$METIQ_INSTALL_DOCKER_CLI"
[[ -n "${METIQ_APT_PACKAGES:-}" ]]   && upsert_env_var "$ENV_FILE" "METIQ_APT_PACKAGES" "$METIQ_APT_PACKAGES"

# ── Build image ─────────────────────────────────────────────────────────────
echo ""
echo "==> Building metiq Docker image"
docker compose -f "$COMPOSE_FILE" build metiqd

# ── Fix data directory permissions ──────────────────────────────────────────
# The container runs as uid 1000 (metiq user). Host-created directories may
# have different ownership, causing permission errors.
echo ""
echo "==> Fixing data-directory permissions"
docker compose -f "$COMPOSE_FILE" run --rm --user root --entrypoint sh metiqd -c \
  'chown -R metiq:metiq /data 2>/dev/null || true'

# ── Start services ──────────────────────────────────────────────────────────
echo ""
echo "==> Starting metiqd"
docker compose -f "$COMPOSE_FILE" up -d metiqd

echo ""
echo "==> metiqd is running"
echo ""
echo "  Health:  curl -fsS http://127.0.0.1:${METIQ_ADMIN_PORT:-7423}/health"
echo "  Logs:    docker compose logs -f metiqd"
echo "  Stop:    docker compose down"
echo "  Restart: docker compose restart metiqd"
echo ""
echo "To enable the full agent variant (Python + Node + browser):"
echo "  docker compose --profile full up -d"
echo ""
echo "To enable the browser sandbox:"
echo "  docker compose --profile browser up -d"
