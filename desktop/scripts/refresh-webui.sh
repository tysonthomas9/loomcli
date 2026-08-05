#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${DESKTOP_DIR}/.." && pwd)"

WEBUI_FRONTEND_DIR="${REPO_ROOT}/internal/webui/frontend"
WEBUI_DIST_DIR="${WEBUI_FRONTEND_DIR}/dist"
RESOURCE_DIR="${DESKTOP_DIR}/src-tauri/resources/webui"
APP_BUNDLE="${APP_BUNDLE:-${DESKTOP_DIR}/src-tauri/target/release/bundle/macos/Loom Agents.app}"
APP_WEBUI_DIR="${APP_BUNDLE}/Contents/Resources/webui"

RESTART=1
if [[ "${1:-}" == "--no-restart" ]]; then
  RESTART=0
fi

echo "[desktop] building web UI"
(
  cd "${WEBUI_FRONTEND_DIR}"
  npm run build
)

if [[ ! -f "${WEBUI_DIST_DIR}/index.html" ]]; then
  echo "web UI dist is missing at ${WEBUI_DIST_DIR}" >&2
  exit 1
fi

echo "[desktop] syncing web UI resources"
mkdir -p "${RESOURCE_DIR}"
rsync -a --delete "${WEBUI_DIST_DIR}/" "${RESOURCE_DIR}/"

if [[ -d "${APP_BUNDLE}" ]]; then
  echo "[desktop] syncing packaged app web UI"
  mkdir -p "${APP_WEBUI_DIR}"
  rsync -a --delete "${WEBUI_DIST_DIR}/" "${APP_WEBUI_DIR}/"
  "${SCRIPT_DIR}/reseal-local-app.sh" "${APP_BUNDLE}"
else
  echo "[desktop] packaged app not found at ${APP_BUNDLE}; skipping app bundle sync"
  RESTART=0
fi

if [[ "${RESTART}" == "1" ]]; then
  echo "[desktop] restarting Loom Agents.app"
  osascript -e 'tell application "Loom Agents" to quit' >/dev/null 2>&1 || true
  sleep 2
  open "${APP_BUNDLE}"
fi

echo "[desktop] web UI refresh complete"
