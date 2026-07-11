# Lead Agent Epic Runner Product Spec

**Status:** Draft
**Date:** 2026-05-11
**Related:** `docs/product/agent-run-ux-spec.md`,
`docs/product/daemon-agent-runtime-architecture.md`,
`docs/product/agent-lifecycle-state-machine.md`,
`docs/product/session-artifact-contract.md`,
`docs/design/epic-runner-lead-control.md`

## Purpose

Define how Loom should present and enforce epic-level orchestration through a
first-class lead agent.

The product direction is:

- The epic runner is not a separate page.
- The epic runner runs inside a lead agent terminal.
- The lead agent owns at most one epic at a time.
- The existing agent route becomes the orchestration surface:
  `/ws/:workspace/agents/:agent`.

This keeps the user in one familiar place: select an agent on the left, interact
with that agent terminal in the center, and see the relevant epic/task scope on
the right.

## Product Decisions

| Decision | Product rule |
|---|---|
| Lead agent is first-class | A `LeadAgent` is represented by an agent row with `role_name=lead` or equivalent lead role metadata. |
| Lead is terminal-backed | The lead must have a resumable terminal/session because users interact with the lead through the terminal. |
| Active epic uses existing field | `agent.parent = <epic_id>` is the active epic assignment for the lead. |
| One epic per lead | A lead cannot run two different epics at the same time. |
| Completion does not clear ownership | When the epic drains, `agent.parent` remains set until the user talks to the lead and clears or changes it. |
| Backend enforces conflicts | Starting `loom epic run --parent X` from lead `A` is rejected if `A.parent` is non-empty and not `X`. |
| Start from UI or terminal | Users can click "Run Epic" or type `loom epic run --parent <epic_id>` manually. Both paths use the same backend command path. |
| No DAG visual in MVP | The right panel shows epic-grouped task cards, statuses, blockers, and current work. It does not need an arrow DAG first. |
| Worker terminals are clickable | Clicking a spawned worker switches the center terminal to that worker. The right panel remains scoped to the lead's epic. |
| Ephemeral worker is single-use | One ephemeral worker represents one task attempt. It may retry the same attempt after process failure, but it must not complete one task and then claim another. |
| Completed workers become history | Completed ephemeral workers leave the live agent rail and appear as task attempts / worker history with logs, diffs, artifacts, and cleanup actions. |
| Cleanup is first-class | Users can reclaim disk from completed ephemeral worktrees without losing the run's persisted evidence. |

## Existing UI Fit

Use the current route:

```text
/ws/E2E/agents/worker-e2e-2
```

as the model for the future lead-agent route:

```text
/ws/E2E/agents/<lead-agent-name>
```

Target layout:

```text
+----------------------------+----------------------------------+------------------------------+
| Left: Agents               | Center: Selected Terminal        | Right: Epic / Task Scope      |
+----------------------------+----------------------------------+------------------------------+
| Workspace: E2E             | Lead: nova                       | If nova.parent is set:        |
|                            | state: active                    |   show that epic + children   |
| Lead Agents                |                                  |                              |
| > Nova                     | $ loom epic run --parent E2E-1   | EPIC E2E-1 Auth Epic         |
|   parent: E2E-1            |                                  |   E2E-5 active               |
|   active                   | [epic-run] spawned worker...     |   E2E-6 blocked              |
|   + worker-auth-1 active   | [epic-run] 1 ready, 1 blocked    |   E2E-2 queued               |
|   + worker-auth-2 idle     |                                  |                              |
|                            |                                  | If nova.parent is empty:      |
| Atlas                      |                                  |   show open epics/tasks       |
|   parent: empty            |                                  |   user talks to lead          |
|   idle                     |                                  |                              |
+----------------------------+----------------------------------+------------------------------+
```

The important product behavior is selection-driven:

```text
select lead with parent=""
  center: lead terminal
  right: open epics and open tasks

select lead with parent="E2E-1"
  center: lead terminal
  right: only E2E-1 and descendant tasks

select worker spawned by selected lead
  center: worker terminal
  right: still E2E-1 and descendant tasks

select completed ephemeral worker history
  center: read-only run detail, not a live terminal
  right: still E2E-1 and descendant tasks
```

Completed ephemeral workers must not appear as idle live agents in the left
rail. The left rail is for leads and currently live/reconnectable workers. Past
ephemeral work belongs in worker history and task attempt history.

## Data Model Mapping

The MVP should use existing shared fields where possible.

### Lead Agent

```text
agent.workspace_key             workspace scope
agent.name                      lead identity
agent.role_name                 lead role
agent.state                     idle | active | stopped
agent.parent                    active epic id, or empty
agent.orchestrator_session_id   resumable lead terminal/session id
agent.mode                      persistent or normal lead mode
agent.desired_state             running when lead should be available
```

`agent.parent` is the product-level epic lock:

```text
agent.parent == ""
  lead has no active epic

agent.parent == "E2E-1"
  lead owns E2E-1
```

### Epic and Tasks

```text
issue.id                        epic or task id
issue.issue_type                epic | task
issue.parent / parent_id        task belongs to epic
issue.status                    open | in_progress | blocked | closed | deferred
issue.source_repo               repo/worktree routing
issue.design                    worker instructions
issue.dependencies[].depends_on_id
                                blocker edge
issue.dependencies[].type       blocks | parent-child | waits-for | conditional-blocks
```

### FleetDB State Contract Guardrail

FleetDB is the source of truth for issue lifecycle state, dependency state, and
computed work views. Loom must not locally redefine what `ready`, `blocked`,
`deferred`, or `newly unblocked` mean.

This matters because FleetDB has both raw lifecycle statuses and computed views:

```text
raw status examples:
  issue.status == open
  issue.status == blocked
  issue.status == deferred
  issue.status == review

computed view examples:
  ready      = work currently available to run
  blocked    = work currently blocked by explicit status, dependency, or parent
  deferred   = work currently held by explicit status or future defer_until
  unblocked  = work that became ready after a blocker was resolved
```

Product rule:

```text
Loom consumes FleetDB computed views as authoritative.
Loom may filter or render those views, but it must not patch their semantics by
unioning status lists, dependency lists, or local UI state.
```

If Loom discovers that a FleetDB computed view is incomplete, the long-term fix
is a FleetDB contract change with Redis/Postgres parity tests. A Loom-side
workaround is allowed only as a temporary compatibility shim, and it must be
clearly marked with the FleetDB version/contract it can be removed after.

Required FleetDB contracts for epic runner correctness:

```text
blocked:
  includes issue.status == blocked
  includes issues with non-terminal dependency blockers
  includes descendants of blocked parents
  excludes closed/tombstoned issues
  reports a stable reason/source such as status-blocked, direct, parent-blocked

deferred:
  includes issue.status == deferred
  includes open issues with defer_until in the future
  excludes open issues with defer_until in the past
  treats status=deferred with no defer_until as an indefinite defer

ready:
  includes only status=open work
  excludes epics
  excludes canonical blocked work
  excludes canonical deferred work
  applies assignee filters as query filters, not as alternate state semantics

newly unblocked:
  returns only issues that now satisfy canonical ready semantics
```

Loom implementation rules:

```text
do:
  use FleetDB /issues/ready for runnable work
  use FleetDB /issues/blocked for blocked work
  use FleetDB /issues/deferred for deferred work
  use issue list/status endpoints only for display, counts, and fallback detail
  add failing FleetDB contract tests before changing computed-state behavior

do not:
  treat status=blocked alone as the blocked view in one Loom layer
  treat dependency-blocked alone as the blocked view in another Loom layer
  compute ready by subtracting local blocked/deferred sets in the UI
  make Redis and Postgres semantics diverge silently
  add SSE event-specific state rules that duplicate FleetDB query semantics
```

Loom-side adapters must preserve the FleetDB view contract:

```text
FleetBackend.Ready:
  forwards ready filters to FleetDB /issues/ready
  treats ready as an availability view, not as a status-filtered list
  does not re-add deferred, blocked, review, in_progress, or epic issues

FleetBackend.Blocked:
  consumes FleetDB /issues/blocked as authoritative once the FleetDB contract is canonical
  does not permanently union /issues/blocked with status=blocked list results
  keeps any compatibility union behind a clearly marked temporary shim
  preserves blocker/source metadata when FleetDB exposes it

FleetBackend deferred handling:
  uses FleetDB /issues/deferred for operational "currently deferred" views
  uses status=deferred counts only when the UI explicitly asks for raw lifecycle status

FleetBackend.Close:
  preserves canonical newly-unblocked results from FleetDB when returned
  does not synthesize newly-unblocked work from partial local ready predicates
```

SSE and realtime updates are invalidation/delivery mechanisms, not alternate
state authorities. On an SSE event, Loom clients should refresh or reconcile
against the same FleetDB computed views used by the runner and CLI. Adding a new
FleetDB state field or event type must not require each UI/client layer to learn
a new version of the `ready`/`blocked`/`deferred` rules.

The right panel filter follows the selected lead:

```text
if selectedLead.parent != "":
  show issue.id == selectedLead.parent
  show tasks where issue.parent == selectedLead.parent

if selectedLead.parent == "":
  show open epics
  show open tasks not scoped to another selected lead
```

### Spawned Workers

Workers spawned by a lead-owned epic runner should be attributable to that lead.

```text
worker.role_name                task
worker.mode                     ephemeral
worker.parent                   same epic id as lead.parent
worker.orchestrator_session_id  same lead session id
worker.state                    idle | active | stopped
worker.current_task             assigned task while live, derived from
                                command/session/task claim
worker.completed_task           completed task after stop, derived from the
                                terminal task session
```

Workers should appear nested under the lead when possible:

```text
Nova
  parent: E2E-1
  + worker-e2e-5 active   current: E2E-5
  + worker-e2e-6 idle     current: none
```

Ephemeral worker invariant:

```text
worker.mode == ephemeral
  worker must be started with exactly one task_id
  worker may claim only that task_id
  worker stops after one successful task completion
  worker remains queryable as run history after it stops
```

Service workers are different:

```text
worker.mode == service
  worker may keep polling
  worker may claim multiple tasks over time
  worker remains a live/restartable agent
```

Do not introduce a separate `TaskRun` entity for the MVP. Use existing
control-plane primitives:

```text
Agent              lead/worker identity and lifecycle
AgentCommand       start/stop/yield dispatch, including task_id
AgentSession       task attempt lifecycle and artifact handle
Issue              epic/task status, blocker graph, assignee
TerminalSession    live PTY or archived terminal/log handle
```

If reporting later needs an aggregate read model, it should be derived from
these records first.

### Worker History Surfaces

Ephemeral worker history must be visible in three places.

Primary surface: selected lead page.

```text
/ws/:workspace/agents/:lead-agent-name
```

When the selected lead has an active epic, the right panel shows the epic,
children, live workers, and completed worker history:

```text
Epic E2E-1
  Tasks
    E2E-2 closed       attempt #1 completed
    E2E-3 in_progress  attempt #1 running
    E2E-4 open         no attempt yet

  Worker History
    worker-e2e-2-a1  E2E-2  completed  logs diff cleanup
    worker-e2e-3-a1  E2E-3  running    terminal
    worker-e2e-5-a2  E2E-5  failed     logs rerun cleanup
```

Per-task surface: task attempt history.

```text
Task E2E-2 Reset password flow
  status: closed
  attempts:
    #1 worker-e2e-2-a1 completed  8m  codex  logs | diff | cleanup
```

This is the best surface for understanding what happened to one task.

Workspace cleanup surface:

```text
/ws/:workspace/agents/history
```

This global history/cleanup view lists completed ephemeral worker attempts
across leads and epics with filters:

```text
filters: lead, epic, task, status, age, disk usage
actions: delete worktree, archive logs, delete artifacts, rerun
```

The cleanup surface exists because completed ephemeral workers may no longer be
visible in the live agent rail, but users still need to reclaim disk safely.

### Agent Command

The existing command channel dispatches task workers.

```text
agent_command.target_agent_id   worker name
agent_command.target_node_id    daemon node id
agent_command.type              start
agent_command.payload.task_id   assigned task id; required for ephemeral
                                workers
agent_command.status            queued | acked | running | succeeded | failed
```

## Backend Rules

### Runner Start

`loom epic run` must identify the lead. Preferred inference:

```text
LOOM_AGENT_NAME=<lead-name>
LOOM_WORKSPACE=<workspace>
```

Fallback for manual or non-terminal usage:

```bash
loom epic run --parent E2E-1 --lead nova
```

Start rules:

```text
load lead agent by workspace + lead name

if lead.parent == "":
  set lead.parent = requested_epic_id
  start runner

if lead.parent == requested_epic_id:
  resume / continue runner

if lead.parent != "" and lead.parent != requested_epic_id:
  reject
```

Suggested rejection text:

```text
Lead nova is already assigned to E2E-2.
Ask the lead to clear or finish that epic before running E2E-1.
```

### Runner Completion

When all child work drains:

```text
do not clear lead.parent
do not auto-assign a new epic
leave ownership visible in the UI
```

The user clears or changes epic ownership by talking to the lead. This keeps the
lead's memory, terminal context, and handoff explicit.

### Ephemeral Worker Start

For `worker.mode=ephemeral`, backend start rules are stricter than service
agent start rules:

```text
if start command has no task_id:
  reject

if worker already has a completed task session:
  reject restart and require a new attempt worker

if requested task_id is not ready/workable:
  do not claim a fallback task

if requested task_id is ready:
  claim only that task_id
```

This prevents a completed ephemeral worker from becoming a generic queue
consumer after a manual restart or UI reconnect.

### Ephemeral Worker Completion

On clean completion after a task claim:

```text
set worker.state = stopped
set worker.desired_state = stopped
complete AgentSession with task_id and artifacts
keep logs/transcript/diff/test metadata queryable
do not show the worker as an idle live agent
```

On failure:

```text
retry the same attempt according to daemon retry policy
if retries are exhausted, mark session failed
leave worker in history with rerun action
```

Rerun creates a new ephemeral worker attempt. It does not reuse a completed
ephemeral worker.

### UI Run Epic Button

The UI button and terminal command should use the same backend run semantics.
A user may still type:

```bash
loom epic run --parent <epic_id>
```

For provider-backed interactive leads, the UI button should first write durable
backend assignment/run state, then deliver that assignment through the selected
lead session's provider adapter. For Codex app-server-backed leads, that means a
visible `turn/start` on the same Codex thread the user is already viewing. It
does not mean blind PTY typing into the composer.

This guarantees the UI button and manual terminal command share:

- the same lead identity
- the same session/resume behavior
- the same backend conflict enforcement
- the same observable terminal output

Implementation status, 2026-05-17:

- The CLI and UI now share the `internal/epicrunner` reconcile/dispatch path.
- The UI run route validates backend, repo, epic, command channel, daemon node,
  and lead ownership before mutating lead assignment.
- The UI run route performs the first reconcile pass and queues real daemon
  worker `start` commands for ready child tasks, then continues a backend loop
  so newly unblocked child work can be scheduled.
- Browser validation covered a fresh two-epic Slack mock workspace with
  `atlas` and `nova` as separate leads. The no-repo case fails with a clear
  setup error, repo-backed runs dispatch one worker per lead/epic, duplicate
  active runs return `already_running`, and assigning a busy lead to a second
  epic returns 409.
- Lead startup and Claude hook delivery now share a compact backend assignment
  context. `loom lead` starts with the assigned epic when `agent.parent` is set,
  and Claude receives assignment `additionalContext` on session start, user
  prompt, task-tool boundary, and stop. The UI now reports
  `delivery_state: pending` separately from runner `run_state`.
- Codex app-server delivery now launches Codex leads as controlled app-server
  runtimes from startup. The visible TUI attaches through `codex --remote`, and
  backend delivery uses provider `turn/start` on the recorded thread when the
  thread is idle. If the runtime is still starting or busy, delivery remains
  pending and retries instead of typing into the terminal.
- Already-running raw Codex TUIs remain legacy/uncontrolled sessions. They are
  not retrofitted with app-server control; users should reconnect/restart those
  leads to get visible backend-originated Run Epic delivery.

## Terminal and Resume Requirements

Each lead agent must have a stable terminal/session identity.

For Codex, Claude, and similar backends, Loom should persist enough session
metadata to reconnect to the same lead conversation where the backend supports
resume by session id.

Required behavior:

```text
select lead
  auto-connect to lead terminal
  if terminal process exists, attach
  if backend session is resumable, resume same lead session
  if not resumable, show explicit stale/offline state

select spawned worker
  switch center pane to worker terminal
  keep right pane scoped to lead epic

select completed ephemeral worker
  show read-only run detail
  show logs, transcript, diff, artifacts, task, and cleanup actions
  do not spawn or reconnect a PTY
```

The right panel should not lose epic scope when switching from lead terminal to
worker terminal.

## User Flows

### Idle Lead Starts an Epic

```text
1. User selects lead "nova".
2. nova.parent is empty.
3. Right panel shows open epics and open tasks.
4. User clicks Run Epic on E2E-1, or types:
     loom epic run --parent E2E-1
5. Backend sets nova.parent = E2E-1.
6. Center terminal shows the runner output.
7. Right panel filters to E2E-1 and children.
```

### Lead Already Owns the Same Epic

```text
1. User selects nova.
2. nova.parent = E2E-1.
3. User runs:
     loom epic run --parent E2E-1
4. Backend allows resume/continue.
5. Runner picks up existing ready/blocked/active state.
```

### Lead Already Owns a Different Epic

```text
1. User selects nova.
2. nova.parent = E2E-2.
3. User tries:
     loom epic run --parent E2E-1
4. Backend rejects the command.
5. UI/terminal tells the user to talk to the lead to clear or finish E2E-2.
```

### Worker Inspection

```text
1. User selects lead nova.
2. Right panel shows E2E-1.
3. User clicks worker-e2e-5 under nova.
4. Center switches to worker-e2e-5 terminal.
5. Right panel still shows E2E-1.
6. Current task E2E-5 is highlighted.
```

### Completed Worker History And Cleanup

```text
1. User selects lead nova.
2. nova.parent = E2E-1.
3. Right panel shows E2E-1 and Worker History.
4. User clicks worker-e2e-2-a1, which is completed.
5. Center switches to read-only run detail.
6. User can view logs, transcript, diff, artifacts, and worktree path.
7. User clicks Delete worktree to reclaim disk.
8. Persisted run evidence remains visible after worktree deletion.
```

### Workspace Cleanup

```text
1. User opens /ws/E2E/agents/history.
2. User filters completed ephemeral workers older than 7 days.
3. UI shows disk usage, lead, epic, task, status, and artifact presence.
4. User bulk-deletes worktrees while preserving logs and session metadata.
```

## Current Task Display

The UI should make "what is this agent doing right now" visible without opening
logs.

For a lead:

```text
current epic: E2E-1
runner state: running | idle | drained | disconnected
workers: 1 active, 1 queued, 0 blocked
```

For a worker:

```text
current task: E2E-5 Implement login form
task status: in_progress
source repo: app
command status: running
```

## MVP Scope

Ship these first:

1. First-class lead agent role in UI and backend metadata.
2. `agent.parent` used as active epic assignment.
3. Backend conflict rejection for lead running a different epic.
4. UI selection model:
   - lead selected: show lead terminal
   - worker selected: show worker terminal
   - right panel follows lead epic scope
5. "Run Epic" button that uses the same backend run semantics as the lead
   terminal command and delivers the assignment through the lead session
   adapter rather than blind terminal typing.
6. Auto-connect/resume lead terminal where session metadata exists.
7. Show current task/epic for selected lead or worker.
8. Enforce single-task ephemeral worker lifecycle in backend start/claim/restart
   paths.
9. Show completed ephemeral workers as history on the lead page and task detail.
10. Provide workspace-level cleanup for completed ephemeral worker worktrees.

Do not include in MVP:

- visual DAG arrows
- policy marketplace
- multi-epic lead scheduling
- automatic clearing of lead epic ownership
- separate epic runner page
- separate `TaskRun` write model

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Manual terminal command bypasses UI restrictions | Enforce one-epic rule in backend runner start, not just UI. |
| Lead drains epic but still owns it | This is intentional. Show "drained, still assigned" and require user/lead action to clear. |
| Terminal reconnect fails | Persist terminal/session metadata and show explicit disconnected state with retry. |
| Worker click loses epic context | Store selected lead scope separately from selected terminal. |
| Existing task workers are not attributable to a lead | Set `orchestrator_session_id` and `parent` on spawned workers. |
| `agent.parent` is overloaded | Treat `parent` as active epic for lead/task orchestration and document the invariant clearly. |
| Completed ephemeral workers look restartable | Remove them from the live agent rail and render them as read-only task attempts. |
| Manual restart makes ephemeral worker claim another task | Require `task_id` for ephemeral start and reject restart after completed task session. |
| Users cannot reclaim disk | Provide lead-scoped and workspace-level cleanup actions that delete worktrees while preserving session artifacts. |
| History disappears after cleanup | Store logs, transcript, diff, test, commit, and summary artifacts before deleting worktree/container state. |

## Open Implementation Notes

- Decide whether lead role is a built-in role name (`lead`) or a role category
  on top of existing custom roles.
- Add `--lead` to `loom epic run` only if terminal env inference is not always
  available.
- Ensure worker terminal session names are stable enough for clicking nested
  workers in the sidebar.
- Add API support for "lead scope" if current endpoints cannot efficiently
  return lead, child workers, active epic, and scoped issues in one request.
- Add a compact derived read model only if the UI cannot efficiently join
  agents, commands, sessions, terminal tabs, and issues for worker history.
