# loom

Agent management CLI for parallel Claude Code workflows with [beads](https://github.com/steveyegge/beads).

## Installation

### Go (recommended)
```bash
go install github.com/tysonthomas9/loomcli/...@latest
```
This installs both `loom` and `loomcli` commands.

### npm
```bash
npm install -g loomcli
```

### Pre-built binaries
Download from [Releases](https://github.com/tysonthomas9/loomcli/releases).

## Usage

```bash
loom - Agent Management CLI

Commands:
  plan     Run a planning agent (creates designs, marks for review)
  task     Run an implementation agent (implements approved tasks)
  merge    Merge worktree branches with AI conflict resolution
  sync     Sync worktrees with integration branch
  reset    Hard reset worktrees to a specific branch
  monitor  Display real-time agent status dashboard
  list     List all agent worktrees and their status
  claim    Register a task with the agent monitor

Examples:
  loom plan falcon              # Run planning agent in falcon worktree
  loom task falcon              # Run implementation agent in falcon
  loom merge --all              # Merge all worktrees to integration branch
  loom sync --all               # Sync all worktrees from integration branch
  loom monitor                  # Show real-time agent dashboard
```

## Auto Mode

Both `plan` and `task` commands support continuous auto mode with the `--auto` flag:

```bash
loom plan falcon --auto              # Continuous planning mode
loom task falcon --auto              # Continuous implementation mode
loom plan falcon -a -m 5             # Process up to 5 tasks
loom plan falcon -a -t 30            # Exit after 30 min idle
```

### Implementation Flow

```
loom plan <worktree> --auto   OR   loom task <worktree> --auto
        │
        ▼
   Acquire Lock (prevent concurrent agents)
        │
        ▼
   Setup Signal Handler (Ctrl+C)
        │
        ▼
┌─────────────────────────────────────────┐
│    Auto Mode Loop                       │
│  ┌───────────────────────────────────┐  │
│  │ 1. Check shutdown signal          │  │
│  │    (non-blocking Ctrl+C check)    │  │
│  └───────────────────────────────────┘  │
│              │                          │
│              ▼                          │
│  ┌───────────────────────────────────┐  │
│  │ 2. Check max tasks limit          │  │
│  │    (exit if --max-tasks reached)  │  │
│  └───────────────────────────────────┘  │
│              │                          │
│              ▼                          │
│  ┌───────────────────────────────────┐  │
│  │ 3. Check available tasks          │  │
│  │  Plan: tasks WITHOUT design       │  │
│  │  Task: tasks WITH approved design │  │
│  │  (skips [Need Review], in_progress│  │
│  │   and epic tasks)                 │  │
│  └───────────────────────────────────┘  │
│        │              │                 │
│     No tasks      Has tasks             │
│        │              │                 │
│        ▼              │                 │
│  Check idle timeout   │                 │
│  Sleep(--interval)    │                 │
│        │              │                 │
│        │              ▼                 │
│        │     Generate prompt            │
│        │     InvokeClaude()             │
│        │     Increment task count       │
│        │     Brief pause (2s)           │
│        │              │                 │
│        └──────────────┘                 │
│              │                          │
│        (loop continues)                 │
└─────────────────────────────────────────┘
        │
        ▼ (on exit condition)
   Release Lock
   Print Summary (exit reason, tasks completed)
```

### Exit Conditions
- Ctrl+C signal received
- Max tasks limit reached (`--max-tasks`)
- Idle timeout exceeded (`--idle-timeout`)

## API Reference

See [API Reference](docs/api.md) for the WebUI HTTP API.

## Development

```bash
make dev       # default: same as make dev-loom
make dev-loom  # air + loom serve --dev + frontend dist auto-rebuild
make dev-vite  # air + Vite (frontend at :3000 with HMR)
```

Use `make dev-loom` when validating the actual Loom-served UI on `http://localhost:8080`.

## Environment Variables

- `LOOM_DEFAULT_BRANCH` - Default integration branch (default: main)
- `LOOM_WORKTREES_DIR` - Worktrees directory (default: ./worktrees)

## License

MIT
