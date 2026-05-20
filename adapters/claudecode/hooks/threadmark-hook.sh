#!/usr/bin/env bash
set -u

ARGS=(hook claude-code)
if [ -n "${THREADMARK_ROOT:-}" ]; then
  ARGS+=(--root "$THREADMARK_ROOT")
fi
if [ -n "${THREADMARK_SOCKET:-}" ]; then
  ARGS+=(--socket "$THREADMARK_SOCKET")
fi

if [ -n "${THREADMARK_BIN:-}" ]; then
  exec "$THREADMARK_BIN" "${ARGS[@]}" "$@"
fi

if command -v threadmark >/dev/null 2>&1; then
  exec threadmark "${ARGS[@]}" "$@"
fi

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../../.." >/dev/null 2>&1 && pwd)"

for candidate in "${REPO_ROOT}/bin/threadmark" "${REPO_ROOT}/threadmark"; do
  if [ -x "$candidate" ]; then
    exec "$candidate" "${ARGS[@]}" "$@"
  fi
done

LOG_DIR="${THREADMARK_ROOT:-${HOME}/.threadmark}"
mkdir -p "$LOG_DIR" 2>/dev/null || true
printf '%s threadmark binary not found\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "${LOG_DIR}/hook.log" 2>/dev/null || true
exit 0
