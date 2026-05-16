# Epic Runner Lead Control Direction

Date: 2026-05-16
Status: Design direction, needs implementation tests

## Goal

The epic runner should let users keep interacting with a visible lead AI session
while the backend remains the source of truth for which epic the lead should
work on.

The direction is:

- The backend owns lead assignment, run locking, and run state.
- Claude hooks inject that backend state into the lead session at lifecycle
  boundaries.
- The visible Claude TUI remains the human-facing lead conversation.
- PTY injection is only a wake-up mechanism for idle sessions, not the
  authoritative transport for run details.
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
  version
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

### Agent-Browser UI Tests

Use named `agent-browser --session` values to avoid collisions.

- Run Epic button on an idle lead shows `assigned` then `active`.
- Running an already assigned epic shows resume/retry state rather than starting
  a duplicate run.
- Running a different epic on a busy lead shows queued/pending assignment state.
- The lead panel explains that the update will be picked up at the next safe
  point.
- The terminal composer is not polluted by backend state while the lead is busy.

## Open Questions

- Do we use a new `LeadAssignment` table/store, or derive it from `EpicRun` plus
  `AgentSession` for the first implementation?
- Should assignment acknowledgement be explicit, for example
  `assignment_ack_version`, or inferred from hook/run events?
- Which states should allow a hard interrupt rather than a soft hook boundary?
- Should Codex get a parallel adapter with app-server/remote-control hooks, or
  should the first implementation be Claude-only?
