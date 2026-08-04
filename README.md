# loom

> **Status:** Current · *audited 2026-08-03*

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
fleet-db subprocess on first command — but only when a `fleet-db` binary is
already discoverable. The installer above ships **only** the `loom` binary and
does **not** bundle fleet-db (`scripts/install.sh:120-133`). Local mode locates
the binary via `FLEET_DB_BIN`, then `fleet-db` on `PATH`, then a sibling of the
`loom` binary, then `~/.loom/bin/fleet-db`; if none resolve, the first
fleet-db-backed command fails with a remediation hint instead of starting
(`internal/bootstrap/embedded.go:315-322`, `internal/bootstrap/embedded.go:526-539`).
Cloud mode instead points loom at a shared fleet-db via `LOOM_FLEET_DB_URL` and
needs no local `fleet-db` binary. Runtime code has only two valid
control-plane paths:

```text
local mode:
loomcli -> HTTP client -> fleet-db subprocess -> RedisStorage -> miniredis or external Redis

cloud mode:
loomcli -> HTTP client -> fleet-db service -> Redis/Postgres
```

**Local-mode prerequisite:** before the first command, make a `fleet-db` binary
discoverable through one of the four locations above — e.g. `export
FLEET_DB_BIN=/path/to/fleet-db`, put `fleet-db` on your `PATH`, drop it beside
the `loom` binary, or place it at `~/.loom/bin/fleet-db`. fleet-db lives in a
separate repository and is not produced by the installer. Verify the first-run
setup before creating anything:

```bash
loom doctor    # local mode: reports "embedded fleet-db binary is not ready"
               # until fleet-db is discoverable, then "embedded fleet-db ready
               # (<path>)" (internal/cli/doctor/doctor_fleetdb.go:132-148)
```

```bash
# 1. Create a workspace + a repo + a role + an agent
loom workspace add ACME             # Creates workspace ACME, records it as the selected-workspace hint
export LOOM_WORKSPACE=ACME          # Runtime commands need this (or --workspace); the hint is not an implicit default
loom repo add my-app git@github.com:org/my-app.git
loom role add reviewer --prompt-file ./prompts/reviewer.txt
loom agentdef add falcon --role reviewer --repos my-app

# 2. Create tasks
loom data create --title "Add login" --type feature --priority 2

# 3. Run agents
loom plan falcon              # Planning agent creates designs
loom lead                     # Default interactive terminal agent for review/triage
loom task falcon              # Implementation agent builds it
```

`loom serve` is a separate, optional step — the commands above talk to fleet-db
directly (local mode auto-spawns it). Run `loom serve` **in its own shell**; it
blocks in the foreground:

```bash
loom serve                    # JSON/SSE/WebSocket API on :8080 — API-only by default
```

In cloud mode (shared fleet-db), set `LOOM_FLEET_DB_URL=https://fleet.example.com`
before running any of the above. Local mode is the default.

## Server and web UI

`loom serve` is a **pure JSON / SSE / WebSocket API server**. It does **not**
serve the web UI by default: with no `--frontend-dir` it logs
`api-only mode — frontend served externally` and registers no `/` handler
(`internal/webui/app/server_app.go:99-103`,
`internal/webui/app/frontend.go:11-15`). The frontend is a separate artifact,
served by Vite in development and by nginx/CDN in production (see
[`deploy/README.md`](deploy/README.md) — nothing in the Makefile covers the
production serving path).

Three ways to get a browser UI:

| Setup | How |
|---|---|
| Development | `make dev` — Go API on `:8080`, Vite on `:3000`, `/api/*` proxied. Open `http://localhost:3000` (`scripts/dev.sh:5-7`). |
| Single process | `loom serve --frontend-dir <built dist>` (env `LOOM_FRONTEND_DIR`) serves the SPA for non-API routes. |
| Production | Two containers, nginx in front — see [`deploy/README.md`](deploy/README.md). |

```bash
loom serve                                              # API on http://localhost:8080
loom serve --auth-url https://auth.example.com          # Enable JWT auth
loom serve --bind 0.0.0.0                               # All interfaces
loom serve --frontend-url https://app.example.com       # Allow a cross-origin frontend (CORS)
loom serve --fleet-mode                                 # Enable fleet coordination (requires real Redis for multi-node)
```

### Terminal state persistence

`loom serve` persists terminal tab labels, pinning, ordering, notes, and
per-issue tab layouts so they survive restarts.

- **Default** (no `--redis-addr`): state lives in an in-process miniredis
  and is dumped to `~/.loom/terminal-state/snapshot.json` every 30s and
  on shutdown (`internal/webui/localredis/manager.go:41`,
  `internal/cli/serve/daemonwire/localredis.go:20`). No external dependency.
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

The stale detector is the one feature that needs **both**: with `--fleet-mode`
but no `--redis-addr`, `/stale-detector` returns 404, because miniredis is
single-node and has no peer servers to mark stale
(`internal/cli/serve/serve.go:217-222`).

The `LOOM_FLEET_MODE=true` env var is equivalent to `--fleet-mode`
(`internal/cli/serve/serve.go:172`).

**Views** — routes under `/ws/:workspaceId/` (`internal/webui/frontend/src/router.tsx:87-166`).
The index and any unknown path redirect to `kanban` (`router.tsx:88,166`).

| Route | What it shows |
|---|---|
| `kanban` | Drag-and-drop swim-lane board (`SwimLaneBoard`, `@dnd-kit/core`). Lanes group by `epic`, `assignee`, `priority`, `type`, `label` or `repo`; `none` delegates to a flat `KanbanBoard` (`components/SwimLaneBoard/groupingUtils.ts:12-20`, `SwimLaneBoard.tsx:5,160`). |
| `list` | Issues grouped by epic into collapsible sections of flat rows — status dot, id, title, status chip, assignee (`views/ListPage.tsx:1-6`). |
| `table` | Sortable data table with a bulk-action toolbar (`IssueTable` + `BulkActionToolbar`, `views/TablePage.tsx:1`). |
| `graph` | Dependency graph rendered with React Flow (`@xyflow/react`, `components/GraphView/GraphView.tsx:14`). |
| `monitor` | Multi-agent operator dashboard: project health, agent activity, usage (`components/MonitorDashboard/MonitorDashboard.tsx:1-8`). |
| `observability` | Metrics dashboard — five panels fed by `useObservabilityMetrics` (`components/ObservabilityDashboard/ObservabilityDashboard.tsx:1-4`). |
| `terminal` | The route component renders `null` (`router.tsx:121-124`); the terminal surface is mounted outside the router by `App.tsx`. See [`docs/arch/terminal-system.md`](docs/arch/terminal-system.md). |
| `agents`, `agents/:agentName` | Full-page agent workspace: workspace tree, a tabbed Terminal/Info/Git/Diff/Files panel, and the epic-runner Open Queue with workflow-run streams and worker history (`views/AgentsPage.tsx:1-14`). |
| `prs` | Review queue sourced from Loom issues and enriched by `gh pr list`; degrades to a warning banner when `gh` is unavailable rather than blanking (`views/PRsPage.tsx:1-10`). |
| `settings` | Project AI-backend configuration with a per-agent override table (`components/SettingsView/SettingsView.tsx:2-4`). |
| `workspace` | Multi-repo workspace view — still a placeholder component (`components/WorkspaceView/WorkspaceView.tsx:1-4`). |
| `files` | Workspace file browser (`components/FileExplorer` → `WorkspaceFileBrowser`). See [`docs/arch/file-explorer.md`](docs/arch/file-explorer.md). |
| `issues/:issueId` | Issue detail page. See [`docs/arch/issue-detail-view.md`](docs/arch/issue-detail-view.md). |

**Features:**
- **Talk to Lead** — default interactive terminal role running `loom lead`; any role with `kind=interactive` can run as an interactive terminal agent
- **Real-time updates** — SSE pushes mutations to all connected browsers
- **Per-agent terminals** — attach to the tmux sessions CLI auto-mode creates for agents. Optional: a missing `tmux` disables the agent live view rather than failing startup (`internal/webui/app/server_app.go:199-212`)
- **Authentication** — external auth via `--auth-url` (RS256 JWT verification)

## Commands

`loom --help` is the authoritative list. The groupings below are editorial and
do **not** match Cobra's own categories — `loom --help` files `stack`, `driver`,
`workflow`, `trigger` and `connector` under *Workspace Commands*, and `init`,
`monitor`, `data`, `sessions` and `serve` under *Additional Commands*. A few
low-level commands (`claim`, `complete`, `completion`) are omitted here.

### Agent Commands
```
plan       Run a worker planning agent (creates designs, marks for review)
task       Run a worker implementation agent (implements approved designs)
lead       Run the interactive terminal-agent runtime (--prompt for a custom prompt)
agent      Run a custom worker agent with a user-defined prompt template
worker     Run a remote agent worker that connects to a control plane
daemon     Manage the agent supervisor daemon
list       List all agents (worktrees) and their status
recover    Recover agent from error state (clear stale locks, reset tasks)
usage      Display token usage and cost summaries
```

### Git Operations
```
push       Push worktree branch to target with AI conflict resolution
pull       Pull integration branch into worktrees with AI conflict resolution
sync       Full sync: push all completed work, then pull into all worktrees
pr         Create GitHub PR from worktree branch
reset      Hard reset worktree to a specific branch
stack      Manage stack lineage and publish stacked pull requests
```

### Workspace Commands
```
workspace  Manage multi-repo workspaces (add|use|show|status|ops ...)
repo       Manage repos within the active workspace (add|remove|list|show)
role       Manage roles within the active workspace (add|remove|set|unset|show|list)
agentdef   Manage agent assignments (add|remove|list|show|start|stop)
epic       Manage epic-scoped work
status     Show system overview
doctor     Check loom installation and configuration health
cleanup    Purge old data from sessions, usage, and event stores
```

### Workflow Platform
```
driver     Register and run dynamic Loom drivers
workflow   Author, approve, activate, and run Flue workflows
trigger    Manage trigger bindings and inspect trigger events/deliveries
connector  Manage per-source connectors: sealed credentials, egress grants, call audit
```

### Configuration & Runtime
```
backend    Manage AI backends
local      Manage the local desktop runtime (service|start|status|stop|drain|resume|logs)
install-service  Generate or install a platform-native service definition for loom serve
init       Initialize loom with guided setup
```

### Data & Server
```
data       Data-only commands for local or remote loom backends (the issue tracker)
sessions   Manage agent sessions
monitor    Display comprehensive agent and task dashboard
serve      Start the JSON/SSE/WebSocket API server
```

### Agent hooks (hidden)

`loom hooks` is a **hidden** command group (`internal/cli/hooks/hooks_cmd.go:24`),
so it does not appear in `loom --help`. It manages loom's Claude Code lifecycle
hooks — entries written into a worktree's `.claude/settings.json` that mirror the
Claude Code agent backend's transcript, subagent transcripts, and token usage
into the loom session store.

```
hooks install [worktree-path]     Install loom hooks into <path>/.claude/settings.json
                                  (path defaults to "."); skips with a warning
                                  when fleet mode is active unless --force is given
hooks uninstall [worktree-path]   Remove loom hooks from that settings.json
hooks status [worktree-path]      Report whether hooks are installed and list them
```

Workspace setup installs these automatically: creating a worktree during
`loom init` calls `InstallClaudeHooks` on the new worktree unless fleet mode is
active, in which case it is skipped
(`internal/cli/workspace/init_helpers.go:306-314`). Install is idempotent and
preserves unknown settings, so re-running `hooks install` safely repairs a
worktree, `hooks status` inspects it, and `hooks uninstall` removes the entries
(`internal/cli/hooks/hooks_cmd.go:194-270`,
`internal/cli/hooks/hooks_install.go:76-80`).

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
loom serve                    # API at http://localhost:8080 (UI is served separately)

# Multi-agent supervision
loom daemon profile set max_agents 10     # Configure daemon profile (stored in fleet-db)
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

## AI CLI backends

An **agent backend** is the AI CLI a role or agent invokes. (This repo also has
an *issue* backend and a *fleet-db* backend — see
[docs/loom-glossary.md](docs/loom-glossary.md#other-overloaded-names).) Five
are compiled in, each self-registering at init:

| Backend | CLI | Registered at |
|---|---|---|
| `codex` | `codex` | `internal/cli/backends/backend_codex.go:234` (default) |
| `claude` | `claude` | `internal/cli/backends/backend_claude.go:200` |
| `opencode` | `opencode` | `internal/cli/backends/backend_opencode.go:143` |
| `cursor` | `cursor-agent` | `internal/cli/backends/backend_cursor.go:167` |
| `gemini` | `gemini` | `internal/cli/backends/backend_gemini.go:144` |

The web UI validates its backend dropdown against the same five
(`internal/webui/terminal/session_command.go:13`). Two more names can appear in
the registry: `echo`, a deterministic test backend
(`internal/cli/backends/backend_echo.go:306`), and any executable named
`loom-backend-<name>` found on `PATH`, registered as an **external** backend
(`DiscoverExternalBackends`, `internal/cli/backends/backend_external.go:136,174`).
Built-ins win on a name collision.

Set the backend:
```bash
loom plan falcon --backend claude      # Flag (highest priority)
LOOM_BACKEND=claude loom plan falcon   # Environment variable
```

The workspace default backend is a daemon-profile field (`agent_backend`)
edited from the web UI Settings view (`PATCH /api/workspaces/{ws}/config/backend`,
`internal/webui/app/routes.go:156`); `loom daemon profile set` does not expose it.

**Resolution order** differs by who resolves it:

- A directly invoked `loom` command: `--backend` flag > `LOOM_BACKEND` env >
  default `codex` (`internal/cli/backend.go:82`).
- An agent run spawned by serve/the daemon: per-agent backend override >
  workspace daemon profile `agent_backend` > default `codex`
  (`internal/driver/task_bridge.go:754`, `internal/runtimepreflight/preflight.go:55`).

`ResolveBackendName` has **no** daemon-profile step — it returns flag, then env,
then the `codex` constant, and never touches fleet-db
(`internal/cli/backend.go:82-92`). Older docs claimed a workspace-profile step
in this chain; that is wrong for the direct-CLI path.

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
loom repo add frontend git@github.com:org/frontend.git
loom repo list
loom repo show frontend
loom repo remove frontend

# Roles (workspace-scoped). `role add` takes flags; `role set`/`unset` take
# positional <NAME> <KEY> [VALUE] (internal/cli/role/role_cmd.go:71,94).
loom role add reviewer --prompt-file ./prompts/reviewer.txt --backend codex
loom role add pr-review --kind interactive --prompt-file builtin:pr-review
loom role set pr-review kind interactive
loom role show reviewer
loom role unset reviewer backend                         # Clear a single field
loom role list

# Role fields: `description`, `kind` (`interactive` | `worker`, empty uses the
# legacy name convention where lead/orchestrator are interactive), `prompt`,
# `prompt_file`, `model`, `task_filter`, `backend`, `effort`, `read_only`,
# `max_priority`, `max_concurrency`, `max_budget_usd`, `skills`,
# `path_patterns`, `allowed_tools`, `denied_tools`
# (internal/cli/role/role_cmd.go:73-89).

# Interactive terminal agents
loom agentdef add pr-review --role pr-review             # Interactive agent definition
loom lead --prompt builtin:pr-review                     # Run an interactive prompt directly

# Agent definitions (workspace-scoped)
loom agentdef add falcon --role reviewer --repos frontend --auto
loom agentdef start falcon
loom agentdef list

# Daemon profile (one per workspace). Keys: pid_file, log_dir, events_dir,
# issue_backend, max_agents, startup_timeout
# (internal/cli/daemon/profile_cmd.go:38-44). The workspace agent backend is
# NOT settable here — see "AI CLI backends" above.
loom daemon profile set max_agents 20
loom daemon profile show
loom daemon profile unset max_agents                     # Clear an int field
```

### Storage modes

| Mode | Trigger | fleet-db location | Notes |
|---|---|---|---|
| **Local** (default) | `LOOM_FLEET_DB_URL` unset | Embedded fleet-db subprocess, backed by an in-process miniredis with a JSON snapshot at `~/.loom/fleet-db/redis-snapshot.json` (`internal/bootstrap/embedded.go:339`). A healthy running instance is reused, not respawned per invocation (`bootstrap.reuseEmbeddedRuntime`, `internal/bootstrap/embedded.go:183`). | No external services, but a `fleet-db` binary must be discoverable (see Quick Start). The miniredis snapshot is the source of truth for backups — copy that file. |
| **Cloud** | `LOOM_FLEET_DB_URL=<https://...>` | External fleet-db (shared across loom installs). Requires `LOOM_FLEET_DB_API_KEY` for auth, or `LOOM_FLEET_DB_ACTOR=<name>` in dev mode. | Multi-user / multi-machine. State stays on the server. |

`internal/infra/memstore` is test-only. It is not a local runtime, fallback,
cache, or embedded Redis implementation.

`~/.loom/state.json` is a per-user cache of local checkout paths and the last
selected workspace hint. Runtime commands do not use it as an implicit default;
set `LOOM_WORKSPACE` or pass `--workspace`. The cache is regenerable — not
config. Safe to delete.

## API Reference

See [API Reference](docs/api.md) for the WebUI HTTP API. That file is
generated from `api/openapi.yaml` — see [docs/README.md](docs/README.md#generated-files).

## Documentation

[docs/README.md](docs/README.md) indexes the whole `docs/` tree. Read
[docs/loom-glossary.md](docs/loom-glossary.md) first: this project reuses
ordinary words (`loom`, `flue`, `fleet`, `aether`, `codex`, `stack`, `lead`)
as specific concepts.

## Development

```bash
make dev  # Go API server on :8080 + Vite dev server on :3000
```

The Go server is a pure API; the frontend runs on the Vite dev server, which
proxies `/api/*` and `/health` to `:8080` so the browser sees a same-origin app
(`scripts/dev.sh:5-7`). `make dev` starts both in parallel and cleans up on
Ctrl-C. Open `http://localhost:3000`. `make dev-loom` and `make dev-vite` are
deprecated aliases that now run the same script (`Makefile:618-628`).

For a containerized smoke test of the whole stack (including optional Redis
for fleet), use:

```bash
docker compose -f docker-compose.dev.yml up --build
docker compose -f docker-compose.dev.yml --profile fleet up --build   # + Redis
```

Other local harnesses, each with its own README:
[`test/local-mode/`](test/local-mode/README.md) (full dogfood stack),
[`test/playground/`](test/playground/README.md) (daemon failure modes),
[`test/distributed/`](test/distributed/README.md) (two-server fleet smoke),
[`test/fleetdb/`](test/fleetdb/README.md) (empty new-user stack),
[`e2e/`](e2e/README.md) (container E2E),
[`deploy/podman-stack/`](deploy/podman-stack/README.md) (distributed topology).

## Environment Variables

The ones a new user actually needs. `loom serve --help` lists the full server
set (`internal/cli/serve/serve.go:117-133`).

| Variable | Default | Description | Read at |
|---|---|---|---|
| `LOOM_WORKSPACE` | *(none)* | Workspace key for runtime commands; equivalent to `--workspace` | `internal/cli/root.go:114,131` |
| `LOOM_BACKEND` | `codex` | Agent backend CLI (codex, claude, opencode, cursor, gemini) | `internal/cli/backend.go:88` |
| `LOOM_DEFAULT_BRANCH` | `main` | Default integration branch | `internal/cli/worktree.go:128` |
| `LOOM_FLEET_DB_URL` | *(unset)* | Switches local mode → cloud mode | `internal/bootstrap/mode.go:23,55` |
| `LOOM_FLEET_DB_API_KEY` | *(unset)* | Auth for a remote fleet-db | `internal/bootstrap/openstore.go:18` |
| `LOOM_FLEET_DB_ACTOR` | *(unset)* | Actor identity when the remote fleet-db is in dev mode | `internal/bootstrap/openstore.go:22` |
| `LOOM_SERVER_PORT` | `8080` | Server port | `internal/cli/serve/serve.go:153` |
| `LOOM_BIND_ADDR` | `127.0.0.1` | Server bind address | `internal/cli/serve/serve.go:158` |
| `LOOM_FRONTEND_DIR` | *(unset)* | Built SPA directory to serve for non-API routes; unset = API-only | `internal/cli/serve/serve.go:167` |
| `LOOM_FRONTEND_URL` | *(unset)* | Allowed frontend origin(s) for CORS | `internal/cli/serve/serve.go:166` |
| `LOOM_FLEET_MODE` | `false` | Equivalent to `--fleet-mode` | `internal/cli/serve/serve.go:172` |

`LOOM_WORKTREES_DIR` was documented here until 2026-07-24 and is **dead**: no
non-test code reads it. `--worktrees-dir` on `loom init` still mentions it in
its help string (`internal/cli/workspace/init.go:54`), but the fallback is
`cli.GetWorktreesDir()`, which resolves the active workspace root and never
consults the env var (`internal/cli/workspace/init_helpers.go:56-61`,
`internal/cli/worktree.go:52-55`).

## Working on Loom itself

Loom's real E2E, container, and workflow-bundle work needs **two sibling
repositories checked out next to this one**. Nothing in the build tells you
this until a script exits, so clone them before you start:

| Repo | Needed for | Consumed by |
|---|---|---|
| `fleet-db` | any real (non-mocked) E2E, the podman stack, the distributed smoke | `scripts/start-e2e-server.sh:87-94`, `deploy/podman-stack/build.sh:28,41`, `Makefile:243` |
| `flue` | building workflow driver bundles and the podman-stack images | `deploy/podman-stack/build.sh:29,42`, `Makefile:59-62` |

**The consumers disagree on where the checkouts live** — at least five distinct
defaults exist and they differ in *depth* (`../` vs `../../`), so set the env
vars rather than trusting any default:

- `Makefile:475-477` probes `../fleet-db` **then** `../../fleet-db` and falls
  back to `../../fleet-db`.
- `scripts/start-e2e-server.sh:14` tries only `<repo>/../../fleet-db` and
  exits with an error if `cmd/fleet-db` is not there
  (`scripts/start-e2e-server.sh:87-90`).
- `deploy/podman-stack/build.sh:28-29` tries only `<repo>/../fleet-db` and
  `<repo>/../flue`, and dies if either is missing
  (`deploy/podman-stack/build.sh:41-42`).
- `desktop/scripts/prepare-sidecar.sh:8` tries only `<repo>/../fleet-db`.
- `e2e/run_epic_runner_real_codex_octocat_podman.sh:8,10` uses **both** depths
  in one file: `../../fleet-db` and `../flue`.

```bash
export FLEET_DB_REPO=/path/to/fleet-db
export FLUE_REPO=/path/to/flue
```

`flue` must additionally be **built on the host** before the podman-stack image
build; `deploy/podman-stack/build.sh:47-50` refuses to bake an image without
`$FLUE_REPO/packages/cli/dist/flue.js`:

```bash
(cd "$FLUE_REPO" && pnpm install && pnpm build)
```

Without these, `make test-distributed-smoke`, `scripts/test-podman-stack.sh`,
`make test-builtin-workflows`, and the real-backend Playwright lanes all fail at
the build step. Everything else — `make build`, `make check-go`, `make dev`,
unit tests — works from this repo alone.

## Related

- [AGENTS.md](AGENTS.md) — instructions for coding agents working in this repo
- [docs/README.md](docs/README.md) — index of the whole `docs/` tree
- [docs/loom-glossary.md](docs/loom-glossary.md) — the overloaded-term dictionary
- [deploy/README.md](deploy/README.md) — production deployment reference
- [deploy/podman-stack/README.md](deploy/podman-stack/README.md) — local distributed-topology stack
- [desktop/README.md](desktop/README.md) — the Tauri desktop shell
- [sdk/README.md](sdk/README.md) — the `@loom/sdk` workflow SDK
- [docs/testing/README.md](docs/testing/README.md) — index of test surfaces and harnesses

## License

MIT
