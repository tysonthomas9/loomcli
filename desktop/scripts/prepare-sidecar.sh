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
LICENSE_RESOURCE_DIR="${DESKTOP_DIR}/src-tauri/resources/licenses"
RUNTIME_RESOURCE_DIR="${DESKTOP_DIR}/src-tauri/resources/runtime"
PACKAGED_BUILTIN_ROOT="${REPO_ROOT}/internal/infra/workflowdistribution/builtin-dist"
PACKAGED_BUILTIN_WORKFLOWS=(
  "prompt-agent"
  "epic-runner"
  "github-review-agent"
  "bug-fix-agent"
  "review-loop-agent"
  "local-review-agent"
)
NODE_VERSION_FILE="${DESKTOP_DIR}/NODE_VERSION"
NODE_ENTITLEMENTS="${DESKTOP_DIR}/src-tauri/node-runtime.entitlements"

PINNED_NODE_VERSION="$(tr -d '[:space:]' < "${NODE_VERSION_FILE}")"
if [[ ! "${PINNED_NODE_VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid pinned Node.js version in ${NODE_VERSION_FILE}: ${PINNED_NODE_VERSION}" >&2
  exit 1
fi

if ! command -v rustc >/dev/null 2>&1; then
  echo "rustc is required to prepare the Tauri sidecar" >&2
  exit 127
fi

TARGET_TRIPLE="$(rustc -vV | awk '/^host:/ { print $2 }')"
if [ -z "${TARGET_TRIPLE}" ]; then
  echo "failed to determine Rust host target triple" >&2
  exit 1
fi

if ! command -v node >/dev/null 2>&1; then
  echo "Node.js ${PINNED_NODE_VERSION} is required to prepare the packaged runtime" >&2
  exit 127
fi
NODE_BIN="$(command -v node)"
ACTUAL_NODE_VERSION="$("${NODE_BIN}" --version)"
if [ "${ACTUAL_NODE_VERSION}" != "v${PINNED_NODE_VERSION}" ]; then
  echo "Node.js v${PINNED_NODE_VERSION} is required to prepare the packaged runtime; found ${ACTUAL_NODE_VERSION}" >&2
  exit 1
fi
NODE_BIN="$("${NODE_BIN}" -p 'require("node:fs").realpathSync(process.execPath)')"
NODE_LICENSE="$(dirname "$(dirname "${NODE_BIN}")")/LICENSE"
if [ ! -f "${NODE_LICENSE}" ]; then
  echo "Node.js license is missing at ${NODE_LICENSE}; refusing to package an incomplete runtime" >&2
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
# Vite content hashes change between builds. Clear the generated resource tree
# before copying so an older hashed bundle cannot remain alongside the current
# dist and ship in the application.
rm -rf "${WEBUI_RESOURCE_DIR}"
mkdir -p "${WEBUI_RESOURCE_DIR}"
cp -R "${WEBUI_DIST_DIR}/." "${WEBUI_RESOURCE_DIR}/"
mkdir -p "${LICENSE_RESOURCE_DIR}"
cp "${NODE_LICENSE}" "${LICENSE_RESOURCE_DIR}/node-LICENSE"

if [ -z "${FLUE_REPO:-}" ]; then
  for candidate in "${REPO_ROOT}/../flue" "${REPO_ROOT}/../../flue" "${REPO_ROOT}/flue"; do
    if [ -f "${candidate}/packages/cli/bin/flue.mjs" ]; then
      FLUE_REPO="${candidate}"
      break
    fi
  done
fi
if [ -z "${FLUE_REPO:-}" ] || [ ! -f "${FLUE_REPO}/packages/cli/bin/flue.mjs" ]; then
  echo "pinned Flue CLI is required to package the built-in workflows; set FLUE_REPO" >&2
  exit 1
fi

# The tagged Go embed includes every directory present under builtin-dist.
# Recreate that ignored tree so a developer's unrelated scratch bundle cannot
# leak into the signed sidecar and so every package build has the same payload.
rm -rf "${PACKAGED_BUILTIN_ROOT}"
mkdir -p "${PACKAGED_BUILTIN_ROOT}"
for builtin_workflow in "${PACKAGED_BUILTIN_WORKFLOWS[@]}"; do
  packaged_dist="${PACKAGED_BUILTIN_ROOT}/${builtin_workflow}/dist"
  echo "[desktop] building packaged ${builtin_workflow} workflow"
  FLUE_REPO="${FLUE_REPO}" \
  BUILTIN_WORKFLOW="${builtin_workflow}" \
  BUILTIN_DIST_DEST="${packaged_dist}" \
    "${REPO_ROOT}/scripts/rebuild-builtin-bundle.sh"
  # Source maps dominate the generated bundle size and are not used by the
  # product runtime. Keep them in scratch/diagnostic builds, but not in the
  # signed sidecar payload.
  find "${packaged_dist}" -type f -name '*.map' -delete
  if [ ! -f "${packaged_dist}/server.mjs" ] || [ ! -s "${packaged_dist}/source-digest.txt" ]; then
    echo "packaged ${builtin_workflow} bundle is incomplete at ${packaged_dist}" >&2
    exit 1
  fi
done

# The script and Go spec intentionally keep separate file lists. Compile the
# tagged embed and compare every required marker to SourceDigest(spec.Files) so
# a future spec change cannot silently ship a missing or stale generated
# bundle.
(
  cd "${REPO_ROOT}"
  go test -tags loom_packaged_builtins ./internal/infra/workflowdistribution/authoring \
    -run '^TestEmbeddedPackagedBuiltins' -count=1
)

BUILD="${BUILD:-$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
OUT="${BIN_DIR}/loom-${TARGET_TRIPLE}"
FLEET_OUT="${BIN_DIR}/fleet-db-${TARGET_TRIPLE}"
NODE_OUT="${RUNTIME_RESOURCE_DIR}/node"

echo "[desktop] copying pinned Node.js runtime: ${NODE_OUT}"
mkdir -p "${RUNTIME_RESOURCE_DIR}"
cp "${NODE_BIN}" "${NODE_OUT}"
chmod +x "${NODE_OUT}"
if [[ "${TARGET_TRIPLE}" == *-apple-darwin ]]; then
  if ! command -v codesign >/dev/null 2>&1; then
    echo "codesign is required to prepare the packaged macOS Node.js runtime" >&2
    exit 127
  fi
  if [[ ! -f "${NODE_ENTITLEMENTS}" ]]; then
    echo "Node.js runtime entitlements are missing at ${NODE_ENTITLEMENTS}" >&2
    exit 1
  fi
  NODE_SIGNING_IDENTITY="${APPLE_SIGNING_IDENTITY:--}"
  NODE_SIGN_ARGS=(--force --options runtime --entitlements "${NODE_ENTITLEMENTS}")
  if [[ "${NODE_SIGNING_IDENTITY}" != "-" ]]; then
    NODE_SIGN_ARGS+=(--timestamp)
  fi
  codesign "${NODE_SIGN_ARGS[@]}" --sign "${NODE_SIGNING_IDENTITY}" "${NODE_OUT}"
  codesign --verify --strict --verbose=2 "${NODE_OUT}"
fi
PACKAGED_NODE_VERSION="$("${NODE_OUT}" --version)"
if [ "${PACKAGED_NODE_VERSION}" != "v${PINNED_NODE_VERSION}" ]; then
  echo "packaged Node.js runtime failed its version smoke check: expected v${PINNED_NODE_VERSION}, found ${PACKAGED_NODE_VERSION}" >&2
  exit 1
fi
"${NODE_OUT}" -e 'if (new Function("return 42")() !== 42) process.exit(1)'

echo "[desktop] building loom sidecar: ${OUT}"
(
  cd "${REPO_ROOT}"
  go build \
    -tags loom_packaged_builtins \
    -ldflags="-X github.com/tysonthomas9/loomcli/internal/cli.Build=${BUILD}" \
    -o "${OUT}" \
    ./cmd/loom
)
chmod +x "${OUT}"

if [ -d "${FLEET_DB_REPO}" ]; then
  FLEET_BUILD="${FLEET_BUILD:-$(git -C "${FLEET_DB_REPO}" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
  echo "[desktop] building fleet-db sidecar: ${FLEET_OUT}"
  (
    cd "${FLEET_DB_REPO}"
    go build \
      -ldflags="-X main.commit=${FLEET_BUILD}" \
      -o "${FLEET_OUT}" \
      ./cmd/fleet-db
  )
  chmod +x "${FLEET_OUT}"
else
  echo "[desktop] warning: fleet-db repo not found at ${FLEET_DB_REPO}; local runtime will need FLEET_DB_BIN" >&2
fi
