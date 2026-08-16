# Daemon and Agent Runtime Architecture

**Status:** Draft
**Date:** 2026-05-05
**Related:** `docs/product/agent-execution-prd.md`,
`docs/product/local-mode-product-spec.md`,
`docs/product/container-runner-mvp-spec.md`,
`docs/design/distributed-control-plane.md`,
`docs/design/distributed-control-plane-data-model.md`

## Purpose

Define the product architecture for running Loom agents in local mode and
cloud mode without building a fully centralized task assignment service.

This document narrows the distributed-control-plane direction into a simpler
runtime model:

- FleetDB owns shared intent and coordination.
- Runtime providers decide where a logical agent process runs.
- The agent process still selects and claims tasks atomically.
- A workspace can have many runtime nodes, but each logical agent has at most
  one active instance.

## Product Position

Loom should separate two concerns that are currently easy to mix together:

| Concern | Owner | Product rule |
|---|---|---|
| Agent placement | Runtime provider, such as local daemon or Kubernetes | Decide where the agent process runs. |
| Task assignment | Agent runner plus FleetDB atomic claim | Decide which task the agent takes. |

Loom should not start with a central `assign-next` service that chooses the
next task for every agent. Agents should continue to evaluate their local queue
and claim work through FleetDB. FleetDB only needs to make ownership and claims
safe.

## Core Invariant

For each configured logical agent:

```text
(workspace_id, agent_id) -> at most one active owner
```

This invariant must be enforced by a FleetDB-backed agent ownership lease, not
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
share a machine or FleetDB instance.

## Target Architecture

```text
FleetDB / control plane
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

## Product Concepts

| Concept | Meaning |
|---|---|
| Agent definition | Long-lived workspace-scoped intent: name, role, repos, backend, desired state. |
| Logical agent | The named agent described by an agent definition, for example `falcon`. |
| Agent runner | One executable process that tries to run one logical agent. |
| Runtime provider | The system that starts/stops agent runners. |
| Local daemon | Local runtime provider that supervises agent-runner child processes. |
| Cloud controller | Runtime reconciler that creates/deletes cloud runtime objects, such as Kubernetes pods. |
| Agent ownership lease | FleetDB lease keyed by `(workspace_id, agent_id)` that grants the right to run the logical agent. |
| Session lease | Per-run/per-session lease that fences issue mutations during an execution attempt. |
| Task claim | Atomic issue claim performed by the runner after it owns the logical agent. |

## Local Mode

Local mode uses one developer machine and a local or embedded FleetDB, but it
must still use the same ownership and claim model as cloud mode.

```text
loom daemon
  loads FleetDB agent definitions
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
FleetDB AgentDefinition(desired_state=running)
        |
        v
Loom cloud controller
        |
        v
Kubernetes AgentRunner pod
        |
        v
Agent runner acquires FleetDB ownership lease
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
- Enforce one logical owner through FleetDB ownership leases.
- Preserve sessions and artifacts.
- Keep task assignment decentralized through atomic claims.

FleetDB ownership leases remain required even with Kubernetes. Kubernetes can
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

FleetDB must make the claim atomic. It does not need to pick the task.

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

The current codebase already has many pieces needed for this architecture.

| Area | Exists today | Gap |
|---|---|---|
| Agent definitions | FleetDB-backed `Agent` with role, mode, desired state, repos, backend. | No runtime placement or ownership owner field. |
| Local daemon | `loom daemon` loads FleetDB config and supervises local processes. | Starts every runnable agent locally; no global agent ownership lease. |
| Node records | Supervisor registers a FleetDB node with runtime provider and heartbeat. | Node identity is not used for logical agent ownership. |
| Sessions | Daemon creates FleetDB agent sessions before spawning. | Session lifecycle and cloud runner lifecycle are not unified behind `agent-runner`. |
| Session leases | FleetDB `AgentLease` exists and IPC validates lease token. | Lease is session-scoped, not `(workspace, agent)` ownership-scoped. |
| Commands | FleetDB `AgentCommand` exists with optional target node. | Untargeted commands can be picked by any daemon; commands should route to owner. |
| Task claim | Supervisor locally scores ready tasks and uses atomic claim. | This is the right direction; it should move into the shared agent-runner path. |
| Fleet/cloud mode | Fleet mode can suppress local supervision. Runtime provider enum includes Kubernetes. | No cloud controller, no agent-runner pod, no Kubernetes integration. |

## MVP Implementation Scope

The first implementation should be intentionally small.

1. Add `AgentOwnershipLease` to FleetDB and Loom store interfaces.
2. Add atomic acquire, heartbeat, release, list, and get operations.
3. Modify the local supervisor so it acquires ownership before spawning an
   agent process.
4. Heartbeat ownership while the supervised child process is expected to be
   alive.
5. Stop or drain the child process if ownership heartbeat fails.
6. Release ownership on graceful stop, desired state stopped, or daemon drain.
7. Show ownership state in CLI/UI status.

This MVP makes multiple local daemons across different nodes safe for the same
workspace without adding a scheduler.

## Cloud Implementation Scope

After ownership leases exist, cloud mode can be added without changing task
assignment.

1. Add `loom agent-runner --workspace <id> --agent <name>`.
2. Package an agent-runner container image.
3. Add a simple Loom cloud controller.
4. Map each running agent definition to one desired Kubernetes runner object.
5. Let Kubernetes schedule the pod.
6. Make the pod acquire and heartbeat the FleetDB ownership lease.
7. Make duplicate pods exit if ownership is denied.
8. Persist session, logs, transcript, diff, and final status through FleetDB.

The cloud controller should reconcile desired state. It should not choose the
next task for the agent.

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

### Draining An Agent (One-Shot)

`desired_state=draining` is a **one-shot** park, unlike `stopped`, which stays
indefinite and explicit. A drain is honored only while it is addressed to the
currently running supervisor and still inside its TTL:

```text
loom data agent yield <name> [--ttl 5m | --until-restart]
  -> desired_state=draining
  -> drain_node_id=<current supervisor node id>
  -> drain_expires_at=now+ttl        (omitted for --until-restart)
```

At startup the daemon reconciles drains once, before it builds its agent list:

| Drain shape | Startup behavior |
|---|---|
| Addressed to this supervisor, unexpired | Honored — agent stays parked. |
| Addressed to a different supervisor | Released — the supervisor it named is gone. |
| Past its expiry | Released. |
| No `drain_node_id` and no `drain_expires_at` | Released — unattributable. |

Clearing happens **only** at startup, never on the 30s reconcile tick, so a
yield issued seconds before a tick is not undone by that tick. `stopped` is
untouched by all of this.

Parked agents remain visible: they are reported in `loom daemon status` as
`parked`, counted in the Agents line, and warned about at startup and roughly
every five minutes, always carrying `resume="loom data agent start <name>"`.

**Deploy note.** Every agent parked by a pre-one-shot-drain `yield` has the
unattributable shape above, so the first daemon start after this change
releases them all at once, each with a `Warn` naming the untargeted drain. That
is a one-time release and is the intended migration: previously such a park
could only be undone by an explicit `loom data agent start` per agent.

### Completing A Run (on_complete Hooks)

An agent definition may carry an ordered `hooks.on_complete` pipeline. The
supervisor executes it itself after a successful turn, so the state transition
that marks a stage done is owned by the daemon rather than by the agent's
prompt. A run that dies mid-turn then cannot leave the issue in an ambiguous
state with no retry signal.

```text
subprocess exits 0
supervisor classifies the exit
supervisor runs on_complete in stored order   <- before finalize/recovery
  comment(final_reply)  -> post the run's final assistant reply
  add_label(<label>)    -> stamp the task
supervisor finalizes the session, checkpoints, recovers
```

Two actions exist: `comment` with `source: final_reply`, and `add_label` with a
literal `value`. Nothing else — no commands, URLs, or templates. Agent
definitions are remotely writable control-plane data, so an action carrying a
shell string would be daemon-host code execution.

**Write before stamp.** All comment actions must precede every `add_label`.
Fleet-db enforces this on write and the supervisor re-checks it before
executing, refusing an out-of-order stored pipeline rather than reordering it.
Actions run strictly in order and stop at the first error, so a completion label
can never be observed without the artifact it certifies. The converse is
possible: a crash between chunks can duplicate a comment on retry. That is the
accepted trade.

**Eligibility.** Hooks run only for a real claimed-task run whose subprocess
exited 0 and concluded on its own. Skipped for: spawn failure, nonzero exit,
no-work, watchdog/timeout, shutdown/drain, yield, config removal, backend
unavailable, and exit 0 with no task. An agent with no hooks configured is
completely unaffected.

**Failure.** Any hook failure — including an empty or unreadable final reply
when a comment action is configured — converts the run into a synthetic failure
(`LastExitCode = -1`, class `CompletionHookFailure`) *before* session finalize,
checkpoint, and post-mortem recovery. The session records failed, the owned task
reopens, and `agentpolicy` applies bounded counted retry with the default
backoff, blocking after the retry budget is spent. The already-emitted
`AgentStopped` process event keeps its factual exit code 0; the higher-level
hook failure lives in the session state and `last_error_class`.

**Reply extraction** reads the current session's transcript through
`sessions.Store.LoadNativeEvents` and takes the last contiguous run of assistant
text events, stopping at the preceding tool cycle or user turn. The boundary is
structural rather than identity-based because canonical TS-leaf events carry no
message UUID. The read waits a bounded window for the leaf to flush, then fails
closed. When the session has nothing readable yet — the case for a raw backend
such as codex, whose rollout is otherwise only mirrored during finalize, which
runs *after* the hooks — the supervisor mirrors the backend's native transcript
itself before waiting. Sessions that already have a transcript (the TS leaf, and
Claude via live hook dispatch) are read as-is and never re-mirrored. Replies over
fleet-db's 10,000-byte comment cap are split at rune
boundaries with `[final reply - part i/n]` headers, and every chunk must land
before a label action runs. Reply text is never logged.

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
- Replace FleetDB atomic task claims.
- Treat Kubernetes pod count as the correctness boundary.
- Make one local daemon process global across all nodes.

In other words, Loom should use ownership leases and atomic claims for
correctness, while local daemons, Kubernetes, and future runtime providers handle
process placement and restart behavior.

## Open Questions

- Should the ownership lease be a new table or a generalized resource lease
  table shared with future checkout/run locks?
- Should `AgentMode` distinguish `local`, `cloud`, and `auto`, or should that
  be a separate runtime placement policy?
- How long should the ownership TTL be for local mode and cloud mode?
- Should commands target the ownership lease owner automatically?
- Should the first cloud controller live in `loom serve`, a new `loom
  cloud-controller` command, or an external operator?
