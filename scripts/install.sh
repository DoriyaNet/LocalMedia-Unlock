#!/usr/bin/env bash
# Install LocalMedia Unlock on a Linux host.
#
# Usage:
#   sudo ./scripts/install.sh
#   sudo PREFIX=/opt/localmedia-unlock ./scripts/install.sh
#
# Environment:
#   PREFIX             install prefix (default: /opt/localmedia-unlock)
#   BIN_DIR            binary directory (default: /usr/local/bin)
#   CONFIG_DIR         config directory (default: /etc/localmedia-unlock)
#   DATA_DIR           runtime data directory (default: /var/lib/localmedia-unlock)
#   SERVICE_NAME       systemd unit name (default: localmedia-unlock)
#   REPO               GitHub repository, owner/repo
#   LOCALMEDIA_VERSION release tag or "latest" (default: latest)
#   ASSET_NAME         explicit release asset name (optional)
#   DOWNLOAD_BASE_URL  custom release download base URL (optional)
#   LOCALMEDIA_BIN     use an existing binary instead of downloading/building
#   SKIP_SYSTEMD       set to 1 to install files without enabling the service
#   NO_START           set to 1 to install without starting the service
#
# The installer never downloads a proxy kernel and never overwrites an existing
# config.json. Re-running it upgrades the binary and service files in place.

set -euo pipefail
umask 022

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly PROJECT_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

PREFIX="${PREFIX:-/opt/localmedia-unlock}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-/etc/localmedia-unlock}"
DATA_DIR="${DATA_DIR:-/var/lib/localmedia-unlock}"
INIT_LISTEN="${INIT_LISTEN:-127.0.0.1:19091}"
SERVICE_NAME="${SERVICE_NAME:-localmedia-unlock}"
REPO="${REPO:-}"
LOCALMEDIA_VERSION="${LOCALMEDIA_VERSION:-latest}"
ASSET_NAME="${ASSET_NAME:-}"
DOWNLOAD_BASE_URL="${DOWNLOAD_BASE_URL:-}"
LOCALMEDIA_BIN="${LOCALMEDIA_BIN:-}"
SKIP_SYSTEMD="${SKIP_SYSTEMD:-0}"
NO_START="${NO_START:-0}"
SERVICE_USER="${SERVICE_USER:-localmedia}"
SERVICE_GROUP="${SERVICE_GROUP:-localmedia}"

BIN_PATH="${BIN_DIR}/localmedia-unlock"
CONFIG_PATH="${CONFIG_DIR}/config.json"
ENV_PATH="${CONFIG_DIR}/localmedia-unlock.env"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
WORK_DIR=""
ARCH=""
ASSET=""
CREATED_GROUP=0
CREATED_USER=0

log() {
  printf '[install] %s\n' "$*"
}

die() {
  printf '[install] ERROR: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "${WORK_DIR}" && -d "${WORK_DIR}" ]]; then
    rm -rf -- "${WORK_DIR}"
  fi
}
trap cleanup EXIT

on_error() {
  local status="$?"
  if [[ "${CREATED_USER}" -eq 1 ]]; then
    userdel "${SERVICE_USER}" 2>/dev/null || true
  fi
  if [[ "${CREATED_GROUP}" -eq 1 ]]; then
    groupdel "${SERVICE_GROUP}" 2>/dev/null || true
  fi
  exit "${status}"
}
trap on_error ERR

require_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    die "run as root (for example: sudo $0)"
  fi
}

need_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      ARCH="amd64"
      ;;
    aarch64|arm64)
      ARCH="arm64"
      ;;
    *)
      die "unsupported architecture $(uname -m); supported: amd64, arm64"
      ;;
  esac
  log "detected architecture: ${ARCH}"
}

resolve_version() {
  if [[ "${LOCALMEDIA_VERSION}" == "latest" ]]; then
    local effective_url="${DOWNLOAD_BASE_URL%/}"
    if [[ -z "${effective_url}" ]]; then
      require_network_tools
      local api_url="https://api.github.com/repos/${REPO}/releases/latest"
      LOCALMEDIA_VERSION="$(
        curl -fsSL --retry 2 --max-time 20 "${api_url}" 2>/dev/null \
          | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
          | head -n 1
      )" || true
      if [[ -z "${LOCALMEDIA_VERSION}" ]]; then
        log "could not resolve the latest release for ${REPO}"
        return 1
      fi
    else
      # A custom mirror can expose a static "latest" asset. Keep the tag as
      # "latest" unless the caller supplied a concrete LOCALMEDIA_VERSION.
      :
    fi
  fi
  log "release version: ${LOCALMEDIA_VERSION}"
}

require_network_tools() {
  need_command curl
  need_command tar
}

download_url() {
  if [[ -n "${DOWNLOAD_BASE_URL}" ]]; then
    printf '%s/%s/%s' "${DOWNLOAD_BASE_URL%/}" "${LOCALMEDIA_VERSION}" "${ASSET}"
  else
    printf 'https://github.com/%s/releases/download/%s/%s' \
      "${REPO}" "${LOCALMEDIA_VERSION}" "${ASSET}"
  fi
}

verify_checksum() {
  local archive="$1"
  local dir="$2"
  local base
  base="$(basename "${archive}" .tar.gz)"
  local checksum_url="${CHECKSUM_URL:-}"
  if [[ -z "${checksum_url}" ]]; then
    if [[ -n "${DOWNLOAD_BASE_URL}" ]]; then
      checksum_url="${DOWNLOAD_BASE_URL%/}/${LOCALMEDIA_VERSION}/${base}.sha256"
    else
      checksum_url="https://github.com/${REPO}/releases/download/${LOCALMEDIA_VERSION}/${base}.sha256"
    fi
  fi

  local checksum_file="${dir}/checksums.txt"
  if ! curl -fsSL --retry 2 --max-time 20 "${checksum_url}" -o "${checksum_file}"; then
    log "checksum file unavailable; skipping checksum verification"
    return 0
  fi

  local expected
  expected="$(grep -Eo '[a-fA-F0-9]{64}' "${checksum_file}" | head -n 1)"
  [[ -n "${expected}" ]] || die "checksum file has no SHA-256 digest: ${checksum_url}"

  local actual
  actual="$(sha256sum "${archive}" | awk '{print $1}')"
  if [[ "${actual,,}" != "${expected,,}" ]]; then
    die "checksum mismatch for ${archive}"
  fi
  log "SHA-256 checksum verified"
}

download_release() {
  require_network_tools
  local archive_name
  if [[ -n "${ASSET_NAME}" ]]; then
    ASSET="${ASSET_NAME}"
  else
    ASSET="localmedia-unlock-linux-${ARCH}.tar.gz"
  fi
  archive_name="${WORK_DIR}/${ASSET}"
  local url
  url="$(download_url)"
  log "downloading ${url}"
  curl -fL --retry 3 --connect-timeout 15 --max-time 300 "${url}" -o "${archive_name}"
  verify_checksum "${archive_name}" "${WORK_DIR}"

  local extract_dir="${WORK_DIR}/release"
  mkdir -p "${extract_dir}"
  tar -xzf "${archive_name}" -C "${extract_dir}"
  local candidate
  candidate="$(find "${extract_dir}" -type f \( -name 'localmedia-unlock' -o -name 'localmedia-unlock-*' \) -print -quit)"
  [[ -n "${candidate}" ]] || die "release archive does not contain a localmedia-unlock binary"
  install -m 0755 "${candidate}" "${WORK_DIR}/localmedia-unlock"
}

build_from_source() {
  need_command go
  log "building from source at ${PROJECT_ROOT}"
  (
    cd "${PROJECT_ROOT}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${ARCH}" \
      go build -trimpath -ldflags="-s -w" -o "${WORK_DIR}/localmedia-unlock" .
  )
}

obtain_binary() {
  if [[ -n "${LOCALMEDIA_BIN}" ]]; then
    [[ -f "${LOCALMEDIA_BIN}" ]] || die "LOCALMEDIA_BIN does not exist: ${LOCALMEDIA_BIN}"
    install -m 0755 "${LOCALMEDIA_BIN}" "${WORK_DIR}/localmedia-unlock"
    log "using supplied binary: ${LOCALMEDIA_BIN}"
    return 0
  fi

  if [[ -z "${REPO}" ]]; then
    log "REPO is not configured; falling back to source build"
    build_from_source
    return 0
  fi
  if ! resolve_version; then
    log "release lookup failed; falling back to source build"
    build_from_source
    return 0
  fi
  if ! download_release; then
    log "release download failed; falling back to source build"
    rm -f -- "${WORK_DIR}"/*.tar.gz
    build_from_source
  fi
}

validate_binary() {
  local binary="${WORK_DIR}/localmedia-unlock"
  [[ -x "${binary}" ]] || die "downloaded/built binary is not executable"
  if "${binary}" version >/dev/null 2>&1; then
    log "binary version: $("${binary}" version 2>/dev/null | head -n 1)"
  else
    log "warning: 'version' command did not run successfully; continuing"
  fi
}

run_as_service() {
  if command -v runuser >/dev/null 2>&1; then
    runuser -u "${SERVICE_USER}" -- "$@"
  elif command -v setpriv >/dev/null 2>&1; then
    setpriv --reuid "${SERVICE_USER}" --regid "${SERVICE_GROUP}" \
      --clear-groups -- "$@"
  elif command -v su >/dev/null 2>&1; then
    su -s /bin/sh -c "$(printf '%q ' "$@")" "${SERVICE_USER}"
  else
    die "no runuser/setpriv/su available to initialize configuration"
  fi
}

port_is_busy() {
  local address="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -lntH 2>/dev/null | awk '{print $4}' | grep -Fq "${address}"
  elif command -v nc >/dev/null 2>&1; then
    nc -z 127.0.0.1 "${address##*:}" >/dev/null 2>&1
  else
    return 1
  fi
}

ensure_service_account() {
  if ! getent group "${SERVICE_GROUP}" >/dev/null 2>&1; then
    groupadd --system "${SERVICE_GROUP}"
    CREATED_GROUP=1
    log "created group ${SERVICE_GROUP}"
  fi
  if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    local nologin
    nologin="$(command -v nologin || printf '/usr/sbin/nologin')"
    useradd --system --gid "${SERVICE_GROUP}" --home-dir "${DATA_DIR}" \
      --shell "${nologin}" --no-create-home "${SERVICE_USER}"
    CREATED_USER=1
    log "created system user ${SERVICE_USER}"
  fi
}

install_directories() {
  install -d -m 0755 "${PREFIX}" "${PREFIX}/bin"
  install -d -m 0755 "${BIN_DIR}"
  # The directory contains the token-bearing config, so it is not world
  # readable. The service owns the directory so it can atomically replace the
  # 0600 file when settings are saved from the Web UI.
  install -d -m 0750 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" "${CONFIG_DIR}"
  install -d -m 0750 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" "${DATA_DIR}"
}

install_binary() {
  install -m 0755 "${WORK_DIR}/localmedia-unlock" "${BIN_PATH}"
  if [[ "${BIN_PATH}" != "${PREFIX}/bin/localmedia-unlock" ]]; then
    install -m 0755 "${WORK_DIR}/localmedia-unlock" "${PREFIX}/bin/localmedia-unlock"
  fi
  if ! run_as_service "${BIN_PATH}" version >/dev/null 2>&1; then
    die "${SERVICE_USER} cannot execute ${BIN_PATH}; ensure every parent directory is traversable (for example: chmod o+x on the parent path). The systemd service uses this exact path."
  fi
  log "installed ${BIN_PATH}"
}

render_config() {
  if [[ -e "${CONFIG_PATH}" ]]; then
    # Never rewrite a user's token or routes during an upgrade.
    chown "${SERVICE_USER}":"${SERVICE_GROUP}" "${CONFIG_PATH}" "${CONFIG_DIR}"
    chmod 0750 "${CONFIG_DIR}"
    chmod 0600 "${CONFIG_PATH}"
    log "kept existing configuration: ${CONFIG_PATH}"
    return 0
  fi

  if port_is_busy "${INIT_LISTEN}"; then
    die "${INIT_LISTEN} is already in use; set INIT_LISTEN to a free loopback port"
  fi

  # A missing config is created by the real serve process, which expands a
  # complete validated default and generates the admin/proxy secrets. Do not
  # hand-write a partial JSON object: it would fail validation.
  local init_log="${WORK_DIR}/init.log"
  local init_pid=""
  run_as_service "${BIN_PATH}" serve \
    --config "${CONFIG_PATH}" \
    --data-dir "${DATA_DIR}" \
    --listen "${INIT_LISTEN}" >"${init_log}" 2>&1 &
  init_pid=$!

  local attempt
  for attempt in $(seq 1 60); do
    if grep -q '"adminToken"' "${CONFIG_PATH}" 2>/dev/null; then
      break
    fi
    if ! kill -0 "${init_pid}" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done
  kill "${init_pid}" 2>/dev/null || true
  wait "${init_pid}" 2>/dev/null || true
  chown "${SERVICE_USER}":"${SERVICE_GROUP}" "${CONFIG_DIR}" "${DATA_DIR}"
  chmod 0750 "${CONFIG_DIR}" "${DATA_DIR}"

  if [[ ! -s "${CONFIG_PATH}" ]] || ! grep -q '"adminToken"' "${CONFIG_PATH}"; then
    cat "${init_log}" >&2 || true
    die "could not initialize ${CONFIG_PATH}; inspect the binary's serve output"
  fi

  chown "${SERVICE_USER}":"${SERVICE_GROUP}" "${CONFIG_PATH}"
  chmod 0600 "${CONFIG_PATH}"
  log "created configuration: ${CONFIG_PATH}"
}

show_token() {
  [[ -r "${CONFIG_PATH}" ]] || return 0
  if [[ "${SKIP_SYSTEMD}" != "1" && "${NO_START}" != "1" ]]; then
    local attempt
    for attempt in $(seq 1 20); do
      if grep -q '"adminToken"' "${CONFIG_PATH}" 2>/dev/null; then
        break
      fi
      sleep 0.5
    done
  fi
  local token=""
  token="$(sed -n 's/^[[:space:]]*"adminToken"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${CONFIG_PATH}" | head -n 1)"
  if [[ -n "${token}" ]]; then
    printf '\nManagement token (also stored in %s):\n  %s\n\n' "${CONFIG_PATH}" "${token}"
  else
    printf '\nManagement token: generated on first start; read %s after the service starts.\n\n' "${CONFIG_PATH}"
  fi
}

install_env_example() {
  if [[ ! -e "${ENV_PATH}" ]]; then
    if [[ -f "${PROJECT_ROOT}/deploy/localmedia-unlock.env.example" ]]; then
      install -m 0600 -o root -g root \
        "${PROJECT_ROOT}/deploy/localmedia-unlock.env.example" "${ENV_PATH}"
    else
      : >"${ENV_PATH}"
      chmod 0600 "${ENV_PATH}"
    fi
    log "installed environment example: ${ENV_PATH}"
  else
    log "kept existing environment file: ${ENV_PATH}"
  fi
}

install_unit() {
  [[ "${SKIP_SYSTEMD}" == "1" ]] && return 0
  need_command systemctl
  install -d -m 0755 /etc/systemd/system

  local unit_tmp="${WORK_DIR}/localmedia-unlock.service"
  if [[ -f "${PROJECT_ROOT}/deploy/localmedia-unlock.service" ]]; then
    sed \
      -e "s#^ExecStart=.*#ExecStart=${BIN_PATH} serve --config ${CONFIG_PATH}#" \
      -e "s#^WorkingDirectory=.*#WorkingDirectory=${DATA_DIR}#" \
      "${PROJECT_ROOT}/deploy/localmedia-unlock.service" >"${unit_tmp}"
  else
    cat >"${unit_tmp}" <<EOF
[Unit]
Description=LocalMedia Unlock streaming proxy controller
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_GROUP}
ExecStart=${BIN_PATH} serve --config ${CONFIG_PATH}
WorkingDirectory=${DATA_DIR}
EnvironmentFile=-${ENV_PATH}
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR} ${CONFIG_DIR}
PrivateTmp=true
CapabilityBoundingSet=
AmbientCapabilities=

[Install]
WantedBy=multi-user.target
EOF
  fi
  # Keep the user/group and path substitutions consistent with the variables
  # used by this invocation.
  sed -i \
    -e "s#^User=.*#User=${SERVICE_USER}#" \
    -e "s#^Group=.*#Group=${SERVICE_GROUP}#" \
    -e "s#^EnvironmentFile=-.*#EnvironmentFile=-${ENV_PATH}#" \
    -e "s#^ReadWritePaths=.*#ReadWritePaths=${DATA_DIR} ${CONFIG_DIR}#" \
    "${unit_tmp}"
  install -m 0644 "${unit_tmp}" "${UNIT_PATH}"
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}.service" >/dev/null
  log "installed and enabled ${UNIT_PATH}"
}

start_service() {
  [[ "${SKIP_SYSTEMD}" == "1" || "${NO_START}" == "1" ]] && return 0
  systemctl restart "${SERVICE_NAME}.service"
  log "service started: ${SERVICE_NAME}"
}

main() {
  require_root

  [[ -z "${PREFIX}" || "${PREFIX}" == "/" ]] && die "unsafe PREFIX: ${PREFIX}"
  [[ -z "${BIN_DIR}" || "${BIN_DIR}" == "/" ]] && die "unsafe BIN_DIR: ${BIN_DIR}"
  [[ -z "${CONFIG_DIR}" || "${CONFIG_DIR}" == "/" ]] && die "unsafe CONFIG_DIR: ${CONFIG_DIR}"
  [[ -z "${DATA_DIR}" || "${DATA_DIR}" == "/" ]] && die "unsafe DATA_DIR: ${DATA_DIR}"
  [[ "${SERVICE_USER}" == "root" || "${SERVICE_GROUP}" == "root" ]] &&
    die "refusing to run the service as root"

  if [[ "${SKIP_SYSTEMD}" != "1" ]] && [[ -e "${UNIT_PATH}" ]]; then
    log "existing ${UNIT_PATH} will be replaced (configuration and data remain intact)"
  fi

  detect_arch

  WORK_DIR="$(mktemp -d)"
  chmod 0700 "${WORK_DIR}"

  obtain_binary
  validate_binary
  ensure_service_account
  install_directories
  install_binary
  render_config
  install_env_example
  install_unit
  start_service
  CREATED_USER=0
  CREATED_GROUP=0

  show_token
  log "installed. Web UI: http://127.0.0.1:9090"
  if [[ "${SKIP_SYSTEMD}" != "1" ]]; then
    log "logs: journalctl -u ${SERVICE_NAME} -f"
  fi
}

main "$@"
