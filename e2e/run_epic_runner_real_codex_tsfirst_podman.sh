#!/usr/bin/env bash
# Build and run the TypeScript-SDK-driven real Codex epic-runner E2E in Podman
# against octocat/Hello-World. Orchestrates via `loom check`/`loom apply`/`loom run`
# instead of `loom epic run`; runs epic_runner_real_codex_tsfirst.sh inside the image.

set -euo pipefail

IMAGE="${IMAGE:-loomcli-epic-runner-e2e}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# fleet-db may sit as a sibling of loomcli (this checkout) or one level up.
if [[ -z "${FLEET_DB_REPO:-}" ]]; then
    if [[ -d "$ROOT_DIR/../fleet-db/cmd/fleet-db" ]]; then
        FLEET_DB_REPO="$ROOT_DIR/../fleet-db"
    else
        FLEET_DB_REPO="$ROOT_DIR/../../fleet-db"
    fi
fi
FLEET_DB_BIN="${FLEET_DB_BIN:-}"
CODEX_HOME="${CODEX_HOME:-/private/tmp/codex-e2e-home}"
ARTIFACTS_DIR="${ARTIFACTS_DIR:-/private/tmp/loom-tsfirst-codex-epic-artifacts}"
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-900s}"
RECONCILE_INTERVAL="${RECONCILE_INTERVAL:-3}"
CODEX_VERSION="${CODEX_VERSION:-0.129.0}"

podman build -f "$ROOT_DIR/e2e/Dockerfile" -t "$IMAGE" "$ROOT_DIR"

IMAGE_ARCH="$(podman image inspect "$IMAGE" --format '{{.Architecture}}')"
if [[ -z "$IMAGE_ARCH" ]]; then
    echo "could not determine image architecture for $IMAGE" >&2
    exit 1
fi
if [[ -z "$FLEET_DB_BIN" ]]; then
    FLEET_DB_BIN="$ROOT_DIR/tmp/e2e/fleet-db-linux-$IMAGE_ARCH"
fi

# Build fleet-db for the container's OS/arch if missing or not a Linux ELF
# (a stale macOS/host build mounted into the container fails with "exec format error").
if [[ ! -x "$FLEET_DB_BIN" ]] || ! file -b "$FLEET_DB_BIN" 2>/dev/null | grep -q 'ELF'; then
    if [[ ! -d "$FLEET_DB_REPO/cmd/fleet-db" ]]; then
        echo "fleet-db repo not found at $FLEET_DB_REPO" >&2
        echo "Set FLEET_DB_REPO or FLEET_DB_BIN before running this E2E." >&2
        exit 1
    fi
    mkdir -p "$(dirname "$FLEET_DB_BIN")"
    (cd "$FLEET_DB_REPO" && GOOS=linux GOARCH="$IMAGE_ARCH" CGO_ENABLED=0 go build -o "$FLEET_DB_BIN" ./cmd/fleet-db)
fi

if ! file -b "$FLEET_DB_BIN" | grep -q 'ELF'; then
    echo "fleet-db binary is not a Linux ELF: $(file -b "$FLEET_DB_BIN")" >&2
    echo "path: $FLEET_DB_BIN" >&2
    exit 1
fi

if [[ ! -d "$CODEX_HOME" ]]; then
    echo "Codex home not found at $CODEX_HOME" >&2
    echo "Set CODEX_HOME to a directory containing real Codex CLI auth/config." >&2
    exit 1
fi

mkdir -p "$ARTIFACTS_DIR"

podman run --rm \
    -e "EPIC_RUNNER_TIMEOUT=$EPIC_RUNNER_TIMEOUT" \
    -e "RECONCILE_INTERVAL=$RECONCILE_INTERVAL" \
    -e "CODEX_VERSION=$CODEX_VERSION" \
    -e "ARTIFACTS_OUT=/artifacts" \
    -v "$FLEET_DB_BIN:/usr/local/bin/fleet-db:ro" \
    -v "$ROOT_DIR/e2e/epic_runner_real_codex_tsfirst.sh:/usr/local/bin/epic_runner_real_codex_tsfirst.sh:ro" \
    -v "$CODEX_HOME:/root/.codex" \
    -v "$ARTIFACTS_DIR:/artifacts" \
    "$IMAGE" /usr/local/bin/epic_runner_real_codex_tsfirst.sh
