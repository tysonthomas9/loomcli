#!/usr/bin/env bash
#
# scripts/dev.sh — single dev entry point post-Phase 5.
#
# Starts the Go API server (via air, on :8080) and the Vite dev server
# (on :3000) in parallel. Vite proxies /api/* and /health → :8080, so the
# browser sees a same-origin app at http://localhost:3000.
#
# CORS-mode dev: set VITE_API_BASE_URL=http://localhost:8080 to bundle
# absolute URLs and exercise CORS preflights. .air.toml passes
# `--frontend-url http://localhost:3000` so the Go server allows the
# Vite dev origin in that mode (inert when same-origin).
#
# Cleanup is handled via a trap on EXIT — Ctrl-C kills both processes.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$REPO_ROOT/internal/webui/frontend"

AIR_PID=""
VITE_PID=""

cleanup() {
    echo ""
    echo "Shutting down..."
    # Kill process groups (negative PID) to clean up child processes
    if [[ -n "$VITE_PID" ]]; then
        kill -TERM -"$VITE_PID" 2>/dev/null || kill "$VITE_PID" 2>/dev/null || true
        wait "$VITE_PID" 2>/dev/null || true
    fi
    if [[ -n "$AIR_PID" ]]; then
        kill -TERM -"$AIR_PID" 2>/dev/null || kill "$AIR_PID" 2>/dev/null || true
        wait "$AIR_PID" 2>/dev/null || true
    fi
}

# Preflight checks
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

export LOOM_ISSUE_BACKEND="${LOOM_ISSUE_BACKEND:-fleetdb}"

# Install frontend deps when out of sync. A bare existence check on
# node_modules/ misses the case where package.json or package-lock.json
# was updated since the last install (e.g., after `git pull`), leaving
# Vite to fail at import time on a missing dependency.
node_modules_marker="$FRONTEND_DIR/node_modules/.package-lock.json"
if [[ ! -d "$FRONTEND_DIR/node_modules" ]] \
    || [[ ! -f "$node_modules_marker" ]] \
    || [[ "$FRONTEND_DIR/package-lock.json" -nt "$node_modules_marker" ]] \
    || [[ "$FRONTEND_DIR/package.json" -nt "$node_modules_marker" ]]; then
    echo "Installing frontend dependencies..."
    (cd "$FRONTEND_DIR" && npm install)
fi

echo ""
echo "Starting loom dev environment..."
echo "  API:      http://localhost:8080"
echo "  Frontend: http://localhost:3000"
echo "  Note:     /api/* and /health are proxied through Vite → :8080"
echo "  Backend:  ${LOOM_ISSUE_BACKEND}"
echo ""

trap cleanup EXIT

# Start air (Go hot-reload) from repo root
cd "$REPO_ROOT"
air &
AIR_PID=$!

# Start Vite dev server from frontend dir
cd "$FRONTEND_DIR"
npm run dev &
VITE_PID=$!

# Wait for either process to exit
wait "$AIR_PID" "$VITE_PID" 2>/dev/null || true
