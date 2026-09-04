#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${DESKTOP_DIR}/.." && pwd)"
BIN_DIR="${DESKTOP_DIR}/src-tauri/binaries"
FLEET_DB_REPO="${FLEET_DB_REPO:-${REPO_ROOT}/../fleet-db}"
FLUE_REPO="${FLUE_REPO:-${REPO_ROOT}/../flue}"
WEBUI_FRONTEND_DIR="${REPO_ROOT}/internal/webui/frontend"
WEBUI_DIST_DIR="${WEBUI_FRONTEND_DIR}/dist"
WEBUI_RESOURCE_DIR="${DESKTOP_DIR}/src-tauri/resources/webui"
BUILTIN_WORKFLOW_RESOURCE_DIR="${DESKTOP_DIR}/src-tauri/resources/workflows/epic-runner"

if ! command -v rustc >/dev/null 2>&1; then
  echo "rustc is required to prepare the Tauri sidecar" >&2
  exit 127
fi

TARGET_TRIPLE="$(rustc -vV | awk '/^host:/ { print $2 }')"
if [ -z "${TARGET_TRIPLE}" ]; then
  echo "failed to determine Rust host target triple" >&2
  exit 1
fi

mkdir -p "${BIN_DIR}"

if command -v npm >/dev/null 2>&1; then
  echo "[desktop] building web UI assets"
  (
    cd "${WEBUI_FRONTEND_DIR}"
    npm run build
  )
else
  echo "[desktop] warning: npm not found; using existing web UI dist" >&2
fi

if [ ! -f "${WEBUI_DIST_DIR}/index.html" ]; then
  echo "web UI dist is missing at ${WEBUI_DIST_DIR}; run npm install in internal/webui/frontend" >&2
  exit 1
fi
mkdir -p "${WEBUI_RESOURCE_DIR}"
cp -R "${WEBUI_DIST_DIR}/." "${WEBUI_RESOURCE_DIR}/"

if [ ! -f "${FLUE_REPO}/packages/cli/bin/flue.mjs" ]; then
  echo "Flue CLI is missing at ${FLUE_REPO}; build the pinned sibling Flue checkout or set FLUE_REPO" >&2
  exit 1
fi
echo "[desktop] building packaged epic-runner workflow"
BUILTIN_DIST_DEST="${BUILTIN_WORKFLOW_RESOURCE_DIR}" \
  FLUE_REPO="${FLUE_REPO}" \
  "${REPO_ROOT}/scripts/rebuild-builtin-bundle.sh"
# Source maps are developer diagnostics, not runtime dependencies, and add
# tens of megabytes to every signed desktop package.
find "${BUILTIN_WORKFLOW_RESOURCE_DIR}" -name '*.map' -delete

BUILD="${BUILD:-$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
OUT="${BIN_DIR}/loom-${TARGET_TRIPLE}"
FLEET_OUT="${BIN_DIR}/fleet-db-${TARGET_TRIPLE}"

echo "[desktop] building loom sidecar: ${OUT}"
(
  cd "${REPO_ROOT}"
  go build \
    -ldflags="-X github.com/tysonthomas9/loomcli/internal/cli.Build=${BUILD}" \
    -o "${OUT}" \
    ./cmd/loom
)
chmod +x "${OUT}"

if [ -d "${FLEET_DB_REPO}" ]; then
  echo "[desktop] building fleet-db sidecar: ${FLEET_OUT}"
  (
    cd "${FLEET_DB_REPO}"
    go build \
      -ldflags="-X main.commit=${BUILD}" \
      -o "${FLEET_OUT}" \
      ./cmd/fleet-db
  )
  chmod +x "${FLEET_OUT}"
else
  echo "[desktop] warning: fleet-db repo not found at ${FLEET_DB_REPO}; local runtime will need FLEET_DB_BIN" >&2
fi
