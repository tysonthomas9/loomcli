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

[[ -d "$HOME/.claude"          ]] && args+=(-v "$HOME/.claude:/root/.claude:ro")
[[ -d "$HOME/.codex"           ]] && args+=(-v "$HOME/.codex:/root/.codex:ro")
[[ -d "$HOME/.config/opencode" ]] && args+=(-v "$HOME/.config/opencode:/root/.config/opencode:ro")

echo "==> starting $NAME on http://localhost:$HOST_PORT"
podman run "${args[@]}" "$IMAGE"

echo
echo "  Logs:  podman logs -f $NAME"
echo "  Shell: podman exec -it $NAME bash"
echo "  Stop:  podman stop $NAME && podman rm $NAME"
echo "  URL:   http://localhost:$HOST_PORT  (workspace auto-created)"
