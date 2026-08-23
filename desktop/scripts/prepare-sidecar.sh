#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${DESKTOP_DIR}/.." && pwd)"
BIN_DIR="${DESKTOP_DIR}/src-tauri/binaries"
FLEET_DB_REPO="${FLEET_DB_REPO:-${REPO_ROOT}/../fleet-db}"
WEBUI_FRONTEND_DIR="${REPO_ROOT}/internal/webui/frontend"
WEBUI_DIST_DIR="${WEBUI_FRONTEND_DIR}/dist"
WEBUI_RESOURCE_DIR="${DESKTOP_DIR}/src-tauri/resources/webui"
BUILTIN_RESOURCE_DIR="${DESKTOP_DIR}/src-tauri/resources/builtin-workflows"

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

# Node is always embedded, including when artifact generation is skipped. This
# keeps the escape hatch useful for a cached/prebuilt artifact build without
# allowing a desktop bundle to silently depend on PATH Node.
# shellcheck source=desktop/scripts/prepare-node-runtime.sh
source "${SCRIPT_DIR}/prepare-node-runtime.sh"

rm -rf "${BUILTIN_RESOURCE_DIR}"
mkdir -p "${BUILTIN_RESOURCE_DIR}"

BUILTINS=(epic-runner github-review-agent)
if [ "${LOOM_SKIP_BUILTIN_ARTIFACTS:-0}" = "1" ]; then
  echo "[desktop] warning: LOOM_SKIP_BUILTIN_ARTIFACTS=1; built-in artifacts are not packaged" >&2
  INDEX_DIGEST=""
else
  FLUE_REPO="${FLUE_REPO:-${REPO_ROOT}/../flue}"
  for name in "${BUILTINS[@]}"; do
    dist="${DESKTOP_DIR}/.loom-builtin-dist/${name}"
    mkdir -p "${dist}"
    if [ -n "${LOOM_BUILTIN_PREBUILT_DIST_DIR:-}" ]; then
      prebuilt="${LOOM_BUILTIN_PREBUILT_DIST_DIR}/${name}"
      [ -f "${prebuilt}/server.mjs" ] || { echo "prebuilt dist missing for ${name}: ${prebuilt}" >&2; exit 1; }
      cp -R "${prebuilt}/." "${dist}/"
    else
      BUILTIN_DIST_DEST="${dist}" FLUE_REPO="${FLUE_REPO}" \
        "${REPO_ROOT}/scripts/rebuild-builtin-bundle.sh" "${name}"
    fi
    (cd "${REPO_ROOT}" && go run ./cmd/loom workflow package-builtin "${name}" \
      --dist "${dist}" --out "${BUILTIN_RESOURCE_DIR}" --loom-sdk "${REPO_ROOT}/sdk" \
      --require-all --json > "${DESKTOP_DIR}/.loom-builtin-${name}.json")
    "${NODE_SIDECAR}" "${REPO_ROOT}/desktop/scripts/smoke-load-server.mjs" \
      "${BUILTIN_RESOURCE_DIR}/${name}/dist/server.mjs" "${name}"
  done
  INDEX_DIGEST="$(node -e 'const fs=require("fs"),c=require("crypto");const b=fs.readFileSync(process.argv[1]);console.log("sha256:"+c.createHash("sha256").update(b).digest("hex"))' "${BUILTIN_RESOURCE_DIR}/index.json")"
fi

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

BUILD="${BUILD:-$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
OUT="${BIN_DIR}/loom-${TARGET_TRIPLE}"
FLEET_OUT="${BIN_DIR}/fleet-db-${TARGET_TRIPLE}"

echo "[desktop] building loom sidecar: ${OUT}"
(
  cd "${REPO_ROOT}"
  go build \
    -ldflags="-X github.com/tysonthomas9/loomcli/internal/cli.Build=${BUILD} -X github.com/tysonthomas9/loomcli/internal/workflows/packaged.ExpectedIndexDigest=${INDEX_DIGEST}" \
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

rm -rf "${DESKTOP_DIR}/.loom-builtin-dist" "${DESKTOP_DIR}"/.loom-builtin-*.json
