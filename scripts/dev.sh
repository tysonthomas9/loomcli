#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$REPO_ROOT/internal/webui/frontend"

AIR_PID=""
VITE_PID=""
DAEMON_STARTED=""

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
    if [[ -n "$DAEMON_STARTED" ]]; then
        bd daemon stop 2>/dev/null || true
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

# Start bd daemon if not running
if ! bd daemon status >/dev/null 2>&1; then
    echo "Starting bd daemon..."
    bd daemon start
    DAEMON_STARTED=1
fi

# Install node_modules if missing
if [[ ! -d "$FRONTEND_DIR/node_modules" ]]; then
    echo "Installing frontend dependencies..."
    (cd "$FRONTEND_DIR" && npm install)
fi

echo ""
echo "Starting loom dev environment..."
echo "  API:      http://localhost:8080"
echo "  Frontend: http://localhost:3000"
echo ""

# Start air (Go hot-reload) from repo root
cd "$REPO_ROOT"
air &
AIR_PID=$!

# Start Vite dev server from frontend dir
cd "$FRONTEND_DIR"
npm run dev &
VITE_PID=$!

# Register trap after PIDs are set to avoid race condition
trap cleanup EXIT

# Wait for either process to exit
wait "$AIR_PID" "$VITE_PID" 2>/dev/null || true
