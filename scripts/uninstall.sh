#!/usr/bin/env bash
# Remove LocalMedia Unlock while preserving user data by default.
#
# Usage:
#   sudo ./scripts/uninstall.sh
#   sudo ./scripts/uninstall.sh --purge
#
# Environment:
#   PREFIX, BIN_DIR, CONFIG_DIR, DATA_DIR, SERVICE_NAME and SERVICE_USER are
#   the same as in install.sh. If a non-default prefix was used for install,
#   pass the same variables here.

set -euo pipefail

PREFIX="${PREFIX:-/opt/localmedia-unlock}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-/etc/localmedia-unlock}"
DATA_DIR="${DATA_DIR:-/var/lib/localmedia-unlock}"
SERVICE_NAME="${SERVICE_NAME:-localmedia-unlock}"
SERVICE_USER="${SERVICE_USER:-localmedia}"
SERVICE_GROUP="${SERVICE_GROUP:-localmedia}"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
PURGE=0

log() {
  printf '[uninstall] %s\n' "$*"
}

die() {
  printf '[uninstall] ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: uninstall.sh [--purge]

  (default)  remove the binary, systemd unit and service account; keep
             configuration, generated tokens and runtime data
  --purge    additionally remove config and data directories
EOF
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --purge)
        PURGE=1
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown argument: $1 (use --help)"
        ;;
    esac
    shift
  done
}

require_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    die "run as root (for example: sudo $0)"
  fi
}

remove_path() {
  local path="$1"
  if [[ -e "${path}" || -L "${path}" ]]; then
    rm -rf -- "${path}"
    log "removed ${path}"
  fi
}

remove_empty_dir() {
  local path="$1"
  if [[ -d "${path}" && "${path}" != "/" && -n "${path}" ]]; then
    rmdir -- "${path}" 2>/dev/null || log "kept non-empty directory ${path}"
  fi
}

remove_unit() {
  if ! command -v systemctl >/dev/null 2>&1; then
    log "systemctl not found; skipping unit removal"
    return 0
  fi
  systemctl stop "${SERVICE_NAME}.service" 2>/dev/null || true
  systemctl disable "${SERVICE_NAME}.service" 2>/dev/null || true
  remove_path "${UNIT_PATH}"
  systemctl daemon-reload || true
  systemctl reset-failed "${SERVICE_NAME}.service" 2>/dev/null || true
}

remove_binary() {
  remove_path "${BIN_DIR}/localmedia-unlock"
  remove_path "${PREFIX}/bin/localmedia-unlock"
  if [[ -d "${PREFIX}/bin" ]]; then
    rmdir -- "${PREFIX}/bin" 2>/dev/null || true
  fi
  if [[ -d "${PREFIX}" ]]; then
    rmdir -- "${PREFIX}" 2>/dev/null || true
  fi
}

remove_service_account() {
  if id -u "${SERVICE_USER}" >/dev/null 2>&1 &&
    [[ "$(id -u "${SERVICE_USER}")" -lt 1000 ]]; then
    userdel "${SERVICE_USER}" 2>/dev/null || log "could not remove user ${SERVICE_USER}"
  fi
  if getent group "${SERVICE_GROUP}" >/dev/null 2>&1 &&
    [[ "$(getent group "${SERVICE_GROUP}" | cut -d: -f3)" -lt 1000 ]]; then
    groupdel "${SERVICE_GROUP}" 2>/dev/null || log "could not remove group ${SERVICE_GROUP}"
  fi
}

main() {
  parse_args "$@"
  require_root
  [[ "${PREFIX}" == "/" || -z "${PREFIX}" ]] && die "unsafe PREFIX: ${PREFIX}"
  [[ "${CONFIG_DIR}" == "/" || -z "${CONFIG_DIR}" ]] && die "unsafe CONFIG_DIR: ${CONFIG_DIR}"
  [[ "${DATA_DIR}" == "/" || -z "${DATA_DIR}" ]] && die "unsafe DATA_DIR: ${DATA_DIR}"

  remove_unit
  remove_binary
  remove_empty_dir "${DATA_DIR}"
  remove_empty_dir "${CONFIG_DIR}"

  if [[ "${PURGE}" -eq 1 ]]; then
    remove_path "${CONFIG_DIR}"
    remove_path "${DATA_DIR}"
    log "purged configuration and runtime data"
  else
    log "kept configuration in ${CONFIG_DIR}"
    log "kept runtime data in ${DATA_DIR}"
    log "run with --purge to remove them as well"
  fi

  remove_service_account
  log "uninstall complete"
}

main "$@"

