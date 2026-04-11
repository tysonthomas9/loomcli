#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$REPO_ROOT/internal/webui/frontend"

AIR_PID=""
BUILD_WATCH_PID=""
DAEMON_STARTED=""

cleanup() {
    echo ""
    echo "Shutting down..."
    if [[ -n "$BUILD_WATCH_PID" ]]; then
        kill -TERM -"$BUILD_WATCH_PID" 2>/dev/null || kill "$BUILD_WATCH_PID" 2>/dev/null || true
        wait "$BUILD_WATCH_PID" 2>/dev/null || true
    fi
    if [[ -n "$AIR_PID" ]]; then
        kill -TERM -"$AIR_PID" 2>/dev/null || kill "$AIR_PID" 2>/dev/null || true
        wait "$AIR_PID" 2>/dev/null || true
    fi
    if [[ -n "$DAEMON_STARTED" ]]; then
        bd daemon stop 2>/dev/null || true
    fi
}

check_deps() {
    if ! command -v air >/dev/null 2>&1; then
        echo "Error: air not found."
        echo "Install: go install github.com/air-verse/air@latest"
        exit 1
    fi
    if ! command -v node >/dev/null 2>&1; then
        echo "Error: node not found. Install Node.js >= 20"
        exit 1
    fi
    if ! command -v npm >/dev/null 2>&1; then
        echo "Error: npm not found."
        exit 1
    fi
}

check_deps

if ! bd daemon status >/dev/null 2>&1; then
    echo "Starting bd daemon..."
    bd daemon start
    DAEMON_STARTED=1
fi

if [[ ! -d "$FRONTEND_DIR/node_modules" ]]; then
    echo "Installing frontend dependencies..."
    (cd "$FRONTEND_DIR" && npm install)
fi

# Build the frontend dist once so the Vite preview / static host has something
# to serve alongside the API server. loom serve itself no longer embeds or
# serves static files — it's a pure JSON API.
echo "Building frontend once..."
(cd "$FRONTEND_DIR" && npm run build >/dev/null)

echo ""
echo "Starting loom dev environment (disk-served frontend)..."
echo "  Web UI:   http://localhost:8080"
echo "  API:      http://localhost:8081"
echo "  Frontend: auto-rebuilt to internal/webui/frontend/dist"
echo ""

trap cleanup EXIT

# Keep dist/ fresh on every frontend file change.
# Avoid emptying dist/ between watch rebuilds so go:embed never sees an empty directory.
cd "$FRONTEND_DIR"
npm run build -- --watch --emptyOutDir false &
BUILD_WATCH_PID=$!

# Run loom serve through air for Go/backend hot-restart.
cd "$REPO_ROOT"
air &
AIR_PID=$!

# Keep make/dev attached to the backend lifecycle:
# if air exits (build error/crash), stop watch process and exit non-zero.
wait "$AIR_PID"
