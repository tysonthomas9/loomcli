#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
APP_BUNDLE="${APP_BUNDLE:-${DESKTOP_DIR}/src-tauri/target/release/bundle/macos/Superfactory.app}"

RESTART=1
if [[ "${1:-}" == "--no-restart" ]]; then
  RESTART=0
fi

stop_sidecar() {
  local name="$1"
  local pattern="${APP_BUNDLE}/Contents/MacOS/${name}( |$)"
  if pgrep -f "${pattern}" >/dev/null 2>&1; then
    echo "[desktop] stopping ${name} sidecar"
    pkill -TERM -f "${pattern}" >/dev/null 2>&1 || true
    sleep 1
    pkill -KILL -f "${pattern}" >/dev/null 2>&1 || true
  fi
}

if [[ "${RESTART}" == "1" && -d "${APP_BUNDLE}" ]]; then
  echo "[desktop] stopping Superfactory.app"
  osascript -e 'tell application "Superfactory" to quit' >/dev/null 2>&1 || true
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
  echo "[desktop] starting Superfactory.app"
  open "${APP_BUNDLE}"
fi

echo "[desktop] app refresh complete"
