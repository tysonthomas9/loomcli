# Agent Run Visibility Plan

**Status:** Future plan
**Date:** 2026-05-04
**Related:** `docs/product/agent-execution-prd.md`,
`docs/design/distributed-control-plane.md`,
`docs/design/distributed-control-plane-data-model.md`,
`docs/arch/terminal-system.md`

## Purpose

Agent execution should be visible and debuggable from the UI whether it
runs as a local process, a local daemon-managed agent, or an ephemeral
container/remote worker.

Today those paths do not all publish the same data. A direct `loom plan`
or `loom task` run can claim and update FleetDB tasks, but the UI may not
show an agent card or task session because those records are stored in a
different runtime path or are not registered with the monitor API.

The product goal is:

> Every agent run has one server-visible run record, one stable agent
> identity, one task association, a session/transcript, logs, artifacts,
> and a clear lifecycle state.

## Current Findings

The following gaps were observed while testing Codex planner/coder runs
against the FleetDB regression stack:

- Direct runner containers could claim tasks and update design/close state.
- The UI agent list did not show the runner containers because they were
  not daemon-managed agents and were not registered as monitor-visible
  runtime agents.
- Creating FleetDB agent definitions with `loom agentdef add` made them
  appear in `/api/workspaces/{ws}/agents`, but `/api/monitor/agents` still
  returned only the built-in `workspace` agent in the running stack.
- The task Sessions tab stayed empty because session records are currently
  file-based and the UI only scans the workspace root and repo session
  stores, not every agent worktree or container-local runtime.
- Ephemeral `podman run --rm` containers discard any local session files at
  exit unless those files are written to a shared server-visible runtime.
- `loom data update --status review` and `loom data update --status blocked`
  failed in the tested FleetDB path with `validation_error: at least one
  field must be changed`.
- The runner image lacked expected agent tools such as `jq` and `make`.
- The fixture repo had no `make gate` target and no `origin` remote, which
  makes the default implementation-agent workflow noisy.

## Target UX

The UI should make agent execution inspectable without requiring the user
to know how the agent was launched.

For every run, the UI should show:

- agent name and role
- backend and model
- runtime provider: local process, daemon, podman, remote worker, or other
- current lifecycle state
- claimed task
- session transcript/logs
- changed files and diff stats
- test/gate results
- commit and push result
- exit code and error class

Task pages should show an agent run timeline:

```text
created -> queued -> started -> claimed task -> wrote design
        -> changed files -> ran tests -> committed -> pushed
        -> closed/blocked/failed
```

## Unified Concepts

The product should separate these concepts while presenting them as one
coherent UI model:

| Concept | Meaning |
|---|---|
| Agent definition | Long-lived intent: name, role, backend, repo scope, filters, desired state. |
| Agent run | One execution attempt by an agent definition or ad hoc agent. |
| Runtime provider | The place/mechanism executing the run: local process, daemon, podman container, remote node. |
| Session | Transcript/log/usage/diff telemetry for one run. |
| Task claim | Lease or ownership of one task during a run. |
| Artifact | Diff, patch, commit, test output, scrollback, transcript, or generated design. |

The UI can still render these as a single "agent" card, but the backend
should not overload one object with all responsibilities.

## Local Mode Plan

Local mode should be the simplest version of the same model: one machine,
one shared filesystem, one server, local agent processes.

### Desired Behavior

- Local agents are launched by the daemon/supervisor when the UI is
  running.
- Direct `loom plan` and `loom task` runs still register a server-visible
  run record.
- All local runs write session records to the workspace runtime directory
  that the web server reads.
- Claims use the actual agent identity, such as `codex-designer`, not a
  generic fixture actor.
- The UI shows local PID, command, worktree, backend, role, current task,
  and log tail.

### Required Product Work

1. Add a run-registration API used by both daemon-launched and direct CLI
   runs.
2. Make direct CLI runs adopt the server-visible workspace runtime when a
   server is available.
3. Ensure the session service can find sessions for direct and
   daemon-managed runs.
4. Make `loom plan` and `loom task` warn when they cannot publish session
   data to the UI.
5. Add local preflight checks before launch:
   - backend CLI installed
   - credentials present
   - `git`, `jq`, `make`, and configured gate command available
   - target worktree exists
   - repo remote exists if push is required

### Local Mode Acceptance Criteria

- Starting a local planner from CLI or UI creates an agent card.
- The task card shows the claimed agent within one poll interval.
- The task Sessions tab shows a running session while the agent is active.
- The session becomes completed/failed when the process exits.
- Transcript/log output is readable from the UI.
- A direct CLI run and a daemon-launched run produce the same visible data.

## Distributed and Container Mode Plan

Distributed mode should use the same UI model, but the runtime provider is
explicit and may be outside the server container.

### Desired Behavior

- A runner container registers before it starts work.
- The server creates or receives a run/session record before task claim.
- The runner streams heartbeat, logs, transcript events, tool inventory,
  and artifacts to the control plane.
- Container exit updates the run with exit code and error class.
- Ephemeral containers do not own durable session storage.

### Required Product Work

1. Add a first-class runner protocol:
   - register node/worker
   - create run
   - claim task with lease/fencing token
   - heartbeat
   - append logs/transcript
   - attach artifacts
   - complete/fail run
2. Store session history and run artifacts in FleetDB, Redis, or another
   server-visible durable store, not only container-local files.
3. Add container metadata to the run:
   - image
   - container ID/name
   - node ID
   - runtime provider
   - command
   - start/end timestamps
4. Bake a golden runner image for tests and dogfood:
   - Codex CLI
   - `jq`
   - `make`
   - Git config
   - known workspace mount layout
   - health/preflight script
5. Add a UI action to start a containerized planner/coder and show live
   progress immediately.

### Distributed Acceptance Criteria

- Starting a planner container creates an agent/run card before the model
  begins work.
- The task Sessions tab shows the session even if the container exits.
- Logs and transcript survive `podman run --rm`.
- The UI shows stale/offline state if the runner stops heartbeating.
- The same task timeline works for local and container runs.

## Monitor and Sessions API Requirements

### Agent List

`/api/monitor/agents` should return a merged view of:

- registered FleetDB agent definitions
- currently running local daemon processes
- active remote/container workers
- recently completed or failed runs when useful for debugging

Stored agent definitions should not disappear just because they are idle.

### Task Sessions

`/api/workspaces/{ws}/issues/{issue}/sessions` should return sessions
created by:

- daemon-supervised agents
- direct CLI runs
- local UI-started terminals
- container/remote workers

The endpoint should not depend on scanning only the workspace root and repo
root filesystem paths.

## Preflight and Workflow Improvements

Agent launch should run a preflight and report failures before invoking
the AI backend.

Recommended checks:

- backend CLI and version
- auth readiness
- workspace and repo path binding
- worktree existence
- expected command tools
- configured test/gate command
- remote/push readiness
- server session publication readiness
- UI notification token availability

If a required workflow step is impossible, the agent should write a
structured run failure instead of leaving the task in a confusing state.

## Implementation Phases

### Phase 1: Make Local Runs Visible

- Direct `loom plan` and `loom task` create server-visible run/session
  records when `LOOM_SERVER_URL` or an active local server is available.
- Fix FleetDB status transitions for `review` and `blocked`.
- Ensure `/api/monitor/agents` includes stored FleetDB agents.
- Add a regression test for local planner/coder visibility in the UI.

### Phase 2: Shared Session Store

- Move task session history to a server-visible store.
- Keep filesystem session files as local artifacts or cache, not the only
  source of truth.
- Add transcript/log artifact APIs that work for local and remote runs.

### Phase 3: First-Class Container Runner

- Add a supported Podman runner command or service.
- Register container runs with the control plane.
- Stream logs, heartbeat, and artifacts.
- Add an end-to-end test that starts planner and coder containers and
  verifies UI agent cards and task sessions.

### Phase 4: Full Distributed Control Plane

- Introduce node identity, worker leases, fencing tokens, runtime provider
  metadata, and durable run artifacts.
- Unify local daemon, direct CLI, and container workers behind the same run
  lifecycle.

## Open Questions

- Should "agent definition" and "worker profile" be separate product terms,
  or should the UI hide that distinction?
- Should session transcripts live in FleetDB, Redis, object storage, or a
  hybrid artifact store?
- How long should completed run records remain visible in the agent sidebar?
- Should direct CLI runs auto-register an ad hoc agent definition, or only a
  run record?
- What is the canonical gate command when a repo has no `make gate` target?
