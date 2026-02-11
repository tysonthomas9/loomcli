#!/usr/bin/env bash
# run_local.sh — Build and run the loomcli E2E Docker container locally.
#
# Mounts host auth configs and (optionally) real CLI binaries into the
# container so tests can run against real backends.
#
# Usage: e2e/run_local.sh [OPTIONS] [-- TEST_ARGS...]
set -euo pipefail

IMAGE_NAME="loomcli-e2e"
DO_BUILD=1
MOUNT_CLIS=0
BACKEND=""

usage() {
    cat <<EOF
Usage: e2e/run_local.sh [OPTIONS] [-- TEST_ARGS...]

Options:
  --no-build         Skip docker build (use existing image)
  --mount-clis       Mount real CLI binaries from host PATH
  --backend NAME     Set LOOM_BACKEND env var in container
  --image NAME       Custom image name (default: loomcli-e2e)
  -h, --help         Show usage

Anything after -- is passed to the container CMD, e.g.:
  e2e/run_local.sh -- go test -tags e2e -v -run TestE2E_Foo ./internal/cli/
EOF
    exit 0
}

# ── Parse arguments ─────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-build)   DO_BUILD=0; shift ;;
        --mount-clis) MOUNT_CLIS=1; shift ;;
        --backend)
            if [[ -z "${2:-}" ]]; then
                echo "Error: --backend requires a value" >&2
                exit 1
            fi
            BACKEND="$2"; shift 2 ;;
        --image)
            if [[ -z "${2:-}" ]]; then
                echo "Error: --image requires a value" >&2
                exit 1
            fi
            IMAGE_NAME="$2"; shift 2 ;;
        -h|--help)    usage ;;
        --)           shift; break ;;
        *)            echo "Unknown option: $1" >&2; usage ;;
    esac
done

# ── Pre-flight checks ──────────────────────────────────────────────────────
if ! command -v docker >/dev/null 2>&1; then
    echo "Error: docker is not installed or not in PATH." >&2
    exit 1
fi

# Resolve repo root (script lives in e2e/)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ── Build ───────────────────────────────────────────────────────────────────
if [[ "$DO_BUILD" -eq 1 ]]; then
    echo "==> Building image ${IMAGE_NAME}..."
    docker build -f "$REPO_ROOT/e2e/Dockerfile" -t "$IMAGE_NAME" "$REPO_ROOT"
fi

# ── Construct docker run arguments ──────────────────────────────────────────
DOCKER_ARGS=(--rm)

# API key pass-through
if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
    DOCKER_ARGS+=(-e ANTHROPIC_API_KEY)
else
    echo "Info: ANTHROPIC_API_KEY not set; stubs will be used for Claude."
fi
if [[ -n "${OPENAI_API_KEY:-}" ]]; then
    DOCKER_ARGS+=(-e OPENAI_API_KEY)
fi

# Backend override
if [[ -n "$BACKEND" ]]; then
    DOCKER_ARGS+=(-e "LOOM_BACKEND=$BACKEND")
fi

# Forward STUB_* env vars from host
while IFS= read -r var; do
    DOCKER_ARGS+=(-e "$var")
done < <(env | grep '^STUB_' | cut -d= -f1 || true)

# Mount auth config directories (read-only)
if [[ -d "$HOME/.claude" ]]; then
    DOCKER_ARGS+=(-v "$HOME/.claude:/root/.claude:ro")
fi
if [[ -d "$HOME/.codex" ]]; then
    DOCKER_ARGS+=(-v "$HOME/.codex:/root/.codex:ro")
fi
if [[ -d "$HOME/.config/opencode" ]]; then
    DOCKER_ARGS+=(-v "$HOME/.config/opencode:/root/.config/opencode:ro")
fi

# Mount real CLI binaries (opt-in)
if [[ "$MOUNT_CLIS" -eq 1 ]]; then
    for cli in claude codex opencode; do
        cli_path="$(command -v "$cli" 2>/dev/null || true)"
        if [[ -n "$cli_path" ]]; then
            echo "Mounting real $cli from $cli_path"
            DOCKER_ARGS+=(-v "$cli_path:/usr/local/bin/$cli")
        fi
    done
fi

# ── Run ─────────────────────────────────────────────────────────────────────
echo "==> Running ${IMAGE_NAME}..."
exec docker run "${DOCKER_ARGS[@]}" "$IMAGE_NAME" "$@"
