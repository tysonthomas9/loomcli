# Distributed Control Plane Architecture

**Status:** Draft
**Date:** 2026-04-30
**Related:** `loomcli-26v50`, `loomcli-37h1h`,
`docs/design/distributed-control-plane-data-model.md`

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
| Control plane | The shared API and database that stores intent, metadata, leases, runs, audit, and observed status. |
| Node | A machine or sandbox capable of running work. Examples: a laptop, CI runner, E2B sandbox, Kubernetes pod. |
| Worker | A process on a node that asks for work and executes task runs. |
| Runtime provider | The mechanism used to run work: local loomd, E2B, Kubernetes, bare metal, etc. |
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
2. `loom checkout create ACME` creates or binds local repo paths.
3. `loom worker profile add falcon --role task --repo backend` creates
   a global profile.
4. `loom task run ACME-123` creates a task run and lease.
5. local `loomd` claims the run, performs local effects, and reports
   observed state.

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
  E2B sandbox: ephemeral node per run
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

These mutate global state in fleet-db:

```bash
loom workspace add ACME
loom workspace list
loom workspace show ACME
loom workspace delete ACME

loom repo add ACME backend git@github.com:org/backend.git
loom repo list ACME
loom repo remove ACME backend

loom role add ACME task
loom worker profile add ACME/falcon --role task --repo backend
loom worker profile list ACME
```

### Local Checkout Commands

These mutate local state and local files:

```bash
loom checkout create ACME
loom checkout bind ACME backend /path/to/backend
loom checkout list
loom checkout doctor ACME
loom checkout delete ACME
```

Deleting global metadata and deleting local files must be separate
operations.

Safe default:

```bash
loom workspace delete ACME
```

Deletes fleet-db metadata only.

Local cleanup:

```bash
loom checkout delete ACME
```

Deletes local checkout/worktree state.

Convenience can exist, but must be explicit:

```bash
loom workspace delete ACME --delete-local --force
```

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
- fits E2B and local nodes

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

Suggested interface:

```go
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

| Provider | Purpose |
|---|---|
| local | Interactive local development and local daemon execution. |
| e2b | Ephemeral isolated cloud task runs. |
| future:kubernetes | Team/cloud worker pools. |

## E2B Sandbox Provider

E2B is a good fit as an ephemeral remote runtime provider, especially
for task-driven execution.

Use E2B for:

- isolated task execution
- CI-like validation
- code review agents
- risky/untrusted code
- browser/computer-use tasks
- clean environment runs

Do not make E2B the control plane. Fleet-db remains the control plane.
E2B sandboxes perform local effects for a specific run.

### E2B Run Flow

```text
1. Scheduler creates TaskRun.
2. Scheduler chooses runtime_provider=e2b.
3. Control plane creates or requests an E2B sandbox.
4. Sandbox worker starts with task_run_id and bootstrap token.
5. Worker pulls/accepts assigned run lease.
6. Worker clones repo or restores cached template.
7. Worker runs the agent/test/review command.
8. Worker streams logs and status.
9. Worker uploads patch, transcript, test results, and metadata.
10. Worker completes with lease token.
11. Sandbox is killed or returned to a warm pool.
```

### E2B Global vs Local

Global in fleet-db:

```text
runtime_provider=e2b
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

### E2B Gotchas

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
capabilities: git,codex,claude,browser,e2b
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

E2B, local loomd, and future Kubernetes providers should sit behind the
same runtime abstraction. Do not let E2B-specific concepts become core
task/run semantics. `sandbox_id` is runtime metadata, not the identity
of the work.

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

| Problem | Impact |
|---|---|
| `loom workspace` mixes YAML and fleet-db subcommands. | Users can list/delete a different world than they created. |
| `workspace create/list/remove` still mutates `~/.loom/config.yaml`. | Control-plane metadata and local checkout effects are coupled. |
| `agentdef` writes fleet-db, but `agent/task/plan` resolve YAML worktrees. | Agent definitions do not drive execution. |
| Daemon boots from `loom.yaml` and watches YAML files. | No distributed desired-state reconciler exists. |
| Web UI falls back from store errors to legacy config. | Fleet-db outages can look like stale data or 404. |
| Workspace middleware validates but does not canonicalize `{ws}`. | Name/key ambiguity leaks into backend routing. |
| Fleet worker claim path still uses local daemon RPC pool. | Redis claim primitives are not the authoritative execution lease. |
| Worker JWT claims are not consistently enforced in handlers. | Valid workers may impersonate other worker IDs. |
| `DaemonProfile` mixes global policy with local PID/log paths. | Per-node runtime settings are not cleanly separated. |

## Migration Plan

### Phase 1: Make Fleet-db Mode Honest

- In fleet-db mode, do not silently fall back to YAML.
- Fix `/api/workspaces/active`, repo listing, delete, and create.
- Canonicalize workspace keys in middleware.
- Make `loom workspace list/delete` fleet-db-backed.
- File or fix UI regression gaps for workspace creation and switching.

### Phase 2: Split Checkout From Workspace Metadata

- Add checkout commands and local path binding model.
- Move repo and agent worktree paths into `state.json` or node-local
  storage.
- Keep `workspace delete` metadata-only by default.
- Quarantine or rename legacy worktree commands.

### Phase 3: Unify Agent/Worker Surface

- Retire public `agentdef`.
- Introduce worker profiles or make `loom agent` a parent command.
- Make `loom task/plan/agent` resolve fleet-db profiles first.
- Bind profiles to local checkout state.

### Phase 4: Add Distributed Runtime Primitives

- Add node registration and heartbeat.
- Add task run records.
- Add lease acquire/renew/complete/release with fencing tokens.
- Add runtime provider abstraction.
- Implement local provider first.

### Phase 5: Rewrite Daemon as a Reconciler

- Stop booting from `loom.yaml`.
- Daemon registers as a node.
- Daemon pulls eligible task runs.
- Daemon acquires leases.
- Daemon starts local processes.
- Daemon reports observed state and artifacts.

### Phase 6: Add E2B Provider

- Create E2B runtime provider.
- Add sandbox templates and bootstrap.
- Upload patches, logs, transcripts, and test artifacts.
- Enforce cost, timeout, and concurrency limits.

### Phase 7: Remove YAML Runtime Config

- Complete `loomcli-37h1h`.
- Move daemon config to fleet-db or node-local config as appropriate.
- Delete or quarantine `internal/cli/config` runtime paths.
- Drop `gopkg.in/yaml.v3` once remaining YAML readers are gone.

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
auth, node placement, leases, and runtime providers such as E2B.
