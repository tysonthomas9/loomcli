#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${DESKTOP_DIR}/.." && pwd)"
OUT="${DESKTOP_DIR}/src-tauri/resources/authoring-kit"

if [ "${LOOM_SKIP_AUTHORING_KIT:-0}" = "1" ]; then
  echo "[desktop] warning: LOOM_SKIP_AUTHORING_KIT=1; custom authoring is unavailable" >&2
  rm -rf "${OUT}"
  mkdir -p "${OUT}"
  exit 0
fi

SOURCE="${LOOM_AUTHORING_KIT_SOURCE:-${REPO_ROOT}/../flue}"
[ -d "${SOURCE}" ] || { echo "authoring kit source missing: ${SOURCE}" >&2; exit 1; }
rm -rf "${OUT}"
mkdir -p "${OUT}"
cd "${REPO_ROOT}"
go run ./cmd/loom workflow package-authoring-kit --out "${OUT}" \
  --root flue-runtime="${SOURCE}/packages/runtime" \
  --root flue-cli="${SOURCE}/packages/cli" \
  --root loom-sdk="${REPO_ROOT}/sdk" \
  --root daytona-sdk="${SOURCE}/node_modules/.pnpm/node_modules/@daytona/sdk" \
  --json
