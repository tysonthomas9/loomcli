# loom

Agent management CLI for parallel AI coding workflows backed by fleet-db.

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

## Quick Start

State lives in a fleet-db service. Local mode auto-spawns an embedded
fleet-db on first command (zero install). Cloud mode points loom at a
shared fleet-db via `LOOM_FLEET_DB_URL`. Runtime code has only two valid
control-plane paths:

```text
local mode:
loomcli -> HTTP client -> fleet-db subprocess -> RedisStorage -> miniredis or external Redis

cloud mode:
loomcli -> HTTP client -> fleet-db service -> Redis/Postgres
```

```bash
# 1. Create a workspace + a repo + a role + an agent
loom workspace add ACME             # Creates workspace ACME, sets it active
loom repo add my-app --url git@github.com:org/my-app.git
loom role set reviewer --prompt-file ./prompts/reviewer.txt
loom agentdef add falcon --role reviewer --repo my-app

# 2. Start the UI and create tasks
loom serve
# Open http://localhost:8080 and use New issue

# 3. Run agents
loom plan falcon              # Planning agent creates designs
loom lead                     # Default interactive terminal agent for review/triage
loom task falcon              # Implementation agent builds it
```

In cloud mode (shared fleet-db), set `LOOM_FLEET_DB_URL=https://fleet.example.com`
before running any of the above. Local mode is the default.

## Web UI

`loom serve` starts a web UI for managing agents and tasks through your browser.

```bash
loom serve                                              # http://localhost:8080
loom serve --auth-url https://auth.example.com          # Enable JWT auth
loom serve --bind 0.0.0.0                               # All interfaces
loom serve --fleet-mode                                 # Enable fleet coordination (requires real Redis for multi-node)
```

### Terminal state persistence

`loom serve` persists terminal tab labels, pinning, ordering, notes, and
per-issue tab layouts so they survive restarts.

- **Default** (no `--redis-addr`): state lives in an in-process miniredis
  and is dumped to `~/.loom/terminal-state/snapshot.json` every 30s and
  on shutdown. No external dependency.
- **With `--redis-addr=<host:port>`**: state lives in the external Redis
  (shared across multiple `loom serve` instances).

### Fleet coordination (`--fleet-mode`)

Fleet mode enables multi-server task coordination (cross-node task
claims, shared JWT signing keys, stale server detection, fleet worker
API routes). It is off by default; most users never need it.

| `--fleet-mode` | `--redis-addr` | Behavior |
|---|---|---|
| off (default) | empty | Local in-process miniredis. Terminal state works. No fleet. |
| off | set | External Redis for terminal state. No fleet. |
| on | empty | Local miniredis; single-node fleet (useful for testing). |
| on | set | External Redis; full multi-node fleet. |

The `LOOM_FLEET_MODE=true` env var is equivalent to `--fleet-mode`.

**Views:**
- **Kanban** — drag-and-drop swim-lane board grouped by status, priority, or type
- **Table** — sortable issue list with bulk actions
- **Graph** — visual dependency graph (React Flow)
- **Monitor** — multi-agent operator dashboard with project health and agent activity
- **Settings** — backend configuration per-project and per-agent

**Features:**
- **Talk to Lead** — default interactive terminal role running `loom lead`; any role with `kind=interactive` can run as an interactive terminal agent
- **Real-time updates** — SSE pushes mutations to all connected browsers
- **Per-agent terminals** — attach to live agent tmux sessions
- **Authentication** — external auth via `--auth-url` (RS256 JWT verification)

## Commands

### Agent Commands
```
plan       Run a worker planning agent (creates designs, marks for review)
task       Run a worker implementation agent (implements approved designs)
lead       Run the default interactive terminal agent, or a custom prompt with --prompt
agent      Run a custom worker agent with a user-defined prompt template
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
workspace  Create/list/show workspaces (workspace add|use|show|status)
repo       Manage repos within a workspace (repo add|remove|list|show)
role       Manage agent roles (role set|unset|show|list)
agentdef   Manage agent definitions (agentdef add|remove|update|list|show)
daemon     Daemon profile + lifecycle (daemon profile show|set|unset)
init       Guided setup wizard (workspace and worktree creation)
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
loom lead                     # Default interactive terminal agent

# Git operations
loom push --all               # Push all worktrees to main
loom pull --all               # Pull main into all worktrees
loom sync                     # Full sync: push all + pull all
loom pr falcon                # Create PR from falcon to main

# Monitoring
loom monitor                  # Live terminal dashboard
loom serve                    # Web UI at http://localhost:8080

# Multi-agent supervision
loom daemon profile set --max-agents=10   # Configure daemon profile (stored in fleet-db)
loom daemon                               # Start daemon
loom daemon status                        # Check daemon status
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

Or set the workspace daemon profile so agents inherit it:
```bash
loom daemon profile set --backend codex
```

**Resolution order:** `--backend` flag > `LOOM_BACKEND` env > workspace daemon profile > default `claude`

## Configuration

All loom state lives in fleet-db (workspaces, repos, roles, agent definitions,
daemon profiles, issues). Edit it via the noun-verb CLI:

```bash
# Workspace lifecycle
loom workspace add ACME                                  # Create workspace ACME
export LOOM_WORKSPACE=ACME                               # Select workspace for runtime commands
loom workspace use ACME                                  # Remember ACME as a UI selection hint
loom workspace show ACME                                 # Show workspace state
loom workspace list                                      # All workspaces in the fleet-db

# Repos (workspace-scoped)
loom repo add frontend --url git@github.com:org/frontend.git
loom repo list
loom repo show frontend
loom repo remove frontend

# Roles (workspace-scoped)
loom role set reviewer --prompt-file ./prompts/reviewer.txt --backend codex
loom role add pr-review --kind interactive --prompt-file builtin:pr-review
loom role set pr-review kind interactive
loom role show reviewer
loom role unset reviewer --backend                       # Clear a single field
loom role list

# Role fields: `kind` (`interactive` | `worker`, empty uses the legacy name
# convention where lead/orchestrator are interactive), `prompt_file`, `backend`,
# `model`, `task_filter`, tool allowlists, repo/path filters, and concurrency or
# budget limits.

# Interactive terminal agents
loom agentdef add pr-review --role pr-review             # Interactive agent definition
loom lead --prompt builtin:pr-review                     # Run an interactive prompt directly

# Agent definitions (workspace-scoped)
loom agentdef add falcon --role reviewer --repo frontend
loom agentdef update falcon --auto
loom agentdef list

# Post-run completion hooks: the DAEMON, not the agent's prompt, does the
# bookkeeping after a successful run. Writes happen in order, and a failed
# write reopens the task instead of leaving it half-finished.
loom agentdef add critic --role plan-critic \
  --on-complete-comment-reply \
  --on-complete-add-label criticized
loom agentdef update critic --clear-on-complete

# Daemon profile (one per workspace)
loom daemon profile set --max-agents=20 --log-level=debug
loom daemon profile show
loom daemon profile unset --max-agents                   # Clear an int field
```

### Storage modes

| Mode | Trigger | fleet-db location | Notes |
|---|---|---|---|
| **Local** (default) | `LOOM_FLEET_DB_URL` unset | Embedded subprocess auto-spawned per CLI invocation. Backed by an in-process miniredis with a JSON snapshot at `~/.loom/fleet-db/redis-snapshot.json`. | Zero-install. The miniredis snapshot is the source of truth for backups — copy that file. |
| **Cloud** | `LOOM_FLEET_DB_URL=<https://...>` | External fleet-db (shared across loom installs). Requires `LOOM_FLEET_DB_API_KEY` for auth, or `LOOM_FLEET_DB_ACTOR=<name>` in dev mode. | Multi-user / multi-machine. State stays on the server. |

`internal/infra/memstore` is test-only. It is not a local runtime, fallback,
cache, or embedded Redis implementation.

`LOOM_CONFIG_DIR` changes Loom's per-client state directory, but local embedded
fleet-db runtime files remain host-level by default at `~/.loom/fleet-db`. Set
`LOOM_FLEET_DB_RUNTIME_DIR` only when you intentionally need an isolated local
fleet-db runtime, such as in tests or parallel development stacks.

`~/.loom/state.json` is a per-user cache of local checkout paths and the last
selected workspace hint. Runtime commands do not use it as an implicit default;
set `LOOM_WORKSPACE` or pass `--workspace`. The cache is regenerable — not
config. Safe to delete.

## API Reference

See [API Reference](docs/api.md) for the WebUI HTTP API.

## Development

```bash
make dev  # Go API server on :8080 + Vite dev server on :3000
```

After Phase 5 decoupling, the Go server is a pure API and the frontend runs
on the Vite dev server. `make dev` starts both in parallel and handles
cleanup on Ctrl-C. Open `http://localhost:3000` in your browser.

For a containerized smoke test of the whole stack (including optional Redis
for fleet), use:

```bash
docker compose -f docker-compose.dev.yml up --build
# add --profile fleet to also start Redis
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `LOOM_BACKEND` | `claude` | AI backend CLI (claude, codex, opencode) |
| `LOOM_DEFAULT_BRANCH` | `main` | Default integration branch |
| `LOOM_WORKTREES_DIR` | `./worktrees` | Worktrees directory |
| `LOOM_SERVER_PORT` | `8080` | Server port |
| `LOOM_BIND_ADDR` | `127.0.0.1` | Server bind address |

## License

MIT
