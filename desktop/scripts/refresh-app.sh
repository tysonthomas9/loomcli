#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
APP_BUNDLE="${APP_BUNDLE:-${DESKTOP_DIR}/src-tauri/target/release/bundle/macos/Loom Agents.app}"

RESTART=1
if [[ "${1:-}" == "--no-restart" ]]; then
  RESTART=0
fi

stop_sidecar() {
  local name="$1"
  local pattern="${APP_BUNDLE}/Contents/MacOS/${name}( |$)"
  local pids=()
  while IFS= read -r pid; do
    local command
    command="$(ps -p "${pid}" -o command= 2>/dev/null || true)"
    if [[ "${name}" == "loom" && "${command}" == *" local "* && "${command}" == *" terminal-host"* ]]; then
      echo "[desktop] preserving terminal-host pid ${pid}"
      continue
    fi
    pids+=("${pid}")
  done < <(pgrep -f "${pattern}" 2>/dev/null || true)

  if (( ${#pids[@]} > 0 )); then
    echo "[desktop] stopping ${name} sidecar"
    kill -TERM "${pids[@]}" >/dev/null 2>&1 || true
    sleep 1
    local live=()
    for pid in "${pids[@]}"; do
      if kill -0 "${pid}" >/dev/null 2>&1; then
        live+=("${pid}")
      fi
    done
    if (( ${#live[@]} > 0 )); then
      kill -KILL "${live[@]}" >/dev/null 2>&1 || true
    fi
  fi
}

if [[ "${RESTART}" == "1" && -d "${APP_BUNDLE}" ]]; then
  echo "[desktop] stopping Loom Agents.app"
  osascript -e 'tell application "Loom Agents" to quit' >/dev/null 2>&1 || true
  sleep 2
  stop_sidecar "loom"
  stop_sidecar "fleet-db"
fi

echo "[desktop] building app bundle"
(
  cd "${DESKTOP_DIR}"
  npm run build
)

if [[ ! -d "${APP_BUNDLE}" ]]; then
  echo "app bundle was not produced at ${APP_BUNDLE}" >&2
  exit 1
fi

if [[ "${RESTART}" == "1" ]]; then
  echo "[desktop] starting Loom Agents.app"
  open "${APP_BUNDLE}"
fi

echo "[desktop] app refresh complete"
