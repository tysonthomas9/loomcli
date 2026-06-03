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
loom lead                     # Review and approve plans
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
- **Talk to Lead** — built-in terminal running `loom lead` (wterm + tmux via WebSocket)
- **Real-time updates** — SSE pushes mutations to all connected browsers
- **Per-agent terminals** — attach to live agent tmux sessions
- **Authentication** — external auth via `--auth-url` (RS256 JWT verification)

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

## Runtime Profiles

TypeScript-first workspaces can declare runtime profiles under `.loom/runtimes`.
Loom supports `runtime.local`, `runtime.podman`, `runtime.remote`, and
`runtime.daytona` declarations. Daytona profiles persist provider metadata,
sandbox create options, repo/env grants, lifecycle policy, and declarative image
builder intent in FleetDB.

```ts
// .loom/runtimes/daytona-dev.ts
import { Image } from "@daytona/sdk";
import { runtime } from "@loom/sdk";

export default runtime.daytona({
  name: "daytona-dev",
  image: Image.debianSlim("3.12")
    .runCommands("apt-get update && apt-get install -y --no-install-recommends git nodejs npm")
    .workdir("/workspace/project"),
  language: "typescript",
  cwd: "/workspace/project",
  repos: ["frontend"],
  repoUrl: "https://github.com/acme/frontend.git",
  env: ["GITHUB_TOKEN", "NODE_ENV"],
  resources: { cpu: 2, memory: 4, disk: 8 },
  target: "us",
  apiKeyEnv: "DAYTONA_API_KEY",
  gitTokenEnv: "GITHUB_TOKEN",
  openaiApiKeyEnv: "OPENAI_API_KEY",
  setupCommands: ["npm install"],
  autoStopInterval: 60,
  workspace: {
    owner: "loom",
    cleanup: { mode: "after_ttl", ttl: "6h" },
    filesystem: { persistence: "session", durability: "provider" },
  },
  capabilities: {
    filesystem: { read: true, write: true },
    shell: { enabled: true, commands: ["git", "npm", "node"] },
    network: { enabled: true },
    lifecycle: { materialize: true, cleanup: true, release: true },
  },
});
```

`apiKeyEnv` records the controller-side Daytona API key environment variable
name, not the secret value. Agent and Git credentials are opt-in: declare grant
names through `env`, use `gitTokenEnv`/`gitDeployKeyEnv` for private repo
materialization, and use `openaiApiKeyEnv` or a Daytona-provisioned
`codexAuthFileEnv` for Codex/OpenAI auth.

Daemon-managed Daytona agents use the same explicit setup model as workflows.
Each spawned agent gets its own sandbox, validates that `LOOM_FLEET_DB_URL`,
`LOOM_WORKSPACE`, and either `LOOM_FLEET_DB_API_KEY` or `LOOM_FLEET_DB_ACTOR`
are available, runs a remote `/health` check, clones the configured repo into
the runtime `cwd` (default `/workspace/project`), then runs any declared
`setup_commands` before starting the agent command. Host-local IPC and auth
paths such as `LOOM_DAEMON_SOCKET`, `CODEX_HOME`, `HOME`, and `SSH_AUTH_SOCK`
are not forwarded. Private Git clones use temporary in-sandbox Git auth helpers
backed only by declared token/deploy-key env vars, and secret values are
redacted from captured remote output.

WorkflowContext also supports Daytona-shaped setup code. Workflows can import
`Daytona` from `@daytona/sdk`, create a sandbox, attach it to an agent with
`daytona(sandbox)`, and Loom will record sandbox/session metadata and redact
secret-like fields. When `@daytona/sdk` is installed next to the workflow source,
`ctx.runtime.materializeWorkspace()` creates a Daytona sandbox from the active
`runtime.daytona` profile. Daytona-backed `session.shell(...)` and
`session.prompt(...)` calls execute through `sandbox.process.executeCommand(...)`
when the SDK and selected backend CLI are available; otherwise Loom falls back to
an admission shim so definitions and tests can still run without the SDK
installed.

The native import is `@loom/runtime`; Loom also declares `@flue/runtime` as a
compatibility alias for example-shaped workflows that export named `route` and
`run` handlers with `FlueContext` / `WorkflowRouteHandler` types. New
TypeScript projects include a small `.loom/connectors/daytona.ts` adapter so
workflows can use the sample-style `../connectors/daytona` import.
Loom's native source root remains `.loom`, but copied Flue examples can live
under `.flue/workflows`, `.flue/runtimes`, `.flue/tools`, `.flue/agents`, and
`.flue/connectors` when there are no `.loom` authored entrypoints in the same
project.

```ts
// .loom/connectors/daytona.ts
import { daytona as loomDaytona } from "@loom/runtime";

export function daytona(sandbox, options = {}) {
  return loomDaytona(sandbox, options);
}
```

```ts
// .loom/workflows/code.ts
import { Daytona } from "@daytona/sdk";
import {
  createAgent,
  type FlueContext,
  type WorkflowRouteHandler,
} from "@flue/runtime";
import { daytona } from "../connectors/daytona";

export const route: WorkflowRouteHandler = async (_c, next) => next();

export async function run({ init, payload, env }: FlueContext) {
  const client = new Daytona({ apiKey: env.DAYTONA_API_KEY });
  const sandbox = await client.create({ snapshot: "loom-node-dev", cwd: "/workspace/project" });

  const setup = await (await init(createAgent(() => ({
    name: "setup",
    sandbox: daytona(sandbox, { name: "daytona-setup" }),
  })), { name: "setup" })).session();

  await setup.shell(`git clone ${payload.repo} /workspace/project`);

  const project = await (await init(createAgent(() => ({
    name: "project",
    model: "openai/gpt-5.5",
    sandbox: daytona(sandbox, { name: "daytona-project", cwd: "/workspace/project" }),
  })), { name: "project" })).session();

  return await project.prompt(payload.prompt);
}
```

This makes Daytona profiles, setup shell commands, workflow-runner prompt
execution, and daemon-managed coding-agent sessions first-class in Loom. The
workflow example above deliberately reuses one application-created sandbox for
setup and prompting; daemon-managed `runtime.daytona` agents do not share that
state, and each spawned agent receives its own Daytona sandbox.

To exercise the Slack-clone epic runner with Daytona-backed task agents, keep
the harness local and set the remote runtime inputs explicitly:

```bash
AGENT_RUNTIME=daytona \
DAYTONA_API_KEY=<daytona-key> \
OPENAI_API_KEY=<openai-key> \
LOOM_FLEET_DB_URL=https://fleet.example.com \
LOOM_FLEET_DB_API_KEY=<fleet-key> \
DAYTONA_REMOTE_REPO_URL=https://github.com/acme/slack-clone-e2e.git \
GITHUB_TOKEN=<repo-token> \
bash e2e/run_epic_runner_real_codex_tsfirst_slack_podman.sh
```

For Codex auth, use either `OPENAI_API_KEY` or a Daytona-provisioned remote
`CODEX_AUTH_FILE` that points to an in-sandbox `auth.json`. The e2e scripts
reject host-local FleetDB URLs, local repo paths, malformed remote URLs, and
host-local auth paths before starting Daytona work.

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
loom role show reviewer
loom role unset reviewer --backend                       # Clear a single field
loom role list

# Agent definitions (workspace-scoped)
loom agentdef add falcon --role reviewer --repo frontend
loom agentdef update falcon --auto
loom agentdef list

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
