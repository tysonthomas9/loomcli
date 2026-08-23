# shellcheck shell=bash
# prepare-node-runtime.sh — stage the pinned Node 22 runtime as a Tauri sidecar.
#
# SOURCED by prepare-sidecar.sh (never executed directly): it needs
# TARGET_TRIPLE, DESKTOP_DIR, REPO_ROOT and BIN_DIR in scope, defines
# prepare_node_runtime, calls it, and on return exports
#   NODE_SIDECAR=${BIN_DIR}/node-${TARGET_TRIPLE}   (→ Contents/MacOS/node via externalBin)
#   NODE_VERSION=22.x.y                             (internal/workflows/NODE_VERSION)
# plus resources/licenses/node-LICENSE (→ Contents/Resources/licenses/).
#
# The tarball is fetched from nodejs.org (or LOOM_NODE_MIRROR) into a local
# cache and verified against SHASUMS256.txt before anything is extracted; a
# mismatching cached tarball is deleted so the next run re-downloads. Never
# stages an unverified binary.
#
#   env (all optional, build host only):
#     LOOM_NODE_CACHE=<dir>     default $HOME/Library/Caches/loom-desktop/node
#     LOOM_NODE_TARBALL=<path>  offline tarball; needs LOOM_NODE_SHASUMS or a
#                               SHASUMS256.txt next to it
#     LOOM_NODE_SHASUMS=<path>  sums file for LOOM_NODE_TARBALL
#     LOOM_NODE_MIRROR=<url>    default https://nodejs.org/dist
#
# Failures `return 1` after printing the precise reason (the caller runs under
# set -e and dies); success never exits the sourcing shell.

node_runtime_die() {
  echo "[desktop] node runtime: ERROR: $*" >&2
  return 1
}

prepare_node_runtime() {
  local version_file="${REPO_ROOT}/internal/workflows/NODE_VERSION"
  [ -f "${version_file}" ] || { node_runtime_die "pin file ${version_file} missing"; return 1; }
  NODE_VERSION="$(tr -d '[:space:]' < "${version_file}")"
  if ! printf '%s' "${NODE_VERSION}" | grep -Eq '^22\.[0-9]+\.[0-9]+$'; then
    node_runtime_die "internal/workflows/NODE_VERSION must be 22.x.y, got '${NODE_VERSION}'"
    return 1
  fi

  local node_platform
  case "${TARGET_TRIPLE}" in
    aarch64-apple-darwin) node_platform="darwin-arm64" ;;
    x86_64-apple-darwin) node_platform="darwin-x64" ;;
    x86_64-unknown-linux-gnu) node_platform="linux-x64" ;;
    aarch64-unknown-linux-gnu) node_platform="linux-arm64" ;;
    *)
      node_runtime_die "unsupported target triple '${TARGET_TRIPLE}' (known: aarch64-apple-darwin x86_64-apple-darwin x86_64-unknown-linux-gnu aarch64-unknown-linux-gnu)"
      return 1
      ;;
  esac

  local tarball_name="node-v${NODE_VERSION}-${node_platform}.tar.gz"
  local license_dir="${DESKTOP_DIR}/src-tauri/resources/licenses"
  local license_out="${license_dir}/node-LICENSE"
  NODE_SIDECAR="${BIN_DIR}/node-${TARGET_TRIPLE}"

  # Short-circuit: the right version is already staged with its licence.
  if [ -x "${NODE_SIDECAR}" ] && [ -f "${license_out}" ] \
    && [ "$("${NODE_SIDECAR}" --version 2>/dev/null || true)" = "v${NODE_VERSION}" ]; then
    echo "[desktop] node runtime: using staged node v${NODE_VERSION} at ${NODE_SIDECAR}"
    export NODE_SIDECAR NODE_VERSION
    return 0
  fi

  local tarball sums
  if [ -n "${LOOM_NODE_TARBALL:-}" ]; then
    tarball="${LOOM_NODE_TARBALL}"
    [ -f "${tarball}" ] || { node_runtime_die "LOOM_NODE_TARBALL=${tarball} does not exist"; return 1; }
    sums="${LOOM_NODE_SHASUMS:-$(dirname "${tarball}")/SHASUMS256.txt}"
    [ -f "${sums}" ] || { node_runtime_die "LOOM_NODE_TARBALL needs LOOM_NODE_SHASUMS or a sibling SHASUMS256.txt (looked at ${sums})"; return 1; }
    [ "$(basename "${tarball}")" = "${tarball_name}" ] || { node_runtime_die "LOOM_NODE_TARBALL must be named ${tarball_name} (got $(basename "${tarball}")) so the SHASUMS256.txt line can be matched"; return 1; }
  else
    local mirror="${LOOM_NODE_MIRROR:-https://nodejs.org/dist}"
    local cache="${LOOM_NODE_CACHE:-${HOME}/Library/Caches/loom-desktop/node}/v${NODE_VERSION}"
    mkdir -p "${cache}"
    tarball="${cache}/${tarball_name}"
    sums="${cache}/SHASUMS256.txt"
    local base="${mirror}/v${NODE_VERSION}"
    if [ ! -f "${tarball}" ] || [ ! -f "${sums}" ]; then
      command -v curl >/dev/null 2>&1 || { node_runtime_die "curl is required to download ${base}/${tarball_name} (or set LOOM_NODE_TARBALL)"; return 1; }
      local f url
      for f in "${tarball_name}" "SHASUMS256.txt"; do
        url="${base}/${f}"
        echo "[desktop] node runtime: downloading ${url}"
        if ! curl -fsSL --retry 3 -o "${cache}/${f}.tmp" "${url}"; then
          rm -f "${cache}/${f}.tmp"
          node_runtime_die "download failed: ${url} (set LOOM_NODE_TARBALL/LOOM_NODE_SHASUMS for an offline build)"
          return 1
        fi
        mv "${cache}/${f}.tmp" "${cache}/${f}"
      done
    else
      echo "[desktop] node runtime: using cached ${tarball}"
    fi
  fi

  # Verify: exactly one SHASUMS256.txt line names the tarball and its digest
  # equals the file's. On mismatch drop the cached tarball so a rerun refetches.
  # SHASUMS256.txt lines are `<sha256>  <file>` (shasum format: two spaces,
  # or ` *` for binary mode); match the file name as the whole second field.
  local expected actual matches
  matches="$(awk -v want="${tarball_name}" '$1 ~ /^[0-9a-f]{64}$/ && ($2 == want || $2 == "./" want || $2 == "*" want) { n++ } END { print n + 0 }' "${sums}")"
  if [ "${matches}" != "1" ]; then
    node_runtime_die "expected exactly one line for ${tarball_name} in ${sums}, found ${matches}"
    return 1
  fi
  expected="$(awk -v want="${tarball_name}" '$1 ~ /^[0-9a-f]{64}$/ && ($2 == want || $2 == "./" want || $2 == "*" want) { print $1 }' "${sums}")"
  actual="$(shasum -a 256 "${tarball}" | cut -d' ' -f1)"
  if [ "${expected}" != "${actual}" ]; then
    [ -z "${LOOM_NODE_TARBALL:-}" ] && rm -f "${tarball}"
    node_runtime_die "SHA-256 mismatch for ${tarball_name}: expected ${expected}, actual ${actual} (cached tarball removed; rerun to re-download)"
    return 1
  fi
  echo "[desktop] node runtime: verified ${tarball_name} sha256=${actual}"

  # Extract only bin/node + LICENSE; the full distribution (npm, headers) is
  # not shipped in this slice.
  local tmp
  tmp="$(mktemp -d -t loom-node-runtime.XXXXXX)"
  if ! tar -xzf "${tarball}" -C "${tmp}" --strip-components=1 \
    "node-v${NODE_VERSION}-${node_platform}/bin/node" \
    "node-v${NODE_VERSION}-${node_platform}/LICENSE"; then
    rm -rf "${tmp}"
    node_runtime_die "tar extraction of ${tarball_name} failed"
    return 1
  fi
  mkdir -p "${BIN_DIR}" "${license_dir}"
  install -m 0755 "${tmp}/bin/node" "${NODE_SIDECAR}"
  cp "${tmp}/LICENSE" "${license_out}"
  rm -rf "${tmp}"

  # Post-checks: the staged binary runs, is the pinned version, and can load
  # node:sqlite (the bundle imports it; unflagged since 22.13).
  local got_version
  got_version="$("${NODE_SIDECAR}" --version 2>&1 || true)"
  if [ "${got_version}" != "v${NODE_VERSION}" ]; then
    rm -f "${NODE_SIDECAR}"
    node_runtime_die "staged node prints '${got_version}', expected v${NODE_VERSION}"
    return 1
  fi
  if ! "${NODE_SIDECAR}" -e 'import("node:sqlite").then(()=>process.exit(0),()=>process.exit(3))'; then
    rm -f "${NODE_SIDECAR}"
    node_runtime_die "staged node cannot import node:sqlite (the built-in bundles require it)"
    return 1
  fi

  echo "[desktop] node runtime: v${NODE_VERSION} (${node_platform}) -> ${NODE_SIDECAR}"
  du -sh "${NODE_SIDECAR}" | sed 's/^/[desktop] node runtime: size /'
  export NODE_SIDECAR NODE_VERSION
  return 0
}

prepare_node_runtime
