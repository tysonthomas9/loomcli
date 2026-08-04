# Lead Runtime and Assignment/Message Delivery

> **Status:** Current · *audited 2026-07-23*
>
> How a message written by the server or a workflow ends up as a visible turn
> in a lead's live conversation. Supersedes the codex-only account in
> [epic-runner-lead-control.md](epic-runner-lead-control.md), which predates
> both the harness runtime and the durable queue.
>
> **Boundary with the resume contract.**
> [2026-07-22-lead-conversation-resume.md](2026-07-22-lead-conversation-resume.md)
> owns *how a lead's conversation survives a restart* and explicitly defers the
> backend-agnostic shape of the first delivered turn to `LOOMCLI-195`
> (`:201,248`). This doc describes delivery **as built**, not as decided; where
> the two disagree, the resume doc wins.

**Written:** 2026-07-23

## The two halves

**Controlled lead runtime** — loom owns the lead's AI process instead of just
exec'ing it, so something other than the human can put a turn into the
conversation.

**Delivery** — the attempt-then-enqueue path from "server has something to say
to lead X" to "lead X's TUI shows a new turn".

They meet at one place: the lead's **orchestration `AgentSession`**, whose
`Metadata` map holds the runtime's coordinates and its delivery bookkeeping.

## Controlled lead runtime

`loom lead` resolves the agent backend (`cli.GetBackendName()`,
`internal/cli/agent/lead/lead.go:104`), registers an orchestration session
(`:120`), then dispatches. Its only local flags are `--message` and `--prompt`
(`:79-80`); it defines no backend flag of its own, but it inherits the root
persistent `--backend` flag (`internal/cli/root.go:111`), which
`ResolveBackendName` ranks ahead of `LOOM_BACKEND` and the default
(`internal/cli/backend.go:78-92`).

`backends.RunControlledLeadRuntime`
(`internal/cli/backends/harness_lead_runtime.go:41-75`)

| Backend | Runtime | Entry |
|---|---|---|
| `codex` | Codex app-server + a `--remote` TUI | `leadcontrol.RunCodexLeadRuntime` (`internal/leadcontrol/codex_runtime.go:42`) |
| `claude`, `gemini`, `opencode`, `cursor` | `harness-wrapper` PTY supervision | `leadcontrol.RunHarnessLeadRuntime` (`internal/leadcontrol/harness_runtime.go:96`), launch spec `harnessLeadInvocation` (`harness_lead_runtime.go:98-124`) |
| anything else | none — plain interactive launch | `handled == false` → `cli.InvokeAgent` (`lead.go:145-147`) |

`LOOM_LEAD_CONTROLLED=0` opts out entirely and forces the plain launch
(`harness_lead_runtime.go:17,24-31`). `leadcontrol.IsControlledLeadBackend`
(`internal/leadcontrol/delivery.go:97-104`) is the read-only predicate for the
same list and **must be kept in sync by hand** with the launch dispatch — the
code says so at `delivery.go:95-96`.

### Codex: a private app-server

`RunCodexLeadRuntime` (`codex_runtime.go:42-91`):

1. Allocate a per-lead runtime home and sqlite home
   (`codexLeadRuntimeDirs`, `:258`), then a free loopback WS endpoint
   (`freeLoopbackWSEndpoint`, `:296`).
2. Start `codex app-server --listen <endpoint> -c sqlite_home="<dir>"`
   (`startCodexAppServer`, `:111`; the exec line is `:125`), logging to `<runtimeHome>/app-server.log`.
3. Persist `lead_runtime_status = starting` plus endpoint/pid/homes
   (`persistStartingCodexRuntime`, `:96`), wait for the server (`:309`).
4. Resolve a thread to resume (`resolveResumeCodexThread`, `:211`): the
   session's own `codex_provider_thread_id` first, else the newest thread
   recorded for this work dir.
5. Start the visible TUI as `codex [resume] --remote <endpoint>
   --no-alt-screen --dangerously-bypass-approvals-and-sandbox -C <workDir>`
   (`codexTUIArgs`, `:187-201`).
6. Bind the thread id (`bindCodexLeadThread`, `:169`). Discovery only accepts
   threads created **after** this runtime started
   (`newestCodexThread`, `:429-455`) — the fix for two leads in the same
   workspace/cwd latching onto one older disconnected thread.
7. On TUI exit: stop the app server, set `lead_runtime_status = disconnected`.

The endpoint is a fresh socket every launch, so it is ephemeral metadata; the
thread id is the durable resume key. That split is D1 in the resume doc.

### Harness: a supervised PTY

`RunHarnessLeadRuntime` (`harness_runtime.go:96-150`) opens a harness-wrapper
conversation around the backend's normal interactive command, persists
`HarnessRuntimeMetadata` with `lead_runtime_status = starting`, and registers
the live conversation in a **process-local** registry
(`registerLeadConversation`, `internal/leadcontrol/harness_delivery.go:208`).
It then attaches the PTY to stdout, forwards stdin and terminal resizes, and
starts a watcher that keeps `lead_runtime_status` current.

For `claude` the session id is pinned at launch with `--session-id <uuid>`
(`harness_lead_runtime.go:100-108`), so the transcript location is knowable
from boot rather than scraped off the TUI. `lead_harness_started_at`
(`internal/leadcontrol/harness_metadata.go:24-30`) exists because claude
*rotates* that id on a first boot that clears the folder-trust dialog: only
transcripts recorded after that instant can belong to this runtime.

**The registry is the harness runtime's hard limit.** harness-wrapper has no
cross-process PTY attach, so only the process running the lead runtime can
inject a turn (`harness_delivery.go:199-206`). Every other process misses the
lookup and gets
`"lead runtime is owned by another process; message queued for in-runtime delivery"`
(`harness_delivery.go:34`), i.e. delivery stays enqueue-only and the runtime's
own drain loop picks it up.

### Runtime metadata keys

All on the lead's orchestration `AgentSession.Metadata`.

| Key | Meaning | Defined |
|---|---|---|
| `lead_runtime_provider` | backend name; absent ⇒ codex, for pre-provider sessions | `codex_metadata.go:15` |
| `lead_runtime_controlled` | this session is a controlled runtime | `:16` |
| `lead_runtime_status` | `starting` \| `idle` \| `active` \| `waiting_on_approval` \| `waiting_on_user_input` \| `disconnected` \| `failed` | `:17`, values `:32-38` |
| `codex_app_server_endpoint` / `_pid` / `codex_runtime_home` / `codex_sqlite_home` | ephemeral codex coordinates | `:19-22` |
| `codex_provider_thread_id` | durable codex resume key | `:23` |
| `lead_harness_name` / `_chat_session_id` / `_session_id` / `_pid` / `_started_at` | harness equivalents | `harness_metadata.go:20-30` |
| `lead_assignment_delivered_version` / `_epic` / `_error` / `_attempted_at` | assignment delivery bookkeeping | `codex_metadata.go:24-27` |
| `lead_assignment_acknowledged_version` | ack, distinct from delivered | `:28` |
| `lead_message_delivery_attempted_at` / `_error` | free-message bookkeeping | `:29-30` |

`status_version` and `controller_lease_id`, proposed in the 2026-05 design,
were never built.

## Delivery

### Attempt, then enqueue

Callers do **not** retry in-line. One attempt runs synchronously; anything
short of terminal enqueues durably and the server retries.

```text
caller (workflow op / task event / CLI)
   │
   ├─ 1 inline attempt ──► deliverer ──► delivered? done.
   │
   └─ not delivered/unsupported ──► durable row ──► server retries
```

Two durable layers, and they are different things:

- **Outbox** (`domain.OutboxRecord`, `internal/domain/outbox.go:105`) — the
  *server-side notification queue*. Kinds `leadAssignment` and
  `leadTaskMessage` (`outbox.go:73,77`); statuses `pending` | `delivered` |
  `unsupported` | `failed` (`:84-97`). `DedupeKey` makes `Create` idempotent.
- **Agent inbox** (`domain.AgentInboxMessage`,
  `internal/domain/control_plane.go:235`) — the *per-agent message queue* the
  turn is actually taken from, with a claim lease. Enqueued via
  `agentinbox.Enqueue` (`internal/agentinbox/message.go:29`); record shape and
  provenance fields are documented in
  [docs/product/agent-messaging-and-backpressure.md](../product/agent-messaging-and-backpressure.md).

An outbox row's delivery attempt normally *creates* an inbox row. The
dispatcher treats "an inbox message id came back" as success and stops
retrying the outbox row (`attemptDelivery`,
`internal/driver/outbox_dispatcher.go:206-213`) — the inbox owns it from there.

### Producers

| Producer | Creates | Code |
|---|---|---|
| epic-runner workflow, once per run | outbox `leadAssignment` | op `deliver-lead-assignment` → `internal/webui/handlers/driverapi/module.go:440-481`; workflow side `internal/workflows/builtin/epic-runner.ts:571-588` |
| terminal task-run transition under a lead-bound epic | outbox `leadTaskMessage` | `createLeadTaskOutbox`, `internal/driver/task_events.go:115-141` |
| direct calls (CLI, driver op `deliver-agent-message`) | inbox row directly | `driver.DeliverAgentMessageForDriver`, `internal/driver/outbox_dispatcher.go:42` |

`deliverLeadAssignment` is the clearest example of the pattern: it calls
`leadcontrol.DeliverCurrentAssignment` once, and creates the outbox row only
when the state is neither `delivered` nor `unsupported`, deduped on
`"lead-assignment:" + runID + ":" + leadName` (`module.go:456-479`).

### The dispatcher

`driver.OutboxDispatcher` (`internal/driver/outbox_dispatcher.go:134`) is
always-on server policy — it runs whenever `loom serve` has a store, and is
**not** gated behind `LOOM_DRIVER_EXECUTOR`
(`startOutboxDispatcher`, `internal/cli/serve/serve_loops.go:63-87`, 2s tick).
Per pass it lists due rows per workspace (batch 50,
`defaultOutboxBatchLimit`, `outbox_dispatcher.go:118`), attempts delivery, and
writes the outcome:

- delivered, or state `queued`, or any inbox message id → `delivered`
- `unsupported` → terminal `unsupported`, never retried
- anything else → `pending` with capped exponential backoff,
  `2^attempt` seconds up to 30s (`outboxRetryDelay`, `:281-294`)

Unknown kinds are terminal rather than retried forever (`:258-262`).

### Deliverers

`DeliverCurrentAssignment` (`internal/leadcontrol/delivery.go:106`),
`DeliverLeadMessageWithOptions` (`:162`) and `DeliverPendingLeadMessages`
(`:244`) share one shape:

1. Resolve the lead's orchestration session. No session → enqueue and return
   `pending` with `"lead has no orchestration session"`.
2. `delivererForSession` (`:83-92`) picks the strategy from
   `lead_runtime_provider`. Absent or `codex` → `codexTurnDeliverer`;
   anything else → `harnessTurnDeliverer`. The seam is the unexported
   `leadTurnDeliverer` interface (`:61-77`) — nine methods, delivery only.
   Runtime lifecycle (start/resume/close) is deliberately *not* behind it.
3. For assignments only: if the session's
   `lead_assignment_delivered_version` already equals the assignment version,
   return `delivered` without doing anything (`:127-131`).
4. Enqueue the inbox row, then gate on runtime readiness.
5. `deliverNextLeadInboxMessage` (`:280`) claims the next queued row for this
   lead+session with a **2-minute claim lease** (`:298`) and calls
   `deliverTurn`.
6. Delivered → `Complete{Outcome: "delivered", DeliveredThreadID}`, and for an
   assignment also `MarkAssignmentDelivered` (`:353-373`). Not delivered →
   `Complete{Outcome: "retry", ErrorClass: "<provider>_delivery_pending"}`,
   which returns the row to the queue (`:325-348`).

### The four states

`DeliveryState` (`internal/leadcontrol/delivery.go:19-23`):

| State | Meaning |
|---|---|
| `none` | nothing to deliver (no assignment, or the queue is empty) |
| `pending` | queued; the runtime was absent, starting, disconnected, or busy |
| `delivered` | a turn was started in the live conversation |
| `unsupported` | this session can never accept a turn — **terminal** |

`unsupported` is the one that stops retrying, so the two deliverers are
careful about it. Codex returns it when the session has runtime metadata that
is not a controlled codex runtime (`codex_delivery.go:53-58`); harness returns
it when `lead_runtime_controlled` is false (`harness_delivery.go:256-261`).
Everything transient — `starting`, `disconnected`, `failed`, a busy thread, a
registry miss — is `pending`.

### Codex turn injection

`deliverCodexLeadTurn` (`internal/leadcontrol/codex_delivery.go:92-131`),
30s budget: dial the persisted endpoint, `ReadThread`, refresh
`lead_runtime_status` from the thread status, and only then `StartTurn`. If
`thread.Status.CanStartTurn()` is false the result is `pending` with
`"codex thread is <status>"` — a busy lead is queued behind, never interrupted.
Because delivery dials a socket, **any** process can deliver to a codex lead.

### Harness turn injection

`harnessTurnDeliverer.deliverTurn` (`harness_delivery.go:287`) looks the
session up in the process-local registry; a miss is `pending` with the
registry-miss reason. On a hit it must be far more careful than codex, because
it is typing into a TUI:

- 5s budget to acquire the chat-layer control token
  (`harnessAcquireControlTimeout`, `:21`).
- The PTY must be quiet for 2s before injection (3s for the generic adapter),
  so delivered text does not interleave with streaming output or a
  half-typed human message; keystroke echo resets the clock
  (`harnessOutputQuietWindow` / `harnessGenericQuietWindow`, `:25,28`).
- An in-flight turn quiet for 90s is treated as finished, so a missed
  turn-completion marker cannot wedge delivery forever
  (`harnessInFlightOverrideWindow`, `:32`).
- `leadConversationHandle.stagedText` (`:42-55`) remembers text already typed
  into the composer by a send whose submit never fired, so a retry of the
  *same* message does not paste a second copy.

### The drain loop

Both runtimes start `drainLeadMessageQueue`
(`internal/leadcontrol/delivery.go:380`) as a goroutine —
`codex_runtime.go:80`, `harness_runtime.go:137` — ticking every 2s
(`leadMessageDrainInterval`, `delivery.go:53`) and calling
`DeliverPendingLeadMessages`. This is what closes the harness gap: messages
enqueued by *any* process land in the visible session as soon as the owning
runtime is ready.

## Known sharp edges

- **`IsControlledLeadBackend` is a hand-maintained duplicate** of the launch
  dispatch. Adding a backend to one and not the other silently mis-reports
  whether a lead can receive messages (`delivery.go:95-104` vs
  `harness_lead_runtime.go:53-61`).
- **`lead_assignment_delivered_version` is derived from `lead.UpdatedAt`**
  (`internal/epicrunner/assignment_context.go:49-52`), not an explicit
  counter. Any unrelated write to the lead agent changes the version and makes
  an already-delivered assignment look new.
- **Delivery failure is not user-visible.** Reasons land in
  `lead_assignment_delivery_error` / `lead_message_delivery_error` and nowhere
  else; the resume doc records this as an open question
  (`2026-07-22-lead-conversation-resume.md:579-597`, `LOOMCLI-197`).

## Related

- [2026-07-22-lead-conversation-resume.md](2026-07-22-lead-conversation-resume.md)
  — the resume contract; owns which pointer is durable and why.
- [2026-07-22-lead-resume-defects.md](2026-07-22-lead-resume-defects.md) —
  pre-existing bugs found while writing that contract.
- [epic-runner-workflow-architecture.md](epic-runner-workflow-architecture.md)
  — the workflow that produces most assignment deliveries.
- [docs/product/agent-messaging-and-backpressure.md](../product/agent-messaging-and-backpressure.md)
  — the `AgentInboxMessage` record itself, and what `internal/notify` is not.
- [epic-runner-lead-control.md](epic-runner-lead-control.md) — the 2026-05
  design this replaces (historical).
- [docs/loom-glossary.md](../loom-glossary.md) — *agent inbox*, *outbox*,
  *harness*, *session* (five senses).
