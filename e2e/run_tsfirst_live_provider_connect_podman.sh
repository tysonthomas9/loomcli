#!/usr/bin/env bash
# Build and run the opt-in live-provider TypeScript-first local-connect E2E in
# Podman. Defaults to Codex and mounts CODEX_HOME for real CLI auth/config.

set -euo pipefail

IMAGE="${IMAGE:-loomcli-e2e}"
BACKEND="${BACKEND:-codex}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CODEX_HOME="${CODEX_HOME:-/private/tmp/codex-e2e-home}"
ARTIFACTS_DIR="${ARTIFACTS_DIR:-/private/tmp/loom-tsfirst-live-connect-artifacts}"
CODEX_VERSION="${CODEX_VERSION:-0.129.0}"
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-180s}"

podman build -f "$ROOT_DIR/e2e/Dockerfile" -t "$IMAGE" "$ROOT_DIR"

mkdir -p "$ARTIFACTS_DIR"

case "$BACKEND" in
    codex)
        if [[ ! -d "$CODEX_HOME" ]]; then
            echo "Codex home not found at $CODEX_HOME" >&2
            echo "Set CODEX_HOME to a directory containing real Codex CLI auth/config." >&2
            exit 1
        fi
        podman run --rm \
            -e "BACKEND=$BACKEND" \
            -e "CODEX_VERSION=$CODEX_VERSION" \
            -e "CONNECT_TIMEOUT=$CONNECT_TIMEOUT" \
            -e "ARTIFACTS_OUT=/artifacts" \
            -v "$CODEX_HOME:/root/.codex" \
            -v "$ARTIFACTS_DIR:/artifacts" \
            "$IMAGE" bash -lc 'rm -f /usr/local/bin/codex && npm install -g "@openai/codex@${CODEX_VERSION}" >/tmp/codex-install.log && e2e/tsfirst_live_provider_connect.sh'
        ;;
    *)
        echo "Podman wrapper currently supports BACKEND=codex. Run e2e/tsfirst_live_provider_connect.sh directly for $BACKEND with its CLI/auth mounted." >&2
        exit 2
        ;;
esac
