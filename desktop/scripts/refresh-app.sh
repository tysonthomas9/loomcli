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
  local candidates=()
  while IFS= read -r pid; do
    candidates+=("${pid}")
  done < <(pgrep -f "${pattern}" 2>/dev/null || true)

  local terminal_hosts=()
  if [[ "${name}" == "loom" ]]; then
    local pid
    if (( ${#candidates[@]} > 0 )); then
      for pid in "${candidates[@]}"; do
        local command
        command="$(ps -p "${pid}" -o command= 2>/dev/null || true)"
        if [[ "${command}" == *" local "* && "${command}" == *" terminal-host"* ]]; then
          terminal_hosts+=("${pid}")
        fi
      done
    fi
  fi

  local pids=()
  local pid
  if (( ${#candidates[@]} > 0 )); then
    for pid in "${candidates[@]}"; do
      local command
      command="$(ps -p "${pid}" -o command= 2>/dev/null || true)"
      if (( ${#terminal_hosts[@]} > 0 )) && is_pid_or_descendant_of_any "${pid}" "${terminal_hosts[@]}"; then
        echo "[desktop] preserving terminal-host process tree pid ${pid}"
        continue
      fi
      pids+=("${pid}")
    done
  fi

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

is_pid_or_descendant_of_any() {
  local pid="$1"
  shift
  local root
  for root in "$@"; do
    if is_pid_or_descendant_of "${pid}" "${root}"; then
      return 0
    fi
  done
  return 1
}

is_pid_or_descendant_of() {
  local pid="$1"
  local root="$2"
  while [[ -n "${pid}" && "${pid}" != "0" && "${pid}" != "1" ]]; do
    if [[ "${pid}" == "${root}" ]]; then
      return 0
    fi
    pid="$(ps -p "${pid}" -o ppid= 2>/dev/null | tr -d '[:space:]' || true)"
  done
  return 1
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
