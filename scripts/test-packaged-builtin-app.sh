#!/usr/bin/env bash
set -Eeuo pipefail

# Exercise the locally-built app bundle without a checkout/runtime on PATH.
# Full runtime tamper coverage belongs to the desktop acceptance harness; this
# fast gate proves the shipped node and both artifact entrypoints are loadable.
APP="${APP:-${1:-${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}/desktop/src-tauri/target/release/bundle/macos/Loom Agents.app}}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NODE="${APP}/Contents/MacOS/node"
ARTIFACTS="${APP}/Contents/Resources/builtin-workflows"

for path in "${APP}/Contents/MacOS/loom" "${NODE}" "${APP}/Contents/MacOS/fleet-db" "${ARTIFACTS}/index.json"; do
  [ -e "${path}" ] || { echo "missing packaged app path: ${path}" >&2; exit 1; }
done

TMP="$(mktemp -d -t loom-packaged-app.XXXXXX)"
trap 'rm -rf "${TMP}"' EXIT
COPY="${TMP}/Loom Agents.app"
cp -R "${APP}" "${COPY}"

run_clean() {
  env -i HOME="${TMP}/home" TMPDIR="${TMP}" PATH=/usr/bin:/bin:/usr/sbin:/sbin \
    USER="${USER:-loom}" LOGNAME="${LOGNAME:-loom}" SHELL=/bin/sh LANG=en_US.UTF-8 \
    LOOM_REAL_FLUE_CMD=/usr/bin/false "$@"
}

for name in epic-runner github-review-agent; do
  run_clean "${COPY}/Contents/MacOS/node" "${ROOT}/desktop/scripts/smoke-load-server.mjs" \
    "${COPY}/Contents/Resources/builtin-workflows/${name}/dist/server.mjs" "${name}"
done

echo "node=$(${NODE} --version)"
echo "index=$(shasum -a 256 "${ARTIFACTS}/index.json" | awk '{print $1}')"
echo "PASS"
