#!/usr/bin/env bash
# Start a real localmedia-unlock process in a temporary directory and verify
# the health endpoint and management API. No proxy kernel is required.
#
# Usage:
#   bash scripts/smoke.sh [path-to-binary]
#   LOCALMEDIA_SMOKE_BINARY=/path/to/binary bash scripts/smoke.sh
#   # optional: override the temporary API port and test token
#   LOCALMEDIA_SMOKE_PORT=19090 LOCALMEDIA_SMOKE_TOKEN=... bash scripts/smoke.sh

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
BINARY="${1:-${LOCALMEDIA_SMOKE_BINARY:-${ROOT_DIR}/bin/localmedia-unlock}}"
PORT="${LOCALMEDIA_SMOKE_PORT:-19090}"
BASE_URL="http://127.0.0.1:${PORT}"
TMP_DIR=""
PID=""
SMOKE_TOKEN="${LOCALMEDIA_SMOKE_TOKEN:-smoke-test-admin-token}"

log() {
  printf '[smoke] %s\n' "$*"
}

die() {
  printf '[smoke] ERROR: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "${PID}" ]] && kill -0 "${PID}" 2>/dev/null; then
    kill "${PID}" 2>/dev/null || true
    wait "${PID}" 2>/dev/null || true
  fi
  if [[ -n "${TMP_DIR}" && -d "${TMP_DIR}" ]]; then
    rm -rf -- "${TMP_DIR}"
  fi
}
trap cleanup EXIT

need_curl() {
  command -v curl >/dev/null 2>&1 || die "curl is required"
}

request() {
  local method="$1"
  local path="$2"
  local output="$3"
  local token="${4:-}"
  local body="${5:-}"
  local args=(-sS -o "${output}" -w '%{http_code}' -X "${method}")
  if [[ -n "${token}" ]]; then
    args+=(-H "Authorization: Bearer ${token}")
  fi
  if [[ -n "${body}" ]]; then
    args+=(-H 'Content-Type: application/json' --data "${body}")
  fi
  curl "${args[@]}" "${BASE_URL}${path}"
}

expect_status() {
  local actual="$1"
  local expected="$2"
  local label="$3"
  local output="$4"
  if [[ "${actual}" != "${expected}" ]]; then
    printf '%s\n' "--- response body ---" >&2
    cat "${output}" >&2 || true
    die "${label}: expected HTTP ${expected}, got ${actual}"
  fi
  log "${label}: HTTP ${actual}"
}

main() {
  need_curl
  [[ -x "${BINARY}" ]] || die "binary not executable: ${BINARY}"
  TMP_DIR="$(mktemp -d)"
  local config="${TMP_DIR}/config.json"
  local log_file="${TMP_DIR}/server.log"

  "${BINARY}" serve --config "${config}" --data-dir "${TMP_DIR}" \
    --listen "127.0.0.1:${PORT}" --token "${SMOKE_TOKEN}" \
    >"${log_file}" 2>&1 &
  PID=$!

  local attempt
  for attempt in $(seq 1 100); do
    if ! kill -0 "${PID}" 2>/dev/null; then
      cat "${log_file}" >&2 || true
      die "server exited before becoming healthy"
    fi
    if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1 || {
    cat "${log_file}" >&2 || true
    die "health check timed out"
  }
  log "health check passed"

  local token="${SMOKE_TOKEN}"
  local body="${TMP_DIR}/body"
  local code
  for attempt in $(seq 1 50); do
    code="$(request GET /api/v1/status "${body}" "${token}")"
    if [[ "${code}" == "200" ]]; then
      break
    fi
    sleep 0.1
  done
  code="$(request GET /api/v1/config "${body}" "")"
  expect_status "${code}" "401" "unauthenticated config request" "${body}"

  code="$(request GET /api/v1/config "${body}" "${token}")"
  expect_status "${code}" "200" "authenticated config request" "${body}"
  grep -q '"settings"' "${body}" || die "config response has no settings object"
  if grep -q '"adminToken"' "${body}"; then
    die "config response leaked adminToken"
  fi
  grep -q '"subscriptionToken"' "${body}" || die "authenticated config response is missing subscriptionToken"
  log "config omitted adminToken and returned the authenticated subscription token"

  code="$(request GET /api/v1/status "${body}" "${token}")"
  expect_status "${code}" "200" "status request" "${body}"

  code="$(request GET /api/v1/auth/status "${body}" "")"
  expect_status "${code}" "200" "auth status request" "${body}"
  grep -q '"authRequired":true' "${body}" || die "auth status did not report required authentication"

  log "all checks passed"
}

main "$@"
