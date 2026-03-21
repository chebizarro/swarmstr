#!/usr/bin/env bash
# metiq installer
# Usage: curl -fsSL https://metiq.dev/install.sh | bash
# Or:    curl -fsSL https://metiq.dev/install.sh | bash -s -- --prefix /usr/local

set -euo pipefail

# ── Colours ───────────────────────────────────────────────────────────────────
ACCENT='\033[38;2;100;180;255m'   # nostr-blue
SUCCESS='\033[38;2;0;229;180m'    # teal
WARN='\033[38;2;255;176;32m'      # amber
ERR='\033[38;2;230;57;70m'        # red
MUTED='\033[38;2;90;100;128m'     # dim
NC='\033[0m'

info()    { printf "${MUTED}·${NC} %s\n" "$*"; }
success() { printf "${SUCCESS}✓${NC} %s\n" "$*"; }
warn()    { printf "${WARN}!${NC} %s\n" "$*" >&2; }
error()   { printf "${ERR}✗${NC} %s\n" "$*" >&2; }
banner()  {
  printf "\n"
  printf "${ACCENT}  ⚡ metiq installer${NC}\n"
  printf "${MUTED}  Nostr-native AI agent daemon.${NC}\n"
  printf "\n"
}

# ── Defaults ──────────────────────────────────────────────────────────────────
PREFIX="${PREFIX:-}"
TAG="${TAG:-latest}"
INSTALL_SYSTEMD="${INSTALL_SYSTEMD:-auto}"   # auto | yes | no
GITHUB_REPO="${GITHUB_REPO:-metiq/metiq}"
CONFIG_DIR="${HOME}/.metiq"
DRY_RUN="${DRY_RUN:-}"

# ── Arg parse ─────────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)       PREFIX="$2"; shift 2 ;;
    --prefix=*)     PREFIX="${1#*=}"; shift ;;
    --tag)          TAG="$2"; shift 2 ;;
    --tag=*)        TAG="${1#*=}"; shift ;;
    --no-systemd)   INSTALL_SYSTEMD=no; shift ;;
    --systemd)      INSTALL_SYSTEMD=yes; shift ;;
    --dry-run)      DRY_RUN=1; shift ;;
    -h|--help)
      echo "Usage: install.sh [--prefix DIR] [--tag TAG] [--no-systemd] [--dry-run]"
      exit 0
      ;;
    *) warn "Unknown option: $1"; shift ;;
  esac
done

banner

# ── Platform detection ────────────────────────────────────────────────────────
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux)   GOOS=linux ;;
  Darwin)  GOOS=darwin ;;
  *)       error "Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64)  GOARCH=amd64 ;;
  arm64|aarch64) GOARCH=arm64 ;;
  armv7l)        GOARCH=arm ;;
  *)             error "Unsupported architecture: $ARCH"; exit 1 ;;
esac

success "Detected: ${GOOS}/${GOARCH}"

# ── Resolve install prefix ────────────────────────────────────────────────────
if [[ -z "$PREFIX" ]]; then
  if [[ "$GOOS" == "darwin" ]] && command -v brew &>/dev/null; then
    PREFIX="$(brew --prefix)"
    info "Using Homebrew prefix: $PREFIX"
  elif [[ -w /usr/local/bin ]]; then
    PREFIX=/usr/local
  else
    PREFIX="${HOME}/.local"
    mkdir -p "${PREFIX}/bin"
  fi
fi
BIN_DIR="${PREFIX}/bin"

# ── Resolve download URL ──────────────────────────────────────────────────────
resolve_url() {
  local tag="$1"
  if [[ "$tag" == "latest" ]]; then
    # Query GitHub releases API for latest
    local api_url="https://api.github.com/repos/${GITHUB_REPO}/releases/latest"
    if command -v curl &>/dev/null; then
      tag=$(curl -fsSL "$api_url" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
    elif command -v wget &>/dev/null; then
      tag=$(wget -qO- "$api_url" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
    else
      error "curl or wget is required"
      exit 1
    fi
    [[ -z "$tag" ]] && { error "Could not determine latest release tag"; exit 1; }
    info "Resolved latest tag: $tag"
  fi
  echo "https://github.com/${GITHUB_REPO}/releases/download/${tag}/metiqd-${GOOS}-${GOARCH}"
}

DOWNLOAD_URL="$(resolve_url "$TAG")"
info "Download URL: $DOWNLOAD_URL"

# ── Download ──────────────────────────────────────────────────────────────────
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
TMP_BIN="${TMP_DIR}/metiqd"

if [[ -n "$DRY_RUN" ]]; then
  info "[DRY RUN] Would download: $DOWNLOAD_URL → ${BIN_DIR}/metiqd"
else
  info "Downloading metiqd …"
  if command -v curl &>/dev/null; then
    curl -fsSL --progress-bar "$DOWNLOAD_URL" -o "$TMP_BIN"
  elif command -v wget &>/dev/null; then
    wget -q --show-progress "$DOWNLOAD_URL" -O "$TMP_BIN"
  else
    error "curl or wget is required"
    exit 1
  fi
  chmod +x "$TMP_BIN"
  success "Downloaded"
fi

# ── Install binary ────────────────────────────────────────────────────────────
if [[ -n "$DRY_RUN" ]]; then
  info "[DRY RUN] Would install to: ${BIN_DIR}/metiqd"
else
  mkdir -p "$BIN_DIR"
  if [[ -w "$BIN_DIR" ]]; then
    mv "$TMP_BIN" "${BIN_DIR}/metiqd"
  else
    sudo mv "$TMP_BIN" "${BIN_DIR}/metiqd"
  fi
  success "Installed to ${BIN_DIR}/metiqd"
fi

# ── Create config dir ─────────────────────────────────────────────────────────
ENV_FILE="${CONFIG_DIR}/.env"
if [[ -n "$DRY_RUN" ]]; then
  info "[DRY RUN] Would create config dir: $CONFIG_DIR"
elif [[ ! -d "$CONFIG_DIR" ]]; then
  mkdir -p "$CONFIG_DIR"
  success "Created config dir: $CONFIG_DIR"
fi

if [[ -z "$DRY_RUN" && ! -f "$ENV_FILE" ]]; then
  cat > "$ENV_FILE" <<'EOF'
# metiq environment configuration
# Copy the relevant keys and fill in your values.

# ── Nostr ─────────────────────────────────────────────────────────────────────
# Your agent's Nostr private key (hex or nsec bech32)
#METIQ_NOSTR_KEY=

# Optional: comma-separated relay URLs
#METIQ_NOSTR_RELAYS=wss://nos.lol,wss://relay.primal.net,wss://relay.sharegap.net

# ── LLM providers ─────────────────────────────────────────────────────────────
#ANTHROPIC_API_KEY=
#OPENAI_API_KEY=
#GEMINI_API_KEY=
#XAI_API_KEY=
#GROQ_API_KEY=
#MISTRAL_API_KEY=
#OPENROUTER_API_KEY=
#TOGETHER_API_KEY=

# Default model (e.g. claude-sonnet-4-5, gpt-4o, gemini-2.0-flash, grok-3)
#METIQ_DEFAULT_MODEL=claude-sonnet-4-5

# ── Browser sandbox (optional) ────────────────────────────────────────────────
# URL of a running Playwright proxy (see scripts/docker/)
#METIQ_BROWSER_URL=http://localhost:3500

# ── Skills ───────────────────────────────────────────────────────────────────
# Override location of managed (user-installed) skills
#METIQ_MANAGED_SKILLS_DIR=${HOME}/.metiq/skills
EOF
  success "Created ${ENV_FILE}"
fi

# ── Systemd user service (Linux only) ─────────────────────────────────────────
install_systemd() {
  local service_dir="${HOME}/.config/systemd/user"
  local unit_src
  unit_src="$(dirname "$0")/systemd/metiqd.service"

  if [[ ! -f "$unit_src" ]]; then
    # inline the unit if the scripts/ tree isn't alongside the installer
    unit_src="${TMP_DIR}/metiqd.service"
    cat > "$unit_src" <<EOF
[Unit]
Description=metiq daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=%h/.metiq/.env
ExecStart=${BIN_DIR}/metiqd
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
  fi

  mkdir -p "$service_dir"
  cp "$unit_src" "${service_dir}/metiqd.service"
  systemctl --user daemon-reload
  systemctl --user enable --now metiqd.service
  success "Systemd user service installed and started"
}

if [[ "$GOOS" == "linux" ]]; then
  case "$INSTALL_SYSTEMD" in
    yes)
      if [[ -n "$DRY_RUN" ]]; then
        info "[DRY RUN] Would install systemd user service"
      else
        install_systemd
      fi
      ;;
    no)
      info "Skipping systemd service (--no-systemd)"
      ;;
    auto)
      if command -v systemctl &>/dev/null && systemctl --user show-environment &>/dev/null 2>&1; then
        if [[ -n "$DRY_RUN" ]]; then
          info "[DRY RUN] Would install systemd user service (auto-detected)"
        else
          install_systemd
        fi
      else
        info "systemd not available, skipping service setup"
      fi
      ;;
  esac
fi

# ── PATH hint ─────────────────────────────────────────────────────────────────
if [[ ":$PATH:" != *":${BIN_DIR}:"* ]]; then
  warn "${BIN_DIR} is not in your PATH."
  case "$SHELL" in
    */zsh)  printf "  Add to ~/.zshrc:   ${MUTED}export PATH=\"%s:\$PATH\"${NC}\n" "$BIN_DIR" ;;
    */fish) printf "  Add to config:    ${MUTED}fish_add_path %s${NC}\n" "$BIN_DIR" ;;
    *)      printf "  Add to ~/.bashrc:  ${MUTED}export PATH=\"%s:\$PATH\"${NC}\n" "$BIN_DIR" ;;
  esac
fi

printf "\n"
printf "${ACCENT}⚡ metiq installed!${NC}\n"
printf "\n"
info "Config:  ${CONFIG_DIR}/.env"
info "Binary:  ${BIN_DIR}/metiqd"
printf "\n"
info "Next steps:"
info "  1. Edit ${CONFIG_DIR}/.env and add your Nostr key + API keys"
info "  2. Run: metiqd"
info "  3. Send a DM on Nostr to your agent's pubkey"
printf "\n"
