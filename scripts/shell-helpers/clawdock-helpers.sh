#!/usr/bin/env bash
# metiqd Docker helpers
# Shell helpers for managing metiqd running in Docker.
#
# Installation:
#   mkdir -p ~/.swarmdock && curl -sL https://raw.githubusercontent.com/metiq/metiq/main/scripts/shell-helpers/clawdock-helpers.sh -o ~/.swarmdock/clawdock-helpers.sh
#   echo 'source ~/.swarmdock/clawdock-helpers.sh' >> ~/.zshrc
#
# Usage:
#   swarmdock-help    # Show all available commands

# =============================================================================
# Colors
# =============================================================================
_CLR_RESET='\033[0m'
_CLR_BOLD='\033[1m'
_CLR_DIM='\033[2m'
_CLR_GREEN='\033[0;32m'
_CLR_YELLOW='\033[1;33m'
_CLR_BLUE='\033[0;34m'
_CLR_MAGENTA='\033[0;35m'
_CLR_CYAN='\033[0;36m'
_CLR_RED='\033[0;31m'

# Styled command output (green + bold)
_clr_cmd() {
  echo -e "${_CLR_GREEN}${_CLR_BOLD}$1${_CLR_RESET}"
}

# Inline command for use in sentences
_cmd() {
  echo "${_CLR_GREEN}${_CLR_BOLD}$1${_CLR_RESET}"
}

# =============================================================================
# Config
# =============================================================================
SWARMDOCK_CONFIG="${HOME}/.swarmdock/config"

# Common paths to check for metiq
SWARMDOCK_COMMON_PATHS=(
  "${HOME}/metiq"
  "${HOME}/workspace/metiq"
  "${HOME}/projects/metiq"
  "${HOME}/dev/metiq"
  "${HOME}/code/metiq"
  "${HOME}/src/metiq"
)

_swarmdock_filter_warnings() {
  grep -v "^WARN\|^time="
}

_swarmdock_trim_quotes() {
  local value="$1"
  value="${value#\"}"
  value="${value%\"}"
  printf "%s" "$value"
}

_swarmdock_read_config_dir() {
  if [[ ! -f "$SWARMDOCK_CONFIG" ]]; then
    return 1
  fi
  local raw
  raw=$(sed -n 's/^SWARMDOCK_DIR=//p' "$SWARMDOCK_CONFIG" | head -n 1)
  if [[ -z "$raw" ]]; then
    return 1
  fi
  _swarmdock_trim_quotes "$raw"
}

# Ensure SWARMDOCK_DIR is set and valid
_swarmdock_ensure_dir() {
  # Already set and valid?
  if [[ -n "$SWARMDOCK_DIR" && -f "${SWARMDOCK_DIR}/docker-compose.yml" ]]; then
    return 0
  fi

  # Try loading from config
  local config_dir
  config_dir=$(_swarmdock_read_config_dir)
  if [[ -n "$config_dir" && -f "${config_dir}/docker-compose.yml" ]]; then
    SWARMDOCK_DIR="$config_dir"
    return 0
  fi

  # Auto-detect from common paths
  local found_path=""
  for path in "${SWARMDOCK_COMMON_PATHS[@]}"; do
    if [[ -f "${path}/docker-compose.yml" ]]; then
      found_path="$path"
      break
    fi
  done

  if [[ -n "$found_path" ]]; then
    echo ""
    echo "🐝 Found metiq at: $found_path"
    echo -n "   Use this location? [Y/n] "
    read -r response
    if [[ "$response" =~ ^[Nn] ]]; then
      echo ""
      echo "Set SWARMDOCK_DIR manually:"
      echo "  export SWARMDOCK_DIR=/path/to/metiq"
      return 1
    fi
    SWARMDOCK_DIR="$found_path"
  else
    echo ""
    echo "❌ metiq not found in common locations."
    echo ""
    echo "Clone it first:"
    echo ""
    echo "  git clone https://github.com/metiq/metiq.git ~/metiq"
    echo "  cd ~/metiq && ./docker-setup.sh"
    echo ""
    echo "Or set SWARMDOCK_DIR if it's elsewhere:"
    echo ""
    echo "  export SWARMDOCK_DIR=/path/to/metiq"
    echo ""
    return 1
  fi

  # Save to config
  if [[ ! -d "${HOME}/.swarmdock" ]]; then
    /bin/mkdir -p "${HOME}/.swarmdock"
  fi
  echo "SWARMDOCK_DIR=\"$SWARMDOCK_DIR\"" > "$SWARMDOCK_CONFIG"
  echo "✅ Saved to $SWARMDOCK_CONFIG"
  echo ""
  return 0
}

# Wrapper to run docker compose commands
_swarmdock_compose() {
  _swarmdock_ensure_dir || return 1
  local compose_args=(-f "${SWARMDOCK_DIR}/docker-compose.yml")
  if [[ -f "${SWARMDOCK_DIR}/docker-compose.extra.yml" ]]; then
    compose_args+=(-f "${SWARMDOCK_DIR}/docker-compose.extra.yml")
  fi
  command docker compose "${compose_args[@]}" "$@"
}

# Basic Operations
swarmdock-start() {
  _swarmdock_compose up -d metiqd
}

swarmdock-stop() {
  _swarmdock_compose down
}

swarmdock-restart() {
  _swarmdock_compose restart metiqd
}

swarmdock-logs() {
  _swarmdock_compose logs -f metiqd
}

swarmdock-status() {
  _swarmdock_compose ps
}

# Navigation
swarmdock-cd() {
  _swarmdock_ensure_dir || return 1
  cd "${SWARMDOCK_DIR}"
}

swarmdock-config() {
  cd ~/.metiq
}

swarmdock-workspace() {
  cd ~/.metiq/workspace
}

# Container Access
swarmdock-shell() {
  _swarmdock_compose exec metiqd bash
}

swarmdock-exec() {
  _swarmdock_compose exec metiqd "$@"
}

# Maintenance
swarmdock-rebuild() {
  _swarmdock_compose build metiqd
}

swarmdock-clean() {
  _swarmdock_compose down -v --remove-orphans
}

# Health check
swarmdock-health() {
  _swarmdock_ensure_dir || return 1
  _swarmdock_compose exec metiqd metiq status
}

# Show all available swarmdock helper commands
swarmdock-help() {
  echo -e "\n${_CLR_BOLD}${_CLR_CYAN}🐝 SwarmDock - Docker Helpers for metiqd${_CLR_RESET}\n"

  echo -e "${_CLR_BOLD}${_CLR_MAGENTA}⚡ Basic Operations${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-start)       ${_CLR_DIM}Start metiqd${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-stop)        ${_CLR_DIM}Stop metiqd${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-restart)     ${_CLR_DIM}Restart metiqd${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-status)      ${_CLR_DIM}Check container status${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-logs)        ${_CLR_DIM}View live logs (follows)${_CLR_RESET}"
  echo ""

  echo -e "${_CLR_BOLD}${_CLR_MAGENTA}🐚 Container Access${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-shell)       ${_CLR_DIM}Shell into container${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-exec) ${_CLR_CYAN}<cmd>${_CLR_RESET}  ${_CLR_DIM}Execute command in metiqd container${_CLR_RESET}"
  echo ""

  echo -e "${_CLR_BOLD}${_CLR_MAGENTA}⚙️  Navigation${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-cd)          ${_CLR_DIM}Jump to metiq project directory${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-config)      ${_CLR_DIM}Open config directory (~/.metiq)${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-workspace)   ${_CLR_DIM}Open workspace directory${_CLR_RESET}"
  echo ""

  echo -e "${_CLR_BOLD}${_CLR_MAGENTA}🔧 Maintenance${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-health)      ${_CLR_DIM}Run health check${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-rebuild)     ${_CLR_DIM}Rebuild Docker image${_CLR_RESET}"
  echo -e "  $(_cmd swarmdock-clean)       ${_CLR_RED}⚠️  Remove containers & volumes (nuclear)${_CLR_RESET}"
  echo ""

  echo -e "${_CLR_BOLD}${_CLR_CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${_CLR_RESET}"
  echo -e "${_CLR_BOLD}${_CLR_GREEN}🚀 First Time Setup${_CLR_RESET}"
  echo -e "${_CLR_CYAN}  1.${_CLR_RESET} $(_cmd swarmdock-start)          ${_CLR_DIM}# Start the daemon${_CLR_RESET}"
  echo -e "${_CLR_CYAN}  2.${_CLR_RESET} $(_cmd swarmdock-health)         ${_CLR_DIM}# Verify it's running${_CLR_RESET}"
  echo -e "${_CLR_CYAN}  3.${_CLR_RESET} $(_cmd swarmdock-logs)           ${_CLR_DIM}# Watch startup logs${_CLR_RESET}"
  echo ""

  echo -e "${_CLR_CYAN}💡 All commands guide you through next steps!${_CLR_RESET}"
  echo -e "${_CLR_BLUE}📚 Config: ${_CLR_RESET}${_CLR_CYAN}~/.metiq/config.json${_CLR_RESET}"
  echo ""
}
