#!/bin/bash
# claude-reset.sh - Reset worktrees to a specific branch
#
# Usage:
#   ./claude-reset.sh <worktree> [branch]       # Reset one worktree
#   ./claude-reset.sh all [branch]              # Reset all worktrees
#
# Examples:
#   ./claude-reset.sh falcon                    # Reset falcon to feature/web-ui
#   ./claude-reset.sh falcon main               # Reset falcon to main
#   ./claude-reset.sh all                       # Reset all worktrees to feature/web-ui
#   ./claude-reset.sh all main                  # Reset all worktrees to main
#
# WARNING: This discards ALL local changes in the worktree(s)!

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

WORKTREE="$1"
TARGET_BRANCH="${2:-feature/web-ui}"

if [ -z "$WORKTREE" ]; then
    echo "Usage: ./claude-reset.sh <worktree|all> [branch]"
    echo ""
    echo "Examples:"
    echo "  ./claude-reset.sh falcon                    # Reset falcon to feature/web-ui"
    echo "  ./claude-reset.sh falcon main               # Reset falcon to main"
    echo "  ./claude-reset.sh all                       # Reset all worktrees to feature/web-ui"
    echo "  ./claude-reset.sh all main                  # Reset all worktrees to main"
    echo ""
    echo "WARNING: This discards ALL local changes!"
    exit 1
fi

reset_worktree() {
    local name="$1"
    local target="$2"
    local worktree_path="$SCRIPT_DIR/worktrees/$name"

    if [ ! -d "$worktree_path" ]; then
        echo "Error: Worktree '$name' not found at $worktree_path"
        return 1
    fi

    echo "========================================="
    echo "Resetting: $name → $target"
    echo "========================================="

    cd "$worktree_path"

    # Get current branch name
    current_branch=$(git branch --show-current)

    # Fetch latest
    git fetch origin

    # Discard any local changes
    echo "Discarding local changes..."
    git reset --hard HEAD
    git clean -fd

    # Reset to target branch
    echo "Resetting to origin/$target..."
    git reset --hard "origin/$target"

    # Force push to update remote branch
    echo "Force pushing to origin/$current_branch..."
    git push origin "$current_branch" --force

    echo "✓ Reset complete: $name is now at origin/$target"
    echo "  Branch: $current_branch (force pushed)"

    cd "$SCRIPT_DIR"
}

# Handle "all" worktrees
if [ "$WORKTREE" = "all" ]; then
    echo "========================================="
    echo "Resetting ALL worktrees → $TARGET_BRANCH"
    echo "========================================="
    echo ""
    echo "⚠ WARNING: This will discard ALL local changes in ALL worktrees!"
    echo ""

    # List what will be reset
    for dir in "$SCRIPT_DIR/worktrees"/*/; do
        if [ -d "$dir" ]; then
            name=$(basename "$dir")
            branch=$(cd "$dir" && git branch --show-current)
            echo "  - $name ($branch)"
        fi
    done

    echo ""
    read -p "Are you sure? (y/N) " -n 1 -r
    echo ""

    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Aborted."
        exit 1
    fi

    echo ""

    for dir in "$SCRIPT_DIR/worktrees"/*/; do
        if [ -d "$dir" ]; then
            name=$(basename "$dir")
            reset_worktree "$name" "$TARGET_BRANCH" || true
            echo ""
        fi
    done

    echo "========================================="
    echo "All worktrees reset to $TARGET_BRANCH!"
    echo "========================================="
else
    # Single worktree reset
    echo ""
    echo "⚠ WARNING: This will discard ALL local changes in '$WORKTREE'!"
    read -p "Are you sure? (y/N) " -n 1 -r
    echo ""

    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Aborted."
        exit 1
    fi

    echo ""
    reset_worktree "$WORKTREE" "$TARGET_BRANCH"
fi
