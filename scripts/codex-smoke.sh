#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." >/dev/null 2>&1 && pwd)"
GO_BIN="${GO_BIN:-${HOME}/.local/go/bin/go}"
CODEX_BIN="${CODEX_BIN:-codex}"
THREADMARK_ROOT="${THREADMARK_ROOT:-}"
THREADMARK_WORK="${THREADMARK_WORK:-}"
THREADMARK_SOCKET="${THREADMARK_SOCKET:-}"
DEBOUNCE_WINDOW="${THREADMARK_TEST_DEBOUNCE_WINDOW:-1s}"
TICK_INTERVAL="${THREADMARK_TEST_TICK_INTERVAL:-0}"
CLEANUP=0
NO_BUILD=0

usage() {
  cat <<'USAGE'
usage: scripts/codex-smoke.sh [options]

Builds Threadmark, creates an isolated temp project/root, installs Codex
project hooks there, launches Codex with Threadmark env vars, then checks the
daemon log after Codex exits.

This script uses your normal authenticated Codex environment. It does not set
CODEX_HOME because an isolated CODEX_HOME starts at Codex sign-in.

Options:
  --root DIR       Threadmark root to use. Default: temp dir.
  --work DIR       Temp project/work dir to use. Default: temp dir.
  --cleanup        Remove the temp root/work after a successful run.
  --no-build       Reuse existing bin/threadmark and bin/threadmarkd.
  -h, --help       Show this help.

Inside Codex:
  1. Run /hooks and trust the Threadmark project hooks.
  2. Send: Threadmark interactive Codex smoke test. Please acknowledge and do not edit files.
  3. Run /compact.
  4. Exit Codex.
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --root)
      shift
      THREADMARK_ROOT="${1:?missing --root value}"
      ;;
    --work)
      shift
      THREADMARK_WORK="${1:?missing --work value}"
      ;;
    --cleanup)
      CLEANUP=1
      ;;
    --no-build)
      NO_BUILD=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

THREADMARK_BIN="${ROOT_DIR}/bin/threadmark"
THREADMARKD_BIN="${ROOT_DIR}/bin/threadmarkd"

say() {
  printf '\n==> %s\n' "$*" >&2
}

require_file() {
  if [ ! -e "$1" ]; then
    echo "missing expected file: $1" >&2
    return 1
  fi
}

stop_daemon() {
  pkill -f "${THREADMARKD_BIN}.*-root ${THREADMARK_ROOT}" >/dev/null 2>&1 || true
}

build_binaries() {
  if [ "${NO_BUILD}" -eq 1 ]; then
    say "Skipping build"
    return
  fi
  say "Building local binaries"
  mkdir -p "${ROOT_DIR}/bin"
  "${GO_BIN}" build -o "${THREADMARK_BIN}" ./cmd/threadmark
  "${GO_BIN}" build -o "${THREADMARKD_BIN}" ./cmd/threadmarkd
}

prepare_dirs() {
  if [ -z "${THREADMARK_ROOT}" ]; then
    THREADMARK_ROOT="$(mktemp -d)"
  else
    mkdir -p "${THREADMARK_ROOT}"
  fi
  if [ -z "${THREADMARK_WORK}" ]; then
    THREADMARK_WORK="$(mktemp -d)"
  else
    mkdir -p "${THREADMARK_WORK}"
  fi
  if [ -z "${THREADMARK_SOCKET}" ]; then
    THREADMARK_SOCKET="${THREADMARK_ROOT}/daemon.sock"
  fi
  git -C "${THREADMARK_WORK}" init -q
}

install_hooks() {
  say "Installing Codex project hooks"
  "${THREADMARK_BIN}" init codex \
    --hooks-file "${THREADMARK_WORK}/.codex/hooks.json" \
    --hook-script "${ROOT_DIR}/adapters/codex/hooks/threadmark-hook.sh"
}

launch_codex() {
  say "Launching Codex for interactive smoke"
  cat <<EOF
Temp state:
  THREADMARK_ROOT=${THREADMARK_ROOT}
  THREADMARK_WORK=${THREADMARK_WORK}
  THREADMARK_SOCKET=${THREADMARK_SOCKET}

Inside Codex:
  1. Run /hooks and trust the Threadmark project hooks.
  2. Send:
     Threadmark interactive Codex smoke test. Please acknowledge and do not edit files.
  3. Run /compact.
  4. Exit Codex.

After Codex exits, this script will inspect:
  ${THREADMARK_ROOT}/daemon.log
EOF

  (
    cd "${THREADMARK_WORK}"
    env \
      THREADMARK_ROOT="${THREADMARK_ROOT}" \
      THREADMARK_SOCKET="${THREADMARK_SOCKET}" \
      THREADMARK_BIN="${THREADMARK_BIN}" \
      THREADMARKD_BIN="${THREADMARKD_BIN}" \
      THREADMARKD_ARGS="-no-journal -tick-interval ${TICK_INTERVAL} -debounce-window ${DEBOUNCE_WINDOW}" \
      "${CODEX_BIN}" --no-alt-screen
  )
}

check_log() {
  say "Checking Codex smoke log"
  local log_path="${THREADMARK_ROOT}/daemon.log"
  require_file "${log_path}" || {
    echo "Codex may not have run the trusted hooks. Re-run and make sure /hooks trusts the project hooks." >&2
    return 1
  }

  grep -q '"kind":"event.received"' "${log_path}"
  grep -q '"harness":"codex"' "${log_path}"
  grep -q '"event_kind":"text"' "${log_path}"
  grep -q '"trigger":"pre-compact"' "${log_path}"
  grep -q '"trigger":"pre-compact","reason":"pre-compact:manual"' "${log_path}"
  grep -q '"kind":"journal.skipped"' "${log_path}"

  say "Codex smoke passed"
  tail -n 40 "${log_path}"
}

cleanup_success() {
  stop_daemon
  if [ "${CLEANUP}" -eq 1 ]; then
    say "Cleaning up temp state"
    rm -rf "${THREADMARK_ROOT}" "${THREADMARK_WORK}"
  else
    cat <<EOF

Kept temp state for inspection:
  THREADMARK_ROOT=${THREADMARK_ROOT}
  THREADMARK_WORK=${THREADMARK_WORK}
EOF
  fi
}

main() {
  cd "${ROOT_DIR}"
  build_binaries
  prepare_dirs
  install_hooks
  launch_codex
  check_log
  cleanup_success
}

main "$@"
