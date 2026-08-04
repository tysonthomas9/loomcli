# Agent Execution PRD

> **Status:** Partially implemented · *audited 2026-07-23*. Phases 1–2 are
> partially delivered. The run-record model this PRD proposed was superseded
> by `domain.AgentSession` — there is no `run` object. Sections corrected
> against code are marked inline.

**Last updated:** 2026-07-23
**Related:** [`README.md`](README.md),
[`session-stores.md`](session-stores.md),
[`session-artifact-contract.md`](session-artifact-contract.md),
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md),
[`local-mode-product-spec.md`](local-mode-product-spec.md),
[`daemon-agent-runtime-architecture.md`](daemon-agent-runtime-architecture.md),
[`agent-run-ux-spec.md`](agent-run-ux-spec.md),
[`../design/agent-run-visibility-plan.md`](../design/agent-run-visibility-plan.md),
[`../design/distributed-control-plane.md`](../design/distributed-control-plane.md),
[`../design/distributed-control-plane-data-model.md`](../design/distributed-control-plane-data-model.md)

## Summary

Loom should make AI agent execution visible, controllable, and debuggable
from the UI and CLI across local and distributed environments.

Users should be able to start or observe a planner, coder, reviewer, or
custom role and immediately answer:

- Which agent is running?
- What task did it pick?
- What is it doing now?
- Where are the logs and transcript?
- What changed?
- Did tests pass?
- Did it commit, push, close, block, or fail?

The first product slice should prioritize local mode because it has the
fewest infrastructure dependencies and gives a clear path to a reliable
dogfood loop.

## Problem

Agent execution currently has multiple partially connected surfaces:

- FleetDB stores task state and agent definitions.
- The monitor API renders daemon/runtime agent state.
- Sessions are stored in filesystem runtime directories.
- Direct `loom plan` and `loom task` commands can claim tasks without
  publishing all UI-visible agent/session metadata.
- Container runners can execute real work but may disappear with their
  local logs and session files.

This creates confusing product behavior:

- A task can be claimed or closed, but no agent appears in the UI.
- An agent can run and complete, but the task Sessions tab is empty.
- Failures such as missing tools, missing gate commands, or missing git
  remotes appear as agent confusion rather than clear product states.
- Local and distributed modes behave differently enough that users cannot
  build a reliable mental model.

## Goals

- Provide one coherent execution model for local, daemon-managed, direct
  CLI, and distributed/container agent runs.
- Make every run visible in the UI before the backend model starts work.
- Preserve session history, logs, transcript, and artifacts after the run
  exits.
- Show task claim, status, and run lifecycle in one task timeline.
- Give clear preflight errors before launching an agent.
- Make local mode a polished, shippable first slice.
- Keep the model compatible with a future distributed control plane.

## Non-Goals

- Build a full remote scheduler in the first slice.
- Require containers for local mode.
- Replace FleetDB's existing issue/task model.
- Solve all artifact storage decisions immediately.
- Build a complete CI/push workflow for repos that do not have remotes or
  gate commands.

## Users

| User | Need |
|---|---|
| Solo developer | Run local planner/coder agents and inspect progress without leaving the UI. |
| Dogfood operator | Validate that agents pick tasks, write designs, implement changes, and leave usable evidence. |
| Team lead | See which agents are active, what they own, and why work is blocked or failed. |
| Platform engineer | Debug local and distributed runner failures from logs, sessions, and artifacts. |

## Core User Stories

### Local Agent Execution

As a developer, I can start a local planning agent from the UI or CLI and
see an agent card immediately, so I know the process started.

As a developer, I can open a task and see the running session, transcript,
and current lifecycle state, so I can debug the agent while it works.

As a developer, I can run `loom task` directly and still see a task session
in the UI, so CLI workflows do not become invisible.

### Task Ownership

As a team lead, I can see which named agent claimed a task, so task
ownership is clear.

As a team lead, I can distinguish `needs_plan`, `has_design`, and custom
role filters, so I understand why an agent picked or skipped a task.

### Failure Recovery

As a developer, I can see a preflight failure before an agent starts, so I
can fix missing Codex auth, missing tools, or missing worktrees quickly.

As a developer, I can see that `make gate` is missing or `git push` failed,
so I know the task result is limited by repo setup rather than model
behavior.

### Distributed/Container Execution

As an operator, I can start a containerized agent and see container
identity, logs, heartbeat, and task session in the UI.

As an operator, I can inspect a completed container run after the container
has exited, so ephemeral execution does not lose evidence.

## MVP Scope

The MVP should focus on local mode and establish the shared run/session
contract used later by containers.

### In Scope

- Agent run record for every local `loom plan` and `loom task` execution.
- Server-visible session record created before task claim.
- UI-visible agent identity and current task.
- Task Sessions tab populated for local direct and daemon-launched runs.
- Basic transcript/log artifact attached to the run.
- Preflight results for backend auth, required tools, worktree, gate
  command, and git remote. *(Only backend binary + auth shipped —
  `internal/runtimepreflight/preflight.go:77`.)*
- Clear session final states. The shipped terminal statuses are in
  [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md);
  `preflight_failed` was never a state and is not an error class either.
- Fix status transitions needed by the agent workflow: review and blocked.
- Regression test for planner/coder local run visibility.

### Out of Scope for MVP

- Remote lease scheduler.
- Multi-node capacity planning.
- Artifact object storage.
- Kubernetes/E2B support.
- Full UI for configuring every role field.

## Functional Requirements

### Agent Run Creation

> **Superseded.** There is no separate run record. The execution attempt *is*
> the session; `session_id` is its identity. The field set is
> `domain.AgentSession` / `sessions.SessionRecord`, tabulated in
> [`session-artifact-contract.md`](session-artifact-contract.md). Notably the
> create payload has no `role`, no `runtime_provider`, and no `command`
> (`internal/infra/fleetdb/control_plane.go:93-124`), and `agent_id` is the
> worktree name (`internal/cli/daemon/supervisor/supervisor.go:527`).

- The system must create a session record before invoking the AI backend.
- The session must be associated with a `session_id`.
- The session may start without a task ID, but task ID must be filled after
  claim.

### Agent Identity

- Claims and run records must use the actual agent identity, not a generic
  server actor.
- Direct CLI runs may use an explicit `--agent` value, `LOOM_AGENT_NAME`,
  or a generated ad hoc name.
- The UI must show whether the agent is registered or ad hoc.

### Session Visibility

- Task Sessions must include sessions from:
  - local daemon agents
  - direct CLI agents
  - UI-started local agents
  - future container/remote workers
- Session records must survive process exit.
- The session list must not rely only on scanning a single worktree-local
  filesystem path.

### Task Timeline

- The task view should show lifecycle events:
  - run created
  - preflight passed or failed
  - backend started
  - task claimed
  - design updated
  - files changed
  - tests/gate run
  - commit created
  - push attempted
  - task closed/blocked/failed
- Timeline events should be queryable through an API, not only rendered
  from logs.

### Preflight

**Shipped.** `PreflightLocalTaskRunner`
(`internal/runtimepreflight/preflight.go:77`) checks exactly two things via
`backends.CheckBackendHealth`: backend binary on PATH, and backend auth. It is
fail-closed — the run is never queued, so there is no failed record to render
(`preflight.go:69-77`). Failure identifiers are `local_backend_unavailable`
and `local_backend_auth_missing` (`preflight.go:94-101`); see
[`error-class-reference.md`](error-class-reference.md).

> **Unbuilt.** The remaining six checks below, and "failed preflight creates a
> failed run record plus a visible task note".

- workspace/repo path binding exists
- agent worktree exists or can be created
- required tools exist
- configured gate command exists
- git remote exists when push is required
- session publication is available

### Artifacts

The recorded artifact fields are defined once, in
[`session-artifact-contract.md`](session-artifact-contract.md). A session
should be able to attach: transcript, log tail, diff patch, changed files,
test/gate output, commit ID, push result, and error class with message.

### Status Transitions

- Agent workflow transitions must work in FleetDB:
  - open -> in_progress
  - in_progress -> review
  - in_progress -> blocked
  - in_progress -> closed
  - blocked -> open
  - review -> open
- Validation errors must explain the rejected field and allowed values.

## Non-Functional Requirements

- UI updates should appear within one polling interval or one realtime
  event.
- Run/session APIs should be idempotent for retries.
- Completed run records should be durable across server restarts.
- Stale observed runtime state must be marked stale instead of shown as
  active.
- Logs and transcripts must avoid leaking credentials.
- Local mode must work without a remote control plane.
- Distributed mode must not depend on global filesystem paths.

## Product Surfaces

### UI

- Agent sidebar/card list
- Task card assignee/status indicators
- Task detail Sessions tab
- Task timeline
- Run detail panel
- Logs/transcript viewer
- Preflight failure panel

### CLI

The registered entry points from `internal/cli/agent` (each via
`cli.RegisterCommand`):

| Command | Declared at |
|---|---|
| `loom agent <worktree> --prompt <path> [flags]` | `internal/cli/agent/agent_cmd.go:35` |
| `loom plan [worktree\|workspace]` | `internal/cli/agent/plan.go:36` |
| `loom task [worktree\|workspace]` | `internal/cli/agent/task.go:28` |
| `loom claim <task-id>` | `internal/cli/agent/claim.go:17` |
| `loom complete` | `internal/cli/agent/complete.go:14` |
| `loom recover <worktree>` | `internal/cli/agent/recover.go:26` |
| `loom list` | `internal/cli/agent/list.go:16` |

There is no `loom run` and no `loom agent run` subcommand. `loom agentdef`
(`internal/cli/agentdef/agentdef_cmd.go:50`) remains the assignment/config
surface. `loom doctor` already exists (`internal/cli/doctor/doctor.go:71`,
package present since 2026-04-05) and is the agent-readiness command this PRD
listed as future work.

`loom plan` and `loom task` publish **only** the filesystem session record
(`internal/cli/agent/plan.go:245,263-274`), not the control-plane one. See
[`session-stores.md`](session-stores.md).

### APIs

The control-plane surface for agent execution
(`internal/infra/fleetdb/control_plane.go`):

| Operation | Route |
|---|---|
| Create session | `POST /api/v1/{ws}/agent-sessions` (`:93`) |
| Get session | `GET /api/v1/{ws}/agent-sessions/{id}` (`:126`) |
| List sessions | `GET /api/v1/{ws}/agent-sessions` (`:134`) |
| Heartbeat | `POST /api/v1/{ws}/agent-sessions/{id}/heartbeat` (`:198`) |
| Update session | `PATCH /api/v1/{ws}/agent-sessions/{id}` (`:206`) |

There is no run CRUD, no artifact-attach endpoint, and no preflight-result
endpoint. Agent monitoring is `GET /api/monitor/agents`
(`internal/webui/app/routes.go:99`).

## Success Metrics

- 100% of local agent runs show a UI-visible session.
- 100% of task claims show the named agent that claimed the task.
- Time from launch to visible agent card is under 2 seconds locally.
- A completed run has transcript/log and final status available after
  server restart.
- Dogfood planner/coder E2E passes without manual database or filesystem
  repair.
- Preflight failures are reported before model invocation.

## Rollout Plan

### Phase 1: Local Visibility

- Add run/session registration to direct local `loom plan` and `loom task`.
  **Partially delivered:** filesystem session only; neither command writes a
  control-plane `AgentSession` (`internal/cli/agent/plan.go:245,263-274`;
  the only `AgentSessions().Create` callers are
  `internal/cli/daemon/supervisor/supervisor.go:524`,
  `internal/cli/agent/lead/lead.go:337`,
  `internal/driver/task_bridge_session.go:164`,
  `internal/webui/handlers/terminal/agent_session.go:447`, and
  `internal/cli/daemon/seed_transcript_cmd.go:74`).
- Populate task Sessions for direct and daemon-launched local agents.
- Fix FleetDB status transitions for review and blocked.
- Make `/api/monitor/agents` include stored idle agents and active runs.
  **Delivered:** route registered at `internal/webui/app/routes.go:99`.

### Phase 2: Local UX Polish

- Add preflight UI and CLI output.
- Add task timeline.
- Add run detail panel with logs/transcript/artifacts.
- Add UI actions to start local planner/coder agents.

### Phase 3: Container Runner MVP

- Add supported Podman runner image and command.
- Register container runs before model invocation.
- Stream logs and heartbeat to the server.
- Persist sessions and artifacts outside the container.

### Phase 4: Distributed Control Plane

- Add node identity, leases, fencing tokens, and runtime-provider metadata.
- Support multi-node runners with stale-state semantics.
- Unify local, daemon, direct CLI, and container workers behind one run
  lifecycle.

## Risks

- The existing terms "agent", "worker", "session", and "daemon" are
  overloaded and may confuse users unless the UI simplifies them.
- A filesystem-only session store will continue to fail for remote and
  ephemeral execution.
- Preflight can become noisy if every repo has different test conventions.
- Local mode and distributed mode can diverge again unless they share the
  same run contract.

## Open Questions

Still open:

- Should direct CLI runs auto-create an ad hoc agent definition or only a
  session record?
- Where should durable transcripts and logs live long term?
- How should users configure the canonical gate command per repo?
- Should a push failure close the task, block it, or complete the session
  with a warning?
- How long should completed runs remain visible **in the agent sidebar**? No
  code answers this. On-disk retention is defined (see below), but that is a
  different question — nothing bounds how long a finished run stays rendered.

Answered since this PRD was written:

- *"What is the product name for one execution attempt?"* — **session**.
  `session_id` is the attempt identity in both stores
  (`internal/domain/control_plane.go:83`, `internal/sessions/types.go:35`);
  `attempt` / `attempt_num` is a counter inside it.
- *"How long do completed runs survive on disk?"* (not the sidebar question
  above — this one is about the filesystem session store) — retention is
  explicit: `Store.PurgeOlderThan` (`internal/sessions/purge.go:15`) behind
  `loom cleanup sessions clean` (`internal/cli/cleanup/sessions_cmd.go:26`).
  Filesystem sessions additionally self-heal to `aborted` after
  `StaleSessionThreshold = 4h` (`internal/sessions/stale.go:12`).

## Related

- [`session-stores.md`](session-stores.md) — the two records called "session"
- [`session-artifact-contract.md`](session-artifact-contract.md) — the evidence contract
- [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) — state vocabularies
- [`error-class-reference.md`](error-class-reference.md) — failure vocabularies
- [`agent-run-ux-spec.md`](agent-run-ux-spec.md) — the UI surfaces
- [`README.md`](README.md) — index for this folder
