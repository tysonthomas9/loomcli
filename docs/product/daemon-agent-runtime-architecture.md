# Daemon and Agent Runtime Architecture

> **Status:** Partially implemented — the agent-ownership-lease MVP
> ([MVP Implementation Scope](#mvp-implementation-scope), items 1-7) shipped
> end to end: `domain.AgentOwnershipLease`
> (`internal/domain/control_plane.go:182-195`) through
> `store.AgentOwnershipLeaseStore` (`internal/store/control_plane_store.go:284-290`)
> to the daemon's spawn gate (`internal/cli/daemon/supervisor/supervisor.go:274-283`).
> The [Cloud Implementation Scope](#cloud-implementation-scope) — `loom
> agent-runner`, a runner image, and a Kubernetes controller — was **never
> built**. *audited 2026-07-24*

**Date:** 2026-05-05
**Related:** see [Related](#related) at the bottom.

## Purpose

Define the product architecture for running Loom agents in local mode and
cloud mode without building a fully centralized task assignment service.

This document narrows the distributed-control-plane direction into a simpler
runtime model:

- fleet-db owns shared intent and coordination.
- Runtime providers decide where a logical agent process runs.
- The agent process still selects and claims tasks atomically.
- A workspace can have many control-plane nodes, but each logical agent has at
  most one active instance.

## What Shipped

Read this section before the design narrative below; the rest of the document
is the 2026-05-05 plan, and only part of it is code.

| Piece | Status | Where |
|---|---|---|
| `AgentOwnershipLease` record | Shipped | `internal/domain/control_plane.go:182-195` |
| Acquire / Get / List / Heartbeat / Release | Shipped | `internal/store/control_plane_store.go:284-290` |
| fleet-db HTTP surface `/api/v1/{ws}/agent-ownership-leases/{agent}/…` | Shipped (loom-side client verified; server impl is in the fleet-db repo, UNVERIFIED here) | `internal/infra/fleetdb/control_plane.go:678-736` is the loom **client** that POSTs these routes; it proves loom calls them, not that fleet-db serves them |
| Daemon acquires ownership before spawning | Shipped | `internal/cli/daemon/supervisor/supervisor.go:274-279` |
| Ownership heartbeat while the child runs | Shipped | `internal/cli/daemon/supervisor/ownership.go:108-145` |
| Release on stop / drain | Shipped | `internal/cli/daemon/supervisor/ownership.go:85-106` |
| Ownership visible in CLI status | Shipped | `internal/cli/daemon/daemon_display.go:34-35`, `internal/cli/daemon/daemon_cmd.go:47-49` |
| Ownership visible in the **web UI** | **Not implemented** — no ownership field is read anywhere under `internal/webui` |
| `loom agent-runner` command | **Never built** — `cmd/` contains only `loom` |
| Agent-runner container image | **Never built** |
| Loom cloud controller / Kubernetes integration | **Never built** — `RuntimeProviderKubernetes` exists as an enum value (`internal/domain/control_plane.go:26`) with no code behind it |

Two implementation details worth knowing before reading the design:

- **Ownership TTL is 30 minutes**, shared with the session lease constant
  `defaultLeaseTTL` (`internal/cli/daemon/supervisor/supervisor.go:222-229`).
  The comment there explains the sizing: it must outlive a typical agent turn,
  because the session lease has no periodic heartbeat loop. The *ownership*
  lease does heartbeat, but **not** at a TTL-proportional rate: the interval is
  `ttl/4` capped at `defaultNodeInterval`, and with a 30-minute TTL the cap
  always wins, so the effective interval is 30 seconds
  (`ownershipHeartbeatBaseInterval`, `internal/cli/daemon/supervisor/ownership.go:160-166`).
- **A failed heartbeat does not immediately surrender ownership.** The
  supervisor retries, then arbitrates by re-acquiring
  (`verifyAgentOwnershipAfterHeartbeatFailure` and
  `arbitrateOwnershipByReacquire`, `internal/cli/daemon/supervisor/ownership.go:212-275`).
  It kills the agent in exactly two cases: the re-acquire proves another owner
  holds the lease (`verifiably_lost`), or the outcome stays inconclusive past
  the validity window — one TTL since the last *confirmed* renewal
  (`ownership_unverifiable`, `continueOwnershipIfWithinValidity`,
  `ownership.go:282-292`). A transient control-plane outage therefore does not
  kill a running agent as long as it clears within that bounded window; this is
  a bounded fail-open, not an unconditional one.

## Product Position

Loom should separate two concerns that are currently easy to mix together:

| Concern | Owner | Product rule |
|---|---|---|
| Agent placement | Runtime provider, such as local daemon or Kubernetes | Decide where the agent process runs. |
| Task assignment | Agent runner plus fleet-db atomic claim | Decide which task the agent takes. |

Loom should not start with a central `assign-next` service that chooses the
next task for every agent. Agents should continue to evaluate their local queue
and claim work through fleet-db. fleet-db only needs to make ownership and claims
safe.

## Core Invariant

For each configured logical agent:

```text
(workspace_id, agent_id) -> at most one active owner
```

This invariant must be enforced by a fleet-db-backed agent ownership lease, not
by local PID files, local daemon locks, Kubernetes pod counts, or best-effort
process checks.

Runtime providers may accidentally create duplicate runners during restarts,
rollouts, retries, or node partitions. That is acceptable only if all
duplicates must acquire the same ownership lease before doing work. The runner
that cannot acquire the lease exits quickly.

## Workspace Selection

Runtime execution must always be scoped to an explicit workspace. A daemon,
agent runner, or remote CLI command must receive the workspace through
`LOOM_WORKSPACE` or `--workspace`; it must not infer runtime scope from a
machine-local "default workspace" preference.

The product keeps browser route state and per-machine checkout paths as
convenience state only. For example, `/ws/TEST/kanban` selects the UI workspace
and `~/.loom/state.json` can remember checkout paths, but neither grants
runtime authority to mutate tasks. This avoids the random cross-workspace
behavior caused by stale defaults when multiple workspaces, daemons, or nodes
share a machine or fleet-db instance.

## Target Architecture

```text
fleet-db / control plane
  - workspace metadata
  - role and agent definitions
  - desired agent state
  - agent ownership leases
  - sessions and session leases
  - node heartbeats
  - agent commands
  - atomic task claims

Runtime providers
  - local daemon
  - Kubernetes
  - future: Podman, Nomad, ECS, E2B, CI, VM pools

Agent runner
  - one logical agent instance
  - acquires ownership lease
  - claims tasks atomically
  - runs planner/coder/custom role
  - heartbeats ownership and session state
  - finalizes session and artifacts
```

The same agent execution logic should run in both local and cloud mode:

```text
local:
  loom daemon -> loom agent-runner process

cloud:
  Kubernetes -> loom agent-runner pod
```

The local daemon and Kubernetes are runtime adapters. They should not own task
assignment policy.

> **As built, this diagram is aspirational.** There is no `loom agent-runner`.
> The local daemon spawns the agent by re-execing the `loom` binary directly
> (`internal/cli/daemon/supervisor/spawn.go`), and the remote shape that exists
> is `loom worker` — a process that registers with a `loom serve` control plane
> over HTTP and runs the same auto-mode loop
> (`internal/cli/serve/worker/worker_cmd.go:40-50`). See
> [Container And Remote Placement](#container-and-remote-placement).

## Product Concepts

| Concept | Meaning |
|---|---|
| Agent definition | Long-lived workspace-scoped intent: name, role, repos, backend, desired state. |
| Logical agent | The named agent described by an agent definition, for example `falcon`. |
| Agent runner | One executable process that tries to run one logical agent. |
| Runtime provider | The system that starts/stops agent runners. |
| Local daemon | Local runtime provider that supervises agent-runner child processes. |
| Cloud controller | Runtime reconciler that creates/deletes cloud runtime objects, such as Kubernetes pods. |
| Agent ownership lease | fleet-db lease keyed by `(workspace_id, agent_id)` that grants the right to run the logical agent. |
| Session lease | Per-run/per-session lease that fences issue mutations during an execution attempt. |
| Task claim | Atomic issue claim performed by the runner after it owns the logical agent. |

## Local Mode

Local mode uses one developer machine and a local or embedded fleet-db, but it
must still use the same ownership and claim model as cloud mode.

```text
loom daemon
  loads fleet-db agent definitions
  registers local node
  reconciles desired_state
  for each runnable agent:
    try acquire AgentOwnershipLease(workspace, agent)
    if acquired:
      start loom agent-runner
      heartbeat ownership
      supervise process
    if lease lost:
      drain or kill process
```

Local mode responsibilities:

- Start and stop local child processes.
- Register and heartbeat a local node.
- Acquire ownership before starting a logical agent.
- Restart owned child processes according to local restart policy.
- Drain on desired state changes.
- Preserve logs, sessions, and artifacts.

Local mode should not:

- Treat `.loom/daemon.lock` as workspace-global correctness.
- Start every runnable agent on every daemon instance.
- Depend on one daemon process being globally unique across all nodes.
- Implement centralized task assignment.

## Cloud Mode

Cloud mode should use the target platform for placement. For Kubernetes, Loom
should let the Kubernetes scheduler choose the node for the agent pod.

```text
fleet-db AgentDefinition(desired_state=running)
        |
        v
Loom cloud controller
        |
        v
Kubernetes AgentRunner pod
        |
        v
Agent runner acquires fleet-db ownership lease
        |
        v
Agent runner selects and claims tasks
```

Kubernetes responsibilities:

- Place pods on nodes.
- Restart failed pods.
- Enforce resource requests and limits.
- Handle node pressure and eviction behavior.
- Provide secrets, service accounts, and pod logs.

Loom responsibilities:

- Store desired agent state.
- Create or delete runtime objects from desired state.
- Enforce one logical owner through fleet-db ownership leases.
- Preserve sessions and artifacts.
- Keep task assignment decentralized through atomic claims.

fleet-db ownership leases remain required even with Kubernetes. Kubernetes can
produce transient duplicate pods during rollout, retries, control-plane lag, or
operator mistakes. The lease is the product correctness guard.

## Assignment Model

Assignment has two layers.

### Placement Assignment

Placement answers:

```text
Where should this logical agent process run?
```

In local mode, the local daemon answers this by starting a child process if it
can acquire the agent ownership lease.

In Kubernetes mode, Kubernetes answers this by scheduling an agent-runner pod.
Loom should not duplicate Kubernetes node scoring in the first cloud
implementation.

### Task Assignment

Task assignment answers:

```text
Which task should this running agent work on next?
```

This remains decentralized:

```text
agent runner:
  list eligible ready tasks
  score tasks using role, repo, parent epic, labels, priority, and constraints
  try atomic claim
  if claim succeeds:
    execute task
  if claim conflicts:
    remove that candidate and retry
  if no work:
    idle/backoff
```

fleet-db must make the claim atomic. It does not need to pick the task.

## Required Data Model

The minimum new concept is an agent ownership lease:

```text
AgentOwnershipLease
  workspace_id
  agent_id
  owner_id
  runtime_provider
  node_id
  lease_id
  token
  fencing_token
  status
  expires_at
  last_heartbeat
  created_at
  updated_at
```

Required operations:

| Operation | Requirement |
|---|---|
| Acquire | Atomic acquire by `(workspace_id, agent_id)` when no active lease exists, the lease is expired, or the same owner is renewing. |
| Heartbeat | Only the current token holder can extend `expires_at`. |
| Release | Only the current token holder can release. |
| Steal expired | A different node can acquire only after expiry. |
| Observe | UI can show desired state, owner, runtime provider, heartbeat age, and stale state. |

The existing per-session `AgentLease` should remain useful for session
mutation fencing, but it should not be treated as logical agent ownership.

## Existing Codebase Fit

*History — this table describes the tree as of 2026-05-05, before the MVP
landed. The "Gap" column for agent definitions, the local daemon, node records
and session leases has since been closed; see [What Shipped](#what-shipped).
Kept because it records what the design was reacting to.*

| Area | Existed 2026-05-05 | Gap then |
|---|---|---|
| Agent definitions | fleet-db-backed `Agent` with role, mode, desired state, repos, backend. | No runtime placement or ownership owner field. |
| Local daemon | `loom daemon` loads fleet-db config and supervises local processes. | Starts every runnable agent locally; no global agent ownership lease. |
| Node records | Supervisor registers a fleet-db node with runtime provider and heartbeat. | Node identity is not used for logical agent ownership. |
| Sessions | Daemon creates fleet-db agent sessions before spawning. | Session lifecycle and cloud runner lifecycle are not unified behind `agent-runner`. |
| Session leases | fleet-db `AgentLease` exists and IPC validates lease token. | Lease is session-scoped, not `(workspace, agent)` ownership-scoped. |
| Commands | fleet-db `AgentCommand` exists with optional target node. | Untargeted commands can be picked by any daemon; commands should route to owner. |
| Task claim | Supervisor locally scores ready tasks and uses atomic claim. | This is the right direction; it should move into the shared agent-runner path. |
| Fleet/cloud mode | Fleet mode can suppress local supervision. Runtime provider enum includes Kubernetes. | No cloud controller, no agent-runner pod, no Kubernetes integration. |

## MVP Implementation Scope

**Shipped.** The first implementation was intentionally small, and all seven
items landed (item 7 for the CLI only — the web UI does not surface ownership).

1. Add `AgentOwnershipLease` to fleet-db and Loom store interfaces. — shipped
2. Add atomic acquire, heartbeat, release, list, and get operations. — shipped
3. Modify the local supervisor so it acquires ownership before spawning an
   agent process. — shipped
4. Heartbeat ownership while the supervised child process is expected to be
   alive. — shipped
5. Stop or drain the child process if ownership heartbeat fails. — shipped,
   but only after the arbitration described in [What Shipped](#what-shipped);
   a single failed heartbeat is not enough.
6. Release ownership on graceful stop, desired state stopped, or daemon drain.
   — shipped
7. Show ownership state in CLI/UI status. — **CLI only**

This MVP makes multiple local daemons across different control-plane nodes safe
for the same workspace without adding a scheduler.

## Cloud Implementation Scope

> **Never built.** None of the eight items below exist in this tree. The
> Kubernetes path in particular has no code: `RuntimeProviderKubernetes` is an
> enum value (`internal/domain/control_plane.go:26`) that nothing consumes.
> Kept as the recorded plan, not a description.

After ownership leases exist, cloud mode can be added without changing task
assignment.

1. Add `loom agent-runner --workspace <id> --agent <name>`.
2. Package an agent-runner container image.
3. Add a simple Loom cloud controller.
4. Map each running agent definition to one desired Kubernetes runner object.
5. Let Kubernetes schedule the pod.
6. Make the pod acquire and heartbeat the fleet-db ownership lease.
7. Make duplicate pods exit if ownership is denied.
8. Persist session, logs, transcript, diff, and final status through fleet-db.

The cloud controller should reconcile desired state. It should not choose the
next task for the agent.

## Container And Remote Placement

Loom never grew the generic "container agent runner" this document's cloud
scope assumed. Three concrete placement mechanisms exist instead. They are not
interchangeable — check which layer you mean.

| Mechanism | What it places | Where |
|---|---|---|
| `loom worker` | An **agent** process on a remote machine or in a container. It registers with a `loom serve` control plane over HTTP and runs the auto-mode loop with HTTP-backed lock/event/log bridges. There is no separate worker binary — same `loom` binary, different subcommand. | `internal/cli/serve/worker/worker_cmd.go:40-50`; containerized at `deploy/podman-stack/Containerfile.worker` |
| Driver sandbox | A **workflow driver / task run**, not an agent, inside a rootless container. `LOOM_DRIVER_SANDBOX=container`, podman-first with docker fallback, read-only rootfs, `no-new-privileges`, mandatory memory/cpu/pids caps. | `internal/driver/sandbox/container.go:145-170` |
| Daytona | A **task run** in a remote sandbox. Selected by a single machine-local default (`AgentRuntimeConfig.Default`, one value per desktop data dir — *not* per workspace, `internal/localsettings/settings.go:57-60,121`) set to `localsettings.AgentRuntimeDaytona`; entrypoint `daytona-task-runner`. | `internal/localsettings/settings.go:27-28`, `internal/driver/bundled_runner.go:20` |

The codified multi-process reference deployment is `deploy/podman-stack/`
(serve + fleet-db + redis + workers + stub upstream). Its README is the
authoritative description.

## Product Behavior

### Starting An Agent

```text
user sets desired_state=running
runtime provider observes desired state
runtime starts candidate runner
runner acquires ownership lease
runner enters idle/running state
runner claims tasks when eligible work exists
```

If a runner cannot acquire ownership:

```text
agent already running on node <node_id>
candidate runner exits
UI continues to show current owner
```

### Stopping An Agent

```text
user sets desired_state=stopped
owner observes command or desired state
owner writes yield/drain signal
runner stops after safe point or timeout
owner releases ownership lease
UI shows stopped
```

If the owner disappears:

```text
heartbeat expires
UI shows stale
another runtime may acquire ownership
```

### Multiple Nodes

Multiple daemons or cloud runtimes may watch the same workspace. They may all
attempt to run the same logical agent, but only one may acquire ownership.

This supports:

- local laptop plus CI node
- multiple cloud nodes
- Kubernetes rollout duplicate pods
- failover after node loss

## Acceptance Criteria

- Starting two daemons for the same workspace cannot produce two active
  instances of the same logical agent.
- A duplicate candidate runner exits without claiming a task.
- If the owner crashes, a different node can run the agent only after lease
  expiry.
- Task selection remains decentralized and conflict-safe.
- Kubernetes cloud mode does not require Loom to implement node scoring.
- UI and CLI can show desired state, owner node, runtime provider, heartbeat
  age, current session, and stale state.

## Out Of Scope

The following items are intentionally not part of this design. They should not
be built for the MVP or first cloud implementation unless this product direction
changes.

- Build a fully centralized `assign-next` service.
- Build a Loom-native Kubernetes scheduler.
- Replace fleet-db atomic task claims.
- Treat Kubernetes pod count as the correctness boundary.
- Make one local daemon process global across all nodes.

In other words, Loom should use ownership leases and atomic claims for
correctness, while local daemons, Kubernetes, and future runtime providers handle
process placement and restart behavior.

## Open Questions

Three of the original five were settled by the implementation; the answers are
recorded here so a reader does not re-litigate them.

- ~~Should the ownership lease be a new table or a generalized resource lease
  table shared with future checkout/run locks?~~ **Settled: a dedicated
  record.** `AgentOwnershipLease` is its own type and its own store interface,
  separate from `AgentLease` (`internal/domain/control_plane.go:182`,
  `internal/store/control_plane_store.go:284`).
- ~~Should `AgentMode` distinguish `local`, `cloud`, and `auto`?~~ **Settled:
  no.** `AgentMode` remained `ephemeral` | `service`
  (`internal/domain/control_plane.go:8-9`); placement is carried by
  `RuntimeProvider` instead. (Note `internal/domain/control_plane.go:8-9` shows
  the two constants; the type is declared just above them.)
- ~~How long should the ownership TTL be for local mode and cloud mode?~~
  **Settled for local: 30 minutes**, reusing `defaultLeaseTTL`
  (`internal/cli/daemon/supervisor/supervisor.go:222-229`). No cloud value was
  ever chosen, because no cloud runner exists.
- Still open: should commands target the ownership lease owner automatically?
  `AgentCommand` still carries an optional `TargetNodeID`
  (`internal/domain/control_plane.go:214`) with no owner-routing rule.
- Still open (and moot until a cloud runner exists): should the first cloud
  controller live in `loom serve`, a new `loom cloud-controller` command, or an
  external operator?

## Related

- [`local-mode-product-spec.md`](local-mode-product-spec.md) — the
  single-machine deployment of this model, and the CI-enforced control-plane
  topology invariant.
- [`container-runner-mvp-spec.md`](container-runner-mvp-spec.md) — the
  container *agent* runner proposal referenced by this document's cloud scope.
  Never built; see [Container And Remote Placement](#container-and-remote-placement).
- [`desktop-app-runtime-spec.md`](desktop-app-runtime-spec.md) — how the macOS
  app supervises the same daemon.
- [`orchestrator-worker-model.md`](orchestrator-worker-model.md) — the
  role-level view of who spawns whom.
- [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) — the
  per-agent state transitions the supervisor drives.
- `docs/design/distributed-control-plane.md` — the broader architecture this
  narrows.
- `docs/design/2026-07-23-control-plane-as-built.md` — where the shipped
  control plane actually lives; start there when looking for code.
- `docs/loom-glossary.md` — the four senses of "worker", the three senses of
  "backend", and the three senses of "node".
