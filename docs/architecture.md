# Loom Architecture

## Overview

**loom** is an agent-management CLI for parallel AI coding workflows. It
supervises AI coding agents (Claude, Codex, OpenCode) across git worktrees,
tracks their work as issues in [beads](https://github.com/steveyegge/beads)
(`bd`), and serves a web UI (Cortex) for monitoring and control.

This document is canonical for the system's structure. Canonical definitions
for the domain vocabulary (agent, worktree, workspace, role, backend, epic,
daemon, …) live in the glossary (`docs/glossary.md`); the HTTP surface is
documented in [docs/api.md](api.md). An HTML+SVG replica of this page is kept
at `docs/architecture.html`.

### Key Design Decisions

- **Single binary** — one `loom` executable provides the CLI, the agent
  supervisor (`loom daemon`), the servers (`loom serve`), and internal helper
  commands (`loom log-router`).
- **Issues live in beads** — loom does not own an issue store; it drives the
  `bd` CLI and the bd daemon's unix-socket RPC. Issue data syncs through the
  dedicated `beads-sync` git branch, not `main`.
- **One agent per worktree** — each supervised agent is bound to a git
  worktree and a role; a lock file (`.agent.lock`) enforces exclusivity.
- **Backends are pluggable** — agents invoke an AI coding CLI (`claude`,
  `codex`, `opencode`, or any external `loom-backend-*` executable) through a
  common `Backend` interface, with per-agent failover.
- **Supervision is crash-tolerant** — restart with exponential backoff,
  rate-limit-aware retries, output-timeout watchdog, checkpoint files for
  resume, and worktree recovery before and after every run.
- **Two web servers, one process** — `loom serve` runs the Cortex webui
  (default `:8080`) and a dashboard API server (default `:8081`) in one
  process; the frontend reaches the latter only through the `/api/loom/*`
  reverse proxy.
- **Redis is optional** — fleet coordination, terminal tab metadata, and
  stale-worker detection activate only when `--redis-addr` is configured.

### Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go (module `github.com/tysonthomas9/loomcli`) |
| CLI framework | cobra (`internal/cli/root.go`) |
| Issue store | beads (`bd`) — SQLite + JSONL export, vendored at `third_party/beads/` |
| bd IPC | JSON over unix socket (`internal/rpc`), plus `bd … --json` subprocesses |
| Web UI | React + Vite (`internal/webui/frontend/`), embedded via `embed.FS` |
| Real-time | SSE fan-out hub + WebSocket terminal relay (`nhooyr.io/websocket`, `creack/pty`, tmux) |
| Coordination (optional) | Redis (fleet claims, JWT keys, tab metadata, stale detection) |
| Observability | day-rotated JSONL event log, Prometheus `/metrics`, optional OTLP export |

---

## Project Structure

```
loomcli/
├── cmd/loom/                  # main() → internal/cli.Execute()
├── internal/
│   ├── cli/                   # ~81 files — cobra commands, daemon supervisor,
│   │                          #   backends, auto mode, worktree resolver,
│   │                          #   git/PR ops, monitor TUI, dashboard API server
│   ├── webui/                 # ~41 files — Cortex HTTP server: REST over bd RPC,
│   │   │                      #   SSE hub, terminal WS relay, fleet endpoints,
│   │   │                      #   git/file handlers, middleware
│   │   ├── daemon/            # bd-daemon connection pool + circuit breaker
│   │   ├── fleet/             # Redis fleet store, JWT keys, claim timeouts
│   │   ├── editor/            # external editor detection/launch
│   │   ├── tabmeta/           # Redis-backed terminal tab metadata
│   │   └── frontend/          # React app (api/, components/, hooks/, utils/)
│   ├── rpc/                   # bd daemon client: wire protocol, unix transport,
│   │                          #   socket-path rules (103-byte limit fallback)
│   ├── types/                 # shared beads domain types (Issue, enums, locks)
│   ├── events/                # event bus + JSONL writer + metrics (+ otelexport/)
│   ├── usage/                 # token/cost accounting → usage.jsonl
│   ├── logrouter/             # loom log-router engine (agent/task log tee)
│   ├── agenterr/              # agent failure classification (per backend)
│   ├── kv/                    # Redis worker claim/heartbeat + stale detector
│   ├── lockfile/              # flock + process-liveness (reads bd daemon.lock)
│   ├── circuitbreaker/        # generic closed/open/half-open breaker
│   └── debug/ testutil/
├── third_party/beads/         # vendored beads (bd)
└── docs/                      # api.md, security.md, beads-sync.md, design/,
                               #   glossary.md, this file + architecture.html
```

File counts are approximate by design — packages are the stable unit.

---

## Process Topology

```
                    ┌─────────────┐        ┌──────────────┐
                    │   Browser   │        │  loom CLI    │
                    │  (Cortex)   │        │  one-shots   │
                    └──────┬──────┘        └──────┬───────┘
              REST / SSE / │ WS                   │ bd <cmd> --json
                    ┌──────▼──────────────┐       │
   loom serve ──────│ webui server :8080  │       │
   (one process)    │ (internal/webui)    │       │
                    │  /api/loom/* proxy ─┼──┐    │
                    │ dashboard API :8081 │◄─┘    │
                    │ (internal/cli/serve)│       │
                    └──────┬──────────────┘       │
                    RPC pool│+ circuit breaker    │
                    ┌──────▼──────────┐    ┌──────▼───────┐
                    │   bd daemon     │◄───│  bd CLI      │
                    │ .beads/bd.sock  │    │ subprocesses │
                    └──────┬──────────┘    └──────────────┘
                           │
                    ┌──────▼──────────┐
                    │ .beads/beads.db │  + JSONL on the beads-sync branch
                    └─────────────────┘

   loom daemon ──spawns──► loom task|plan|agent --auto --daemon-mode
                             │ (per worktree, process group)
                             ├── tmux session + loom log-router (tee logs)
                             └── backend CLI: claude / codex / opencode
```

| Process | Started by | Talks over |
|---------|-----------|------------|
| `loom` one-shots (`list`, `push`, `pr`, `monitor`, …) | user | `bd <cmd> --json` subprocesses, git |
| `loom daemon` | user | spawns agent subprocesses (`Setpgid`); state in `.loom/` |
| Agent processes (`loom task/plan/agent --auto --daemon-mode`) | loom daemon | `bd` subprocesses; backend CLI; log files |
| tmux session + `loom log-router` | agent auto mode (when tmux exists) | tee to `~/.loom/logs/agents/` and `~/.loom/logs/tasks/` |
| Backend CLI (`claude`/`codex`/`opencode`/`loom-backend-*`) | agent process | stdout stream-JSON |
| `loom serve` webui server | user (`loom serve`, goroutine) | HTTP `:8080` (`--webui-port`, auto-increments if busy) |
| `loom serve` dashboard API | same process | HTTP `:8081` (`--port` / `LOOM_SERVER_PORT`) |
| bd daemon (external) | auto-started by `loom serve` (unless `--no-daemon`) | unix socket `.beads/bd.sock` (or `/tmp/beads-<hash>/bd.sock` when the path would exceed the 103-byte unix limit — `internal/rpc/socket_path.go`) |
| Redis (external, optional) | — | `--redis-addr` / `LOOM_REDIS_ADDR` / `daemon.redis_url` |

**Two bd access paths.** `internal/cli` shells out to the `bd` CLI
(`execCommand` via the `BDRunner` interface in `deps.go`); `internal/webui`
speaks the bd daemon's unix-socket RPC directly through a connection pool
(`internal/webui/daemon/pool.go`) wrapped in a circuit breaker
(failure threshold 5, open timeout 30 s, half-open probe 1).

**Two HTTP APIs with overlapping names.** Both servers expose `/health`,
`/api/stats`, and `/api/metrics`, but they are different handlers with
different data sources: the webui's come from bd RPC; the dashboard API's
come from the monitor collector (subprocess-derived). Docs and clients must
say which port they mean. See [docs/api.md](api.md).

---

## Daemon Supervision

`loom daemon` (`internal/cli/daemon_cmd.go`, `daemon.go`) supervises the
agents declared in `loom.yaml`.

### Configuration

`LoadDaemonConfig` merges built-in defaults → global `~/.loom/config.yaml` →
project `loom.yaml` (local wins; agents come from the project file only).
Before parsing, the config bytes pass env substitution
(`config_envsubst.go`), secret resolution (`secrets.go` —
`${secret:…}`-style refs via pluggable backends), a version check, and
validation.

`loom.yaml` shape:

```yaml
version: …
backend: claude                # project default backend
daemon:
  pid_file: .loom/daemon.pid
  log_dir: .loom/logs
  events_dir: .loom/events
  max_agents: 20
  redis_url: …                 # optional
  restart_policy:              # see Restart & Backoff below
  otel: …                      # optional OTLP export
roles:
  <name>:                      # prompt_file, model, task_filter, backend,
                               # path_patterns, skills, max_priority,
                               # max_concurrency, read_only, allowed/denied_tools
agents:
  - worktree: <name>           # + role, auto, backend, fallback_backends,
                               #   path_patterns
```

### Startup

`runDaemon` validates paths, takes the exclusive `.loom/daemon.lock`, writes
`.loom/daemon.pid`, and starts the `Daemon`. `Start()` first runs
`resetWorktreeBranches()` — WIP-committing dirty worktrees and checking each
back onto its own branch to avoid git cross-checkout deadlocks — then runs
one health-checker goroutine plus one `superviseAgent` goroutine per agent.
Supervisor state snapshots go to `.loom/daemon-agents.json` (read by
`loom daemon status`).

### The supervision loop

Each `superviseAgent` iteration (`daemon.go`):

```
 1. acquire per-role concurrency slot        (ConcurrencyTracker, blocking)
 2. pre-flight worktree recovery             (RecoverWorktree)
 3. epic assignment                          (EpicAssigner → epic_assigned)
 4. ensure branch                            (epic branch or worktree branch)
 5. spawn agent                              (agent_started)
 6. wait for exit                            (agent_stopped)
 7. classify exit + checkpoint               (agenterr; save/clear .agent.checkpoint.json)
 8. post-mortem worktree recovery
 9. ensure epic PR                           (non-fatal; pr_created)
10. release epic + concurrency slot
11. epic transition                          (epic_exhausted → reassign or non-epic mode)
12. backend failover                         (fallback_backends index)
13. restart decision + backoff sleep         (agent_restarted)
```

Spawned commands (`daemon_spawn.go:buildCommand`):

- built-in roles: `loom <plan|task> <worktree> --auto --daemon-mode
  [--backend X] [--parent <epicID>]`
- custom roles: `loom agent <worktree> --prompt <file> [--task-filter F]
  --auto --daemon-mode …`

The environment is scrubbed (`FilteredEnv()`) and extended with `BD_ACTOR`,
`LOOM_WORKTREE_PATH`, `LOOM_EVENTS_DIR`, and the `LOOM_ROLE_*` /
`LOOM_ALLOWED_TOOLS` / `LOOM_DENIED_TOOLS` / `LOOM_READ_ONLY` variables that
the agent-side task router reads back (`task_router.go`).

### Restart & Backoff

`daemon_restart.go` — defaults: `max_retries` 3, `backoff_initial` 2s,
`backoff_max` 300s, `output_timeout` 900s.

- Clean exit resets counters; a run longer than one minute also resets
  backend failover to the primary.
- `NoWork` always restarts, never counts against retries, sleeps
  `no_work_backoff` (30s).
- Rate limits retry on a separate counter (unlimited by default,
  `rate_limit_no_count`), capped by `rate_limit_max_wait`, honoring server
  `Retry-After`.
- Fatal classes (auth, billing, model-not-found — `agenterr.IsFatal`) stop
  the supervisor for that agent.
- Everything else: exponential `initial × 2ⁿ` clamped to `backoff_max`.

The health checker watches each agent's log-file mtime; silence beyond
`output_timeout` kills the process group (watchdog).

### Epic assignment

`EpicAssigner` (`daemon_epic.go`) assigns each worktree the highest-priority
open epic that has ready tasks (probed via `bd ready --parent <epic> --json
--limit 1`), scoping the agent's discovery with `--parent`. When an epic
runs dry the daemon emits `epic_exhausted` and either reassigns or switches
the agent to non-epic mode. Epic-mode agents work on a per-epic branch
(`daemon_branch.go`).

---

## Agent Execution Path

An agent iteration in auto mode:

```
 idle ──► poll ready tasks (bd ready --json, task-filter predicates)
   │            │ none → idle-timeout / no-work backoff
   │            ▼
   │      set lock active, capture HEAD
   │            ▼
   │      generate prompt (role prompt + checkpoint context)
   │            ▼
   │      invoke backend CLI (stream-JSON; usage collector accumulates)
   │            ▼
   │      record session usage (usage.jsonl)
   │            ▼
   └──── back to idle (lock idle; task counted if a task was claimed)
```

**Task discovery** — `automode_poller.go` runs `bd ready --json --limit 100`;
predicates in `taskfilter.go` (`NeedsPlan`, `ReadyToImplement`,
`IsWorkableTask`, …) route issues by role, deliberately kept in sync with
the frontend's `issueCategory.ts`. Role constraints arrive via the
`LOOM_ROLE_*` env vars and are applied by `task_router.go`
(`MatchTask`, `SelectBestTask`).

**Two loop implementations** — with tmux available, `RunAutoModeTmux` runs
the agent inside a tmux session piped through `loom log-router` (per-agent
and per-task/phase logs with rotation), streams the log to the console, and
supports live attach. Without tmux, `RunAutoModeLoop` drives the backend
directly and parses its stream-JSON output. There is **no PTY on the agent
path** — `creack/pty` is used only by the browser terminal relay.

**Backends** — `backend.go` defines the `Backend` interface and registry;
resolution order: `--backend` flag > `LOOM_BACKEND` > project `loom.yaml` >
`~/.loom/config.yaml` > `claude`.

- `claude`: `claude -p --verbose --output-format stream-json
  --dangerously-skip-permissions`, prompt fed via a stdin pipe (kept out of
  `ps`), stdout parsed line-by-line for display and usage.
- `codex`: `codex exec --json --dangerously-bypass-approvals-and-sandbox`.
- `opencode`: `opencode run [--format json]`.
- external: any `loom-backend-*` executable on `$PATH`, auto-registered
  unless `LOOM_NO_EXTERNAL_BACKENDS`; built-ins win name collisions.

Failures are classified per backend (`internal/agenterr`) into retryable
(rate limit, timeout, transient, no-work) vs fatal (auth, billing,
model-not-found, context overflow) classes that drive daemon restart
decisions. A process guard delivers SIGTERM to the process group on
shutdown.

**Usage accounting** — `internal/usage`: a `Collector` accumulates token
counts from stream events (deduplicated by message ID); each session appends
a row to `<beadsDir>/usage.jsonl` under flock, with cost estimated from
per-backend pricing tables. Surfaced by `loom usage` and the Usage
dashboard.

**Lock & checkpoint files** — `.agent.lock` holds pid, agent, current task,
and execution state (`active`/`idle`); acquisition is `O_CREATE|O_EXCL` with
retries, staleness detection, and force-release via `loom recover`. On
non-zero exit the daemon writes `.agent.checkpoint.json` (agent, task, epic,
≤4 KB git diff, exit code, error class); the next session's prompt includes
that context. In workspace mode the lock dir collapses to the workspace root
so all repos share one lock.

---

## Worktree Resolution (legacy vs workspace mode)

`internal/cli/worktree.go` — the `Resolver` has two modes:

- **Legacy**: scan `./worktrees/` in the current repo.
- **Workspace**: named workspaces from `~/.loom/config.yaml`, each grouping
  multiple repos with per-repo default branches.

The mode determines worktree discovery, lock-file placement, beads-dir
resolution (`GetBeadsDir`), diff capture, and how `push`/`pull`/`sync`/`pr`
iterate repos. `ResolveAgentTarget` maps a CLI argument to a concrete
worktree in either mode.

**Git integration** — `push`/`pull`/`sync` move work between worktree
branches and the integration branch (default `main`,
`LOOM_DEFAULT_BRANCH`/per-repo override). On merge conflicts, loom invokes
an AI agent with a conflict-resolution prompt
(`conflict_resolution.go` → `Backend.InvokeAgentForConflicts`), emitting
`conflict_resolved`. `loom pr` creates PRs; the daemon's `EnsureEpicPR` does
the same for epic branches.

---

## WebUI Server (Cortex)

`internal/webui/server.go` + `routes.go`. Serves the embedded React frontend
(SPA fallback; `--dev` serves from disk) and the REST API.

**Middleware chain** (outermost first): rate limit → security headers
(optional HSTS) → API-key auth (key at `~/.loom/webui-api-key`) → CORS →
mux, wrapped in h2c.

**API groups** (full reference: [docs/api.md](api.md)):

| Group | Backing |
|-------|---------|
| Issues, comments, deps, ready/blocked, graph | bd RPC pool |
| SSE `/api/events` | `DaemonSubscriber` long-polls bd mutations (`wait_for_mutations`, fallback polling + DB-mtime watch) → `SSEHub` fan-out |
| Terminal `/api/terminal/*`, `/api/agents/{name}/terminal/*` | `TerminalManager`: tmux sessions attached over `creack/pty`, one-time-token WS auth; default command `loom lead` |
| Fleet `/api/fleet/*` (Redis only) | `internal/webui/fleet`: Lua CAS claim store, JWT auth (signing keys in Redis), claim timeout enforcer |
| Git/files `/api/agents/{name}/git/*`, `/files` | implementations injected from `internal/cli` (`NewGitOps()`) |
| Logs `/api/agents/{name}/logs`, `/api/tasks/{id}/logs/{phase}` | tails `~/.loom/logs` via fsnotify, base64 chunk SSE |
| Editors, config, health/stats | local |
| `/api/loom/*` | SSRF-guarded reverse proxy to the dashboard API server |

**bd connectivity** — pooled unix-socket RPC clients with auto-discovery
(`BEADS_SOCKET` env, else computed socket path; walks up to find `.beads/`),
protected by the circuit breaker; handlers check out a client per request.

---

## Dashboard API Server

`internal/cli/serve.go` (`--port`, default 8081) exposes
`/api/status|agents|tasks|stats|sync|usage|workspaces|observability/*` plus
Prometheus `/metrics` (KEDA-oriented). Its data comes from the **monitor
collector** (`monitor.go`) — the same code that powers the `loom monitor`
TUI — which runs `bd ready/blocked/stats` and per-worktree git in parallel.
Results are cached for 2 s (`serve_cache.go`) so frontend polling doesn't
fork 60–90 subprocesses per cycle. `loom serve` also auto-starts the bd
daemon (unless `--no-daemon`) and, when Redis is configured, the fleet
endpoints' supporting services and the **stale detector** (leader-elected
via a Redis lock) whose `Reconciler` resets claims of workers that stopped
heartbeating (`internal/kv`).

---

## Events & Observability

`internal/events` is loom's own observability stream, disjoint from the
webui's SSE mutations: daemon and agents emit `agent_*`, `epic_*`, `task_*`,
`pr_created`, `conflict_resolved`, `health_check` events through an
`Emitter` into day-rotated JSONL files under `.loom/events/` (50 MB
rotation). `LOOM_EVENTS_DIR` propagates the destination to subprocesses.
Optional OTLP export mirrors events as traces/metrics
(`internal/events/otelexport`, configured by `daemon.otel`). The dashboard
API's `/api/observability/*` endpoints query these files with a metrics
cache.

---

## Data Stores

| Store | Location | Owner |
|-------|----------|-------|
| beads SQLite (+WAL) | `.beads/beads.db` | bd daemon |
| beads JSONL export | `.beads/issues.jsonl` (+ config, lock, log) | bd; synced on the `beads-sync` git branch (`docs/beads-sync.md`) |
| bd socket | `.beads/bd.sock` or `/tmp/beads-<hash>/bd.sock` | bd |
| Daemon state | `.loom/daemon.pid`, `.loom/daemon.lock`, `.loom/daemon-agents.json` | loom daemon |
| Daemon agent logs | `.loom/logs/<role>-<worktree>.log` | loom daemon |
| Router logs | `~/.loom/logs/agents/<agent>.log`, `~/.loom/logs/tasks/<id>/{planning,implementation}.log` | log-router (read by webui log streaming) |
| Event JSONL | `.loom/events/events-YYYY-MM-DD.jsonl` | `internal/events` |
| Usage | `<beadsDir>/usage.jsonl` | `internal/usage` |
| Per-worktree agent state | `<lockDir>/.agent.lock`, `.agent.checkpoint.json` | agent / daemon |
| Global config + webui API key | `~/.loom/config.yaml`, `~/.loom/webui-api-key` | loom |
| Redis (optional) | fleet claims/registrations/JWT keys, `terminal:meta:<session>`, worker heartbeats + stale-detector leadership | `internal/webui/fleet`, `tabmeta`, `internal/kv` |

---

## Testability

`internal/cli` stays testable through a dependency container
(`deps.go`: `Git`, `Exec`, `FS`, `Clock`, `BD` interfaces), package-level
swappable `execCommand`/`lookPath`, and mockable backend invokers.
`scripts/check-no-raw-exec.sh` enforces that subprocess calls go through the
container. Quality gates: `make gate` (Go + frontend), `gate-e2e`,
`gate-e2e-full` (see `docs/testing/`).
