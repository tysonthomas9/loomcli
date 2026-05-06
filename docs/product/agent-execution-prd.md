# Agent Execution PRD

**Status:** Draft
**Date:** 2026-05-04
**Related:** `docs/product/README.md`,
`docs/product/local-mode-product-spec.md`,
`docs/product/daemon-agent-runtime-architecture.md`,
`docs/product/agent-run-ux-spec.md`,
`docs/product/session-artifact-contract.md`,
`docs/design/agent-run-visibility-plan.md`,
`docs/design/distributed-control-plane.md`,
`docs/design/distributed-control-plane-data-model.md`

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
  command, and git remote.
- Clear run final states: completed, failed, blocked, preflight_failed.
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

- The system must create a run record before invoking the AI backend.
- The run record must include workspace ID, agent name, role, backend,
  runtime provider, command, and start time.
- The run must be associated with a session ID.
- The run may start without a task ID, but task ID must be filled after
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

- Before model invocation, the system should check:
  - backend CLI installed
  - backend credentials usable
  - workspace/repo path binding exists
  - agent worktree exists or can be created
  - required tools exist
  - configured gate command exists
  - git remote exists when push is required
  - session publication is available
- Failed preflight should create a failed run record and a visible task
  note when task context exists.

### Artifacts

- A run should be able to attach:
  - transcript
  - log tail
  - diff patch
  - changed files
  - test/gate output
  - commit ID
  - push result
  - error class and message

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

- `loom plan` and `loom task` publish run/session data.
- `loom agentdef` remains the assignment/config surface.
- Future command: `loom run` or `loom agent run` for explicit run
  lifecycle.
- Doctor/preflight command for agent readiness.

### APIs

- Create/update/finalize run
- Append log/transcript event
- Attach artifact
- List runs by task
- List active/stale agents
- Record preflight result

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
- Populate task Sessions for direct and daemon-launched local agents.
- Fix FleetDB status transitions for review and blocked.
- Make `/api/monitor/agents` include stored idle agents and active runs.

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

- What is the product name for one execution attempt: run, session, job, or
  task run?
- Should direct CLI runs auto-create an ad hoc agent definition or only a
  run record?
- Where should durable transcripts and logs live long term?
- How long should completed runs remain visible in the agent sidebar?
- How should users configure the canonical gate command per repo?
- Should a push failure close the task, block it, or complete the run with
  a warning?
