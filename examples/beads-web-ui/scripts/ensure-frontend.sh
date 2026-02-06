#!/usr/bin/env bash
#
# ensure-frontend.sh - Auto-rebuild frontend dist when stale or missing.
# Called by air pre_cmd, Makefile targets, or manually.
#
set -euo pipefail

# Resolve to beads-web-ui root regardless of where the script is called from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$PROJECT_DIR/frontend"

DIST_MARKER="$FRONTEND_DIR/dist/index.html"
PKG_JSON="$FRONTEND_DIR/package.json"
PKG_LOCK_MARKER="$FRONTEND_DIR/node_modules/.package-lock.json"

# Check that npm is available.
if ! command -v npm &>/dev/null; then
    echo "Error: npm is required to build the frontend. Install Node.js from https://nodejs.org" >&2
    exit 1
fi

cd "$FRONTEND_DIR"

# Ensure node_modules is installed.
need_install=false
if [ ! -d "node_modules" ]; then
    need_install=true
elif [ "$PKG_JSON" -nt "$PKG_LOCK_MARKER" ] 2>/dev/null; then
    need_install=true
fi

if [ "$need_install" = true ]; then
    echo "Installing frontend dependencies..."
    npm install
fi

# Check if dist needs to be (re)built.
if [ ! -f "$DIST_MARKER" ]; then
    echo "Frontend dist missing, building..."
    npm run build
    echo "Frontend build complete."
    exit 0
fi

# Check if any frontend source file is newer than dist/index.html.
# Checks src/, vite.config.ts, tsconfig files, index.html, and public/.
if [ -n "$(find "$FRONTEND_DIR/src" "$FRONTEND_DIR/public" \
    "$FRONTEND_DIR/vite.config.ts" "$FRONTEND_DIR/tsconfig.json" \
    "$FRONTEND_DIR/tsconfig.node.json" "$FRONTEND_DIR/index.html" \
    -newer "$DIST_MARKER" -type f -print -quit 2>/dev/null)" ]; then
    echo "Frontend sources changed, rebuilding..."
    npm run build
    echo "Frontend build complete."
    exit 0
fi

# Also rebuild if package.json changed (new dependencies may affect output).
if [ "$PKG_JSON" -nt "$DIST_MARKER" ] 2>/dev/null; then
    echo "package.json changed, rebuilding frontend..."
    npm run build
    echo "Frontend build complete."
    exit 0
fi

echo "Frontend is up to date."
