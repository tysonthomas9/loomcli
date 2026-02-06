#!/bin/bash
# claude-merge.sh - Use Claude to merge a branch and resolve conflicts
#
# Usage:
#   ./claude-merge.sh <source-branch> [target-branch]
#   ./claude-merge.sh all [target-branch]
#
# Examples:
#   ./claude-merge.sh webui/falcon                    # Merge to feature/web-ui (default)
#   ./claude-merge.sh webui/falcon feature/web-ui     # Explicit target
#   ./claude-merge.sh webui/falcon main               # Merge to main
#   ./claude-merge.sh all                             # Merge all worktrees to feature/web-ui
#   ./claude-merge.sh all main                        # Merge all worktrees to main
#
# The script will:
#   1. Checkout target branch
#   2. Attempt merge from source branch(es)
#   3. If conflicts, launch Claude to resolve them
#   4. Complete the merge and push

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

SOURCE_BRANCH="$1"
TARGET_BRANCH="${2:-feature/web-ui}"

if [ -z "$SOURCE_BRANCH" ]; then
    echo "Usage: ./claude-merge.sh <source-branch|all> [target-branch]"
    echo ""
    echo "Examples:"
    echo "  ./claude-merge.sh webui/falcon                  # Merge to feature/web-ui"
    echo "  ./claude-merge.sh webui/falcon main             # Merge to main"
    echo "  ./claude-merge.sh all                           # Merge all worktrees to feature/web-ui"
    echo "  ./claude-merge.sh all main                      # Merge all worktrees to main"
    exit 1
fi

# Function to merge a single branch
merge_branch() {
    local source="$1"
    local target="$2"

    echo "========================================="
    echo "Merge: $source → $target"
    echo "========================================="

    # Fetch latest
    git fetch origin

    # Checkout target branch
    echo "Checking out $target..."
    git checkout "$target"
    git pull origin "$target"

    # Check if source has any commits not in target
    if [ -z "$(git log $target..origin/$source --oneline 2>/dev/null)" ]; then
        echo "✓ Already up to date (no new commits in $source)"
        return 0
    fi

    # Attempt merge
    echo "Attempting merge from $source..."
    if git merge "origin/$source" -m "Merge $source into $target

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"; then
        echo "✓ Merge completed successfully (no conflicts)"
        git push origin "$target"
        echo "✓ Pushed to origin/$target"
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

You are resolving merge conflicts for: $source → $target

### Conflicted Files
The following files have conflicts:
$CONFLICTS

### Step 1: Understand the Conflict
For each conflicted file:
- Read the file to see the conflict markers (<<<<<<, =======, >>>>>>>)
- Understand what changes came from each branch
- The HEAD section is from $target (current branch)
- The incoming section is from $source (being merged)

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
git commit -m \"Resolve merge conflicts: $source → $target

Conflicts resolved in:
$CONFLICTS

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>\"
git push origin $target
\`\`\`

### Step 5: Verify
- Run 'git status' to confirm clean working tree
- Confirm push succeeded

### CRITICAL: Do Not Leave Conflicts
- Every conflict marker must be removed
- The code must compile/build
- If you cannot resolve a conflict, explain why and do NOT commit
"
}

# Handle "all" - merge all worktrees
if [ "$SOURCE_BRANCH" = "all" ]; then
    echo "========================================="
    echo "Merging all worktrees → $TARGET_BRANCH"
    echo "========================================="
    echo ""

    # Collect all worktree branches
    BRANCHES=()
    for dir in "$SCRIPT_DIR/worktrees"/*/; do
        if [ -d "$dir" ]; then
            name=$(basename "$dir")
            # Get the branch name for this worktree
            branch=$(cd "$dir" && git branch --show-current)
            if [ -n "$branch" ]; then
                BRANCHES+=("$branch")
                echo "Found: $name → $branch"
            fi
        fi
    done

    echo ""
    echo "Will merge ${#BRANCHES[@]} branches into $TARGET_BRANCH"
    echo ""

    # Merge each branch
    for branch in "${BRANCHES[@]}"; do
        merge_branch "$branch" "$TARGET_BRANCH" || true
        echo ""
    done

    echo "========================================="
    echo "All worktrees merged into $TARGET_BRANCH!"
    echo "========================================="
else
    # Single branch merge
    merge_branch "$SOURCE_BRANCH" "$TARGET_BRANCH"
fi
