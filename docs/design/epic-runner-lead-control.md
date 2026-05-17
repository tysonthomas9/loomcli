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
- Attach the human's real Codex TUI with `codex --remote <server>`.
- Attach the Loom backend as a second websocket client.
- Subscribe the backend to the lead thread with `thread/read`.
- Track idle/busy from `thread/status/changed`.
- Start backend-assigned work with `turn/start` on the same thread only when the
  backend state says the lead is idle, or queue the assignment when active.

This keeps the Codex TUI as the human UI. Loom does not need to build a full
replacement chat UI before it can observe and control a lead session.

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

## Follow-Up Work

- For Codex, do we need item-level timeline events in addition to
  `thread/status/changed`, and which subscription method exposes them reliably?
- Add a deliberate "switch epic" UX after the strict MVP conflict behavior is
  working.
- Reconsider shared app-server processes only after the per-lead implementation
  has multi-workspace and reconnect coverage.
