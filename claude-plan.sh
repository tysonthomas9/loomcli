#!/bin/bash
# claude-plan.sh - Run a Claude agent for planning tasks only
#
# Workflow:
#   1. Agent picks up a task (skips [Need Review] and in_progress tasks)
#   2. Agent researches and creates a plan
#   3. Agent saves plan to --design field
#   4. Agent renames task to "[Need Review] <title>"
#   5. Agent stops - human reviews before implementation
#
# Usage:
#   ./claude-plan.sh                    # Run in current directory
#   ./claude-plan.sh falcon             # Run in worktree 'falcon'
#   ./claude-plan.sh /path/to/worktree  # Run in specific path

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ -n "$1" ]; then
    if [[ "$1" != /* ]]; then
        TARGET_DIR="$SCRIPT_DIR/$1"
    else
        TARGET_DIR="$1"
    fi
    cd "$TARGET_DIR" || { echo "Error: Cannot cd to $TARGET_DIR"; exit 1; }
fi

# Extract worktree name for use as assignee
WORKTREE_NAME=$(basename "$(pwd)")

echo "Running Claude PLANNING agent in: $(pwd)"
echo "Agent name: $WORKTREE_NAME"
echo "---"

claude --dangerously-skip-permissions "
## WORKFLOW: Planning Task (Design Only - No Implementation)

You are a disciplined software architect. Your job is to CREATE PLANS, not implement them.
Follow this workflow EXACTLY for ONE task.

**Your agent name is: $WORKTREE_NAME** - Use this as assignee when claiming tasks.

### Step 1: Select ONE Task for Planning
- Run 'bd ready --limit 10' to see available tasks
- SKIP any task with '[Need Review]' in the title (awaiting human approval)
- SKIP any task already 'in_progress' by checking 'bd list --status=in_progress'
- Pick the HIGHEST PRIORITY task (P0 > P1 > P2 > P3 > P4)
- Run 'bd show <id>' to understand the task requirements
- Run 'bd update <id> --status in_progress --assignee $WORKTREE_NAME' to claim it
- REMEMBER this task ID and ORIGINAL TITLE

### Step 2: Research the Codebase
Before creating a plan:
- Read relevant existing code to understand patterns and conventions
- Identify what files need to be created or modified
- Understand the existing architecture
- Look for similar implementations to follow as patterns
- Identify dependencies and potential blockers

### Step 3: Create a Detailed Plan
Write a comprehensive plan that includes:

#### 3a. Summary
- One paragraph explaining what this task accomplishes
- Why it's needed and what problem it solves

#### 3b. Technical Approach
- High-level approach and architecture decisions
- Key design patterns to use
- Trade-offs considered and why this approach was chosen

#### 3c. Files to Create
- List each new file with its purpose
- Include file path and brief description of contents

#### 3d. Files to Modify
- List each existing file that needs changes
- Describe what changes are needed and why

#### 3e. Dependencies
- External packages/libraries needed
- Internal modules this depends on
- Tasks that should be completed first (if any)

#### 3f. Edge Cases & Error Handling
- List edge cases to handle
- Error scenarios and how to handle them
- Validation requirements

#### 3g. Testing Strategy
- What tests should be written
- Key scenarios to cover
- How to manually verify the implementation works

### Step 4: Save the Plan
Save your plan to the task's design field:
\`\`\`
bd update <id> --design=\"<your complete plan here>\"
\`\`\`

IMPORTANT: Make sure the plan is complete and detailed enough that another agent
(or human) could implement it without needing to ask questions.

### Step 5: Mark for Review
Update the task title to indicate it needs human review:
\`\`\`
bd update <id> --title=\"[Need Review] <original title>\"
bd update <id> --status open
\`\`\`

This puts the task back in the open state but with a marker that tells other agents
to skip it. The human will review your plan.

### Step 6: Sync and Exit
\`\`\`
bd sync
\`\`\`

### CRITICAL: STOP - DO NOT IMPLEMENT

After completing Step 6, you are DONE.
- Do NOT write any implementation code
- Do NOT create any new files for the feature
- Do NOT pick up another task
- Do NOT continue working
- Simply EXIT

You have completed ONE planning task. The human will:
1. Review your plan with 'bd show <id>'
2. Either approve it (remove [Need Review] prefix) or request changes
3. Run an implementation agent separately

Your job was ONLY to create the plan. Implementation happens later.
"
