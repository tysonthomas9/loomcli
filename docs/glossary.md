# Glossary

Canonical definitions for loom's domain vocabulary. When docs, code, or UI copy
disagree with this file, this file wins — fix the other one.

Terms are grouped by area. "Not to be confused with" notes call out known
overloads; several terms in this codebase have historically meant two or three
different things, and the point of this file is to pin each word to one meaning.

## Core execution model

### loom / loomcli
`loom` is the CLI binary; `loomcli` is the repository and Go module
(`github.com/tysonthomas9/loomcli`). The web UI is called **Cortex** (see
below). Prefer "loom" when talking about the tool, "loomcli" only for the repo.

### Agent
A supervised AI coding session bound to one git worktree and one role. In the
CLI, "agent" and "worktree" are near-synonyms (`loom list` lists agents ==
worktrees). Under the daemon, an agent is an `AgentProcess`: a subprocess with
restart tracking, an assigned epic, and a backend failover index
(`internal/cli/daemon.go`).

*Not to be confused with:* the **backend** (Claude, Codex, OpenCode) that the
agent invokes. Some UI design docs use "agent" for the backend vendor; that
usage is deprecated — say "backend".

### Worktree
A git worktree under `./worktrees/<name>` (legacy mode) or under a workspace
directory (workspace mode), each on its own branch, where exactly one agent
works at a time (`internal/cli/worktree.go`).

### Workspace
A named group of repositories registered in `~/.loom/config.yaml`, with a path
and a `repos` list. Enables **workspace mode** (`internal/cli/config.go`,
`workspace_cmd.go`). This is the only canonical meaning of "workspace" in
loomcli.

*Not to be confused with:*
- the fleet-db **workspace** — a Redis key namespace / tenant string
  (`LOOM_FLEETDB_WORKSPACE`); qualify it as "fleet-db workspace";
- the daemon RPC `workspace_path` parameter, which is the beads DB directory;
- UI copy that says "workspace" when it means a single worktree — that usage
  is wrong and should be fixed to "worktree".

### Legacy mode / workspace mode
The two `ResolverMode`s for discovering worktrees: legacy scans `./worktrees/`
in the current repo; workspace mode reads worktrees and repos from
`~/.loom/config.yaml`. The mode determines lock-file placement and the beads
directory (`internal/cli/worktree.go`, `lock.go`).

### Role
The behavior profile an agent runs under: built-in `plan` and `task`, or custom
roles defined in `loom.yaml` under `roles:`. A `RoleConfig` carries the prompt
file, model, task filter, backend, path patterns, skills, max priority,
max concurrency, and tool restrictions (`internal/cli/daemon_config.go`).

### Backend
The AI coding CLI an agent invokes: `claude`, `codex`, `opencode`, or
`external`. Resolution order: `--backend` flag > `LOOM_BACKEND` > project
`loom.yaml` > `~/.loom/config.yaml` > `claude`. Roles can list
`fallback_backends` for failover (`internal/cli/backend.go`).

*Not to be confused with:* the **issue backend** (beads vs fleet-db) — a
different axis entirely; always qualify as "issue backend".

### Auto mode
Continuous loop mode (`--auto`): the agent polls for ready tasks, invokes the
backend once per task, and exits on Ctrl+C, `--max-tasks`, or
`--idle-timeout`. Tracks poll interval, parent-epic scoping, and no-progress
backoff (`internal/cli/automode.go`).

### Daemon (loom daemon)
The agent supervisor started by `loom daemon`: reads `loom.yaml`, spawns and
restarts agents per the restart policy, assigns epics, enforces concurrency
limits, and emits daemon events. State lives in `.loom/` (`daemon.pid`,
`daemon.sock`, `daemon.lock`, `daemon-agents.json`) (`internal/cli/daemon.go`).

*Not to be confused with:* the **bd daemon** — the beads sync daemon that
auto-commits and pushes JSONL to the `beads-sync` branch
(`docs/beads-sync.md`). `GET /api/daemon/status` reports the bd daemon;
`loom daemon status` reports the loom daemon. Always qualify which daemon you
mean.

### Restart policy
Daemon supervision tuning per `loom.yaml`: `max_retries`,
`backoff_initial`/`backoff_max`, `output_timeout` (watchdog kill on log
silence), rate-limit backoff (does not count toward `max_retries` by default),
`no_work_backoff`, and `idle_poll_interval` (`internal/cli/daemon_config.go`).

## Work items and workflow

### Issue
The unit of work, stored in beads (or a fleet-db workspace). Types:
`bug | feature | task | epic | chore` (`internal/types/enums.go`).

"Issue" is the canonical noun. "Task" is acceptable when specifically meaning
an issue an agent picks up to work on; "ticket" (lead prompts) and "bead"
(beads-inherited code) are legacy synonyms — prefer "issue".

### Beads / bd
The vendored issue tracker (`third_party/beads/`) that is loom's default issue
store. `bd` is its CLI; `.beads/` is its data directory.

### beads-sync branch
A dedicated git branch holding beads JSONL data instead of `main`. The bd
daemon exports, commits, and pushes to it on DB mutations; fresh clones hydrate
from `origin/beads-sync` via `bd init` (`docs/beads-sync.md`).

### Epic
An issue of type `epic` that groups child tasks. Agents never work on epics
directly; the daemon's `EpicAssigner` assigns each agent to the
highest-priority open epic that has ready tasks, scoping that agent's task
discovery to `--parent` (`internal/cli/daemon_epic.go`, `taskfilter.go`).

An agent with an assigned epic is in **epic mode**; with none, it works the
whole backlog (**non-epic mode**). When an epic runs out of ready tasks the
daemon emits `epic_exhausted` and reassigns the agent.

### Design (field)
The `design` field on an issue holds the approved implementation plan. Its
presence is the pivot of the plan→implement workflow: planning roles select
issues *without* a design; implementation roles select issues *with* one
(`internal/cli/taskfilter.go`).

### needs-revision
The label added when a plan review is rejected. An issue that has a design
*plus* this label routes back to planning instead of implementation
(`internal/cli/taskfilter.go`). UI copy such as "Needs Review" and the
`[Need Review]` title prefix refer to this same gate; the label and the
`review` status are what the code actually keys off.

### Ready / blocked
An issue is **ready** when it is open or in progress with no unsatisfied
blockers (`bd ready`, `GET /api/ready`). It is **blocked** when it has open
dependencies of a type that affects ready work: `blocks`, `parent-child`,
`conditional-blocks`, `waits-for` (`internal/cli/taskfilter.go`).

### Task filter
A named predicate on a role (e.g. `task_filter: has_design`) selecting which
issues that role may claim. Implemented by the predicates in
`internal/cli/taskfilter.go` (`NeedsPlan`, `ReadyToImplement`,
`IsWorkableTask`), which must stay in sync with the frontend's
`issueCategory.ts`.

### Claim
`loom claim <task-id>`: records in the agent's lock file which task the agent
has taken, so `loom monitor` can attribute work (`internal/cli/claim.go`).

*Not to be confused with:* **fleet claim** (`POST /api/fleet/claim`) — an
atomic Redis-backed assignment of a task to a fleet worker. Unrelated
mechanism; always say "fleet claim".

### Lock / lock file
`.agent.lock` in a worktree (or the workspace lock directory) holding the
owning agent's PID, command, name, current task, and execution state
(`active` = backend executing, `idle` = polling). Prevents two agents from
sharing a worktree (`internal/cli/lock.go`).

*Not to be confused with:* the beads **ExclusiveLock** (holder/pid/hostname)
that tells the bd daemon to skip a database (`internal/types/lock.go`).

### Checkpoint
`.agent.checkpoint.json`, written when an agent exits non-zero: agent name,
task ID, epic ID, truncated git diff, exit code, and error class. Lets the next
session resume where the failed one stopped (`internal/cli/checkpoint.go`).

### Recover
`loom recover <worktree>`: clears stale locks and resets tasks stuck in
`in_progress` back to open/unassigned (`internal/cli/recover.go`).

### Error class
`agenterr.ErrorClass`: classification of an agent subprocess failure —
RateLimited, AuthFailure, BillingError, ModelNotFound, ContextOverflow,
Timeout, Transient, NoWork, Unknown. Drives retry-vs-fatal decisions and
daemon restart behavior; classified per backend (`internal/agenterr/`).

### Agent state
`types.AgentState`: `idle`, `spawning`, `running`, `working`, `stuck`, `done`,
`stopped`, `dead` (timeout-inferred) (`internal/types/enums.go`).

*Not to be confused with:* the lock file's two-value execution state
(`active`/`idle`) or the UI's status dots (Ready/Working/Error), which are
coarser views over this.

## Git integration

### Integration branch
The branch agents integrate into — default `main`, configurable via
`LOOM_DEFAULT_BRANCH` or per-repo `default_branch`. `loom push` sends a
worktree branch into it, `loom pull` brings it into worktrees, `loom sync`
does both.

### AI conflict resolution
On a merge conflict during push/pull, loom launches an AI agent with a
generated conflict-resolution prompt to resolve the conflicted files, then
emits a `conflict_resolved` daemon event
(`internal/cli/conflict_resolution.go`).

### Landing the plane
The mandatory session-close protocol in `AGENTS.md`: file follow-up issues,
run quality gates, update issue status, sync and push, clean up, hand off.
Work is not complete until `git push` succeeds.

### Quality gate
`make gate` (and `gate-e2e`, `gate-e2e-full`): the pre-push check suite — Go
tests plus frontend checks, optionally Playwright API e2e and container tests
(`docs/testing/README.md`).

## Server, UI, and fleet

### Cortex
The name of the loom web UI served by `loom serve` (README calls it "Web UI";
Cortex is the product name used in the frontend and design docs).

### Lead
The human-collaborative agent mode (`loom lead`): reviews and approves or
rejects plans, creates issues, triages and prioritizes the backlog, and
manages dependencies. Requires no worktree (`internal/cli/lead.go`). The
Cortex "Talk to Lead" terminal runs `loom lead` over WebSocket.

### Fleet / fleet worker
Redis-backed multi-server task distribution. A **fleet worker** registers via
`POST /api/fleet/register` (authenticated with `X-Fleet-API-Key`), receives a
short-lived JWT, then claims tasks, heartbeats, and reports completion
(`docs/api.md`, `internal/webui/fleet/`).

*Not to be confused with:* **fleet-db**, the issue-tracking backend (below).

### fleet-db
An alternative, Redis-backed, event-sourced issue-tracking backend selectable
per project instead of beads, accessed in-process rather than via the `bd`
subprocess (`docs/design/fleetdb-integration.md`). fleet-db has its own
glossary in its repository; its "workspace" is a tenant namespace, not a loom
workspace.

### Stale detector / reconciler / orphaned task
A leader-elected loop that flags fleet workers whose heartbeat exceeds the
stale threshold; the **reconciler** then resets their **orphaned tasks** so
they can be reassigned (`internal/kv/stale.go`, `reconcile.go`).

*Not to be confused with:* beads-level "orphan detection"
(`internal/types/orphans.go`), which finds issues with no parent.

### Mutation (SSE event)
An issue change broadcast to connected browsers over `GET /api/events`:
`create`, `update`, `delete`, `comment`, `status`, `bonded`, `squashed`,
`burned` (`docs/api.md`, `internal/webui/sse.go`).

### Daemon event
A JSONL observability record written to `.loom/events/` by the loom daemon:
`task_claimed`, `task_started`, `task_completed`, `task_failed`,
`agent_started/restarted/stopped`, `epic_assigned`, `epic_exhausted`,
`pr_created`, `conflict_resolved`, `health_check`
(`internal/events/event.go`).

*Not to be confused with:* SSE mutations above — both get called "events";
qualify as "SSE mutation" vs "daemon event".

### Log router / phase
`loom log-router` (`internal/logrouter/`) tees agent stdout into per-agent and
per-task log files with rotation. Each task keeps two log streams, one per
**phase**: `planning` and `implementation` (`docs/api.md`).

### Usage
Per-session token accounting — input/output/cache tokens plus estimated USD
cost from per-backend pricing tiers, keyed by agent, backend, task, and epic.
Surfaced by `loom usage` and the Usage dashboard (`internal/usage/`).

### Circuit breaker
The Closed/Open/HalfOpen state machine guarding the webui→daemon RPC
connection pool; state is reported in `GET /api/health`
(`internal/circuitbreaker/`).

## Inherited beads vocabulary

Terms that arrive via the vendored beads code and surface in loom's types or
API without being loom concepts. Defined here so readers aren't left guessing;
avoid introducing them into new loom code or docs.

### Molecule / mol_type
Beads' swarm-coordination work grouping. `mol_type` values: `swarm`
(coordinated multi-worker), `patrol` (recurring operational work), `work`
(regular, default). Exposed as a query parameter on `GET /api/ready`
(`internal/types/enums.go`).

### Hooked (status)
Beads status meaning work is attached to an agent's hook (GUPP protocol);
inherited, not used by loom's own workflow (`internal/types/enums.go`).

### Work type / sort policy
`work_type`: `mutex` (one worker, exclusive — default) vs `open_competition`
(many submit, buyer picks). `sort_policy` orders ready work: `hybrid`,
`priority`, `oldest` (`internal/types/enums.go`).
