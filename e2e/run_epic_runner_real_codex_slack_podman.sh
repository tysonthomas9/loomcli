#!/usr/bin/env bash
# Build and run the real Codex epic-runner E2E in Podman against the Slack fixture.

set -euo pipefail

IMAGE="${IMAGE:-loomcli-epic-runner-e2e}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEET_DB_REPO="${FLEET_DB_REPO:-$HOME/codebase/code-agents/fleet-db}"
FLEET_DB_BIN="${FLEET_DB_BIN:-}"
HOST_CODEX_HOME="${HOST_CODEX_HOME:-$HOME/.codex}"
CODEX_E2E_HOME="${CODEX_E2E_HOME:-/private/tmp/codex-e2e-home-slack}"
ARTIFACTS_DIR="${ARTIFACTS_DIR:-/private/tmp/loom-real-codex-slack-epic-artifacts}"
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-1200s}"

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

if [[ ! -s "$HOST_CODEX_HOME/auth.json" ]]; then
    echo "Codex auth not found at $HOST_CODEX_HOME/auth.json" >&2
    echo "Set HOST_CODEX_HOME to a directory containing real Codex CLI auth/config." >&2
    exit 1
fi

rm -rf "$CODEX_E2E_HOME"
mkdir -p "$CODEX_E2E_HOME"
chmod 700 "$CODEX_E2E_HOME"
install -m 600 "$HOST_CODEX_HOME/auth.json" "$CODEX_E2E_HOME/auth.json"
if [[ -f "$HOST_CODEX_HOME/config.toml" ]]; then
    install -m 600 "$HOST_CODEX_HOME/config.toml" "$CODEX_E2E_HOME/config.toml"
fi

mkdir -p "$ARTIFACTS_DIR"

podman run --rm \
    -e "EPIC_RUNNER_TIMEOUT=$EPIC_RUNNER_TIMEOUT" \
    -e "ARTIFACTS_OUT=/artifacts" \
    -e "CODEX_CLI_VERSION=${CODEX_CLI_VERSION:-0.129.0}" \
    -v "$FLEET_DB_BIN:/usr/local/bin/fleet-db:ro" \
    -v "$ROOT_DIR/e2e/epic_runner_real_codex_slack.sh:/usr/local/bin/epic_runner_real_codex_slack.sh:ro" \
    -v "$CODEX_E2E_HOME:/root/.codex" \
    -v "$ARTIFACTS_DIR:/artifacts" \
    "$IMAGE" /usr/local/bin/epic_runner_real_codex_slack.sh
