#!/usr/bin/env bash
# dev-container-run.sh — build + launch the Loom dev container.
#
# Usage:
#   scripts/dev-container-run.sh                  # build + run
#   scripts/dev-container-run.sh --no-build       # run existing image
#   HOST_PORT=9000 scripts/dev-container-run.sh   # different host port
#   IMAGE=my-loom-dev scripts/dev-container-run.sh
#
# Env pass-through (optional):
#   ANTHROPIC_API_KEY, OPENAI_API_KEY — forwarded to the container
#
# Host mounts (automatic if present):
#   ~/.claude, ~/.codex, ~/.config/opencode — mounted read-only so the
#   container CLIs use your local auth state.
set -euo pipefail

IMAGE=${IMAGE:-loomcli-dev}
NAME=${NAME:-loomcli-dev}
HOST_PORT=${HOST_PORT:-8091}
DO_BUILD=1

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-build) DO_BUILD=0; shift ;;
        -h|--help)
            sed -n '3,17p' "$0"; exit 0 ;;
        *) echo "Unknown flag: $1" >&2; exit 1 ;;
    esac
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if ! command -v podman >/dev/null 2>&1; then
    echo "Error: podman not found in PATH" >&2
    exit 1
fi

if [[ "$DO_BUILD" -eq 1 ]]; then
    echo "==> building $IMAGE (Dockerfile.dev)"
    podman build -f "$REPO_ROOT/Dockerfile.dev" -t "$IMAGE" "$REPO_ROOT"
fi

# Remove any prior instance so --name doesn't collide
podman rm -f "$NAME" >/dev/null 2>&1 || true

args=(-d --init --name "$NAME" -p "$HOST_PORT:3000"
      -e "LOOM_FRONTEND_URL=http://localhost:$HOST_PORT")

[[ -n "${ANTHROPIC_API_KEY:-}" ]] && args+=(-e ANTHROPIC_API_KEY)
[[ -n "${OPENAI_API_KEY:-}"    ]] && args+=(-e OPENAI_API_KEY)
# DAYTONA_API_KEY enables the flue Daytona-per-task sandbox path. LOOM_FLUE_SANDBOX
# (=daytona) is the operator switch that routes flue agents into a sandbox per
# task; it rides the LOOM_ allowlist down to spawned agents. Both forwarded if set.
[[ -n "${DAYTONA_API_KEY:-}"   ]] && args+=(-e DAYTONA_API_KEY)
[[ -n "${LOOM_FLUE_SANDBOX:-}" ]] && args+=(-e LOOM_FLUE_SANDBOX)

[[ -d "$HOME/.claude"          ]] && args+=(-v "$HOME/.claude:/root/.claude:ro")
[[ -d "$HOME/.codex"           ]] && args+=(-v "$HOME/.codex:/root/.codex:ro")
[[ -d "$HOME/.config/opencode" ]] && args+=(-v "$HOME/.config/opencode:/root/.config/opencode:ro")

# fleet-db is required by `loom serve --issue-backend=fleetdb` but isn't built
# from this repo (separate project). The dev image doesn't bake it in, so look
# for a prebuilt binary in well-known places and bind-mount it onto PATH. Set
# FLEET_DB_BIN=/path/to/fleet-db to override.
if [[ -z "${FLEET_DB_BIN:-}" ]]; then
    for candidate in \
        "$REPO_ROOT/tmp/distributed-smoke/bin/fleet-db" \
        "$REPO_ROOT/tmp/e2e-workspace/.loom-config/fleet-db" \
        "$REPO_ROOT/.loom-config/bin/fleet-db" \
        "$HOME/.loom/bin/fleet-db"
    do
        [[ -x "$candidate" ]] && FLEET_DB_BIN=$candidate && break
    done
fi
if [[ -n "${FLEET_DB_BIN:-}" && -x "$FLEET_DB_BIN" ]]; then
    echo "==> mounting fleet-db from $FLEET_DB_BIN"
    args+=(-v "$FLEET_DB_BIN:/usr/local/bin/fleet-db:ro")
else
    echo "Warning: no fleet-db binary found; loom serve will fail to start." >&2
    echo "         Set FLEET_DB_BIN=/path/to/fleet-db before re-running." >&2
fi

# LOOM_BIN lets you test a locally-built loom (cross-compiled for the container
# arch) without a full image rebuild: mounts it over the baked binary.
if [[ -n "${LOOM_BIN:-}" && -x "$LOOM_BIN" ]]; then
    echo "==> mounting loom from $LOOM_BIN"
    args+=(-v "$LOOM_BIN:/usr/local/bin/loom:ro")
fi

echo "==> starting $NAME on http://localhost:$HOST_PORT"
podman run "${args[@]}" "$IMAGE"

echo
echo "  Logs:  podman logs -f $NAME"
echo "  Shell: podman exec -it $NAME bash"
echo "  Stop:  podman stop $NAME && podman rm $NAME"
echo "  URL:   http://localhost:$HOST_PORT  (workspace auto-created)"
