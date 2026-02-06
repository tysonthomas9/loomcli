#!/bin/bash
# Install git hooks by setting core.hooksPath to .beads-hooks/
#
# This script:
# 1. Sets core.hooksPath to .beads-hooks/ (works in main repo and worktrees)
# 2. Removes any Husky-based core.hooksPath if previously set
# 3. Works correctly in both main repo and worktrees

set -e

# Find repo root (works in worktrees too)
REPO_ROOT="$(git rev-parse --show-toplevel)"
HOOKS_DIR=".beads-hooks"

# Check if hooks directory exists
if [ ! -d "$REPO_ROOT/$HOOKS_DIR" ]; then
    echo "Error: $HOOKS_DIR directory not found at $REPO_ROOT"
    exit 1
fi

# Get current hooks path (may be Husky or something else)
CURRENT_HOOKS_PATH=$(git config --get core.hooksPath 2>/dev/null || echo "")

# Check if we're replacing Husky
if [ -n "$CURRENT_HOOKS_PATH" ] && echo "$CURRENT_HOOKS_PATH" | grep -q ".husky"; then
    echo "Replacing Husky hooks path: $CURRENT_HOOKS_PATH"
fi

# Set core.hooksPath to .beads-hooks/ (relative path works from any worktree)
echo "Setting core.hooksPath to $HOOKS_DIR..."
git config core.hooksPath "$HOOKS_DIR"

# Verify the hooks are executable
for hook in "$REPO_ROOT/$HOOKS_DIR"/*; do
    if [ -f "$hook" ] && [ ! -x "$hook" ]; then
        chmod +x "$hook"
        echo "  Made executable: $(basename "$hook")"
    fi
done

echo "✓ Git hooks installed successfully"
echo ""
echo "Hooks directory: $HOOKS_DIR"
echo "Active hooks:"
ls -1 "$REPO_ROOT/$HOOKS_DIR" | grep -v "^\." || true
echo ""
echo "Note: This configuration is stored in .git/config and applies to this checkout."
echo "Worktrees share this config automatically."
