#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." >/dev/null 2>&1 && pwd)"
GO_BIN="${GO_BIN:-${HOME}/.local/go/bin/go}"
THREADMARK_ROOT="${THREADMARK_ROOT:-${HOME}/.threadmark}"
DEBOUNCE_WINDOW="${THREADMARK_TEST_DEBOUNCE_WINDOW:-1s}"
TICK_INTERVAL="${THREADMARK_TEST_TICK_INTERVAL:-200ms}"
CLAUDE_BIN="${CLAUDE_BIN:-claude}"
INSTALL_SCOPE="project"
MODE="prepare"
RESET_ROOT=0
INSTALL_HOOKS=1
RUN_SYNTHETIC=1
LAUNCH_CLAUDE=0
LIVE_REFLECTOR=0

usage() {
  cat <<'USAGE'
usage: scripts/claude-smoke.sh [options]

Builds Threadmark, installs/prints Claude Code project hooks, runs synthetic
no-journal smoke tests, and optionally launches Claude Code with the right
environment for a live smoke test.

Options:
  --prepare            Build, install project hooks, run synthetic smoke. Default.
  --launch             Do --prepare, then launch Claude Code with no-journal env.
  --live-reflector     Launch Claude Code without THREADMARK_NO_JOURNAL.
  --print-settings     Print generated Claude Code settings and exit.
  --no-install         Do not modify .claude/settings.json.
  --no-synthetic       Skip synthetic hook/daemon smoke tests.
  --reset-root         Remove THREADMARK_ROOT before testing.
  --root DIR           Threadmark root. Default: ~/.threadmark.
  --scope SCOPE        Claude settings scope for install. Default: project.
  -h, --help           Show this help.

Recommended first real test:
  scripts/claude-smoke.sh --launch --reset-root

Inside Claude Code, send a tiny prompt, then exit:
  Please say "Threadmark smoke test started", then stop.
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --prepare)
      MODE="prepare"
      ;;
    --launch)
      MODE="launch"
      LAUNCH_CLAUDE=1
      ;;
    --live-reflector)
      MODE="live-reflector"
      LAUNCH_CLAUDE=1
      LIVE_REFLECTOR=1
      ;;
    --print-settings)
      MODE="print-settings"
      INSTALL_HOOKS=0
      RUN_SYNTHETIC=0
      ;;
    --no-install)
      INSTALL_HOOKS=0
      ;;
    --no-synthetic)
      RUN_SYNTHETIC=0
      ;;
    --reset-root)
      RESET_ROOT=1
      ;;
    --root)
      shift
      THREADMARK_ROOT="${1:?missing --root value}"
      ;;
    --scope)
      shift
      INSTALL_SCOPE="${1:?missing --scope value}"
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
SOCKET_PATH="${THREADMARK_ROOT}/daemon.sock"
DAEMON_ARGS="-debounce-window ${DEBOUNCE_WINDOW} -tick-interval ${TICK_INTERVAL}"

say() {
  if [ "${MODE}" = "print-settings" ]; then
    return
  fi
  printf '\n==> %s\n' "$*" >&2
}

require_file() {
  if [ ! -e "$1" ]; then
    echo "missing expected file: $1" >&2
    exit 1
  fi
}

stop_daemon() {
  pkill -f "${THREADMARKD_BIN}.*-root ${THREADMARK_ROOT}" >/dev/null 2>&1 || true
}

build_binaries() {
  say "Building local binaries"
  mkdir -p "${ROOT_DIR}/bin"
  "${GO_BIN}" build -o "${THREADMARK_BIN}" ./cmd/threadmark
  "${GO_BIN}" build -o "${THREADMARKD_BIN}" ./cmd/threadmarkd
  "${THREADMARK_BIN}" -h >/dev/null 2>&1
  "${THREADMARKD_BIN}" -h >/dev/null 2>&1
}

install_settings() {
  if [ "${INSTALL_HOOKS}" -eq 0 ]; then
    say "Skipping Claude Code settings install"
    return
  fi
  say "Installing Claude Code ${INSTALL_SCOPE} hooks"
  mkdir -p "${ROOT_DIR}/.claude"
  if [ -f "${ROOT_DIR}/.claude/settings.json" ]; then
    cp "${ROOT_DIR}/.claude/settings.json" "${ROOT_DIR}/.claude/settings.json.bak.$(date -u +%Y%m%dT%H%M%SZ)"
  fi
  "${THREADMARK_BIN}" init claude-code --scope "${INSTALL_SCOPE}"
}

print_settings() {
  "${THREADMARK_BIN}" init claude-code --scope "${INSTALL_SCOPE}" --print
}

run_hook() {
  local payload="$1"
  printf '%s' "${payload}" | env \
    THREADMARKD_BIN="${THREADMARKD_BIN}" \
    THREADMARK_NO_JOURNAL=true \
    THREADMARKD_ARGS="${DAEMON_ARGS}" \
    "${THREADMARK_BIN}" hook claude-code \
      --root "${THREADMARK_ROOT}" \
      --socket "${SOCKET_PATH}" \
      --strict \
      --timeout 4s
}

synthetic_smoke() {
  if [ "${RUN_SYNTHETIC}" -eq 0 ]; then
    say "Skipping synthetic smoke"
    return
  fi

  say "Running synthetic no-journal hook smoke"
  stop_daemon
  local project_dir
  project_dir="$(mktemp -d)"
  local transcript_path="${project_dir}/transcript.jsonl"
  local prompt_payload stop_payload
  prompt_payload="$(printf '{"session_id":"threadmark-smoke","transcript_path":"%s","cwd":"%s","permission_mode":"default","hook_event_name":"UserPromptSubmit","prompt":"Synthetic smoke prompt."}' "${transcript_path}" "${project_dir}")"
  stop_payload="$(printf '{"session_id":"threadmark-smoke","transcript_path":"%s","cwd":"%s","permission_mode":"default","hook_event_name":"Stop","last_assistant_message":"Synthetic smoke completed."}' "${transcript_path}" "${project_dir}")"

  run_hook "${prompt_payload}"
  run_hook "${stop_payload}"
  sleep 2

  require_file "${THREADMARK_ROOT}/daemon.log"
  grep -q '"kind":"event.received"' "${THREADMARK_ROOT}/daemon.log"
  grep -q '"kind":"trigger.candidate"' "${THREADMARK_ROOT}/daemon.log"
  grep -q '"kind":"trigger.fired"' "${THREADMARK_ROOT}/daemon.log"
  grep -q '"kind":"journal.skipped"' "${THREADMARK_ROOT}/daemon.log"
  say "Synthetic smoke passed"
  tail -n 12 "${THREADMARK_ROOT}/daemon.log"
  stop_daemon
  rm -rf "${project_dir}"
}

launch_claude() {
  say "Launching Claude Code for manual smoke"
  cat <<EOF
Environment:
  THREADMARK_BIN=${THREADMARK_BIN}
  THREADMARKD_BIN=${THREADMARKD_BIN}
  THREADMARK_ROOT=${THREADMARK_ROOT}
  THREADMARKD_ARGS=${DAEMON_ARGS}
  THREADMARK_NO_JOURNAL=$([ "${LIVE_REFLECTOR}" -eq 1 ] && echo "<unset>" || echo "true")

Inside Claude Code, send:
  Please say "Threadmark smoke test started", then stop.

After Claude exits, this script will check ${THREADMARK_ROOT}/daemon.log.
EOF

  if [ "${LIVE_REFLECTOR}" -eq 1 ]; then
    env \
      THREADMARK_BIN="${THREADMARK_BIN}" \
      THREADMARKD_BIN="${THREADMARKD_BIN}" \
      THREADMARK_ROOT="${THREADMARK_ROOT}" \
      THREADMARKD_ARGS="${DAEMON_ARGS}" \
      "${CLAUDE_BIN}"
  else
    env \
      THREADMARK_BIN="${THREADMARK_BIN}" \
      THREADMARKD_BIN="${THREADMARKD_BIN}" \
      THREADMARK_ROOT="${THREADMARK_ROOT}" \
      THREADMARK_NO_JOURNAL=true \
      THREADMARKD_ARGS="${DAEMON_ARGS}" \
      "${CLAUDE_BIN}"
  fi

  say "Checking live Claude Code smoke log"
  require_file "${THREADMARK_ROOT}/daemon.log"
  grep -q '"kind":"event.received"' "${THREADMARK_ROOT}/daemon.log"
  grep -q '"kind":"trigger.candidate"' "${THREADMARK_ROOT}/daemon.log"
  if [ "${LIVE_REFLECTOR}" -eq 1 ]; then
    grep -q '"kind":"journal.write_completed"' "${THREADMARK_ROOT}/daemon.log"
  else
    grep -q '"kind":"journal.skipped"' "${THREADMARK_ROOT}/daemon.log"
  fi
  tail -n 30 "${THREADMARK_ROOT}/daemon.log"
}

main() {
  cd "${ROOT_DIR}"
  build_binaries

  if [ "${MODE}" = "print-settings" ]; then
    print_settings
    exit 0
  fi

  if [ "${RESET_ROOT}" -eq 1 ]; then
    say "Resetting Threadmark root ${THREADMARK_ROOT}"
    stop_daemon
    rm -rf "${THREADMARK_ROOT}"
  fi

  install_settings
  synthetic_smoke

  if [ "${LAUNCH_CLAUDE}" -eq 1 ]; then
    launch_claude
  else
    say "Ready for manual Claude Code test"
    cat <<EOF
Run this when ready:

  THREADMARK_BIN="${THREADMARK_BIN}" \\
  THREADMARKD_BIN="${THREADMARKD_BIN}" \\
  THREADMARK_ROOT="${THREADMARK_ROOT}" \\
  THREADMARK_NO_JOURNAL=true \\
  THREADMARKD_ARGS="${DAEMON_ARGS}" \\
  ${CLAUDE_BIN}

Then check:
  tail -n 80 "${THREADMARK_ROOT}/daemon.log"
EOF
  fi
}

main "$@"
