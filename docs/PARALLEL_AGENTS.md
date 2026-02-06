# Running Parallel Claude Code Agents with Beads

This document captures learnings from setting up parallel AI agent workflows using beads (`bd`) for task management and git worktrees for isolation.

## Overview

When running multiple Claude Code agents in parallel:
1. **Git worktrees** provide isolated working directories on separate branches
2. **Beads** provides shared task database with dependency management
3. **Blocking dependencies** ensure tasks are worked in correct order
4. **Priority-based selection** lets agents pick the most important ready work
5. **Human-in-the-loop review** via `[Need Review]` prefix pattern
6. **Agent locking** prevents running multiple agents in the same worktree

## vibecli - Agent Management CLI

The `vibecli` command provides a unified interface for managing parallel Claude agents:

```bash
# Build vibecli
go build -o vibecli ./cmd/vibecli

# Or add to your PATH
go install ./cmd/vibecli
```

### Commands

| Command | Purpose | Human Review |
|---------|---------|--------------|
| `vibecli plan [worktree]` | Creates detailed plans, marks tasks `[Need Review]` | Required before implementation |
| `vibecli task [worktree]` | Implements tasks (skips `[Need Review]` tasks) | After completion |
| `vibecli merge <source> [target]` | Merges branches with AI conflict resolution | After merge |
| `vibecli sync <worktree> [branch]` | Syncs worktrees with integration branch | None |
| `vibecli reset <worktree> [branch]` | Resets worktrees to a specific branch | Confirmation required |
| `vibecli list` | Lists all agents and their status | None |
| `vibecli monitor` | Comprehensive dashboard with agents, tasks, sync, stats | None |

### Shell Tab Completion

Enable tab completion for commands, worktree names, and branches:

```bash
# Bash (add to ~/.bashrc)
source <(vibecli completion bash)

# Zsh (add to ~/.zshrc)
source <(vibecli completion zsh)

# Fish
vibecli completion fish | source
```

After enabling, you can tab-complete:
- Commands: `vibecli p<TAB>` → `plan`
- Worktrees: `vibecli plan f<TAB>` → `falcon`
- Branches: `vibecli merge <TAB>` → branch names

### Agent Locking

vibecli automatically prevents running multiple agents in the same worktree:

```bash
# First agent starts successfully
$ vibecli plan falcon &

# Second agent fails immediately
$ vibecli task falcon
Error: agent already running: plan (PID 12345, started 5m ago)
```

The lock is automatically released when the agent exits. Stale locks from crashed processes are detected and overwritten.

Check running agents with:
```bash
$ vibecli list
Agents (Worktrees):
-------------------
  cobalt        webui/cobalt          ✓ clean
  falcon        webui/falcon          ● running (plan, 5m ago)
  nova          webui/nova            ✓ clean
```

### The Review Gate Pattern

```
Task: "Add authentication"
        ↓ vibecli plan
[Need Review] Add authentication    ← Agent saves plan to --design field
        ↓ Human reviews & approves (removes [Need Review] prefix)
Add authentication                  ← Ready for implementation
        ↓ vibecli task
Closed: Add authentication
```

### Why Separate Plan and Task Commands?

1. **Quality control** - Humans review plans before code is written
2. **No wasted work** - Catch design issues before implementation
3. **Clear handoff** - `[Need Review]` in title = human action required
4. **Race condition prevention** - Implementation agents skip review tasks

## Setup

### 1. Create Git Worktrees

```bash
# From your main repo, create worktrees for each agent
bd worktree create falcon --branch work/falcon
bd worktree create cobalt --branch work/cobalt
bd worktree create nova --branch work/nova
bd worktree create ember --branch work/ember
bd worktree create zephyr --branch work/zephyr

# List all worktrees
bd worktree list
```

**What this does:**
- Creates a new directory for each worktree
- Sets up `.beads/redirect` so all worktrees share the same database
- Each worktree has its own branch for isolated code changes

### 2. Create Task Structure with Dependencies

#### Phase Epics

Create epics for each phase of work with appropriate priorities:

```bash
bd create "Phase 1: Setup" -t epic -p 0
bd create "Phase 2: API Layer" -t epic -p 0
bd create "Phase 3: Feature A" -t epic -p 1
bd create "Phase 4: Feature B" -t epic -p 1
# ... etc
```

#### Parent-Child Relationships

Make tasks children of their phase epic:

```bash
# Add parent-child dependency (task belongs to epic)
bd dep add <task-id> <epic-id> --type parent-child
```

#### Blocking Dependencies Between Phases

```bash
# Phase 2 blocked by Phase 1
bd dep add <phase2-epic> <phase1-epic> --type blocks

# Phase 3 blocked by Phase 2
bd dep add <phase3-epic> <phase2-epic> --type blocks
```

#### Blocking Dependencies Within Phases

For sequential tasks within a phase:

```bash
# T002 blocked by T001
bd dep add <t002-id> <t001-id> --type blocks

# T003 blocked by T001 (can run parallel with T002)
bd dep add <t003-id> <t001-id> --type blocks

# T004 blocked by T002
bd dep add <t004-id> <t002-id> --type blocks
```

### 3. Labels for Organization

Add phase labels for filtering:

```bash
bd label add <task-id> phase-1
bd label add <task-id> phase-2
# ... etc
```

## Agent Commands

### Planning: `vibecli plan`

Use this to have agents create detailed plans that you review before implementation:

```bash
vibecli plan falcon    # Run planning agent in falcon worktree
vibecli plan           # Run in current directory
```

**What it does:**
1. Acquires agent lock (prevents concurrent agents)
2. Picks up a task (skips `[Need Review]` tasks)
3. Researches codebase and creates detailed plan
4. Saves plan to task's `--design` field
5. Renames task to `[Need Review] <original title>`
6. Sets status back to `open` and exits

**After planning agent completes:**
```bash
# Review the plan
bd show <task-id>

# If approved - remove [Need Review] prefix
bd update <task-id> --title="<original title>"

# If changes needed - add a comment
bd comment <task-id> "Please reconsider the approach for X"
bd update <task-id> --title="<original title>"  # Remove [Need Review] to let agent retry
```

### Implementation: `vibecli task`

Use this for tasks that are ready for implementation (no `[Need Review]` prefix):

```bash
vibecli task falcon    # Run implementation agent in falcon worktree
vibecli task           # Run in current directory
```

**What it does:**
1. Acquires agent lock (prevents concurrent agents)
2. Picks up a task (SKIPS any with `[Need Review]` in title)
3. Checks for `--design` field and follows that plan if present
4. Implements, tests, reviews code
5. Commits and pushes
6. Closes task and exits

### Merging: `vibecli merge`

Use this to merge worktree branches into the integration branch with AI-assisted conflict resolution:

```bash
vibecli merge webui/falcon                  # Merge to feature/web-ui (default target)
vibecli merge webui/falcon feature/web-ui   # Explicit target branch
vibecli merge webui/cobalt main             # Merge directly to main
vibecli merge --all                         # Merge ALL worktrees to feature/web-ui
vibecli merge --all main                    # Merge ALL worktrees to main
```

**What it does:**
1. Fetches latest from origin
2. Checks out the target branch and pulls
3. Attempts merge from source branch
4. If no conflicts: commits and pushes automatically
5. If conflicts: launches Claude to resolve them

**Conflict resolution workflow:**
1. Claude reads each conflicted file to understand the conflict markers
2. Determines correct resolution (keep one side, combine both, or write new code)
3. Edits files to remove ALL conflict markers
4. Verifies code compiles and no markers remain
5. Commits and pushes the resolution

**Example branch hierarchy:**
```
main (production)
  └── feature/web-ui (integration branch - main folder)
        ├── webui/falcon  (worktree branch)
        ├── webui/cobalt  (worktree branch)
        ├── webui/nova    (worktree branch)
        ├── webui/ember   (worktree branch)
        └── webui/zephyr  (worktree branch)
```

**Workflow:**
1. Agent completes work on `webui/falcon` branch
2. Run `vibecli merge webui/falcon` to merge to `feature/web-ui`
3. Repeat for other worktrees as they complete work
4. Eventually merge `feature/web-ui` to `main`

### Syncing: `vibecli sync`

Use this to update worktrees with the latest changes from the integration branch:

```bash
vibecli sync falcon               # Sync falcon with feature/web-ui
vibecli sync falcon main          # Sync falcon with main
vibecli sync --all                # Sync all worktrees with feature/web-ui
vibecli sync --all main           # Sync all worktrees with main
```

**What it does:**
1. Changes to the worktree directory
2. Fetches latest from origin
3. Merges the source branch into the worktree's branch
4. If conflicts: launches Claude to resolve them
5. Pushes the updated branch

**When to use:**
- After merging work into `feature/web-ui`, sync other worktrees to get those changes
- Before starting new work, ensure worktree has latest code
- After PR is merged to `main`, sync all worktrees with `main`

### Resetting: `vibecli reset`

Use this to hard reset worktrees to a specific branch, discarding all local changes:

```bash
vibecli reset falcon                    # Reset falcon to feature/web-ui
vibecli reset falcon main               # Reset falcon to main
vibecli reset --all                     # Reset all worktrees to feature/web-ui
vibecli reset --all main                # Reset all worktrees to main
vibecli reset --all --force             # Skip confirmation prompt
```

**What it does:**
1. Discards all local changes (`git reset --hard`, `git clean -fd`)
2. Resets to the target branch (`origin/feature/web-ui` or `origin/main`)
3. Force pushes the worktree branch to match

**When to use:**
- Discard failed/abandoned work and start fresh
- After a PR is merged, reset all worktrees to `main`
- Clean slate for a new feature cycle

**WARNING:** This is destructive! Requires confirmation prompt (use `--force` to skip).

### Listing Agents: `vibecli list`

View all agents and their status:

```bash
vibecli list    # or: vibecli ls
```

Shows:
- Worktree name and branch
- Status: running agent, dirty working tree, or clean
- Number of uncommitted changes

### Reviewing Tasks Awaiting Approval

```bash
# List all tasks waiting for your review
bd list --status=open --title-contains="Need Review"

# Review a specific task's plan
bd show <task-id>

# Approve (remove prefix, ready for implementation)
bd update <task-id> --title="Implement user authentication"

# Request changes (add comment, remove prefix to trigger re-planning)
bd comment <task-id> "Need to handle edge case X"
bd update <task-id> --title="Plan user authentication"
```

## Running Parallel Agents

### Planning Phase (Human Review Required)

Run planning agents to create plans for review:

```bash
# Terminal 1 - Planning agent
vibecli plan falcon

# Terminal 2 - Planning agent
vibecli plan cobalt
```

After agents complete, review their plans:

```bash
bd list --status=open --title-contains="Need Review"
bd show <task-id>  # Review the --design field
bd update <task-id> --title="<approved title without [Need Review]>"
```

### Implementation Phase (After Approval)

Once plans are approved, run implementation agents:

```bash
# Terminal 1
vibecli task falcon

# Terminal 2
vibecli task cobalt

# Terminal 3
vibecli task nova

# Terminal 4
vibecli task ember

# Terminal 5
vibecli task zephyr
```

### Mixed Workflow

You can run both types simultaneously - they won't interfere:
- Planning agents only touch tasks without `[Need Review]` prefix
- Implementation agents skip tasks with `[Need Review]` prefix
- Agent locking prevents running two agents in the same worktree

## Key Commands

### Task Selection
```bash
bd ready                     # Show unblocked tasks sorted by priority
bd ready --limit 10          # Limit results
bd list --status=in_progress # See claimed tasks
bd blocked                   # See what's waiting on dependencies
```

### Review Workflow
```bash
bd list --title-contains="Need Review"       # Tasks awaiting human review
bd show <id>                                  # View plan in --design field
bd update <id> --title="Approved title"      # Approve (remove [Need Review])
bd comment <id> "Feedback..."                # Request changes
```

### Task Management
```bash
bd update <id> --status in_progress  # Claim a task
bd close <id> --reason "Done"        # Complete a task
bd show <id>                         # View task details with dependencies
```

### Dependency Management
```bash
bd dep add <task> <blocker> --type blocks       # Task blocked by blocker
bd dep add <child> <parent> --type parent-child # Hierarchical relationship
```

### Checking Progress
```bash
bd stats                     # Overall statistics
bd list -l phase-1           # Tasks in a specific phase
bd list --status=closed      # Completed tasks
```

### Merging Completed Work
```bash
vibecli merge webui/falcon              # Merge falcon to feature/web-ui
vibecli merge webui/cobalt              # Merge cobalt to feature/web-ui
vibecli merge --all                     # Merge all worktrees to feature/web-ui
vibecli merge --all main                # Merge all worktrees to main
vibecli merge feature/web-ui main       # Merge integration branch to main
```

### Syncing Worktrees
```bash
vibecli sync falcon                     # Sync falcon with feature/web-ui
vibecli sync --all                      # Sync all worktrees
vibecli sync --all main                 # Sync all worktrees with main
```

### Resetting Worktrees
```bash
vibecli reset falcon                    # Reset falcon to feature/web-ui
vibecli reset --all                     # Reset all worktrees to feature/web-ui
vibecli reset --all main                # Reset all worktrees to main
```

### Checking Agent Status
```bash
vibecli list                            # Show all agents and their status
vibecli monitor                         # Comprehensive dashboard
vibecli monitor --watch                 # Auto-refresh dashboard every 5s
vibecli mon -w -i 3                     # Refresh every 3 seconds
```

### Monitor Dashboard

The `vibecli monitor` command shows a comprehensive dashboard with four sections:

```
╔════════════════════════════════════════════════════════════════════╗
║                          VIBECLI MONITOR                           ║
║                       Last updated: 14:32:15                       ║
╠════════════════════════════════════════════════════════════════════╣
║  AGENTS                                                            ║
╠════════════════════════════════════════════════════════════════════╣
║   cobalt     webui/cobalt       ✓ clean        ↑2 ↓1               ║
║   ember      webui/ember        ✓ clean        ↑2 ↓5               ║
║   falcon     webui/falcon       ● 1 changes    ↑2 ↓5               ║
╠════════════════════════════════════════════════════════════════════╣
║  TASKS                                                             ║
╠════════════════════════════════════════════════════════════════════╣
║   Ready: 4     In Progress: 0     Need Review: 2     Blocked: 66   ║
║                                                                    ║
║   READY (top 5):                                                   ║
║     [P0] bd-487: Phase 2: API Layer                                ║
║     [P2] bd-pmt: T015: POST /api/issues/:id/close endpoint         ║
║                                                                    ║
║   NEED REVIEW (top 5):                                             ║
║     [P1] bd-xyz: [Need Review] Auth design                         ║
║                                                                    ║
║   IN PROGRESS:                                                     ║
║     (none)                                                         ║
╠════════════════════════════════════════════════════════════════════╣
║  SYNC STATUS                                                       ║
╠════════════════════════════════════════════════════════════════════╣
║   Database:  ✓ synced                                              ║
║   Git:       ⚠ 5 need push, 5 need pull                            ║
╠════════════════════════════════════════════════════════════════════╣
║  STATS                                                             ║
╠════════════════════════════════════════════════════════════════════╣
║   Open: 82    Closed: 26    Total: 108   Completion: 24%           ║
╚════════════════════════════════════════════════════════════════════╝
```

**AGENTS section:**
- Shows each worktree with its branch name
- Status: `✓ clean`, `● N changes`, or `● running (command, Xm ago)`
- Sync indicators: `↑N` commits ahead, `↓N` commits behind the integration branch

**TASKS section:**
- Summary counts for ready, in_progress, need_review, and blocked tasks
- Top 5 ready tasks with priority and title
- Top 5 tasks awaiting review (with `[Need Review]` prefix)
- All in-progress tasks

**SYNC STATUS section:**
- Database sync status (beads JSONL export)
- Git sync summary (how many worktrees need push/pull)

**STATS section:**
- Overall issue counts and completion percentage

## Key Learnings

### 1. Dependencies Control Flow, Not Phase Labels

Initially we tried using phase labels to control task order. This was wrong.

**Wrong approach:**
- Check phase labels manually
- Pick tasks from lowest phase number

**Right approach:**
- Use blocking dependencies between tasks
- `bd ready` automatically shows only unblocked tasks
- Agents just pick highest priority ready task

### 2. Hierarchical Structure

```
Phase Epic (P0)
  └── Task 1 (blocks Task 2)
  └── Task 2 (blocks Task 3)
  └── Task 3
```

- Tasks are children of their phase epic (parent-child)
- Tasks within a phase have blocking dependencies
- Phase epics block the next phase's epic

### 3. Priority Ordering

Beads priorities: P0 (critical) > P1 > P2 > P3 > P4 (backlog)

- Set phase epic priorities to control phase order
- Set task priorities within phases for importance
- `bd ready` returns tasks sorted by priority

### 4. Stale Task Detection

Agents should check for abandoned work:

```bash
bd list --status=in_progress --json
```

If `updated_at` is >10 hours old, the task is likely abandoned and can be reclaimed.

### 5. Shared Database, Isolated Code

- All worktrees share the same `.beads` database
- Each worktree has its own git branch
- Agents see the same tasks but commit to different branches
- Merge branches after tasks complete

## Example Dependency Structure

```
Phase 1 (Setup)
  T001 → T002, T003 (parallel)
           ↓
         T004, T005, T006 (parallel)
           ↓
         T007
           ↓
Phase 2 (API) - blocked by Phase 1
  T008 → T009-T017 (parallel)
           ↓
         T018-T022
           ↓
Phase 3, 4, 5, 6 - blocked by Phase 2, can run parallel
           ↓
Phase 7 - blocked by 3, 4, 5, 6
           ↓
Phase 8 - blocked by Phase 7
```

## Troubleshooting

### Agent picks wrong task
- Check `bd ready` - only unblocked tasks should appear
- Verify dependencies with `bd show <id>`
- Add missing blocking dependencies

### Agent keeps going after task complete
- Ensure script has explicit STOP instructions
- Use `--dangerously-skip-permissions` with clear single-task scope

### Tasks not appearing in order
- Check blocking dependencies between tasks
- Use `bd blocked` to see what's waiting
- Verify phase epic dependencies

### Merge conflicts
- Each worktree should be on its own branch
- Use `vibecli merge <source> [target]` for AI-assisted conflict resolution
- Merge to integration branch (`feature/web-ui`) after task completion
- Eventually merge integration branch to `main`
- Use `bd sync` before and after merging

### Agent implements without waiting for review
- Check that agent is using `vibecli plan` (not `vibecli task`)
- `vibecli plan` creates plans and marks `[Need Review]`
- `vibecli task` skips `[Need Review]` tasks

### Plans not appearing in review queue
- Verify planning agent set title to `[Need Review] <title>`
- Check status is `open` (not `in_progress` or `deferred`)
- Use `bd list --title-contains="Need Review"` to find them

### Merge command fails
- Ensure source branch exists: `git branch -a | grep <source>`
- Ensure target branch exists and is pushed: `git checkout <target> && git pull`
- Check Claude has permission to resolve files: Use `--dangerously-skip-permissions`
- If Claude can't resolve complex conflicts, resolve manually then run `git add -A && git commit`
- For persistent issues, consider rebasing instead: `git rebase <target>` on the source branch

### Agent already running error
- Another agent is running in the same worktree
- Check status with `vibecli list`
- Wait for the current agent to finish, or manually remove the stale lock:
  ```bash
  rm worktrees/<name>/.agent.lock
  ```
- If the PID doesn't exist, the lock is stale and will be overwritten automatically
