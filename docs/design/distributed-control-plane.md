# Distributed Control Plane Architecture

> **Status:** Partially implemented — the conceptual model is still current;
> the concrete data model and phase plan below were superseded on 2026-06-03 by
> `docs/design/fleetdb-agent-platform-v2-proposal.md` and by shipped code in
> `internal/domain/platform.go`, `internal/domain/control_plane.go`,
> `internal/store/control_plane_store.go`, and `internal/driver`.
> *audited 2026-07-23*

**Date:** 2026-04-30
**Related:** `loomcli-26v50`, `loomcli-37h1h`,
`docs/design/distributed-control-plane-data-model.md`,
`docs/design/fleetdb-agent-platform-v2-proposal.md`,
`docs/design/2026-07-23-control-plane-as-built.md`

Read this document for the *shape* of the system: what is global vs local vs
observed, why execution is pull-authoritative, what a lease must guarantee,
and which failure modes to design against. Those sections verified clean on
audit. Do not read it for record shapes, command names, or phase status —
for those, see the as-built map
(`docs/design/2026-07-23-control-plane-as-built.md`) and the code it cites.

## Purpose

This document defines the target architecture for moving loom from a
local YAML/worktree supervisor into a fleet-db-backed control plane that
works for both:

- single-user local development
- multi-user distributed execution

The central rule is simple:

> Fleet-db owns shared intent and coordination. Nodes own local side
> effects. Observed state is reported by nodes and expires.

The current codebase only partially follows this rule. This document
records the intended boundary, the main gotchas, and the migration path
needed to make the system coherent.

For a model-by-model comparison of the current codebase against this
target architecture, see
`docs/design/distributed-control-plane-data-model.md`.

## Terms

| Term | Meaning |
|---|---|
| Control plane | The shared API and database that stores intent, metadata, leases, runs, audit, and observed status. In this document, unqualified "control plane" means fleet-db plus the `loom serve` layer above it; see `docs/loom-glossary.md`. |
| Node | A machine or sandbox capable of running work. Examples: a laptop, CI runner, remote sandbox, Kubernetes pod. |
| Worker | **Worker-as-process** — a process on a node that asks for work and executes task runs. This is one of several senses of "worker" in the repo (role kind, agent mode, the `loom worker` command); see `docs/loom-glossary.md` before reusing the word. |
| Runtime provider | The mechanism used to run work: local loomd, remote sandbox, Kubernetes, bare metal, etc. |
| Desired state | User/team intent, such as "run task ACME-123" or "profile falcon is enabled". |
| Observed state | Node-reported fact, such as "run is running on node X" or "checkout is dirty". |
| Lease | Time-bounded ownership with a fencing token. Only the holder may mutate the owned run/task. |
| Fencing token | A unique token returned on lease acquisition. Required for heartbeat, completion, and release. |
| Checkout binding | Local mapping from a global repo identity to a node-local filesystem path. |

## State Ownership

Every field must be classified as global, local, or observed.

Ask this question:

> Would this value be the same for Alice's laptop, Bob's workstation,
> and CI?

If yes, it is global. If no, it is local. If it is reported by a node
and can become stale, it is observed.

### Global State

Global state is canonical, shared, and stored in fleet-db.

| Area | Global fields |
|---|---|
| Workspace | key, name, description, lifecycle state |
| Repo | name, remote URL, default branch, groups, source repo ID |
| Role | prompt, model, backend, tools, filters, concurrency and budget policy |
| Task/issue | title, status, priority, dependencies, assignee, repo/source ID |
| Worker profile | named execution profile, role, repo scope, filters, desired state |
| Task run | run ID, task ID, workspace, role/profile used, status, timestamps |
| Node | node ID, owner/service account, labels, capabilities, drain status |
| Lease | owner node/worker, fencing token, expiry |
| Artifact | patch, commit ID, transcript pointer, logs pointer, test result |
| Audit | who changed desired state, who claimed work, who completed work |

Global state must not contain user-specific absolute filesystem paths.

### Local State

Local state is true only on one machine or sandbox. It lives in
`~/.loom/state.json`, node-local storage, or the runtime provider.

| Area | Local fields |
|---|---|
| UX preference | last selected workspace |
| Workspace checkout | local workspace root path |
| Repo checkout | local path for `workspace/repo` |
| Worktree | local path for `workspace/repo/run-or-profile` |
| Terminal | PTY process, terminal socket, scrollback cache |
| Git working tree | index state, uncommitted files, local branch checkout |
| Daemon runtime | PID, local socket, process group, local logs |
| Secrets | API keys, credential helper access, local secret files |
| Tool inventory | installed CLIs and versions, backend availability |

Local state can be reported as observed status, but the source of truth
remains local.

### Observed State

Observed state is stored globally for UI and coordination, but it is
written by nodes and must have timestamps or TTL semantics.

| Area | Observed fields |
|---|---|
| Node | last heartbeat, current capacity, version, tool inventory |
| Checkout | branch, commit, dirty flag, last scan time |
| Run | starting/running/failed/completed, exit code, error class |
| Agent/profile | currently active on node X |
| Git | changed file summary, diff summary |
| Terminal | open/closed, attached users, last activity |

The UI must render staleness explicitly:

```text
desired=running observed=unknown last_heartbeat=4m ago
```

Do not display stale observed data as current fact.

## Deployment Modes

Local and distributed modes use the same model. Local mode is just the
distributed model with one user, one node, and a locally managed control
plane.

### Single-User Local Mode

```text
laptop
  fleet-db embedded/local
  loomd node-local
  ~/.loom/state.json
  repo checkouts
  agent worktrees
```

Behavior:

1. `loom workspace add ACME` creates global metadata in local fleet-db.
   *(Shipped.)*
2. Local repo paths are bound. *(No `loom checkout` command exists — see
   "Local Checkout Commands" below. Local paths live in
   `internal/bootstrap/statecache.go:33-52` and are resolved by
   `internal/localworkspace/localworkspace.go:28-30`.)*
3. `loom worker profile add falcon --role task --repo backend` creates
   a global profile. *(Shipped, scoped to the active workspace:
   `internal/cli/serve/worker/profile_cmd.go`.)*
4. A task run and lease are created. *(There is no `loom task run` verb —
   `loom task` takes a worktree/workspace name. Task runs are created by
   `internal/driver` via `store.TaskRunStore.Create` /
   `ClaimQueued`, `internal/store/platform_store.go:762-773`.)*
5. local `loomd` claims the run, performs local effects, and reports
   observed state. *(Shipped as the driver executor / task worker:
   `internal/driver/executor.go`, `internal/driver/task_worker.go`.)*

Local mode may use:

- embedded fleet-db
- dev auth
- automatic single-node registration
- local state cache

Local mode should still use leases. That prevents duplicate local
starts and keeps behavior aligned with distributed mode.

### Multi-User Distributed Mode

```text
shared fleet-db/control plane
  Alice laptop: loomd node-alice
  Bob workstation: loomd node-bob
  CI runner: loomd node-ci
  remote sandbox: ephemeral node per run
```

Behavior:

1. Users mutate intent through CLI/UI.
2. Nodes register with labels and capabilities.
3. Workers pull eligible work.
4. Fleet-db grants leases with fencing tokens.
5. Nodes execute locally and heartbeat observed state.
6. Completion is accepted only with a valid lease token.

User identity and node identity are separate:

| Identity | Purpose |
|---|---|
| User | Allowed to change intent and view data. |
| Node | Allowed to execute local side effects. |
| Worker/run | Current execution context. |
| Lease | Current ownership of a task/run/resource. |

## Command Model

The command surface should separate shared control-plane changes from
local checkout materialization.

### Control-Plane Commands

These mutate global state in fleet-db. The shipped surface is
**workspace-scoped, not workspace-positional** — `repo`, `role`, and
`worker profile` operate on the active workspace, so the workspace name is
not an argument. The active workspace comes from `--workspace` or
`LOOM_WORKSPACE` only: `loom workspace use <KEY>` persists a *UI selection
hint* in `~/.loom/state.json` and its own help says "Runtime commands no
longer use this as an implicit default"
(`internal/cli/workspace/workspacev2_cmd.go:47-55`).

```bash
loom workspace add <KEY>           # create in fleet-db
loom workspace list
loom workspace show [KEY]          # defaults to active
loom workspace set <KEY>           # update settings
loom workspace status [KEY]        # lifecycle state
loom workspace use <KEY>           # UI selection hint only
loom workspace remove <NAME>       # removes workspace AND its worktrees
                                   #   (--keep-worktrees for metadata only)

loom repo add <NAME> <REMOTE_URL>  # active workspace
loom repo list
loom repo show <NAME>
loom repo remove <NAME>

loom role add <NAME>               # active workspace
loom worker profile add <NAME> --role task --repo backend
loom worker profile list
loom worker profile show|set|unset|remove
```

Verified against `loom workspace --help`, `loom repo add --help`,
`loom role add --help`, `loom worker profile --help`;
`internal/cli/serve/worker/profile_cmd.go`.

### Local Checkout Commands — NOT IMPLEMENTED

**Status: proposed, never built.** There is no `loom checkout` command in
`loom --help`. This block is retained because splitting local
materialization from control-plane metadata is still an open intention
(Phase 2 below), and because it records the intended separation.

```bash
# NOT IMPLEMENTED — proposed surface only
loom checkout create ACME
loom checkout bind ACME backend /path/to/backend
loom checkout list
loom checkout doctor ACME
loom checkout delete ACME
```

What exists instead: local paths are read and written through
`bootstrap.StateCache` (`internal/bootstrap/statecache.go:33-52` —
`WorkspaceLocalState{Path, Repos, Agents}`) and resolved by
`internal/localworkspace` (`localworkspace.go:28-30`, `RepoPath`). The cache
is explicitly documented as regenerable and "never load-bearing for
correctness" (`statecache.go:30-32`).

Deleting global metadata and deleting local files must be separate
operations. **Partially honored, with the default inverted.** There is no
`loom workspace delete`; the destructive command is `loom workspace remove
<name>`, whose help is "Remove a workspace and its worktrees" — by default it
does both at once. The metadata-only path exists but is opt-*out*, not
opt-in: `--keep-worktrees` ("only remove from config") is the escape hatch,
where this design asked for metadata-only by default plus an explicit
`--delete-local` opt-in. There is no `--delete-local` flag
(`internal/cli/workspace/workspace_cmd.go:86-99`).

## Task-Driven Execution

Task-driven execution is the preferred distributed model.

Avoid making long-lived named agents the unit of distributed ownership.
Use task leases and task runs as the authoritative execution primitive.

### Agent-Driven Model

```text
desired_state(agent falcon) = running
daemon starts falcon
falcon polls for tasks
```

This is harder to distribute because ownership is attached to a process
that may be local, long-lived, and stateful.

### Task-Driven Model

```text
task ACME-123 is ready
worker claims ACME-123 with a lease
worker runs one task
worker reports result with fencing token
```

This maps better to distributed execution because tasks are finite,
leaseable, retryable, and auditable.

### Core Entities

| Entity | Purpose |
|---|---|
| Task | Unit of work. |
| Role | Policy for how work should be done. |
| WorkerProfile | Named queue/filter/profile, for example `falcon`. |
| Node | Machine or sandbox capacity. |
| TaskRun | One execution attempt for one task. |
| TaskLease | Time-bounded ownership of a run or task. |
| Artifact | Patch, commit, transcript, logs, test output. |

### Run Lifecycle

```text
queued -> leased -> starting -> running -> completing -> completed
                         |          |            |
                         v          v            v
                      expired     failed      rejected
                         |
                         v
                      retryable
```

Completion requires:

- valid run ID
- valid lease token
- current lease holder
- non-expired lease
- accepted final state transition

If a node dies, the lease expires and the scheduler can retry.

### Worker Profiles

Profiles provide stable UX without making the process identity the
ownership primitive.

```text
profile falcon
  role=task
  backend=codex
  repos=backend
  max_priority=2
  parent_epic=loomcli-26v50
```

A local daemon can run a worker loop for a profile, but each actual unit
of work still gets a task lease.

## Push vs Pull

Execution should be pull-based for correctness and push-assisted for
latency.

### Pull-Based Authority

Workers own work only after a successful claim:

```text
node -> control plane: give me eligible work
control plane -> node: task run + lease token
node -> control plane: heartbeat with token
node -> control plane: complete with token
```

Pull-based ownership:

- works behind NAT
- gives natural backpressure
- lets nodes advertise capacity
- centralizes lease/fencing checks
- fits remote sandboxes and local nodes

### Push Notifications

Push is only a wake-up or streaming path:

```text
control plane -> node: work may be available
control plane -> UI: status changed
node -> UI: logs/terminal output
```

Push must not grant ownership. A dropped push event must not break the
system because polling/claim still works.

## Lease Semantics

Leases need owner-aware fencing, not just `task_id -> worker_id`.

Every lease should include:

```text
resource_type
resource_id
holder_node_id
holder_worker_id
token
expires_at
version
```

Required operations:

| Operation | Requirement |
|---|---|
| Acquire | Atomic compare-and-set from no lease/expired lease to new holder/token. |
| Renew | Only current holder with current token can extend expiry. |
| Complete | Only current holder with current token can complete. |
| Release | Only current holder with current token can release. |
| Expire | Expired leases can be reclaimed. |

Gotchas:

- Do not let a stale worker complete after losing a lease.
- Do not release another worker's lease by task ID alone.
- Heartbeat must renew active run leases, not only node registration.
- Timeout recovery must update both lease state and task/run state.
- Multi-control-plane timeout scanners need their own coordination or
  idempotent fencing.

## Runtime Providers

Runtime providers perform local effects for a run.

**The interface proposed here was never built.** There is no
`RuntimeProvider` Go interface anywhere in `internal/`. `domain.RuntimeProvider`
(`internal/domain/control_plane.go:21-29`) is a *string enum*
(`local | e2b | kubernetes | ci | other`) recorded on a `Node`, not a
behavioural contract. The shipped abstraction is much narrower:

- `sandbox.SandboxLauncher` (`internal/driver/sandbox/launcher.go:83`) —
  one method, `Launch(ctx, LaunchSpec) (SandboxProcess, error)`.
- `sandbox.SandboxProcess` (`launcher.go:106`) — `Wait` / `Kill`.
- `sandbox.IsolatingLauncher` (`internal/driver/sandbox/policy.go:43`) —
  marks launchers whose runtimes actually isolate.
- Selected at `internal/driver/executor.go:69`, `ResolveSandboxLauncher()`.

Exec / OpenPTY / ListFiles / ReadFile / WriteFile / GetDiff have no
provider-level equivalent. They are served by separate web UI modules
against the node's own filesystem, not routed through a runtime provider.

The original proposal is kept below as history, because the *scope* it
sketched — what a runtime abstraction would eventually have to cover — is
still the open question.

```go
// PROPOSED 2026-04-30, NEVER IMPLEMENTED
type RuntimeProvider interface {
    StartRun(ctx context.Context, spec RunSpec) (*RunHandle, error)
    Exec(ctx context.Context, runID string, cmd CommandSpec) (*CommandResult, error)
    OpenPTY(ctx context.Context, runID string, spec PTYSpec) (*PTYHandle, error)
    ListFiles(ctx context.Context, runID string, path string) ([]FileInfo, error)
    ReadFile(ctx context.Context, runID string, path string) ([]byte, error)
    WriteFile(ctx context.Context, runID string, path string, data []byte) error
    GetDiff(ctx context.Context, runID string) (*DiffSummary, error)
    StopRun(ctx context.Context, runID string) error
}
```

Initial providers:

| Provider | Purpose | Status |
|---|---|---|
| local | Interactive local development and local daemon execution. | Shipped. `domain.RuntimeProviderLocal` is what `internal/driver` registers nodes with (`executor.go:447`, `task_worker.go:195`). |
| e2b | Ephemeral isolated cloud task runs. | Never built. See below. |
| future:kubernetes | Team/cloud worker pools. | Not built; enum value only. |

## Ephemeral Remote Sandboxes (proposed as E2B; shipped as Daytona)

**E2B was never implemented.** The only occurrence of E2B in the Go tree is
the unused enum value `RuntimeProviderE2B` at
`internal/domain/control_plane.go:25`. The remote sandbox provider that
actually shipped is **Daytona** — `internal/driver/bundled_runner.go:16-20`
(`DaytonaTaskRunnerEntrypoint = "daytona-task-runner"`, which provisions a
Daytona sandbox, clones the repo, and runs the agent inside it),
`internal/driver/task_bridge.go`, `internal/runtimepreflight/preflight.go`,
and the TypeScript runner `internal/workflows/builtin/daytona-task-runner.ts`.
Local isolation shipped separately as rootless containers
(`internal/driver/sandbox/container.go`; see AGENTS.md "Workflow Sandbox").

Everything below is **provider-agnostic guidance** — it applies to Daytona
and to any future ephemeral remote runtime. Read "E2B" as "the ephemeral
remote sandbox provider".

An ephemeral remote sandbox is a good fit for task-driven execution.

Use one for:

- isolated task execution
- CI-like validation
- code review agents
- risky/untrusted code
- browser/computer-use tasks
- clean environment runs

Do not make the sandbox provider the control plane. Fleet-db remains the
control-plane data service. Sandboxes perform local effects for a specific
run.

### Sandbox Run Flow

```text
1. Scheduler creates TaskRun.
2. Scheduler chooses the runtime provider.
3. Control plane creates or requests a sandbox.
4. Sandbox worker starts with task_run_id and bootstrap token.
5. Worker pulls/accepts assigned run lease.
6. Worker clones repo or restores cached template.
7. Worker runs the agent/test/review command.
8. Worker streams logs and status.
9. Worker uploads patch, transcript, test results, and metadata.
10. Worker completes with lease token.
11. Sandbox is killed or returned to a warm pool.
```

### Sandbox Global vs Local

Global in fleet-db:

```text
runtime_provider
sandbox_id
task_run_id
repo remote URL
branch/commit
run status
artifact pointers
```

Local to the sandbox:

```text
checkout path
git index
uncommitted files
temporary env vars
PTY process
installed dependencies
```

### Sandbox Gotchas

- Use short-lived GitHub App tokens or scoped deploy tokens.
- Inject only scoped secrets needed for the run.
- Upload artifacts before sandbox expiry.
- Design for sandbox timeouts and retries.
- Control cost and quotas with concurrency limits.
- Use templates for common dependencies.
- Treat sandbox ID as runtime metadata, not user identity.
- Completion still requires the run lease token.

## Git, Files, Diff, and Terminal

Git, file explorer, diff, and terminal are node-local capabilities
routed through the control plane.

The control plane decides:

- who is allowed
- which node/sandbox owns the run/session/checkout
- where the request should be routed
- what should be audited

The node performs:

- filesystem reads/writes
- git status/diff/log
- PTY creation and I/O
- process control

### Git Diff

```text
UI -> control plane -> node runtime -> git diff -> UI
```

Requests should target a run or checkout binding, not arbitrary paths:

```text
GET /api/workspaces/ACME/runs/run-123/diff
```

The control plane resolves `run-123` to:

```text
node_id=node-alice
repo=backend
worktree_ref=wt-abc
```

### File Explorer

```text
GET /api/workspaces/ACME/runs/run-123/files?path=src
GET /api/workspaces/ACME/runs/run-123/file?path=src/main.go
PUT /api/workspaces/ACME/runs/run-123/file
```

Node-side validation must enforce:

- relative paths only
- no `..`
- no symlink escape from checkout root
- file size limits
- binary handling
- permission checks for writes
- no access to secret files outside allowed roots

### Terminal

Terminals are local PTYs.

Local mode:

```text
browser websocket -> local loomd -> PTY
```

Distributed mode:

```text
browser websocket -> control plane -> node outbound tunnel -> PTY
```

Nodes should connect outward to the control plane so laptops and
sandboxes can work behind NAT and firewalls.

Terminal session metadata can be global:

```text
session_id
node_id
workspace
repo
run_id
status
attached_users
last_activity
```

The PTY file descriptor, process, and raw local socket are local only.

## Scheduler and Placement

Start with a simple scheduler:

1. task is ready
2. dependencies are closed
3. node has required repo checkout or runtime can clone
4. node has required backend/tool
5. node has capacity
6. node is authorized
7. lease can be acquired

Represent placement as labels and capabilities early:

```text
node labels: os=linux, pool=ci, user=alice
capabilities: git,codex,claude,browser,daytona
profile requires: backend=codex, repo=backend
```

Future scheduling needs:

- pin to node
- prefer CI
- drain node
- priority
- fairness
- role budgets
- workspace/org quotas
- GPU/tool labels
- user-only execution

## Security Model

Security must separate user identity, node identity, and run ownership.

| Identity | Scope |
|---|---|
| User token | UI/CLI changes to desired state and read access. |
| Node token | Node registration, heartbeat, runtime effects. |
| Run lease token | Completion and mutation rights for one run. |
| Secret reference | Names a secret without exposing the value globally. |

Rules:

- Node APIs must not trust `worker_id` in request body if auth claims
  already identify the worker.
- Completion must require a valid lease token.
- Nodes can only claim work they are authorized to run.
- Secrets are referenced globally and resolved locally or by a scoped
  secret service.
- Terminal access must be explicit and permissioned.
- Logs/transcripts need secret redaction or scoped retention.

## Architecture Gotchas

### Local/Global Drift

The most likely failure mode is allowing fields to leak across the
state boundary.

Bad examples:

- storing `/Users/alice/src/acme` as global workspace path
- using local checkout existence as proof that a repo exists globally
- letting `state.json` become required for correctness
- using workspace display name where stable workspace key is required
- storing PID/log paths as global daemon policy

Rule: global state must be machine-agnostic. Local state can point at
global IDs, but global state must not depend on local paths.

### Silent Fallbacks

In fleet-db mode, a failed store read must not silently fall back to
YAML or cached config. That converts a control-plane outage into stale
data and makes debugging nearly impossible.

Good behavior:

```text
fleet-db unavailable -> 503/unavailable with a clear error
workspace not found -> 404/not found
no local checkout -> valid workspace response plus local checkout warning
```

### Single-User Mode Hides Distributed Bugs

A one-node local setup will not expose split-brain, stale completion,
lease owner mismatch, or routing bugs. Local mode should still use
node registration, leases, and task runs so these paths are exercised
before distributed mode.

### Identity Confusion

Do not collapse user, node, worker, and lease identity.

Examples:

- A user may own multiple nodes.
- A CI node may run work for many users.
- A valid node token should not imply permission to mutate workspace
  metadata.
- A worker ID in a JSON body should not override authenticated claims.

### Filesystem Escapes

File explorer, terminal, and diff APIs must never accept arbitrary
absolute paths. They should address a checkout/worktree/run ID plus a
relative path, then enforce containment on the node.

Path validation must handle:

- `..`
- symlinks
- case-insensitive filesystems
- deleted paths
- large files
- binary files
- concurrent writes

### Git Race Conditions

Git state is mutable and local. Agents, terminals, and file explorer
writes can race.

Common cases:

- dirty worktree before run starts
- branch already exists
- worktree deleted manually
- submodule not initialized
- untracked files not captured by patch
- push succeeded but completion report failed
- completion succeeded but push failed

TaskRun artifacts should capture enough evidence to recover: final
HEAD, branch, patch, changed files, transcript pointer, test result,
and push/PR status.

### Terminal Security

Terminals can reveal secrets and mutate state. Terminal attach should
be explicitly permissioned and auditable.

Important distinctions:

- owner can read/write
- reviewer may be read-only
- admin may kill but not type
- shared terminal sessions need visible attached-user state

### Scheduler Creep

The scheduler should start simple, but its data model should not block
future needs. Labels, capabilities, capacity, drain mode, and placement
constraints should exist early even if the first scheduler uses only a
small subset.

### Runtime Provider Lock-In

Remote sandboxes, local loomd, and future Kubernetes providers should sit
behind the same runtime abstraction. Do not let provider-specific concepts
become core task/run semantics. `sandbox_id` is runtime metadata, not the
identity of the work.

## Current Codebase Audit

The current repo is partway through the migration, but it does not yet
enforce this architecture.

### What Is Good

- `internal/domain` and `internal/store` define useful shared entities.
- `internal/infra/fleetdb` implements fleet-db-backed stores.
- `internal/bootstrap/statecache.go` has the right local-state shape.
- Fleet-backed CLI commands exist for workspace, repo, role, agent
  definitions, and daemon profiles.
- Web UI has a store adapter for workspace views.
- There is a Redis-based fleet worker subsystem that can inform the
  future lease design.

### Main Problems

Audited 2026-07-23. Rows are marked FIXED, STILL TRUE, or UNVERIFIED.

| Problem | Impact | 2026-07-23 status |
|---|---|---|
| `loom workspace` mixes YAML and fleet-db subcommands. | Users can list/delete a different world than they created. | STILL TRUE as a UX problem, but not a YAML one: `loom workspace` exposes both `add` ("Create a new workspace in fleet-db") and `create` ("Create a new workspace with git worktrees") as separate verbs, and `remove` deletes both metadata and worktrees. |
| ~~`workspace create/list/remove` still mutates `~/.loom/config.yaml`.~~ | ~~Control-plane metadata and local checkout effects are coupled.~~ | FIXED. `config.yaml` is gone as a runtime source. `internal/cli/config/config.go:22-23` — "LoomConfig is a FleetDB-backed workspace view"; `LoadConfig` (`config.go:120-134`) opens the fleet-db store and overlays machine-local paths from `bootstrap.LoadStateCache` (`config.go:180`). No non-test writer of `config.yaml` remains. |
| ~~`agentdef` writes fleet-db, but `agent/task/plan` resolve YAML worktrees.~~ | Agent definitions do not drive execution. | HALF FIXED. The YAML half is gone — worktree paths now come from `bootstrap.StateCache` / `internal/localworkspace`, not YAML. The surface split is not: `loom agentdef` is still a separate top-level command whose own help says "Distinct from 'loom agent <worktree>' which runs an actual agent process. Phase 6 will unify these surfaces" — a different phase numbering than this document's Phase 3, so the two plans have diverged. |
| ~~Daemon boots from `loom.yaml` and watches YAML files.~~ | No distributed desired-state reconciler exists. | FIXED. The daemon runs a fleet-db polling reconciler: `internal/cli/daemon/daemon.go:140-141` starts `configReconciler`, which polls every 30 s (`internal/cli/daemon/daemon_reconciler.go:28-40`) and reloads via `config.LoadDaemonConfig` → `bootstrap.OpenStore` → `loadDaemonConfigFromStore` (`internal/cli/config/project.go:165-190`). |
| ~~Web UI falls back from store errors to runtime config.~~ | Fleet-db outages can look like stale data or 404. | FIXED. `internal/webui/handlers/workspace/workspace.go:30-36` returns `handler.HandleServiceError(w, err)` with no YAML/runtime-config fallback. |
| ~~Workspace middleware validates but does not canonicalize `{ws}`.~~ | Name/key ambiguity leaks into backend routing. | FIXED. `internal/webui/server/middleware/workspace.go:18-21` defines `WorkspaceRef{RequestedID, CanonicalID}`; `WorkspaceResolved` (`:77`) resolves the raw route value and `WithWorkspaceRef` (`:54-62`) injects `CanonicalID` as the context workspace. |
| Fleet worker claim path still uses local daemon RPC pool. | Redis claim primitives are not the authoritative execution lease. | STILL TRUE. `internal/webui/fleet/handlers_claim.go:13-15` imports `internal/rpc` and `internal/webui/daemon`; `Module` carries a `daemon.Pool` (`internal/webui/fleet/module.go:20`). The Redis-backed result subsystem is still there too (`internal/webui/fleet/result.go:9`). The authoritative execution lease lives elsewhere — on `domain.TaskRun.LeaseID`/`FencingToken` (`internal/domain/platform.go:513-514`). |
| Worker JWT claims are not consistently enforced in handlers. | Valid workers may impersonate other worker IDs. | UNVERIFIED — not re-checked in the 2026-07-23 audit. Do not treat as either fixed or broken without reading `internal/webui/fleet` and `internal/webui/server/middleware/auth_routes.go`. |
| `DaemonProfile` mixes global policy with local PID/log paths. | Per-node runtime settings are not cleanly separated. | STILL TRUE, and now deliberate. `internal/domain/daemon_profile.go:13-26` still carries `PIDFile`, `LogDir`, `EventsDir` on a fleet-db record; its docstring (`:9-12`) argues the split is intentional — "workspace-scoped daemon installs may want to override defaults" — and draws the line at per-host bootstrap config instead. That contradicts the "global state must be machine-agnostic" rule above; it is an unresolved disagreement, not an oversight. |

## Migration Plan

Phase status audited 2026-07-23. This plan was written 2026-04-30 and has
since diverged from the 2026-06-03 V2 plan
(`docs/design/fleetdb-agent-platform-v2-phased-delivery.md`), which uses its
own phase numbering. When two docs say "Phase 3" they do not mean the same
thing. Where the two disagree, V2 wins.

### Phase 1: Make Fleet-db Mode Honest — LARGELY DONE

- In fleet-db mode, do not silently fall back to YAML.
- Fix `/api/workspaces/active`, repo listing, delete, and create.
- Canonicalize workspace keys in middleware.
- Make `loom workspace list/delete` fleet-db-backed.
- File or fix UI regression gaps for workspace creation and switching.

Done: the store-error fallback (`internal/webui/handlers/workspace/workspace.go:30-36`),
`{ws}` canonicalization (`internal/webui/server/middleware/workspace.go:18-21`),
and the fleet-db-backed workspace view (`internal/cli/config/config.go:120-134`).
Not done: `loom workspace` still has both `add` (fleet-db) and `create`
(worktrees) verbs.

### Phase 2: Split Checkout From Workspace Metadata — NOT DONE

- Add checkout commands and local path binding model.
- Move repo and agent worktree paths into `state.json` or node-local
  storage.
- Keep `workspace delete` metadata-only by default.
- Quarantine or rename old worktree commands.

The middle bullet shipped — paths live in `bootstrap.StateCache`
(`internal/bootstrap/statecache.go:33-52`). The command surface did not:
there is no `loom checkout`, and `loom workspace remove` still deletes
metadata and worktrees together *by default* — `--keep-worktrees` gives the
metadata-only path this bullet asked for, but as an opt-out rather than the
default. Still an open intention.

### Phase 3: Unify Agent/Worker Surface — PARTIALLY DONE

- Retire public `agentdef`. — **Not done.** `loom agentdef` is still a
  top-level command under "Workspace Commands", and its help says
  "Phase 6 will unify these surfaces" (a different plan's Phase 6).
- Introduce worker profiles or make `loom agent` a parent command. —
  **Done for profiles**: `loom worker profile add|list|show|set|unset|remove`
  (`internal/cli/serve/worker/profile_cmd.go`), backed by
  `domain.WorkerProfile` (`internal/domain/platform.go:104`). Added
  *alongside* `agentdef`, not instead of it.
- Make `loom task/plan/agent` resolve fleet-db profiles first. — UNVERIFIED.
- Bind profiles to local checkout state. — UNVERIFIED.

### Phase 4: Add Distributed Runtime Primitives — DONE

- Add node registration and heartbeat. — `domain.Node`
  (`internal/domain/control_plane.go:39`), `store.NodeStore`
  (`internal/store/control_plane_store.go:37`), registered at
  `internal/driver/executor.go:452` and `internal/driver/task_worker.go:200`,
  heartbeat at `executor.go:467` and `task_worker.go:217`.
- Add task run records. — `domain.TaskRun`
  (`internal/domain/platform.go:498`), `store.TaskRunStore`
  (`internal/store/platform_store.go:762-773`).
- Add lease acquire/renew/complete/release with fencing tokens. —
  `TaskRun.LeaseID` / `TaskRun.FencingToken` (`platform.go:513-514`) plus
  `domain.AgentLease` (`control_plane.go:167`) and
  `domain.AgentOwnershipLease` (`control_plane.go:182`). Claim/heartbeat/
  complete/requeue are `TaskRunStore.ClaimQueued`/`Heartbeat`/`Complete`/
  `Requeue`.
- Add runtime provider abstraction. — **Not as specified.** See
  "Runtime Providers" above: what shipped is `sandbox.SandboxLauncher`
  (`internal/driver/sandbox/launcher.go:83`), a launch-only seam.
- Implement local provider first. — Done; `domain.RuntimeProviderLocal` is
  what nodes register with (`executor.go:447`).

### Phase 5: Rewrite Daemon as a Reconciler — PARTIALLY DONE

- Stop booting from `loom.yaml`. — **Done.** Daemon config now loads from
  fleet-db (`internal/cli/config/project.go:165-190`) and is reconciled on a
  30 s poll (`internal/cli/daemon/daemon_reconciler.go:28-40`, started at
  `internal/cli/daemon/daemon.go:140-141`).
- Daemon registers as a node. — Done for the driver executor and task
  worker (see Phase 4); the supervisor also runs a node-heartbeat goroutine
  (`internal/cli/daemon/supervisor/control_plane.go:47-49`).
- Daemon pulls eligible task runs. — Done via `TaskRuns().ClaimQueued`
  (`internal/driver/task_request.go:308`, `:561`).
- Daemon acquires leases. — Done (lease/fencing ride on the claim).
- Daemon starts local processes. — Done.
- Daemon reports observed state and artifacts. — Done; artifacts via
  `store.ArtifactStore` (`internal/store/control_plane_store.go:225`) and
  the task-run artifact ops (`internal/webui/handlers/taskrunapi/module.go:106-117`).

### Phase 6: Add E2B Provider — SUPERSEDED

E2B was never built. The ephemeral remote provider that shipped is Daytona
(`internal/driver/bundled_runner.go:16-20`,
`internal/workflows/builtin/daytona-task-runner.ts`), plus rootless-container
sandboxing for local isolation (`internal/driver/sandbox/container.go`). The
sub-bullets below still describe real requirements for any remote provider;
read "E2B" as "the remote sandbox provider".

- ~~Create E2B runtime provider.~~ Create the remote runtime provider.
- Add sandbox templates and bootstrap.
- Upload patches, logs, transcripts, and test artifacts.
- Enforce cost, timeout, and concurrency limits.

### Phase 7: Remove YAML Runtime Config — PARTIALLY DONE

- Complete `loomcli-37h1h`.
- Move daemon config to fleet-db or node-local config as appropriate. —
  **Done** (`internal/cli/config/project.go:165-190`).
- Delete or quarantine `internal/cli/config` runtime paths. — Partially:
  `internal/cli/config` still exists but is fleet-db-backed
  (`config.go:22-23`).
- Drop `gopkg.in/yaml.v3` once remaining YAML readers are gone. —
  **Not done.** `go.mod:39` still lists `gopkg.in/yaml.v3 v3.0.1` as a
  direct dependency, and `config.WorkspaceConfig` / `RepoConfig` still carry
  `yaml:` struct tags (`internal/cli/config/config.go:47-65`), as does
  `config.DaemonSettings` (`internal/cli/config/project.go:19-28`).

## Design Checklist

For every new feature, answer:

1. Does this mutate global intent, perform a local effect, or report
   observed state?
2. Which identity is authorized: user, node, worker, or lease holder?
3. If this touches files, what checkout/worktree ID scopes it?
4. If this runs code, what lease token owns it?
5. What happens if the node dies halfway through?
6. What happens if a stale worker reports success?
7. What state expires, and how does the UI show staleness?
8. Is this operation valid in local mode, distributed mode, or both?
9. Is a path or secret being written into global state by mistake?
10. Can two nodes do this concurrently without split-brain?

## Non-Goals

- Fleet-db should not become a live filesystem server.
- Control plane should not clone every repo just to serve diffs.
- Local absolute paths should not be global truth.
- Push notifications should not grant ownership.
- Long-lived agent processes should not be the primary distributed
  ownership primitive.

## Summary

The target architecture is a task-driven distributed control plane:

```text
global intent and leases: fleet-db
local effects: nodes/runtimes
observed state: node-reported, timestamped, expiring
execution ownership: task/run leases with fencing tokens
interactive capabilities: routed to the owning node
```

Local mode and distributed mode should share this model. Local mode
uses one embedded control plane and one node. Distributed mode adds
auth, node placement, leases, and remote runtime providers.

## Related

- `docs/design/2026-07-23-control-plane-as-built.md` — where the shipped
  control plane actually lives: domain types → store contracts →
  `internal/driver` call sites → HTTP surface. Start here if you are
  looking for code.
- `docs/design/distributed-control-plane-data-model.md` — the companion
  record-shape proposal. Superseded; read it for reasoning, not field names.
- `docs/design/fleetdb-agent-platform-v2-proposal.md` and
  `docs/design/fleetdb-agent-platform-v2-phased-delivery.md` — the
  2026-06-03 correction that supersedes this document's concrete plan.
- `docs/design/fleetdb-agent-platform-v2-execution-topology-addendum.md` —
  execution topology addendum to V2.
- `docs/product/local-mode-product-spec.md` — the single-user local-mode
  product surface.
- `docs/product/orchestrator-worker-model.md` — the orchestrator/worker
  split in product terms.
- `docs/loom-glossary.md` — disambiguates "worker", "control plane",
  "lead", "fleet".
