#!/usr/bin/env bash
# Build and run the Codex-backed epic runner E2E in Podman.

set -euo pipefail

IMAGE="${IMAGE:-loomcli-epic-runner-e2e}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_DB_REPO="${FLEET_DB_REPO:-$ROOT_DIR/../../fleet-db}"
FLEET_DB_BIN="${FLEET_DB_BIN:-}"

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

podman run --rm \
    -e "EPIC_RUNNER_TIMEOUT=${EPIC_RUNNER_TIMEOUT:-120s}" \
    -v "$FLEET_DB_BIN:/usr/local/bin/fleet-db:ro" \
    "$IMAGE" epic_runner_codex.sh
