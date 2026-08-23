#!/usr/bin/env bash
# Assemble the offline custom-workflow authoring kit shipped in the desktop app.
#
# The kit must be a fully SELF-CONTAINED, SYMLINK-FREE tree: the packager
# (`loom workflow package-authoring-kit`) rejects symlinks so a kit input can
# never escape its declared tree, and at run time the desktop app has no network
# or package manager to resolve dependencies. A raw pnpm checkout is neither —
# its node_modules are symlinks into the virtual store — so we use `pnpm deploy`
# to flatten each package into real files before packaging.
#
# prepare-sidecar.sh runs this, then reads kit-manifest.json's kit_digest and
# bakes it into authoringkit.ExpectedKitDigest.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${DESKTOP_DIR}/.." && pwd)"
# OUT defaults to the desktop resource dir; LOOM_AUTHORING_KIT_OUT overrides it
# (used by validation harnesses so they never write into the app tree).
OUT="${LOOM_AUTHORING_KIT_OUT:-${DESKTOP_DIR}/src-tauri/resources/authoring-kit}"

if [ "${LOOM_SKIP_AUTHORING_KIT:-0}" = "1" ]; then
  echo "[desktop] warning: LOOM_SKIP_AUTHORING_KIT=1; custom authoring is unavailable" >&2
  rm -rf "${OUT}"
  mkdir -p "${OUT}"
  exit 0
fi

SOURCE="${LOOM_AUTHORING_KIT_SOURCE:-${REPO_ROOT}/../flue}"
[ -d "${SOURCE}" ] || { echo "authoring kit source missing: ${SOURCE}" >&2; exit 1; }
command -v pnpm >/dev/null 2>&1 || { echo "pnpm is required to flatten the authoring kit" >&2; exit 1; }

# Refuse to package a drifted Flue: the kit must be reproducible from the pinned
# commit (the same guard rebuild-builtin-bundle.sh applies to the builtin lane).
PIN_FILE="${REPO_ROOT}/internal/workflows/FLUE_COMMIT"
if [ -f "${PIN_FILE}" ] && [ "${ALLOW_FLUE_PIN_DRIFT:-}" != "1" ]; then
  PINNED="$(tr -d '[:space:]' < "${PIN_FILE}")"
  HEAD="$(git -C "${SOURCE}" rev-parse HEAD 2>/dev/null || true)"
  [ "${HEAD}" = "${PINNED}" ] || {
    echo "ERROR: Flue source HEAD (${HEAD}) != pinned ${PINNED} (internal/workflows/FLUE_COMMIT)." >&2
    echo "       Check out Flue at the pin, or set ALLOW_FLUE_PIN_DRIFT=1 to override." >&2
    exit 1
  }
fi

STAGE="$(mktemp -d -t loom-authoring-kit.XXXXXX)"
trap 'rm -rf "${STAGE}"' EXIT

# pnpm deploy produces a self-contained package directory (real files, prod deps
# only). This workspace uses the default ISOLATED (symlinked) node-linker, and a
# plain deploy reproduces that layout — hundreds of node_modules symlinks the kit
# packager rejects. --node-linker=hoisted flattens the deploy into real dirs (only
# a handful of .bin CLI shims remain as symlinks, stripped below). --legacy keeps
# deploy working regardless of the workspace's inject-workspace-packages setting
# (pnpm v10+ refuses a non-legacy deploy without it).
echo "[authoring-kit] flattening @flue/cli and @flue/runtime via pnpm deploy" >&2
( cd "${SOURCE}" && pnpm --filter @flue/cli    --prod deploy --legacy --node-linker=hoisted "${STAGE}/flue-cli" )
( cd "${SOURCE}" && pnpm --filter @flue/runtime --prod deploy --legacy --node-linker=hoisted "${STAGE}/flue-runtime" )
# Strip residual symlinks (the .bin CLI shims): the kit is loaded by path, never
# via .bin, and the packager refuses any symlink in an input tree.
find "${STAGE}/flue-cli" "${STAGE}/flue-runtime" -type l -delete

# Prune Cloudflare/workerd-only packages the node build target never loads, and
# the cross-platform native prebuilds esbuild/rollup ship, to keep the kit small.
# Host esbuild/vite are retained — the node build uses them.
host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
prune_kit() {
  local root="$1"
  [ -d "${root}/node_modules" ] || return 0
  rm -rf \
    "${root}/node_modules/@cloudflare" \
    "${root}/node_modules/agents" \
    "${root}/node_modules/wrangler" \
    "${root}/node_modules/workerd" \
    "${root}/node_modules/miniflare" 2>/dev/null || true
  # Drop non-host native prebuild packages (e.g. @esbuild/linux-x64 on macOS).
  for scope in "${root}/node_modules/@esbuild" "${root}/node_modules/@rollup"; do
    [ -d "${scope}" ] || continue
    for pkg in "${scope}"/*; do
      case "$(basename "${pkg}")" in
        *"${host_os}"*) : ;;              # keep host prebuild
        *) rm -rf "${pkg}" 2>/dev/null || true ;;
      esac
    done
  done
}
prune_kit "${STAGE}/flue-cli"
prune_kit "${STAGE}/flue-runtime"

# @daytona/sdk is imported by the daytona task runner in the workflow source, so
# the build must resolve it AND its full transitive closure. A bare copy loses the
# closure (its store deps are symlinks), so stage-daytona-closure.mjs walks the
# graph and copies every package dereferenced into a flat nested node_modules.
echo "[authoring-kit] staging @daytona/sdk closure" >&2
node "${SCRIPT_DIR}/stage-daytona-closure.mjs" "${SOURCE}" "${STAGE}/daytona-sdk"

rm -rf "${OUT}"
mkdir -p "$(dirname "${OUT}")"
go -C "${REPO_ROOT}" run ./cmd/loom workflow package-authoring-kit --out "${OUT}" \
  --root flue-runtime="${STAGE}/flue-runtime" \
  --root flue-cli="${STAGE}/flue-cli" \
  --root loom-sdk="${REPO_ROOT}/sdk" \
  --root daytona-sdk="${STAGE}/daytona-sdk" \
  --json
