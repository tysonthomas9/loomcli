# loom

Agent management CLI for parallel AI coding workflows with [beads](https://github.com/steveyegge/beads).

Run multiple AI agents in parallel across git worktrees — each agent works independently on its own branch, picking tasks from a shared issue tracker, then integrating back through a structured push/pull/sync workflow.

## Installation

### Quick Install (macOS/Linux)
```bash
curl -fsSL https://raw.githubusercontent.com/tysonthomas9/loomcli/main/scripts/install.sh | bash
```

On macOS, installs to `~/.local/bin`. Add to your PATH if needed:
```bash
export PATH="$HOME/.local/bin:$PATH"
```

### Go
```bash
go install github.com/tysonthomas9/loomcli/cmd/loom@latest
```

### npm
```bash
npm install -g loomcli
```

### Pre-built binaries
Download from [Releases](https://github.com/tysonthomas9/loomcli/releases).

## Quick Start

```bash
# 1. Initialize your project (bd is included with the loom install)
loom init

# 3. Create tasks
bd create --title="Add login feature" --type=feature --priority=2
bd create --title="Fix checkout crash on empty cart" --type=bug --priority=1
bd create --title="Refactor auth middleware" --type=task --priority=3
bd create --title="User onboarding flow" --type=epic --priority=2

# 4. Run agents
loom plan falcon              # Planning agent creates designs
loom lead                     # Review and approve plans
loom task falcon              # Implementation agent builds it
```

## Commands

### Agent Commands
```
plan       Run a planning agent (creates designs, marks for review)
task       Run an implementation agent (implements approved designs)
lead       Interactive AI project management (review plans, triage backlog)
agent      Run a custom agent with a user-defined prompt template
monitor    Live dashboard showing agent status and task progress
list       List all agents (worktrees) and their status
recover    Recover agent from error state (clear locks, reset tasks)
daemon     Supervise multiple agents with auto-restart
```

### Git Operations
```
push       Push worktree branch to target with AI conflict resolution
pull       Pull integration branch into worktrees with AI conflict resolution
sync       Full sync: push all completed work, then pull into all worktrees
pr         Create GitHub PR from worktree branch
reset      Hard reset worktree to a specific branch
```

### Configuration
```
init       Guided setup wizard (beads init, worktree creation)
config     Manage workspace configuration (~/.loom/config.yaml)
workspace  Manage multi-repo workspaces
```

### Server
```
serve      Start web UI + API server for managing agents via browser
```

## Examples

```bash
# Agent workflows
loom plan falcon              # Single planning task in falcon worktree
loom task falcon --auto       # Continuous implementation mode
loom plan falcon -a -m 5      # Process up to 5 tasks, then stop
loom plan falcon -a -t 30     # Exit after 30 min idle
loom lead                     # Interactive backlog management

# Git operations
loom push --all               # Push all worktrees to main
loom pull --all               # Pull main into all worktrees
loom sync                     # Full sync: push all + pull all
loom pr falcon                # Create PR from falcon to main

# Monitoring
loom monitor                  # Live terminal dashboard
loom serve                    # Web UI at http://localhost:8080

# Multi-agent supervision
loom daemon                   # Start daemon (reads loom.yaml)
loom daemon status            # Check daemon status
```

## Web UI

`loom serve` starts a web UI for managing agents and tasks through your browser.

```bash
loom serve                    # http://localhost:8080 (auth enabled)
loom serve --no-auth          # Disable authentication (local dev)
loom serve --bind 0.0.0.0    # All interfaces
```

**Views:**
- **Kanban** — drag-and-drop swim-lane board grouped by status, priority, or type
- **Table** — sortable issue list with bulk actions
- **Graph** — visual dependency graph (React Flow)
- **Monitor** — multi-agent operator dashboard with project health and agent activity
- **Settings** — backend configuration per-project and per-agent

**Features:**
- **Talk to Lead** — built-in terminal running `loom lead` (xterm.js + tmux via WebSocket)
- **Real-time updates** — SSE pushes mutations to all connected browsers
- **Per-agent terminals** — attach to live agent tmux sessions
- **Authentication** — auto-generated API key (disable with `--no-auth`)

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
│        │     InvokeBackend()            │
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

## Backends

Loom supports multiple AI backends:

| Backend | CLI | Description |
|---|---|---|
| `claude` | `claude` | Anthropic Claude Code (default) |
| `codex` | `codex` | OpenAI Codex CLI |
| `opencode` | `opencode` | OpenCode CLI |

Set the backend:
```bash
loom plan falcon --backend codex      # Flag (highest priority)
LOOM_BACKEND=codex loom plan falcon   # Environment variable
```

Or configure in `loom.yaml`:
```yaml
backend: codex
```

**Resolution order:** `--backend` flag > `LOOM_BACKEND` env > project `loom.yaml` > global `~/.loom/config.yaml` > default `claude`

## Configuration

### Project-local: `loom.yaml`

```yaml
backend: codex

daemon:
  max_agents: 20
  restart_policy:
    max_retries: 3
    backoff_initial: 2
    backoff_max: 300
    output_timeout: 900

roles:
  reviewer:
    description: "Code reviewer"
    prompt_file: ./prompts/reviewer.txt
    task_filter: has_design

agents:
  - worktree: falcon
    role: plan
    auto: true
  - worktree: nova
    role: task
    auto: true
```

### Global: `~/.loom/config.yaml`

Used for workspace mode (multi-repo):
```yaml
default_workspace: myproject
workspaces:
  myproject:
    path: /path/to/workspace
    repos:
      - name: frontend
        path: /path/to/workspace/frontend
        default_branch: main
      - name: backend
        path: /path/to/workspace/backend
```

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

| Variable | Default | Description |
|---|---|---|
| `LOOM_BACKEND` | `claude` | AI backend CLI (claude, codex, opencode) |
| `LOOM_DEFAULT_BRANCH` | `main` | Default integration branch |
| `LOOM_WORKTREES_DIR` | `./worktrees` | Worktrees directory |
| `LOOM_SERVER_PORT` | `8081` | Loom API server port |
| `LOOM_BIND_ADDR` | `127.0.0.1` | Server bind address |
| `LOOM_WEBUI_API_KEY` | _(auto)_ | WebUI authentication API key |

## Credits

Loom uses [beads](https://github.com/steveyegge/beads) by Steve Yegge as its issue tracker. A compatible version is vendored at `third_party/beads/`.

## License

MIT
