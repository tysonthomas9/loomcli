#!/usr/bin/env bash
set -euo pipefail

# Pre-commit hook for frontend files.
# Receives staged file paths as arguments from the pre-commit framework,
# filters to frontend src/ files, and runs prettier + eslint on them.

REPO_ROOT="$(git rev-parse --show-toplevel)"
FRONTEND_DIR="$REPO_ROOT/internal/webui/frontend"

# Filter args to only frontend src/ files
frontend_files=()
ts_tsx_files=()
for file in "$@"; do
    if [[ "$file" == internal/webui/frontend/src/*.ts ]] ||
       [[ "$file" == internal/webui/frontend/src/*.tsx ]] ||
       [[ "$file" == internal/webui/frontend/src/*.css ]]; then
        rel="${file#internal/webui/frontend/}"
        frontend_files+=("$rel")
        if [[ "$file" == *.ts || "$file" == *.tsx ]]; then
            ts_tsx_files+=("$rel")
        fi
    fi
done

if [ ${#frontend_files[@]} -eq 0 ]; then
    exit 0
fi

cd "$FRONTEND_DIR"

# Ensure node_modules exist
if [ ! -d "node_modules" ]; then
    echo "Error: node_modules not found. Run: cd internal/webui/frontend && npm install" >&2
    exit 1
fi

exit_code=0

# Check formatting
if ! npx prettier --check "${frontend_files[@]}"; then
    echo "Fix with: cd internal/webui/frontend && npx prettier --write src/" >&2
    exit_code=1
fi

# Lint TS/TSX files only
if [ ${#ts_tsx_files[@]} -gt 0 ]; then
    if ! npx eslint "${ts_tsx_files[@]}"; then
        echo "Fix with: cd internal/webui/frontend && npx eslint src/ --fix" >&2
        exit_code=1
    fi
fi

exit $exit_code
