# loom

Agent management CLI for parallel Claude Code workflows with [beads](https://github.com/steveyegge/beads).

## Installation

```bash
go install github.com/tysonthomas9/loomcli@latest
```

Or download a pre-built binary from [Releases](https://github.com/tysonthomas9/loomcli/releases).

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

## Environment Variables

- `LOOM_DEFAULT_BRANCH` - Default integration branch (default: feature/web-ui)
- `LOOM_WORKTREES_DIR` - Worktrees directory (default: ./worktrees)

## License

MIT
