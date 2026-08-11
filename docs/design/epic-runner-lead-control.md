# Epic Runner Lead Control Direction

Date: 2026-05-16
Status: Design direction, Claude hooks and Codex app-server topology smoke-tested

## Goal

The epic runner should let users keep interacting with a visible lead AI session
while the backend remains the source of truth for which epic the lead should
work on.

The current direction is:

- The backend owns lead assignment, run locking, and run state.
- Provider adapters inject or expose backend state at lifecycle boundaries.
  Claude does this with hooks. Codex can do this through app-server status
  events and backend-originated `turn/start` messages on the same thread.
- The visible AI TUI remains the human-facing lead conversation.
- Raw PTY injection is only a fallback wake-up mechanism for idle sessions, not
  the authoritative transport for run details.
- Busy sessions are updated at safe hook boundaries instead of blindly typing
  into the composer.

## Model

Introduce an explicit assignment/run model instead of treating terminal text as
truth.

```text
LeadAssignment
  lead_id
  workspace_id
  session_id
  epic_id
  epic_run_id
  state: unassigned | assigned | active | paused | completed | cancelled | failed
  delivery_state: none | pending | delivered | acknowledged
  version
  delivered_version
  acknowledged_version
  updated_at
```

```text
EpicRun
  id
  workspace_id
  epic_id
  lead_id
  lead_session_id
  state: queued | assigned | active | paused | completed | cancelled | failed
  start_policy: immediate | ask_lead
  created_at
  updated_at
```

`LeadAssignment` is the current desired state for a lead. `EpicRun` is the
durable execution record. The backend enforces:

- one active run per epic
- one active run per lead
- idempotent UI run requests
- explicit stale/cancelled/paused transitions

The older `agent.parent` can remain as a compatibility/read-model field, but it
should not be the only lock or liveness source.

## Implementation Decisions

These are the decisions for the first implementation. Treat them as settled
unless a test proves they are wrong.

### Controlled Codex Runtime Is Required

Codex lead sessions must be born as controllable app-server sessions. The Run
Epic path cannot safely retrofit control onto an already-running raw Codex TUI,
because the backend has no durable provider thread id and no reliable mutation
channel for `turn/start`.

The required Codex topology is:

```text
loom lead --backend codex
  starts one lead-scoped Codex app-server
  starts the visible user TUI with codex --remote <app-server>
  records endpoint, pid, runtime home, and provider thread id on the lead session

Run Epic
  writes durable Loom assignment/run state
  asks the Codex lead-session adapter to deliver that assignment
  sends turn/start only when the provider thread is idle
  otherwise leaves delivery pending until the provider reports idle
```

This is intentionally not blind PTY input, and it is not a stdio-only Codex
process. Stdio mode is useful for a single parent process, but it does not give
Loom both a real human-facing Codex TUI and a second backend
observer/controller. The first implementation should use Codex app-server over
`ws://127.0.0.1:<port>` or a Unix socket, with the visible TUI attached by
`codex --remote`.

Legacy sessions that were started as raw Codex TUIs are not controllable. For
those sessions, Loom should keep the backend assignment pending and surface a
clear reconnect/restart-required state instead of marking the assignment
delivered.

### Source Of Truth

Use a dedicated `LeadAssignment` store plus durable `EpicRun` records.

`agent.parent` remains a compatibility/read-model field for existing UI and CLI
surfaces, but it is not the authoritative lock. The authoritative invariants
live in `LeadAssignment` and `EpicRun`:

- one non-terminal assignment per lead
- one non-terminal run per epic
- idempotent resume for the same epic/lead
- explicit version increments for every backend assignment transition

Do not derive current assignment only from terminal contents, Codex thread
contents, Claude hook events, or `AgentSession` history.

### Assignment Delivery

Track assignment delivery explicitly.

```text
version                 backend desired-state version
delivered_version       highest version submitted to the lead session
acknowledged_version    highest version the lead has visibly acknowledged
delivery_state          none | pending | delivered | acknowledged
```

For MVP behavior, `delivered_version` is enough to avoid duplicate wake-ups.
`acknowledged_version` should still exist because hook-driven providers can
record real acknowledgment and future UI can distinguish "sent to lead" from
"lead understood it".

### Lead Runtime Record

Persist a provider-neutral runtime record for each lead session.

```text
LeadSessionRuntime
  workspace_id
  lead_id
  provider: codex | claude
  provider_session_id
  provider_thread_id
  runtime_home
  app_server_endpoint
  app_server_pid
  status: disconnected | idle | active | waiting_on_approval | waiting_on_user_input | failed
  status_version
  controller_lease_id
  updated_at
```

`app_server_endpoint` and `app_server_pid` are runtime metadata. Assignment
state must survive their loss. Auth secrets do not belong in this record.

### Codex Process Ownership

Run one Codex app-server per lead session for the first implementation.

The Loom daemon node that owns the lead terminal owns that app-server process.
Use a lead-scoped runtime directory and a lead-scoped Codex `sqlite_home`, for
example:

```text
<loom-runtime>/<workspace>/<lead>/codex/
```

The app-server and its remote TUI also use a lead-scoped `CODEX_HOME`. Loom
seeds that home with the user's `config.toml`, `auth.json`, and global
`AGENTS.md`, but does not copy historical sessions or SQLite databases. A large
shared Codex history can otherwise be reconciled into every new `sqlite_home`
before the listener binds, making interactive lead startup exceed its readiness
deadline. The app-server launcher owns a dedicated process group so timeout and
shutdown cleanup includes package-manager shims and their native Codex child.

This isolates lead conversations, makes cleanup clear, and avoids accidental
cross-workspace subscriptions. A shared app-server can be reconsidered later
only after the per-lead adapter is stable.

### Backend Controller Lease

Only one Loom backend controller may call provider mutation APIs for a lead at a
time.

Browsers and secondary services observe Loom state through the backend. They do
not connect directly to Codex app-server as controllers. For Codex, multiple
websocket observers are acceptable, but only the lease holder may call
`turn/start`, `interrupt`, or future mutation APIs.

### Busy Behavior

If a lead has no active epic but the provider session is busy, create the
assignment and leave it pending. Do not submit a turn while Codex reports
`active`, `waitingOnApproval`, or `waitingOnUserInput`.

When the lead returns to `idle`, the controller sends one visible provider turn
for the current assignment version and records `delivered_version`.

### Conflict Behavior

Use strict conflict rules for the first implementation:

- same lead, same epic: idempotent resume
- same lead, different non-terminal epic: reject
- same epic, different non-terminal lead: reject
- no active epic, busy lead session: queue delivery until idle

Do not implement automatic epic switching in the MVP. Switching should be a
separate explicit user action because it changes the lead's working context.

### Interrupt Policy

Normal "Run Epic" does not interrupt active provider work.

Interrupts are reserved for:

- user clicks a clear stop/cancel/interrupt action
- backend must stop unsafe work after a cancelled or paused run
- provider is stuck and the user chooses recovery

Assignment delivery uses queue-on-busy, not hard interrupt.

### Provider Adapter Boundary

Hide provider-specific control behind a lead-session adapter.

```text
LeadSessionAdapter
  EnsureRuntime(ctx, lead) -> LeadSessionRuntime
  Subscribe(ctx, runtime) -> status/event stream
  StartTurn(ctx, runtime, message, assignment_version) -> delivery result
  Interrupt(ctx, runtime, reason) -> result
  Resume(ctx, runtime) -> LeadSessionRuntime
  Close(ctx, runtime) -> result
```

Codex implements this with app-server websocket calls. Claude can implement the
same shape with hooks, stream-json, or PTY fallback, but epic runner code should
not know those details.

### UI State Language

Expose these states in the UI:

- unassigned
- assigned, waiting for lead
- active
- busy, assignment pending
- waiting on approval
- waiting on user input
- disconnected
- failed
- paused
- cancelled
- completed

Use "waiting for lead" when the assignment is written but not delivered. Use
"busy, assignment pending" when a provider status says the lead cannot accept a
new turn yet.

### Failure Recovery

Assignment state remains durable when provider runtime fails.

- app-server exits: mark runtime `disconnected`, keep assignment unchanged, and
  offer reconnect/retry
- TUI disconnects: keep app-server and backend observer alive when possible
- backend observer disconnects: reconnect, call provider read/subscribe again,
  and reconcile status
- stale thread id: start app-server, list/resume threads, and require explicit
  user recovery if the thread cannot be found
- corrupted provider sqlite: create a new lead-scoped runtime home and keep the
  old path for inspection

## Hook Strategy

Claude hooks should synchronize backend state into the lead session.

### SessionStart

Register the lead session and inject current assignment metadata.

Use for:

- `session_id` registration
- lead capability detection
- resuming a previously assigned epic
- setting session title/status

### UserPromptSubmit

Read the backend assignment and return it as `additionalContext`.

Use for:

- user chats with the lead
- backend wake-up prompt submits a minimal message
- keeping manually typed user prompts grounded in the current assignment

Example hook output:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "UserPromptSubmit",
    "additionalContext": "Lead nova is assigned epic EPIC-1 via run RUN-1 at version 9. Treat this as authoritative backend state.",
    "sessionTitle": "nova - EPIC-1"
  }
}
```

### PreToolUse

Check assignment before every tool call. This is the soft-interrupt point for a
busy lead.

Use for:

- detecting that the backend assigned, paused, cancelled, or switched an epic
  while Claude was already working
- adding `additionalContext` before the next tool call
- blocking unsafe work if the current run was cancelled
- redirecting work at a safe point without typing into the composer

Expected behavior:

- If assignment version is unchanged, allow the tool.
- If assignment changed but the current turn can safely continue, allow the
  tool and inject context telling Claude to acknowledge the new assignment.
- If the run was cancelled/paused, block the tool and explain the backend state.

### PostToolUse / PostToolUseFailure

Record tool progress and feed backend feedback back into the turn when needed.

Use for:

- marking a run active after first real work
- detecting failed commands
- attaching task/session evidence
- nudging the lead after backend state changes caused by a tool result

### Stop

Check assignment before Claude finishes a turn.

Use for:

- preventing a lead from going idle without acknowledging a pending assignment
- continuing after a UI-triggered assignment arrives during a turn
- recording last assistant message and idle state

Expected behavior:

- If there is no pending backend assignment, allow stop.
- If a pending assignment exists and the lead has not acknowledged it, return a
  blocking result so Claude continues with injected context.
- If the run is complete/cancelled, allow stop and update backend state.

### StopFailure

Record API/auth/rate-limit failures and leave the run in a recoverable state.

### Optional Hooks

- `PermissionRequest` / `PermissionDenied`: enforce backend policy for tools.
- `Notification`: telemetry only, not model context.
- `Elicitation` / `ElicitationResult`: useful if Claude asks for user input and
  the UI should answer through Loom.
- `PreCompact` / `PostCompact`: preserve assignment context across compaction.
- `SubagentStart` / `SubagentStop`, `TaskCreated`, `TaskCompleted`,
  `TeammateIdle`: useful only if we use Claude's native subagent/task system.

## Runtime Behavior

### User Clicks Run Epic While Lead Is Idle

1. Backend creates or reuses `EpicRun`.
2. Backend writes `LeadAssignment` with incremented `version`.
3. Backend verifies the lead session is idle.
4. Backend sends a minimal TUI wake-up prompt:

   ```text
   Loom state changed. Check your assigned work.
   ```

5. `UserPromptSubmit` hook injects the authoritative assignment/run context.
6. Claude starts work based on hook context, not based on the tiny prompt.

### User Clicks Run Epic While Lead Is Busy

1. Backend creates or reuses `EpicRun`.
2. Backend writes `LeadAssignment` with incremented `version`.
3. Backend does not type into the active composer.
4. Next `PreToolUse` hook observes the version change and injects context.
5. If no tool boundary happens, the `Stop` hook observes the pending assignment
   before Claude goes idle and blocks stop once with context.
6. Claude acknowledges the new assignment and proceeds or explains why it cannot
   switch safely.

For a true hard interrupt while Claude is mid-generation and not calling tools,
the backend still needs an explicit interrupt capability: PTY `Esc`, Claude
remote control if available, or a stream-json controlled process. Hard
interrupts should be reserved for cancellation or urgent user action, not the
normal run-epic path.

### Codex App-Server Adapter

Codex has a better topology than raw PTY control when using `app-server`:

- Run one Codex app-server per lead session.
- Treat the app-server runtime as part of lead startup, not as a Run-button
  side effect.
- Attach the human's real Codex TUI with `codex --remote <server>`.
- Attach the Loom backend as a second websocket client.
- Subscribe the backend to the lead thread with `thread/read`.
- Track idle/busy from `thread/status/changed`.
- Start backend-assigned work with `turn/start` on the same thread only when the
  backend state says the lead is idle, or queue the assignment when active.

This keeps the Codex TUI as the human UI. Loom does not need to build a full
replacement chat UI before it can observe and control a lead session.

The backend should not attempt to paste assignment text into the xterm or write
to the Codex process stdin for normal delivery. That path loses the provider
thread identity, races with user input, and cannot tell the difference between a
delivered turn and text buffered in the composer.

Provider-neutral mapping:

```text
Codex thread/status/changed idle
  -> lead session is idle and can accept backend turn/start

Codex thread/status/changed active
  -> lead session is busy; queue assignment or wait for safe transition

Codex activeFlags.waitingOnApproval
  -> lead is blocked on approval, not truly available for new epic work

Codex activeFlags.waitingOnUserInput
  -> lead is blocked on user input, not truly available for new epic work
```

Backend-originated prompts should be small and explicit:

```text
Loom assigned you epic EPIC-1 via run RUN-1. Read backend assignment state and
begin only if this thread is idle.
```

The assignment details still come from backend state, not from a long prompt
typed into the user's composer.

### User Chats With Lead After Assignment

1. User types normally in the TUI.
2. `UserPromptSubmit` injects the current assignment/run state.
3. Claude answers with awareness that the backend assigned or changed the epic,
   even if that assignment came from the UI rather than the user's prompt.

## Empirical Checks Run

Manual probes on 2026-05-16:

- Claude interactive TUI can be controlled externally via PTY when run with
  normal user permissions.
- PTY-injected prompt returned `ACK_CLAUDE_TUI_1`.
- PTY input sent while Claude was busy was buffered into the composer and was
  not delivered to the model mid-turn. This confirms PTY injection needs idle
  checks and composer clearing.
- Claude `stream-json` accepted multiple user events in one process and kept
  one `session_id`.
- FIFO-fed `stream-json` accepted incremental messages and returned `ALPHA`,
  then `BETA`.
- `UserPromptSubmit` hook `additionalContext` worked in headless `claude -p`;
  Claude answered `ACK_HOOK_CONTEXT_1` from hook-only context.
- The same hook method worked in interactive TUI; Claude answered
  `ACK_HOOK_TUI_1` from hook-only context and accepted `sessionTitle`.
- Claude idle/busy is observable through hooks: `UserPromptSubmit` fired when
  work began and `Stop` fired when the TUI returned `ACK_IDLE_DETECT_CLAUDE`.
- Codex exposes hook and thread lifecycle events through the generated
  app-server protocol. The generated schema includes `userPromptSubmit`,
  `stop`, `thread/status/changed`, and `activeFlags`.
- Codex app-server can host a real Codex TUI and a second backend observer on
  the same lead session without breaking the session model.
- Test topology:
  - app-server:
    `codex app-server --listen ws://127.0.0.1:17777 -c sqlite_home="/tmp/codex-idle-sqlite"`
  - human TUI:
    `codex --remote ws://127.0.0.1:17777 --no-alt-screen -C <repo>`
  - backend observer/controller: minimal websocket JSON-RPC client using
    `initialize`, `thread/list`, `hooks/list`, `thread/read`, and `turn/start`.
- Important Codex gotcha: connecting to app-server and calling `thread/list` is
  not enough to receive live status transitions. The observer must call
  `thread/read` for the target thread to subscribe.
- TUI-driven Codex prompt test:
  - user typed:
    `IDLE_TOPOLOGY_TEST_2: Reply exactly ACK_TOPOLOGY_CODEX_2 and nothing else.`
  - TUI answered `ACK_TOPOLOGY_CODEX_2`.
  - backend observer received `thread/status/changed` from `active` to `idle`.
- Backend controller Codex prompt test:
  - controller sent `turn/start` on the same thread with:
    `IDLE_TOPOLOGY_CONTROLLER: Reply exactly ACK_TOPOLOGY_CONTROLLER and nothing else.`
  - TUI displayed the backend-started prompt and answer
    `ACK_TOPOLOGY_CONTROLLER`.
  - observer/controller saw the same thread return to `idle`.
- Did not observe a live `turn/started` or `turn/completed` notification on the
  observer during this smoke test. For idle detection,
  `thread/status/changed` was sufficient. Richer timeline rendering still needs
  follow-up testing.
- Node v24's built-in `WebSocket` failed the local app-server handshake with an
  empty error in this environment. A raw RFC6455 Python client worked. The
  production backend should use a websocket client with explicit handshake and
  header control.
- Local `~/.codex` sqlite corruption caused an earlier app-server startup
  failure (`file is not a database`). Starting app-server with a dedicated
  `sqlite_home` avoided the issue while still using the existing authenticated
  Codex installation.

## Required Tests

These should be automated before treating the architecture as complete.

### Hook Unit Tests

- `UserPromptSubmit` returns assignment `additionalContext` from backend state.
- `UserPromptSubmit` returns no extra context when lead is unassigned.
- `PreToolUse` allows tools when assignment version is unchanged.
- `PreToolUse` injects context when assignment version changes.
- `PreToolUse` blocks tools when the run is cancelled or paused.
- `Stop` allows normal stop when no pending assignment exists.
- `Stop` blocks once when a pending assignment exists and the lead has not
  acknowledged it.
- `StopFailure` records recoverable run failure state.

### Backend Tests

- Starting a run is idempotent for the same epic/lead/idempotency key.
- Starting a run rejects conflicts when the lead already owns another active
  run.
- Starting a run rejects conflicts when the epic already has another active
  lead.
- Assignment version increments exactly once per backend state transition.
- Cancelling or pausing a run changes hook-visible assignment state.
- Session idle/busy state determines whether the backend sends a wake-up prompt
  or waits for hooks.

### Integration Tests

- Idle lead run:
  - UI/API starts epic run.
  - Backend writes assignment.
  - TUI receives only a minimal wake-up prompt.
  - Hook injects full run context.
  - Lead acknowledges the assigned epic.

- Busy lead run with tool boundary:
  - Lead starts a long turn that calls a tool.
  - UI/API assigns an epic while lead is busy.
  - Backend does not inject text into the composer.
  - `PreToolUse` injects changed assignment.
  - Lead acknowledges or switches at the next safe point.

- Busy lead run without tool boundary:
  - Lead produces a long answer without tools.
  - UI/API assigns an epic while lead is busy.
  - `Stop` blocks once and feeds assignment context.
  - Lead continues and acknowledges assignment.

- Cancel while busy:
  - Backend marks run cancelled.
  - Next `PreToolUse` blocks unsafe tool work.
  - `Stop` records idle/cancelled status.

- Multi-client:
  - Two browser clients see the same lead assignment state.
  - Only one wake-up is sent for a single assignment version.

- Multi-workspace:
  - Assignments are workspace scoped.
  - Hooks for workspace A cannot read assignment state for workspace B.

- Codex app-server hybrid:
  - Start Codex app-server with isolated `sqlite_home`.
  - Attach a real Codex TUI using `codex --remote`.
  - Attach a backend websocket client to the same app-server.
  - Backend calls `thread/read` for the selected lead thread.
  - User-submitted TUI prompt produces `active` then `idle` status events.
  - Backend `turn/start` on an idle thread appears in the TUI and returns to
    `idle`.
  - Backend `turn/start` while the thread is `active` is rejected or queued by
    Loom, not blindly sent.
  - Multiple backend/browser clients subscribed to the same thread observe the
    same status transitions.
  - Separate workspaces map to separate lead thread subscriptions.

### Agent-Browser UI Tests

Use named `agent-browser --session` values to avoid collisions.

- Run Epic button on an idle lead shows `assigned` then `active`.
- Running an already assigned epic shows resume/retry state rather than starting
  a duplicate run.
- Running an epic while the provider session is busy but the lead is unassigned
  shows queued/pending assignment state.
- Running a different epic while the lead owns a non-terminal epic shows a clear
  conflict and does not queue an implicit switch.
- The lead panel explains that the update will be picked up at the next safe
  point.
- The terminal composer is not polluted by backend state while the lead is busy.

## Validation Log

### 2026-05-17 UTC: Correct Runner Implementation Pass

Scope: replace the bind-only UI route with a real runner start path that reuses
the `loom epic run` worker dispatch semantics.

Implemented:

- Moved the reusable reconcile/dispatch logic into `internal/epicrunner`.
- Kept `loom epic run` as the foreground loop, now backed by the shared runner.
- Changed `POST /api/workspaces/{ws}/epics/{id}/run` to:
  - validate issue backend availability
  - validate the target issue exists and is an epic
  - require at least one workspace repo before mutating the lead assignment
  - require the agent command channel
  - require exactly one active daemon node
  - bind/resume the lead with the same one-lead-per-epic rules
  - run the first reconcile pass synchronously
  - enqueue real worker `start` commands for ready child tasks
  - continue the runner loop in-process after the response so downstream
    unblocked work can be scheduled
- Added UI toast detail for `already_running`, `drained`, dispatched tasks, and
  waiting-for-ready-work outcomes.

Edge cases covered by tests:

- Workspace has no repos: request fails before lead parent is mutated.
- Missing issue backend: route returns unavailable.
- Missing lead, non-lead agent, lead already on another epic, and epic already
  claimed by another lead.
- No active daemon node and multiple active daemon nodes are classified.
- Existing live worker command prevents duplicate dispatch.
- Existing live task session consumes concurrency.
- Stopped deterministic worker is observed once before being treated as fatal.
- Ready task dispatch creates a deterministic ephemeral worker and queues a
  daemon `start` command with the lead orchestration session.

Remaining gotcha:

- The UI route now starts a real backend runner loop, but at this point it did
  not inject assignment context into a busy provider TUI. The lead binding was
  visible in the UI and workers were attributed to the lead session; provider
  delivery remained the next step.

### 2026-05-17 UTC: Lead Assignment Context Delivery Pass

Scope: make backend epic assignment visible to lead runtimes at safe provider
boundaries without returning to blind PTY typing.

Implemented:

- Added a shared `LeadAssignmentContext` helper in `internal/epicrunner` that
  loads the current lead assignment from the backend agent record and formats a
  compact provider context block.
- `loom lead` now appends the current backend assignment to the lead prompt at
  startup when `LOOM_AGENT_NAME`/active workspace resolve to a lead with
  `agent.parent` set.
- Claude hook handling now emits assignment `additionalContext` for
  `SessionStart`, `UserPromptSubmit`, `PreToolUse`, and `Stop` when the lead is
  assigned an epic. This covers user prompts and safe busy-session boundaries
  without typing into the composer.
- The Web UI run response now reports `delivery_state: "pending"` separately
  from `run_state`. `run_state` still describes runner lifecycle
  (`running`, `drained`, `already_running`); delivery state now describes
  whether the lead session has picked up the assignment.
- The Run Epic toast now tells the user when lead context delivery is pending.

Edge cases covered by tests:

- Missing lead, non-lead agents, and unassigned leads do not produce assignment
  context.
- Assigned leads include workspace, lead, epic, assignment version, and
  orchestration session when available.
- Unsupported Claude hooks and empty assignments produce no hook stdout.
- `delivery_state` no longer aliases runner lifecycle state in the HTTP
  response.

Verification notes:

- Focused gates passed:
  `GOCACHE=/tmp/go-build-cache go test ./internal/epicrunner ./internal/cli/hooks ./internal/cli/agent ./internal/webui/handlers/epics`,
  `GOCACHE=/tmp/go-build-cache go build ./cmd/loom`, and frontend
  `npm run typecheck`.
- A broad `GOCACHE=/tmp/go-build-cache go test ./...` run was attempted in the
  sandbox but failed on environment prerequisites unrelated to this slice:
  localhost/Unix socket binds are denied, writes under `/Users/tyson/.loom` are
  denied, tmux-backed tests cannot start, and workspace tests need `fleet-db` on
  `PATH`.

Remaining gotcha:

- Codex app-server delivery is still pending. Codex leads see assignment context
  on a fresh `loom lead` startup, but an already-running Codex TUI still needs
  the per-lead app-server adapter before Loom can safely `turn/start` on the
  same visible thread when the provider reports idle.

### 2026-05-17 UTC: Controlled Codex Runtime Rethink

Scope: record the architecture correction before implementing Codex delivery.

Decision:

- Codex delivery is not a frontend terminal-input feature. It belongs behind a
  lead-session provider adapter.
- `loom lead --backend codex` must create the controllable runtime at startup:
  one lead-scoped Codex app-server plus a visible `codex --remote` TUI.
- The backend/controller connects to that app-server and records provider
  runtime metadata on the lead orchestration session.
- The Run Epic button remains a backend state transition first. After the
  assignment is durable, provider delivery uses `turn/start` on the recorded
  Codex thread.
- Busy Codex threads queue delivery. Normal Run Epic does not interrupt active
  provider work.
- Existing raw Codex TUIs are legacy/uncontrolled sessions. They can receive
  assignment context only at fresh startup; already-running raw TUIs should stay
  `pending` or `reconnect required`.

Implementation order:

1. Add a small Codex app-server JSON-RPC client and lead runtime adapter.
2. Change Codex lead terminal startup so the visible terminal attaches through
   `codex --remote`.
3. Persist endpoint, pid, runtime home, provider thread id, and status metadata
   on the lead orchestration session.
4. Wire Run Epic delivery through the adapter and mark
   `lead_assignment_delivered_version` only after `turn/start` succeeds.
5. Keep multi-browser clients on Loom SSE/API state; browsers do not become
   direct Codex controllers.

Gotchas:

- A provider thread id is only reliable if Loom owns the lead runtime from
  startup.
- `thread/list` alone does not subscribe to live status. The observer must call
  `thread/read` for the selected thread.
- Node's built-in WebSocket failed the local app-server handshake during the POC;
  the Go implementation should use the existing `nhooyr.io/websocket`
  dependency.
- Stdio-only app-server mode is not the right default for this UX because the
  backend and TUI both need to attach without collapsing into one controlling
  parent.

### 2026-05-17 UTC: Fresh Slack Two-Epic UI Run

Scope: validate the UI-driven lead-to-epic binding path after a fresh container
reset. This was not a full Codex worker-completion run of the Slack app.

Steps:

- Removed the stale `loom-slack-epic` container, which already contained a
  `SLACK-UI` workspace from an earlier run.
- Confirmed the Slack test container had no named Podman volume. Its only mounts
  were read-only bind mounts for `/root/.codex` and `/usr/local/bin/fleet-db`, so
  recreating the container reset the writable filesystem state.
- Rebuilt the image from the current branch:
  `podman build -f Dockerfile.dev -t loomcli-dev-slack-epic .`
- Started the replacement container on port 8092 with `DEFAULT_BACKEND=codex`
  and the host Codex auth mounted read-only.
- Confirmed the reset baseline with `GET /api/workspaces`, which returned an
  empty workspace list.
- Seeded workspace `Slack_UI` (`SLACK-UI`), two lead agents (`atlas`, `nova`),
  two Slack epics, and three child tasks per epic.
- Drove the UI with a dedicated browser session:
  `agent-browser --session slack-two-epic-reset`.
- Initial UI state on `atlas`: `Open queue · 2 epics · 6 tasks`, with both
  epics showing a `Run` button and each epic showing its three child tasks.
- Clicked `Run` for `SLACK-UI-1` on `atlas`. The UI changed to active-epic mode
  for `atlas`, and the rail showed `atlas - idle - SLACK-UI-1`.
- Switched to `nova`. The UI showed `SLACK-UI-1` as `claimed by atlas` with no
  Run button, and still showed `Run` for `SLACK-UI-2`.
- Clicked `Run` for `SLACK-UI-2` on `nova`. Backend state updated to
  `atlas.parent=SLACK-UI-1` and `nova.parent=SLACK-UI-2`; after a short SSE
  refresh delay the UI showed `nova - idle - SLACK-UI-2`.
- Switched back to `atlas`; the panel still showed only its active
  collaboration epic.

Evidence:

- Initial screenshot: `/tmp/slack-two-epic-before-run.png`
- Final screenshot: `/tmp/slack-two-epic-after-run.png`
- Final backend check:
  `atlas` parent `SLACK-UI-1`, `nova` parent `SLACK-UI-2`.

Gotchas:

- FleetDB ignored the explicit issue IDs supplied in the create payload and
  auto-assigned IDs. The test had to use the returned IDs:
  `SLACK-UI-1` for collaboration and `SLACK-UI-2` for foundation.
- `agent-browser wait --load networkidle` is not usable on this page because
  the app keeps an SSE connection open. Use bounded waits such as
  `agent-browser --session <name> wait 1000`.
- The second Run click updated backend state immediately, but the visible UI
  took a couple seconds to reconcile through SSE. It did not require a reload,
  but the delay is worth watching in automated assertions.
- The onboarding checklist still appears over the agent page and adds noise to
  screenshots. It did not block the Run buttons.

### 2026-05-17 UTC: Correct Runner UI Validation

Scope: validate the implemented shared `internal/epicrunner` path from the UI
after rebuilding the Slack test container from this branch. This run used real
FleetDB-backed workspaces, agents, issues, daemon commands, and daemon-owned
worker sessions. It was not a full Codex completion run of the Slack app.

Steps:

- Removed `loom-slack-epic`, rebuilt `loomcli-dev-slack-epic`, and started the
  replacement container on port 8092.
- Confirmed `/api/workspaces` was empty after reset.
- Seeded workspace `Slack_UI` (`SLACK-UI`) with two lead agents (`atlas`,
  `nova`), two Slack epics, and three child tasks per epic.
- Drove the browser with isolated session
  `agent-browser --session epic-runner-edge`.
- Clicked `Run` for `SLACK-UI-1` before any repo was attached. The UI showed a
  red error toast:
  `workspace SLACK-UI has no repos attached; add or clone a repo before running an epic`.
  This confirmed the route fails before silently binding a lead or pretending
  work has started.
- Created a git repo inside the container at `/tmp/slack-src` and attached it
  to `SLACK-UI` through `POST /api/workspaces/SLACK-UI/repos`.
- Waited for the dev-container daemon watcher to start a daemon for `SLACK-UI`.
- Clicked `Run` for `SLACK-UI-1` on `atlas`. The UI entered active-epic mode,
  showed `atlas - idle - SLACK-UI-1`, and showed worker
  `slack-ui-1-slack-ui-3-6254cd2d` running task `SLACK-UI-3`.
- Switched to `nova` and clicked `Run` for `SLACK-UI-2`. The UI entered
  active-epic mode for the second lead and showed worker
  `slack-ui-2-slack-ui-7-7ae3ddd3` running task `SLACK-UI-7`.
- Checked the issue API: `SLACK-UI-3` and `SLACK-UI-7` were `in_progress` with
  their deterministic worker assignees, while the other four child tasks stayed
  open.
- Re-posted `Run` for active `atlas`/`SLACK-UI-1`; API returned
  `run_state: "already_running"` instead of dispatching duplicate workers.
- Tried assigning `atlas` to `SLACK-UI-2`; API returned 409 with
  `lead atlas is already running epic SLACK-UI-1`.

Evidence:

- No-repo validation screenshot:
  `/tmp/epic-runner-no-repo-error.png`
- First lead/epic dispatch screenshot:
  `/tmp/epic-runner-dispatch-atlas.png`
- Two-lead/two-epic dispatch screenshot:
  `/tmp/epic-runner-two-leads-two-epics.png`

Gotchas:

- The onboarding sidebar still appears on the agent page. In the no-repo case
  it is useful because it points at the same "Create workspace with repo" setup
  requirement, but it still adds visual noise after the workspace has agents and
  epics.
- The lead terminal autostarts Codex and hit a local auth/state issue:
  `failed to initialize state runtime at /root/.codex-rw: ... file is not a database`.
  This did not block backend runner dispatch, but it is separate evidence that
  provider TUI context injection remains unfinished.
- The terminal trust prompt remains visible for each lead. The backend runner
  does not depend on the prompt, but a real conversational lead experience still
  needs provider/app-server integration.
- `agent-browser wait --load networkidle` is still unsuitable because the app
  holds SSE connections open; use bounded waits.

### 2026-05-17 UTC: Confusing Runner UX Fix And Clean Reset

Scope: re-check whether the epic runner works and whether the UI path makes
sense to a user, starting from the current UI state and then resetting the
Slack test container from scratch. This was a UI/runner dispatch validation,
not a full Slack app implementation run.

Problems found in the current UI state:

- A lead assigned to an epic could still show `unknown` in the agent header.
- The active-epic panel said `0 active` even when a live worker was openable
  for a child task.
- The active-epic heading did not make the assigned epic ID obvious.
- The dev container copied host Codex runtime/plugin state into
  `/root/.codex-rw`, causing user-facing terminal noise:
  `file is not a database` and desktop MCP/plugin startup failures.
- Queue count wording used `active`, which became misleading once a worker
  completed but FleetDB still had the task in `in_progress`.

Fixes made:

- Lead/orchestrator headers now hide placeholder branch values like `unknown`.
- Lead headers now show `assigned epic <id>` or `no epic assigned`.
- The active-epic heading says `Assigned epic` for the selected lead's
  assigned epic and shows the epic ID next to the title.
- Task counts now account for live/openable worker terminals, and the count
  label is `in progress` instead of `active`.
- The dev container now mirrors Codex auth/config into `/root/.codex-rw` without
  host runtime SQLite/log files, host MCP server blocks, plugin blocks, or
  desktop notify hooks.

Fresh reset validation:

- Rebuilt `loomcli-dev-slack-epic`, removed `loom-slack-epic`, and started a
  fresh container on port 8092.
- Confirmed `GET /api/workspaces` returned an empty list.
- Created `/tmp/slack-src` as a git repo inside the container and created
  workspace `Slack_UI` (`SLACK-UI`) with that repo attached.
- Seeded lead agents `atlas` and `nova`, two epics, and three child tasks per
  epic.
- Drove the final browser validation with isolated session
  `agent-browser --session epic-ux-label`.
- Initial UI showed `atlas idle · lead · no epic assigned`,
  `Open queue · 2 epics · 6 tasks`, `0 in progress`, and no Codex SQLite or
  MCP/plugin startup warnings.
- Clicking `Run` for `SLACK-UI-1` assigned `atlas` and the UI showed
  `Assigned epic Slack collaboration shell SLACK-UI-1`.
- Switching to `nova` showed `SLACK-UI-1` as claimed by `atlas` and left
  `SLACK-UI-2` runnable.
- Clicking `Run` for `SLACK-UI-2` assigned `nova`; backend state confirmed
  `atlas.parent=SLACK-UI-1` and `nova.parent=SLACK-UI-2`.

Evidence:

- Clean initial screenshot: `/tmp/epic-ux-clean-initial.png`
- Updated count-label screenshot after atlas run: `/tmp/epic-ux-label-atlas.png`
- Final two-lead screenshot after nova run: `/tmp/epic-ux-label-nova.png`

Gotchas:

- FleetDB issue projection can lag just long enough that immediate post-create
  list calls may not include the newest issues. Use returned create payloads or
  bounded waits when seeding automated UI scenarios.
- `localhost` through Podman/gvproxy was briefly flaky from shell scripts; direct
  one-shot `curl` calls to `127.0.0.1:8092` were reliable once the server was
  ready.
- The terminal still shows the normal Codex trust prompt for a new workspace.
  That is expected setup UI; the previous SQLite and MCP/plugin warnings are
  gone.

Post-validation terminal scroll finding:

- Scrolling the selected lead terminal to the bottom after running
  `SLACK-UI-2` showed the normal `loom lead` startup menu and runtime summary,
  not an acknowledgement of the assigned epic. The UI/backend had assigned the
  epic and dispatched workers, but the already-running Codex TUI had not been
  notified.
- This means the runner dispatch path works, but visible lead-session delivery
  is still incomplete for Codex until the app-server adapter records
  delivered/acknowledged assignment versions.
- The monitor agent payload now exposes `delivery_state` for assigned leads.
  The UI shows pending assignments as `waiting for lead` instead of implying
  that the terminal conversation has already picked up the epic.
- Fresh `loom lead` startup now records
  `lead_assignment_delivered_version` on the orchestration session when the
  startup prompt actually includes backend assignment context. Already-running
  Codex TUIs still remain `pending` until Codex app-server delivery lands.
- Rebuilt-container browser validation:
  - `atlas`: clicked Run from an already-starting Codex terminal; UI showed
    `assigned epic SLACK-UI-1 · waiting for lead`, worker dispatch started, and
    the terminal did not contain the specific assignment.
  - `nova`: assigned `SLACK-UI-2` before opening the lead terminal; startup
    prompt contained `## Loom Backend Assignment`, monitor returned
    `delivery_state: delivered`, and the UI refreshed to `sent to lead`.
  - Screenshots captured:
    `/tmp/epic-delivery-v2-before.png`,
    `/tmp/epic-delivery-v2-atlas-pending.png`,
    `/tmp/epic-delivery-v2-nova-delivered.png`.

### 2026-05-17 UTC: Controlled Codex App-Server Delivery Pass

Scope: implement the long-term Codex delivery path and validate it from a clean
Slack two-epic run through the browser UI.

Implemented:

- `loom lead --backend codex` now starts a lead-scoped Codex app-server and
  launches the visible lead TUI with `codex --remote <endpoint>`.
- The lead orchestration session records Codex runtime metadata:
  endpoint, app-server pid, runtime home, isolated `sqlite_home`, provider
  thread id, controlled-runtime flag, and runtime status.
- Run Epic still writes durable Loom assignment/run state first. After that,
  the HTTP path asks the Codex delivery adapter to deliver the assignment with
  `turn/start` on the recorded Codex thread.
- Delivery records `lead_assignment_delivered_version` only after `turn/start`
  succeeds. The UI then shows `sent to lead`; otherwise it remains
  `waiting for lead`.
- Delivery retries for up to two minutes when the controlled runtime is still
  starting or the Codex thread is busy. It does not type into the terminal or
  interrupt active provider work.
- Codex thread discovery now ignores historical threads and waits for a thread
  created after this lead runtime starts. This prevents a second lead in the
  same workspace/cwd from latching onto an older disconnected thread.
- Orchestration-session lookup now ignores completed sessions so reconnects do
  not reuse stale completed lead sessions.

Validation:

- Rebuilt `loomcli-dev-slack-epic`, removed and recreated
  `loom-slack-epic`, then seeded workspace `Slack_UI` (`SLACK-UI`) with repo
  `slack-src`, leads `atlas` and `nova`, two Slack epics, and three tasks per
  epic.
- Drove the UI with isolated browser session
  `agent-browser --session codex-runtime-final2`.
- Opening `atlas` created one `loom lead`, one Codex app-server, and one
  `codex --remote` TUI under
  `/root/.cache/loom/codex-leads/slack-ui/atlas/<session>/`.
- Clicking Run for `SLACK-UI-1` assigned `atlas`, delivered the backend
  assignment into the visible Codex conversation, marked the UI `sent to lead`,
  and dispatched two child workers.
- Opening `nova` created a separate app-server and `codex --remote` TUI under
  `/root/.cache/loom/codex-leads/slack-ui/nova/<session>/`.
- Clicking Run for `SLACK-UI-2` assigned `nova`, delivered the backend
  assignment into the visible Codex conversation, marked the UI `sent to lead`,
  and dispatched two child workers.
- A second observer browser session
  `agent-browser --session codex-runtime-observer` saw the same delivered state
  for `nova` without creating extra lead runtimes.
- Process inspection after the observer check showed exactly one app-server and
  one `codex --remote` process per lead.

Evidence:

- Final `nova` assigned-state screenshot:
  `/tmp/codex-runtime-final2-nova-run.png`
- Final `nova` terminal-bottom screenshot showing the backend assignment and
  Codex acknowledgement:
  `/tmp/codex-runtime-final2-nova-terminal-bottom.png`
- Second-browser observer screenshot:
  `/tmp/codex-runtime-final2-observer-nova.png`
- Metadata snapshot showed different provider thread ids for the two leads:
  `atlas` used `019e3492-525f-7e03-9ab4-9acabe0bbe51`; `nova` used
  `019e3493-8b58-7f32-9509-d0d2ba34b59c`.

Gotchas:

- A first implementation accepted the first historical thread returned by
  `thread/list`. In a same-workspace/two-lead run, `nova` initially latched
  onto `atlas`'s older thread and stayed `waiting for lead` with
  `codex thread is disconnected`. Filtering discovery by lead runtime start
  time fixed this.
- Host-to-Podman localhost access intermittently failed under sandboxed shell
  commands with `Operation not permitted`; running seed/setup API calls through
  `podman exec` against `127.0.0.1:3000` inside the container was reliable.
- `agent-browser wait <milliseconds>` hung once during the run and had to be
  killed. Short bounded waits of 3-20 seconds were reliable.
- Codex app-server still emits a bubblewrap warning in the dev container before
  falling back to the bundled sandbox. This did not block the controlled runtime
  or delivery path.

### 2026-05-17 UTC: TDD Fixes For Valid Runner Issues

Scope: fix only the issues verified as valid from the Slack run transcript.

Implemented:

- `Blocked` now includes both dependency-blocked issues and issues explicitly
  marked `status=blocked` in the FleetDB and API backends.
- API list queries now pass `parent_id`, so child issue queries stay scoped to
  the assigned epic.
- The epic runner now treats "no ready work, blocked children remain, no active
  workers" as a terminal blocked state instead of polling forever with
  `0 ready, 0 blocked, active 0/2`.
- The HTTP Run Epic path reports `run_state: "blocked"` for that terminal
  blocked state and stops its background run lock.
- Lead terminal launch metadata now carries a configured local agent worktree
  as `Launch.Cwd`; PTY startup uses it when present and valid.

Tests added:

- explicit `status=blocked` issues are returned by API and FleetDB blocked
  backends
- runner exits the foreground loop when only blocked child work remains
- configured lead worktrees are used for terminal launch cwd, while missing
  worktrees fall back safely

### 2026-05-18 UTC: Headless Dev Container Runtime Guard

During Podman UI verification, a lead followed the desktop repair guidance and
ran `loom workspace ops ensure-runtime --json` inside the dev container. That
started a Loom.app-style local runtime on a second port, which then started a
second daemon for the same workspace. The UI Run Epic endpoint correctly
rejected the ambiguous control plane with `multiple active daemon nodes found`.

Decision:

- The dev container is a headless/server deployment: it already owns `loom serve`
  and a workspace-daemon watcher.
- In that topology, `workspace ops ensure-runtime` must not start the desktop
  local runtime.
- `LOOM_LOCAL_RUNTIME=disabled` is the explicit deployment switch. When set,
  workspace ops reports `local_runtime.applicable=false`, `ensure-runtime`
  returns the current status without spawning a runtime, and daemon repair
  guidance no longer points agents back at `ensure-runtime`.

Tested at unit level:

- FleetDB plus `LOOM_LOCAL_RUNTIME=disabled` hides missing `runtime.json` as
  not applicable.
- `ensure-runtime` skips local runtime startup when the status says the local
  runtime is not applicable.
- The no-daemon fix text does not suggest `ensure-runtime` in that topology.

### 2026-05-19 UTC: Runtime Mode Selection Guard

Follow-up from hosted VM testing: relying only on deploy scripts to set
`LOOM_LOCAL_RUNTIME=disabled` still allowed a lead terminal to start a second
desktop runtime when the service was running in server/headless mode.

Decision:

- `loom serve` marks itself as `LOOM_LOCAL_RUNTIME=headless` when no runtime
  mode is explicitly configured.
- `loom local service` marks its child environment as
  `LOOM_LOCAL_RUNTIME=desktop`, preserving the macOS app/runtime path.
- Workspace ops treats API-backed mode and external `LOOM_FLEET_DB_URL` mode as
  not desktop-runtime-managed, so missing `runtime.json` does not become a
  repair prompt to start another runtime.
- An explicit `LOOM_LOCAL_RUNTIME=desktop` still wins when a desktop runtime is
  intentionally managing the server.

Tested at unit level:

- Standalone `loom serve` defaults to headless runtime mode.
- Desktop local service child env exports desktop runtime mode.
- Workspace ops suppresses desktop runtime repair in API and external FleetDB
  modes, while explicit desktop mode keeps runtime health visible.

### 2026-05-19 UTC: Desktop App Full Slack Epic Runner Run

Scope: validate the epic runner through the macOS desktop app, not the browser
or Podman UI, using a fresh Slack clone workspace with two epics, three tasks
per epic, and one lead per epic.

Setup:

- Built the desktop app and copied it to
  `/private/tmp/Loom-E2E-20260519b.app`.
- Started the desktop local runtime with data dir
  `/private/tmp/loom-desktop-slack-e2e-20260519b` on
  `http://127.0.0.1:65325`.
- Seeded workspace `Slack_UI` (`SLACK-UI`) with repo `slack-src`, branch
  `Slack_UI`, leads `atlas` and `nova`, two epics, and six child tasks:
  `SLACK-UI-3` through `SLACK-UI-8`.

Desktop UI validation:

- Opened the desktop app Agents view and clicked Run for `SLACK-UI-1` on
  `atlas`, then Run for `SLACK-UI-2` on `nova`.
- The visible lead terminal received backend assignment context and acknowledged
  the assignment before running `loom epic run --parent <epic>`.
- The table/list view grouped tasks under their epic headers, which made the
  six-task Slack run easier to scan than the flat list.
- The removed Talk to Lead button stayed absent from the UI.
- The reduced viewport scale and global 2px font reduction were readable on the
  desktop app window without obvious text overlap.
- Clicking an active ephemeral worker opened a read-only worker summary instead
  of trying to attach a second live terminal to the daemon-owned worker.

Runner result:

- `SLACK-UI-3`, `SLACK-UI-4`, and `SLACK-UI-5` closed under `atlas`.
- `SLACK-UI-6`, `SLACK-UI-7`, and `SLACK-UI-8` closed under `nova`.
- All ephemeral worker agents ended in `state=stopped`,
  `desired_state=stopped`.
- `loom data blocked` returned no blocked work.
- The target repo was clean at the end:
  `git status --short --branch` returned only `## Slack_UI`.
- The target branch contained the expected integration commits, including:
  - `5963788 Resolve merge conflicts: slack-ui-1-slack-ui-5-92656f8a -> Slack_UI`
  - `544998c Resolve merge conflicts: slack-ui-2-slack-ui-8-2ec91e74 -> Slack_UI`
  - `066f700 Resolve merge conflicts: slack-ui-1-slack-ui-3-6254cd2d -> Slack_UI`
  - `fb4d72d Resolve merge conflicts: slack-ui-1-slack-ui-4-f27ffc15 -> Slack_UI`
  - `b230fa1 Resolve merge conflicts: slack-ui-2-slack-ui-6-117a2a96 -> Slack_UI`
  - `ff631d2 Seed Slack workspace data (SLACK-UI-7)`

Important observations:

- Repo-backed workspaces must start the desktop app-data daemon even when there
  are no runnable long-lived auto agents. The desktop run relied on that daemon
  to launch lead and worker sessions.
- Local-only repos with no `origin` must integrate locally instead of failing
  fetch/pull/push. The Slack seed repo intentionally had no remote.
- Parallel workers caused repeated same-file merge conflicts in `src/app.js`,
  `src/styles.css`, and tests. The per-repo push lock serialized integration,
  and the built-in conflict resolver eventually produced clean merge commits
  with build/test checks passing.
- While an integration resolver owns the target repo, the target branch is
  transiently dirty or unmerged. That state is expected, but the UI should grow
  a clearer "integrating" or "resolving merge" state so users do not read it as
  stuck worker execution.
- Parent epics remained `open` after all child tasks closed. That is consistent
  with the current lead-mode rule to ask before closing epics, but the UI should
  make "all child tasks done, epic still open" explicit.
- After the run, reopening the copied test desktop app through Launch Services
  produced a running `loom-desktop` process with no accessible macOS window.
  The earlier desktop window was usable for the run itself. Treat this as a
  desktop window lifecycle gotcha to investigate separately from runner
  correctness.

## Follow-Up Work

- For Codex, do we need item-level timeline events in addition to
  `thread/status/changed`, and which subscription method exposes them reliably?
- Add a deliberate "switch epic" UX after the strict MVP conflict behavior is
  working.
- Reconsider shared app-server processes only after the per-lead implementation
  has multi-workspace and reconnect coverage.
