#!/usr/bin/env bash
# Build and run the real Codex epic-runner E2E in Podman against octocat/Hello-World.

set -euo pipefail

IMAGE="${IMAGE:-loomcli-epic-runner-e2e}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_DB_REPO="${FLEET_DB_REPO:-$ROOT_DIR/../../fleet-db}"
FLEET_DB_BIN="${FLEET_DB_BIN:-}"
FLUE_REPO="${FLUE_REPO:-$ROOT_DIR/../flue}"
CODEX_HOME="${CODEX_HOME:-/private/tmp/codex-e2e-home}"
ARTIFACTS_DIR="${ARTIFACTS_DIR:-/private/tmp/loom-real-codex-epic-artifacts}"
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-900s}"

podman build -f "$ROOT_DIR/e2e/Dockerfile" -t "$IMAGE" "$ROOT_DIR"

IMAGE_ARCH="$(podman image inspect "$IMAGE" --format '{{.Architecture}}')"
if [[ -z "$FLEET_DB_BIN" ]]; then
    FLEET_DB_BIN="$ROOT_DIR/tmp/e2e/fleet-db-linux-$IMAGE_ARCH"
fi

if [[ ! -x "$FLEET_DB_BIN" ]]; then
    if [[ ! -d "$FLEET_DB_REPO/cmd/fleet-db" ]]; then
        echo "fleet-db repo not found at $FLEET_DB_REPO" >&2
        echo "Set FLEET_DB_REPO or FLEET_DB_BIN before running this E2E." >&2
        exit 1
    fi
    mkdir -p "$(dirname "$FLEET_DB_BIN")"
    (cd "$FLEET_DB_REPO" && GOOS=linux GOARCH="$IMAGE_ARCH" CGO_ENABLED=0 go build -o "$FLEET_DB_BIN" ./cmd/fleet-db)
fi

if [[ ! -d "$CODEX_HOME" ]]; then
    echo "Codex home not found at $CODEX_HOME" >&2
    echo "Set CODEX_HOME to a directory containing real Codex CLI auth/config." >&2
    exit 1
fi

if [[ ! -d "$FLUE_REPO/packages/runtime" ]]; then
    echo "Flue repo not found at $FLUE_REPO" >&2
    echo "Set FLUE_REPO to the checkout containing packages/runtime." >&2
    exit 1
fi

mkdir -p "$ARTIFACTS_DIR"

podman run --rm \
    -e "EPIC_RUNNER_TIMEOUT=$EPIC_RUNNER_TIMEOUT" \
    -e "CODEX_CLI_VERSION=${CODEX_CLI_VERSION:-latest}" \
    -e "ARTIFACTS_OUT=/artifacts" \
    -e "LOOM_FLUE_RUNTIME_ROOT=/opt/flue/packages/runtime" \
    -e 'LOOM_REAL_FLUE_CMD_JSON=["node","/opt/flue/packages/cli/bin/flue.mjs"]' \
    -v "$FLEET_DB_BIN:/usr/local/bin/fleet-db:ro" \
    -v "$FLUE_REPO:/opt/flue:ro" \
    -v "$ROOT_DIR/e2e/epic_runner_real_codex_octocat.sh:/usr/local/bin/epic_runner_real_codex_octocat.sh:ro" \
    -v "$CODEX_HOME:/root/.codex" \
    -v "$ARTIFACTS_DIR:/artifacts" \
    "$IMAGE" /usr/local/bin/epic_runner_real_codex_octocat.sh
