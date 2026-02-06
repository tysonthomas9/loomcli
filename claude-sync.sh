#!/bin/bash
# claude-sync.sh - Sync worktrees with integration branch
#
# Usage:
#   ./claude-sync.sh <worktree>           # Sync one worktree with feature/web-ui
#   ./claude-sync.sh <worktree> <branch>  # Sync with specific branch
#   ./claude-sync.sh all                  # Sync all worktrees
#   ./claude-sync.sh all main             # Sync all worktrees with main
#
# Examples:
#   ./claude-sync.sh falcon               # Sync falcon with feature/web-ui
#   ./claude-sync.sh falcon main          # Sync falcon with main
#   ./claude-sync.sh all                  # Sync all worktrees with feature/web-ui

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

WORKTREE="$1"
SOURCE_BRANCH="${2:-feature/web-ui}"

if [ -z "$WORKTREE" ]; then
    echo "Usage: ./claude-sync.sh <worktree|all> [source-branch]"
    echo ""
    echo "Examples:"
    echo "  ./claude-sync.sh falcon               # Sync falcon with feature/web-ui"
    echo "  ./claude-sync.sh falcon main          # Sync falcon with main"
    echo "  ./claude-sync.sh all                  # Sync all worktrees"
    exit 1
fi

sync_worktree() {
    local name="$1"
    local source="$2"
    local worktree_path="$SCRIPT_DIR/worktrees/$name"

    if [ ! -d "$worktree_path" ]; then
        echo "Error: Worktree '$name' not found at $worktree_path"
        return 1
    fi

    echo "========================================="
    echo "Syncing: $name <- $source"
    echo "========================================="

    cd "$worktree_path"

    # Fetch latest
    git fetch origin

    # Get current branch
    current_branch=$(git branch --show-current)

    # Attempt merge
    echo "Merging $source into $current_branch..."
    if git merge "origin/$source" -m "Sync with $source

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"; then
        echo "✓ Sync completed successfully (no conflicts)"
        git push origin "$current_branch"
        echo "✓ Pushed to origin/$current_branch"
        cd "$SCRIPT_DIR"
        return 0
    fi

    # If we get here, there are conflicts
    echo ""
    echo "⚠ Merge conflicts detected. Launching Claude to resolve..."
    echo ""

    # Get list of conflicted files
    CONFLICTS=$(git diff --name-only --diff-filter=U)
    echo "Conflicted files:"
    echo "$CONFLICTS"
    echo ""

    claude --dangerously-skip-permissions "
## WORKFLOW: Resolve Merge Conflicts

You are resolving merge conflicts for syncing worktree '$name' with $source.

Current branch: $current_branch
Source branch: $source

### Conflicted Files
The following files have conflicts:
$CONFLICTS

### Step 1: Understand the Conflict
For each conflicted file:
- Read the file to see the conflict markers (<<<<<<, =======, >>>>>>>)
- Understand what changes came from each branch
- The HEAD section is from $current_branch (worktree branch)
- The incoming section is from $source (integration branch)

### Step 2: Resolve Each Conflict
For each conflicted file:
- Determine the correct resolution (keep one side, combine both, or write new code)
- Edit the file to remove ALL conflict markers
- Ensure the resulting code is syntactically correct
- Ensure the logic makes sense with both sets of changes integrated

### Step 3: Verify Resolution
- Run any relevant build commands to ensure code compiles
- Run tests if available
- Check that no conflict markers remain: grep -r '<<<<<<' . or grep -r '>>>>>>>' .

### Step 4: Complete the Merge
Once all conflicts are resolved:
\`\`\`bash
git add -A
git commit -m \"Sync with $source - resolve conflicts

Conflicts resolved in:
$CONFLICTS

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>\"
git push origin $current_branch
\`\`\`

### Step 5: Verify
- Run 'git status' to confirm clean working tree
- Confirm push succeeded

### CRITICAL: Do Not Leave Conflicts
- Every conflict marker must be removed
- The code must compile/build
- If you cannot resolve a conflict, explain why and do NOT commit
"

    cd "$SCRIPT_DIR"
}

# Handle "all" worktrees
if [ "$WORKTREE" = "all" ]; then
    echo "Syncing all worktrees with $SOURCE_BRANCH..."
    echo ""

    for dir in "$SCRIPT_DIR/worktrees"/*/; do
        if [ -d "$dir" ]; then
            name=$(basename "$dir")
            sync_worktree "$name" "$SOURCE_BRANCH" || true
            echo ""
        fi
    done

    echo "========================================="
    echo "All worktrees synced!"
    echo "========================================="
else
    sync_worktree "$WORKTREE" "$SOURCE_BRANCH"
fi
