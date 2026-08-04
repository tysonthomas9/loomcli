# Long-running Orchestrators and Ephemeral Workers — PRD

> **Status:** Partially implemented — phase 1 shipped, phases 2–4 never built,
> phase 5 overtaken. *audited 2026-07-23*
>
> - **Phase 1 shipped**, minus `Agent.OrchestratorSessionID`, which was added
>   and then deliberately removed (tombstone at
>   `internal/domain/agent.go:53-55`).
> - **Phases 2–4 were never built.** There is no `/orchestrators` API, no
>   `OrchestratorsPanel`, no `/swarm` route, no `SwarmPage`, no spawn-worker
>   modal. `grep -ri orchestrators internal/` returns no route or handler.
>   Read the Wireframes and phase 2–4 sections as a proposal, never as a
>   description of the UI.
> - **Phase 5 was overtaken.** The generic runner shipped as `DriverRun`
>   (`internal/domain/platform.go:396`) + `TaskRun` (`:498`) + Flue workflows,
>   with the epic-dag policy realised as
>   `internal/workflows/builtin/epic-runner.ts` — not as
>   `Run`/`RunTask`/`WorkflowStep`/`ActionLedger`, none of which exist. See
>   [docs/design/epic-runner-workflow-architecture.md](../design/epic-runner-workflow-architecture.md).
> - **Still canonical:** the Glossary and Conceptual model sections are the
>   clearest statement of the interactive/worker (role `kind`) vs
>   ephemeral/service (`AgentMode`) two-axis split, and they agree with
>   [docs/loom-glossary.md](../loom-glossary.md).

**Originally written:** 2026-05-07
**Last updated:** 2026-07-23
**Owner:** Tyson
**Related:**
- `docs/product/agent-execution-prd.md`
- `docs/product/agent-lifecycle-state-machine.md`
- `docs/product/agent-run-ux-spec.md`
- `docs/product/daemon-agent-runtime-architecture.md`
- `docs/design/distributed-control-plane.md`
- `docs/design/epic-runner-workflow-architecture.md`
- Aether Orchestrator Wireframes (external design ref)

---

## Summary

Loom currently treats every supervised agent the same: it runs, claims a
task, exits, restarts, claims another. There is no notion of a
*long-running* coordinator that talks to the user, plans work, and
dispatches *short-lived* workers to fix specific tasks.

This PRD proposes a small, additive split:

- **Orchestrator** — an interactive terminal agent for any role with
  `kind=interactive` (the built-in `lead` is the default), registered in
  fleet-db as an `AgentSession{Kind: orchestration}`. Persistent. Talks to the
  user. Spawns workers via the existing `loom agentdef add` CLI.
- **Worker** — a `domain.Agent` with `Mode = ephemeral`, scoped to one
  task by `parent` (epic) or `--task`. Exits cleanly after a single
  successful task cycle instead of looping forever.
- **Linkage** — every worker carries an `OrchestratorSessionID` pointing
  back to the lead session that spawned it, so the UI can group
  "this orchestrator and its workers" without inventing new identity.
  *As built the linkage exists but is not a field on `Agent`; see
  Data model additions → Domain.*

The user-visible result is two new things — **neither of which was built**
(see the Wireframes banner):

1. A **Monitor → Orchestrators** panel that shows each active lead
   session, its sprint context, and the workers it currently coordinates.
2. A **Swarm view** (`/ws/:workspaceId/swarm`) modeled on the Aether
   wireframes — orchestrator chat on the left, worker grid + live
   timeline on the right.

Everything underneath this UX reuses primitives that already exist:
`loom lead`, `loom agentdef`, the daemon reconciler, `AgentCard`, the
Terminal tab, fleet-db's `AgentSession`/`Agent` tables.

---

## Problem

A working session today looks like this:

1. User opens the web UI and switches to **Terminal**. A `loom lead`
   session starts in tmux. They start chatting.
2. The lead AI runs `loom agentdef add bolt --role task --auto`. The
   daemon picks it up within 30s and starts supervising bolt.
3. Bolt loops: claim a task, work on it, finish, claim the next one.
4. The user wants to "spin up a worker just for this one P0 bug." There
   is no way to express "one task only." Bolt will keep working forever
   on whatever's in the queue.
5. The user opens **Monitor**. They see bolt and three other agents in a
   flat list. There is no signal that bolt was spawned by this Terminal
   session, no way to see "the workers Nova is currently coordinating."
6. If the user opens a second Terminal tab and starts a second lead, the
   two leads have no separate identity. New agents created from either
   tab show up in the same flat list.

The infrastructure for the better story is already there — `AgentMode =
ephemeral` is a defined enum value, `AgentSessionKind = orchestration`
exists in the schema, the daemon's restart logic has a clean seam — but
nothing currently *uses* these fields. This PRD makes them load-bearing.

---

## Goals

- A single supervised agent can be **scoped to one task** and exit
  cleanly when done, without looping.
- Every `loom lead` session is a **first-class orchestrator** with a
  stable identity, queryable from the API and visible in the UI.
- Workers spawned from inside a lead session are **automatically linked
  back** to that orchestrator. No flag-typing required.
- The UI surfaces this lineage in two places: an **Orchestrators panel**
  in Monitor (low-touch), and a **Swarm view** (the Aether-style screen
  for users actively driving a swarm).
- The user can **spawn a worker by clicking** ("Spawn worker +") in
  addition to asking the lead AI in chat.
- Multiple concurrent orchestrators (multiple Terminal tabs) work without
  collision, each with their own children.

## Non-Goals

- Building an autonomous orchestrator AI that decides without user input
  how many workers to spawn. The orchestrator stays human-driven (with
  AI assistance) for this slice.
- A new spawn API distinct from `loom agentdef add`. The CLI already
  does the job.
- Replacing `MonitorDashboard` or the existing flat agent list. The new
  surfaces are additive.
- Cleaning up *all* orphaned sessions on crash. Heartbeat + 5-minute
  staleness window is enough for MVP.
- Full DAG/Kanban variants from the Aether deck (directions K, J, L).
  Future work.
- A generic policy/workflow runner, SDK-code workflow authoring,
  service-agent triggers, and automatic delivery/merge workflows. This
  PRD captures the future foundation so the MVP does not paint us into
  a corner, but phases 1–4 below remain the first shippable slice.

---

## Users

| User | Need |
|---|---|
| Solo developer driving 1–2 agents | "Just let me spin up a one-shot worker on this bug from chat or a button, without it sticking around." |
| Lead reviewing a sprint | "Show me everything Nova is coordinating right now and where each task is in its lifecycle." |
| Operator running multiple lead sessions | "When I open a second Terminal, the workers I spawn there should belong to that orchestrator, not the first one." |
| Future autonomous-orchestrator product | "Same data model — when an AI decides to fan out, it can use the same `agentdef add` mechanism the human uses." |

---

## Glossary

| Term | Definition |
|---|---|
| **Orchestrator** | *Category sense.* Any role with `kind=interactive` runs as an interactive terminal/orchestrator agent, persisted as `AgentSession{Kind: orchestration}`. The built-in `lead` is the default; custom ones can cover jobs like PR review. This interactive/worker split is by role `kind` and complements the ephemeral/service `AgentMode` axis. **This category is not a capability grant:** only the two literal role names `lead` and `orchestrator` gain epic ownership (`epicrunner.IsLeadRole`, `internal/epicrunner/start.go:71-79`). See the **Orchestrator** entry in [docs/loom-glossary.md](../loom-glossary.md) for both senses. |
| **Worker** | *Agent-mode sense only, throughout this PRD.* A `domain.Agent` with `Mode = ephemeral` (`internal/domain/control_plane.go:8`). Spawned to handle a specific task and exits when that task completes. This is **not** the role-kind sense (`role.kind=worker`, `internal/domain/role.go:12`), nor the `loom worker` process; the two axes are orthogonal. See the **Worker** entry in [docs/loom-glossary.md](../loom-glossary.md). |
| **Lead session** | The terminal + AI process that backs an orchestrator. Already exists; this PRD just gives it an identity. (This is the "lead session" sense in the glossary's five senses of *session*.) |
| **Service agent** | A `domain.Agent` with `Mode = service` (or no mode). Today's default behavior: loops forever. Unchanged by this PRD. |
| **Sprint context** | A short user-supplied string ("Harden auth + ship API spec") attached to an orchestrator session for display. **NEVER BUILT** — `createLeadSession` writes `Metadata{"actor", "lead_workdir"}` only (`internal/cli/agent/lead/lead.go:344-347`); no `sprint` key is ever written or read. |
| **Spawned-by** | Proposed as an `OrchestratorSessionID` field on `domain.Agent`. **The field was added and then removed** — tombstone at `internal/domain/agent.go:53-55`. Attribution now flows through the `LOOM_ORCHESTRATOR_SESSION_ID` env var and the `AgentCommand` payload; see *Data model additions → Domain*. |

---

## Conceptual model

```
                         ┌─────────────────────────────────┐
                         │ AgentSession (Kind = orch.)     │
                         │ session_id: lead-9f2c           │
                         │ status: running                 │
                         │ heartbeat: 22s ago              │
                         │ metadata: {sprint, terminal_id} │
                         └────────────────┬────────────────┘
                                          │ OrchestratorSessionID
                  ┌───────────────────────┼───────────────────────┐
                  │                       │                       │
                  ▼                       ▼                       ▼
      ┌───────────────────┐   ┌───────────────────┐   ┌───────────────────┐
      │ Agent (ephemeral) │   │ Agent (ephemeral) │   │ Agent (ephemeral) │
      │ name: bolt-auth-3 │   │ name: bolt-auth-8 │   │ name: v4-api-2    │
      │ role: task        │   │ role: task        │   │ role: task        │
      │ parent: auth      │   │ parent: auth      │   │ parent: api       │
      │ task: auth-3      │   │ task: auth-8      │   │ task: api-2       │
      │ state: active     │   │ state: error      │   │ state: review     │
      └───────────────────┘   └───────────────────┘   └───────────────────┘
                  │                       │                       │
                  ▼                       ▼                       ▼
            on success: stop        on error: stay         on success: stop
            (no restart)            for triage             (no restart)
```

Two details of the diagram differ from what shipped. The orchestration
session's metadata is `{actor, lead_workdir}`
(`internal/cli/agent/lead/lead.go:344-347`) — `terminal_id` is a first-class
`AgentSession` field rather than a metadata key, and `sprint` was never built.
The `OrchestratorSessionID` edge is real but is not a column on `Agent`: it is
carried by the `LOOM_ORCHESTRATOR_SESSION_ID` env var and the `AgentCommand`
payload (see *Data model additions → Domain*).

A worker that completes its task **does not restart**. The supervisor
sees `Mode = ephemeral`, a clean exit, and a claimed task, and stops the
supervisor loop (`stopAfterEphemeralTask`,
`internal/cli/daemon/supervisor/restart.go:98-108`). The agent record stays in fleet-db
in `state = stopped` for audit (and so the orchestrator panel can show
"completed: 2" for a few minutes before fading).

A service agent (no mode, or `Mode = service`) keeps the existing
restart-loop behavior. This PRD adds nothing to its lifecycle.

---

## User flows

### F1 — First spawn from chat

1. User opens **Terminal** in the web UI. A `loom lead` session starts.
2. The session registers itself in fleet-db: a new
   `AgentSession{Kind: orchestration, status: running}`. It also writes
   `LOOM_ORCHESTRATOR_SESSION_ID` into its env.
3. The user opens **Monitor**. A new "Orchestrators" panel appears with
   one card: `Terminal #1` with 0 workers.
4. The user types in chat: *"Spin up a worker for auth-3."*
5. The lead AI runs `loom agentdef add bolt-auth-3 --role task --mode ephemeral --auto`.
6. `runAgentAdd` reads `LOOM_ORCHESTRATOR_SESSION_ID` from env and
   stamps it onto the new agent record.
   *As built:* `runAgentAdd` does read the env var, with `--orchestrator`
   overriding it (`internal/cli/agentdef/agentdef_cmd.go:115,130-132`), but it
   is **not** stamped on the agent record — `agentCreateFromFlags`
   (`:152-173`) has no such field. It goes into the queued start command's
   payload as `parent_session_id` (`enqueueAgentAddTaskStart`, `:175-181`) —
   but **only when `--task` is also passed**: `enqueueAgentAddTaskStart`
   returns immediately when `agentAddTask == ""` (`:176-178`). The step-5
   command above has no `--task`, so for that exact invocation the
   orchestrator id is read and then dropped; the spawned agent carries no
   attribution at all.
7. Within 30s the daemon reconciler picks up bolt-auth-3 and starts
   supervising it. Bolt-auth-3 claims auth-3, writes a session, and
   begins working.
8. Monitor refreshes (5s polling). The orchestrator card now shows
   `1 active worker`. The bolt-auth-3 row appears under it with a
   dashed border and an `[E]` badge on the avatar.

### F2 — Click-to-spawn

1. User wants to spawn a worker for `auth-7` without typing.
2. From the orchestrator card or `/swarm` view, click **Spawn worker +**.
3. A modal opens, prefilled with the orchestrator context. The user
   picks a task from a "ready issues" dropdown, accepts the auto-named
   worker, clicks **Spawn ✓**.
4. The webui handler calls the same code path as the CLI
   (`runAgentAdd` server-side), passing the orchestrator's session ID.
5. From this point on the flow is identical to F1 step 7.

### F3 — Worker errors out

1. bolt-auth-8 hits a `TokenMissingError` and exits non-zero.
2. The supervisor classifies it as an error, increments retry count,
   and (because of the existing backoff) sleeps before restart.
3. The worker card transitions to red border, status dot turns red,
   action buttons change to `[debug] [retry]`.
4. The orchestrator card's "errored" count goes from 0 to 1.
5. (Out of scope for MVP: a system message is auto-injected into the
   lead chat so the AI can choose to triage.)

### F4 — Worker completes cleanly

1. v4-api-2 finishes work, commits, pushes, and exits zero.
2. The supervisor checks `Mode == ephemeral` + clean exit + a claimed
   task and stops the supervisor loop. *As built:* `stopAfterEphemeralTask`
   gates on `ap.Entry.Mode == AgentModeEphemeral && ap.LastExitCode == 0 &&
   ap.LastError == nil && ap.AssignedTaskID != ""`
   (`internal/cli/daemon/supervisor/restart.go:98-108`). The proposed
   `LastExitOK` / `LastClaimedTask` fields were never added.
3. The supervisor sets `agent.DesiredState = stopped` and removes
   the agent from supervision.
4. The worker card transitions to green border, `100%`, action button
   becomes `[view diff]`. After 5 minutes it fades from the panel.

### F5 — Multiple concurrent orchestrators

1. User opens a second Terminal tab. A second `loom lead` session
   starts and registers a separate `AgentSession`.
2. The Monitor panel now shows two orchestrator cards stacked vertically.
3. The user spawns a worker from the second tab. It is stamped with
   Terminal #2's session ID and appears under that card only.
4. Closing Terminal #1 finalizes its session (status = completed). Its
   workers are not killed — they keep running but lose the orchestrator
   parent display. After their tasks complete they exit normally.

---

## Wireframes

> **NOT BUILT — read W1–W6 as a proposal, not as a description of the UI.**
> None of these surfaces exist. There is no `OrchestratorsPanel`, no
> `SwarmPage`, no `/swarm` route
> (`internal/webui/frontend/src/router.tsx:87-166` registers kanban, list,
> table, graph, monitor, observability, terminal, agents, prs, settings,
> workspace, files, `issues/:issueId`, `agents/:agentName` and nothing else),
> no spawn-worker modal, and no conditional Swarm icon in the NavRail. The
> word "orchestrators" appears nowhere as a route or handler under
> `internal/`. The nearest shipped surface is the lead-scoped `<WorkerHistory>`
> block inside `AgentWorkPanel`
> (`internal/webui/frontend/src/components/AgentWorkPanel/AgentWorkPanel.tsx:573`).

### W1 — Monitor view with new Orchestrators panel

The new panel sits above the existing **Agent Activity** panel. The
existing panel is unchanged; agents appear there too, just additionally
grouped above.

```
┌─ Monitor ─────────────────────────────────────────────────────────────────┐
│ Project Health     [✓ healthy]    repos 3 · open 47 · blocked 2           │
├───────────────────────────────────────────────────────────────────────────┤
│ ORCHESTRATORS                                                  refresh ⟳ │
│ ┌───────────────────────────────────────────────────────────────────────┐ │
│ │ ●  ORCHESTRATOR  Terminal #1   started 14m  ·  heartbeat 22s ago      │ │
│ │ Sprint: Harden auth + ship API spec                                   │ │
│ │ 3 active · 1 review · 1 errored · 2 completed (last 30m)              │ │
│ │                                                                       │ │
│ │ ┌─────────────────────┬─────────────────────┬─────────────────────┐  │ │
│ │ │[E] bolt-auth-3   ●  │[E] bolt-auth-8   ●  │[E] v4-api-2      ●  │  │ │
│ │ │ Path validator      │ Session iso         │ Terminal state pers │  │ │
│ │ │ working      25%    │ TokenMissingErr 62% │ ready for merge 100%│  │ │
│ │ └─────────────────────┴─────────────────────┴─────────────────────┘  │ │
│ │                                                                       │ │
│ │  [Open chat ↗]   [Spawn worker +]   [Open swarm view ↗]               │ │
│ └───────────────────────────────────────────────────────────────────────┘ │
│                                                                           │
│ ┌───────────────────────────────────────────────────────────────────────┐ │
│ │ ●  ORCHESTRATOR  Terminal #2   started 4m  · heartbeat 3s ago         │ │
│ │ Sprint: (not set — click to add)                                      │ │
│ │ 0 active                                                              │ │
│ │  [Open chat ↗]   [Spawn worker +]                                     │ │
│ └───────────────────────────────────────────────────────────────────────┘ │
│                                                                           │
│ AGENT ACTIVITY (existing — unchanged)                                     │
│  …                                                                        │
└───────────────────────────────────────────────────────────────────────────┘
```

The orchestrator card pulls double duty: a quick overview for users who
mostly stay in Monitor, and an entry point to the Swarm view for users
who want the deeper canvas.

### W2 — Swarm view (`/ws/:workspaceId/swarm`)

Two-column layout. Left: orchestrator chat (embedded TerminalView
attached to the lead session). Right: worker swarm with timeline strip
above grid.

```
┌─ /ws/acme/swarm ────────────────────────────────────────────────────────────────┐
│ NavRail │ ORCHESTRATOR · Terminal #1   │ WORKER SWARM   3 active   spawn worker+│
│  W      ├──────────────────────────────┼─────────────────────────────────────────┤
│  ⊕ ◄ on │ ● lead session  · 14m        │ ┌─ LIVE TIMELINE · last 3m ──────────┐ │
│  ▤      │ Sprint: Harden auth + ship  │ │ auth-3   ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░ working │ │
│  ⌘      │   API spec                   │ │ auth-8   ░░▓▓▓▓▓▓▓▓░░░░░░░░ ⚠ err  │ │
│         │                              │ │ api-2    ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓ ✓ rev  │ │
│         │ ┌──────────────────────────┐ │ │                              now │ │ │
│         │ │ me: ship the auth epic    │ │ └──────────────────────────────────┘ │
│         │ │     by EOD. spin up what  │ │                                      │
│         │ │     you need.             │ │ ┌────────────┬────────────┬────────┐│
│         │ │                           │ │ │[auth-3] ●y │[auth-8] ●r │[api-2] ││
│         │ │ N: Dispatched 3 workers:  │ │ │Path valid. │Session iso │Term st.││
│         │ │  ↳ auth-3 path validator  │ │ │bolt/sse-tok│bolt/ses-iso│v4/term ││
│         │ │  ↳ auth-8 session iso     │ │ │writing tst │TokenMissing│ready mg││
│         │ │  ↳ api-2  terminal state  │ │ │▓▓░░░ 25% E │▓▓▓░░ 62% E │▓▓▓ 100%││
│         │ │                           │ │ │┌─term────┐ │┌─term────┐ │┌─term─┐││
│         │ │ N: ⚠ Bolt hit             │ │ ││›editing.│ ││✗TokenMis│ ││✓42 ok│││
│         │ │   TokenMissingError.      │ │ ││+48/-12  │ ││sess.ts:4│ ││✓merge│││
│         │ │   Reassign or retry?      │ │ ││›writing.│ ││⟳awaiting│ ││ready ││ │
│         │ │  [reassign][retry][debug] │ │ │└─────────┘ │└─────────┘ │└──────┘││
│         │ │                           │ │ │[view diff] │[debug][⌫]  │[merge]││
│         │ └──────────────────────────┘ │ │ click→chat │ click→chat │click→ch││
│         │                              │ └────────────┴────────────┴────────┘│
│         │ ┌─ compose ─────────────────┐ │                                      │
│         │ │ message Nova…           ⏎│ │ ┌─ FOOTER ──────────────────────────┐ │
│         │ └───────────────────────────┘ │ │ workspace: acme · sprint ETA 2h12m │ │
│  ⚙      │                              │ │ 12 tasks queued · 3 ephemeral live │ │
│         │                              │ └────────────────────────────────────┘ │
└─────────┴──────────────────────────────┴────────────────────────────────────────┘
```

The orchestrator chat panel is the existing `TerminalView` attached via
the existing `lead` tmux session. We do **not** build a second chat
component. The "[reassign][retry][debug]" buttons are rendered in chat
by the lead AI as inline action chips (out of scope for MVP — chat
shows raw text in MVP).

The timeline rendering reuses the existing `TaskTimeline` component
from `ObservabilityDashboard`, retargeted to a per-worker timeline
filtered to the orchestrator's children, last 3 minutes window, "now"
marker on the right.

Worker cards reuse `AgentCard` styling: avatar swatch (with task ID
inside instead of agent name letter), status dot, progress bar,
truncated terminal preview pulled from `useAgentLogs`. Click → opens
existing `AgentDetailPanel`.

### W3 — Worker card states

Four canonical states. Border style and action chips change with state.

```
WORKING                          REVIEW (ready to merge)
┌─ ╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴ ┐  E      ┌─ ╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴ ┐  E
╴ [auth-3]   ●yellow   ╴         ╴ [api-2]   ●blue       ╴
╴ Path validator       ╴         ╴ Terminal state pers   ╴
╴ bolt/sse-tokens      ╴         ╴ v4/terminal-state     ╴
╴ writing tests   25%  ╴         ╴ ready for merge 100%  ╴
╴ ▓▓▓░░░░░░░░░░░░░░░   ╴         ╴ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓   ╴
╴ ┌─ terminal ──────┐  ╴         ╴ ┌─ terminal ──────┐   ╴
╴ │› editing val.ts │  ╴         ╴ │› npm test       │   ╴
╴ │+48/-12 · 3 files│  ╴         ╴ │✓ 42 passed      │   ╴
╴ │› writing tests..│  ╴         ╴ │✓ merge ready    │   ╴
╴ └─────────────────┘  ╴         ╴ └─────────────────┘   ╴
╴ [view diff]          ╴         ╴ [view diff] [merge]   ╴
└─ ╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴ ┘         └─ ╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴╴ ┘
   dashed border = ephemeral        green border = ready

ERROR                            DONE / STOPPED
┌──────────────────────┐  E ⚠   ┌──────────────────────┐  E
│ [auth-8]   ●red      │        │ [auth-1]   ●gray     │
│ Session isolation    │        │ Token store init     │
│ bolt/session-iso     │        │ bolt/token-fix       │
│ TokenMissingError 62%│        │ completed       100% │
│ ▓▓▓▓▓▓░░░░░░░░░░░░   │        │ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓  │
│ ┌─ terminal ──────┐  │        │ ┌─ terminal ──────┐  │
│ │✗ TokenMissingErr│  │        │ │✓ committed      │  │
│ │at session.ts:47 │  │        │ │✓ pushed         │  │
│ │⟳ awaiting Nova..│  │        │ │✓ closed auth-1  │  │
│ └─────────────────┘  │        │ └─────────────────┘  │
│ [debug] [retry]      │        │ [view diff]          │
│ click → chat         │        │ fades to 0.6 after 5m│
└──────────────────────┘        └──────────────────────┘
   solid red border 2px            solid gray border, opacity .6
```

A service-mode worker (no `[E]` badge, solid border) is identical to
today's `AgentCard` and is unchanged. Visual difference is what flags
the lifecycle expectation.

### W4 — Spawn worker dialog

Opens from `[Spawn worker +]` on the orchestrator card or swarm view.

```
┌─ Spawn ephemeral worker ──────────────────────────────┐
│ Under: Terminal #1 (lead, started 14m)                │
│                                                       │
│ Task           [ auth-7 — Review Falcon's PR  ⌄ ]     │
│   Filter: ready · unassigned · matches role           │
│                                                       │
│ Role           ( ) plan                               │
│                (●) task     (default for ephemeral)   │
│                ( ) custom    (prompt file)            │
│                                                       │
│ Worker name    [ bolt-auth-7                       ]  │
│   Auto-derived from task ID + role; editable          │
│                                                       │
│ Backend        [ inherit from orchestrator        ⌄ ] │
│                                                       │
│ Mode           [✓] ephemeral (exits after task)       │
│                [ ] service (looping)                  │
│                                                       │
│                                  [ Cancel ] [ Spawn ✓]│
└───────────────────────────────────────────────────────┘
```

Posts to `POST /api/workspaces/{ws}/orchestrators/{id}/workers` with
the form fields. Server-side this calls the same `runAgentAdd` code
path as the CLI, pre-stamping `OrchestratorSessionID`.

### W5 — AgentDetailPanel "Spawned by" link (Info tab)

Existing panel, one new section between **Current task** and **Stats**.

```
┌─ AGENT INFO ─────────────────────────────────────────┐
│                                                      │
│ Name           bolt-auth-3                           │
│ Role           task                                  │
│ Mode           ephemeral [E]                         │
│ State          active                                │
│ Branch         bolt/sse-tokens                       │
│ Repo           my-app                                │
│ Worktree       /loom-acme/my-app/bolt-auth-3         │
│                                                      │
│ Current task   auth-3 — Path validator               │
│                ↗ open issue                          │
│                                                      │
│ ─── Lineage ──────────────────────────────────────── │
│ Spawned by     ● Terminal #1 (lead session)          │
│                14m ago · ↗ open chat                 │
│                                                      │
│ ─── Stats ──────────────────────────────────────────  │
│ +12 / -4 lines  ·  2 files  ·  step 2 / 4            │
│  …                                                   │
└──────────────────────────────────────────────────────┘
```

The lineage line is hidden when `OrchestratorSessionID` is empty (i.e.
for unattached agents created outside a lead session).

### W6 — NavRail update

```
┌───┐
│ W │  Workspaces (kanban + table)
├───┤
│ ⊕ │  Swarm    ← NEW; only shown when ≥1 active orchestrator
├───┤
│ ▤ │  Monitor
├───┤
│ ⌘ │  Terminal
├───┤
│ ⚙ │  Settings (pinned bottom)
└───┘
```

The Swarm icon is conditionally rendered: hidden when there are no
active orchestrators in the workspace, so users without the feature
turned on see the existing nav unchanged.

---

## Information architecture

> **NOT BUILT.** Every surface in this table except *Agent detail panel* is
> part of the unbuilt phase 3–4 UI (see the Wireframes banner).

| Surface | Audience | Density | Refresh |
|---|---|---|---|
| **Monitor → Orchestrators panel** | "I sometimes drive workers" | Low — one card per orch + collapsed worker grid | 5 s polling (existing) |
| **Swarm view** | "I am driving workers right now" | High — chat + grid + timeline | 5 s polling + websocket terminal |
| **Agent detail panel** | "Tell me everything about this agent" | High — existing tabs + new lineage line | On open |
| **Spawn dialog** | "Click to spawn" | Tight modal | None — form |

Naming choice: **Swarm** matches the Aether wireframe and reads well
("worker swarm"). An alternative is **Coordinate** or **Dispatch** —
both feel more enterprise; Swarm is concrete. Locking on Swarm.

---

## Future foundation — policy and workflow runtime

> **Overtaken, not abandoned.** The problem this section frames — a generic
> policy/workflow runner over the same primitives — was solved, but with a
> different vocabulary. `DriverRun` (`internal/domain/platform.go:396`) is the
> run; `TaskRun` (`:498`) is the per-task attempt; the workflow itself is a
> Flue TypeScript program, and the `epic-dag` policy below is
> `internal/workflows/builtin/epic-runner.ts`. None of `Run`, `RunTask`,
> `WorkflowStep`, `ActionLedger`, `WorkArtifact`, `RepoRequirement`,
> `TaskAttemptPlacement` or `WorkerRuntime` exists in `internal/`. The
> separation-of-concerns argument below (task selection vs delivery workflow)
> is the part that survived and is worth reading. See
> [docs/design/epic-runner-workflow-architecture.md](../design/epic-runner-workflow-architecture.md).

The MVP above is intentionally human-driven: a lead session talks to the
user and spawns one-shot workers. The same primitives should also support
a more autonomous runner later, where an orchestrator or policy engine
drives an entire end-to-end workflow.

The key design rule: **task selection and delivery workflow are separate
state machines**.

```
Issue backend      = dependency graph truth
Run store          = orchestration truth
Workflow ledger    = side-effect and delivery truth
Worker runtime     = where code work happens
SCM / CI / issues  = external systems, always reconciled
```

This split lets users choose different delivery behaviors without
changing the DAG runner:

- "Create a PR after the task is complete."
- "Merge the worktree branch directly into main after checks."
- "Create a PR, have a reviewer agent gate it, fix review comments,
  rebase, wait for CI, then merge."
- "Let an on-call or Datadog-monitor agent create incident tasks and
  launch remediation workflows."

### Runner policy vs delivery workflow

A **runner policy** decides which task should run next:

```text
Ready(parent_id) -> choose runnable tasks -> start worker attempts
```

A **delivery workflow** decides what happens after a worker produces
changes:

```text
run_agent -> validate -> create_pr | merge_to_target -> close_issue
```

Closing the issue is the default unblock signal. If a workflow wants
downstream tasks to wait until merge, it closes after merge. If a
workflow wants downstream tasks to start once a PR exists, it closes
after PR creation. The system should not invent a second dependency
unblock mechanism for MVP.

### End-to-end runner flow

The MVP runner should be a reconcile loop over issue state, not a custom
code-graph engine.

User starts a bounded run:

```bash
loom run start \
  --parent auth-epic \
  --branch main \
  --workflow pr-review \
  --max-concurrency 2
```

Control-plane setup:

1. Create a `Run` for the epic.
2. Acquire a run lease so only one controller schedules this epic run.
3. Store the configured branch name, workflow name, and concurrency cap.

Reconcile loop:

1. Query children under the epic.
2. Treat normal issue dependencies as the DAG:
   - blocked task: do nothing
   - open and unblocked task: eligible
   - in-progress/review task: already active or waiting
   - closed task: done and may unblock dependents
3. Start eligible tasks up to the concurrency cap.
4. Reconcile in-flight task attempts and delivery steps.
5. Stop when no ready, blocked, in-flight, or review work remains, or
   when the run is cancelled/timeboxed.

Task attempt flow:

1. Create a `RunTask` attempt for one exact task.
2. Place the attempt on a node/runtime with required tools and repo access.
3. Prepare a node-local worktree from the configured branch name.
4. Start an exact-task worker.
5. Worker edits only its attempt branch/worktree.
6. Worker exits and records branch, logs, diff/test artifacts, and status.

Delivery flow:

1. Run the configured delivery workflow for the attempt branch.
2. For a PR workflow, create/update the PR and move the issue to review.
3. Keep the issue open while the PR is not merged if downstream tasks need
   the code on the configured branch.
4. When the PR is merged or direct push succeeds, close the issue.
5. Existing issue dependency rules clear `blocked` on downstream tasks.
6. The runner's next reconcile loop sees the newly unblocked tasks.

This keeps the first slice small:

```text
issues provide DAG state
run lease prevents duplicate schedulers
task attempt owns one worker
worktree/branch artifacts describe code output
delivery decides when issue closes
closed issue unblocks dependents
```

### Relationship to `loom task --auto --parent`

`loom task --auto --parent <epic>` is already the simplest epic runner:

```text
poll ready tasks under parent
invoke one task agent
agent claims one task
agent implements, commits, pushes, closes
repeat
```

For a sequential "drain this epic" workflow, that may be enough. The
generic runner should not reimplement that loop unless it needs behavior
outside the current auto-mode contract.

What auto mode gives today:

- parent-scoped ready-task polling
- existing blocked/ready issue semantics
- one worktree/agent loop
- task claim through the issue backend
- session/log/diff artifacts from the agent run
- prompt-driven commit, push, and issue close
- max-tasks and idle-timeout controls

What a future runner adds:

- a durable `Run` record for the whole epic, not just per-agent sessions
- a run lease so only one controller owns the epic run
- multiple exact-task workers up to a concurrency cap
- node/runtime placement beyond one local worktree loop
- delivery workflow outside the implementation prompt, such as PR review,
  wait-for-merge, reviewer gates, or direct-push policies
- idempotent side-effect ledger for PR creation, status updates, merges,
  and issue close
- recovery after controller/node death by reconciling run state

Practical migration path:

1. Keep `loom task --auto --parent` as the baseline implementation
   engine.
2. Add `loom run start --parent ...` only when the user needs a durable
   run record, parallel workers, or delivery workflow control.
3. Let the first runner reuse exact-task agent execution and existing
   ready/blocked issue APIs instead of inventing a new DAG engine.

### Shared workflow IR

YAML workflows and SDK-code workflows should compile to the same
internal workflow IR. The executor should not care which authoring
surface produced the IR.

```ts
type WorkflowIR = {
  name: string;
  source: "yaml" | "sdk";
  capabilities: Capability[];
  steps: StepIR[];
};

type StepIR = {
  id: string;
  type:
    | "run_agent"
    | "agent_gate"
    | "run_checks"
    | "create_pr"
    | "set_issue_status"
    | "wait_pr_merged"
    | "update_branch"
    | "wait_checks"
    | "merge_pr"
    | "git_push"
    | "close_issue"
    | "human_approval";
  needs?: string[];
  when?: string;
  with: Record<string, unknown>;
  retry?: RetryPolicy;
  idempotencyKey: string;
};
```

The runtime owns side effects. SDK code should emit IR or typed actions;
it should not call git, GitHub, Datadog, Box, or issue APIs directly.

### YAML authoring

YAML is the right default for common workflows:

```yaml
name: pr-review
capabilities:
  workers: [run_agent]
  scm: [create_pr, wait_pr_merged]
  issues: [update, close]

steps:
  - id: implement
    type: run_agent
    with:
      role: task
      task_id: "{{ task.id }}"
    idempotency_key: "implement:{{ task.id }}"

  - id: create_pr
    type: create_pr
    needs: [implement]
    with:
      branch: "{{ steps.implement.branch }}"
      base: "{{ repo.default_branch }}"
    idempotency_key: "pr:{{ task.id }}"

  - id: mark_review
    type: set_issue_status
    needs: [create_pr]
    with:
      task_id: "{{ task.id }}"
      status: review
      reason: "Review PR {{ steps.create_pr.url }}"
    idempotency_key: "review:{{ task.id }}"

  - id: wait_merged
    type: wait_pr_merged
    needs: [create_pr]
    with:
      pr: "{{ steps.create_pr.number }}"
    idempotency_key: "merged:{{ task.id }}"

  - id: close_issue
    type: close_issue
    needs: [wait_merged]
    with:
      task_id: "{{ task.id }}"
      reason: "Merged {{ steps.create_pr.url }}"
    idempotency_key: "close:{{ task.id }}"
```

### SDK-code authoring

SDK code is useful when workflows need loops, dynamic routing, or
scenario-specific policy. The safe shape is a builder API that emits the
same IR:

```ts
import { defineWorkflow } from "@loom/orchestrator";

export default defineWorkflow({
  name: "reviewed-pr-then-merge",

  async build(ctx) {
    const impl = ctx.step("implement", "run_agent", {
      role: "task",
      taskId: ctx.task.id,
    });

    const pr = ctx.step("create_pr", "create_pr", {
      branch: impl.output("branch"),
      base: "main",
    }).after(impl);

    const review = ctx.step("review", "agent_gate", {
      role: "reviewer",
      pr: pr.output("url"),
      decisionSchema: ["approved", "changes_requested", "rejected", "needs_review"],
    }).after(pr);

    ctx.step("merge", "merge_pr", {
      pr: pr.output("number"),
      strategy: "squash",
    }).after(review).when(review.output("decision").equals("approved"));
  },
});
```

The generated workflow is still validated against the same capability
manifest, branch protections, retry limits, and approval requirements as
YAML.

### Built-in policies

The first policy should be `epic-dag`: query ready children of an epic,
start exact-task workers up to a concurrency cap, and reconcile until no
ready, blocked, or in-flight work remains.

Other policies can reuse the same runtime:

| Policy | Behavior |
|---|---|
| `epic-dag` | Run all ready children under an epic, respecting dependencies. |
| `critical-path` | Prioritize tasks that unblock the most downstream work. |
| `priority-drain` | Drain high-priority ready work across a queue. |
| `two-phase` | Run planning/design tasks first, implementation second. |
| `repo-sharded` | Keep concurrency fair across repos. |
| `risk-aware` | Route risky labels to stronger roles and lower parallelism. |
| `timebox` | Stop after a duration, task count, or budget cap. |
| `gated-dag` | Require approval before risky labels or protected branches. |

### Gatekeeper agents

Gatekeeper agents are workflow steps that return a structured decision.
They should not mutate the repository by default.

```ts
type GateDecision = {
  decision: "approved" | "changes_requested" | "rejected" | "needs_review";
  summary: string;
  findings: Array<{
    severity: "blocker" | "major" | "minor";
    file?: string;
    line?: number;
    message: string;
  }>;
};
```

Useful gatekeeper roles:

- `planner-reviewer` — approves task design before implementation.
- `architect-reviewer` — checks boundaries and abstractions.
- `security-reviewer` — blocks auth, crypto, secrets, or data-risk changes.
- `test-reviewer` — checks coverage and failure modes.
- `merge-steward` — decides whether a PR is safe to merge.
- `release-manager` — gates multi-repo release sequencing.

Review/fix loops must have a max-attempt cap and a deterministic
fallback, usually `needs_review`.

### Service agents

On-call, bug-triage, and Datadog-monitor agents are **service agents**,
not task workers. They watch signals, dedupe events, create or update
issues, and launch workflows.

```yaml
kind: service-agent
name: datadog-monitor
trigger:
  type: webhook
  source: datadog
policy:
  dedupe_by: monitor_id + scope
  cooldown: 15m
workflow:
  - create_incident_issue
  - run_diagnostic_agent
  - add_findings_comment
  - if_safe_remediation_available: request_human_approval
```

Service agents need stronger guardrails than workers:

- event dedupe and replay cursors
- cooldowns and severity routing
- one active service-agent lease per scope
- scoped external credentials
- human approval for destructive remediation

### Worktrees and branching

Worktree + branch should be modeled as an attempt-scoped artifact, not
as the task itself.

```text
run_id:      run-abc
task_id:     auth-3
attempt:     1
repo:        backend
agent_name:  run-abc-auth-3-a1
branch:      run-abc-auth-3-a1
worktree:    <workspace>/worktrees/backend/run-abc-auth-3-a1
base_branch: main
```

The agent works only in its isolated branch/worktree. Even "commit to
main" workflows should still run implementation on an isolated branch;
only the delivery step may merge/push the configured branch.

Persist a work artifact for every repo touched:

```go
type WorkArtifact struct {
    RunID        string
    TaskID       string
    Attempt      int
    Repo         string
    Runtime      string // local-worktree, container, upstash-box
    AgentName    string
    WorktreePath string
    Branch       string
    BaseBranch   string
    PRURL        string
    Status       string // provisioned, running, delivered, merged, cleanup_pending
}
```

Multi-repo tasks produce one artifact per repo. The workflow closes the
issue only after all required repo artifacts reach the configured
delivery state.

### Runtime adapters

Workers should run behind a common runtime interface:

```go
type WorkerRuntime interface {
    StartTask(ctx context.Context, spec TaskAttemptSpec) (*TaskAttemptHandle, error)
    GetStatus(ctx context.Context, handle TaskAttemptHandle) (*TaskAttemptStatus, error)
    Cancel(ctx context.Context, handle TaskAttemptHandle) error
}
```

Initial adapter: local daemon + git worktrees. Later adapters can map
the same spec to containers or Upstash Box:

```text
local-worktree: ensure worktree -> daemon start with task_id
container:      clone/mount repo -> run agent -> collect diff
upstash-box:    create box -> clone repo -> run agent -> create PR
```

The orchestrator should store external refs (`box_id`, `agent_session_id`,
`branch`, `pr_number`) before advancing the workflow.

### Node and machine placement

The workspace owns orchestration truth. A node/machine owns execution
capacity. The runner should never depend on one machine being alive to
remember what work exists or what should happen next.

```
FleetDB workspace
  owns tasks, runs, workflows, agents

Node / machine
  runs daemon, workers, local worktrees, local tools

Run controller
  can run on any eligible node, protected by a run lease

Worker attempt
  is placed onto one node or external runtime
```

The existing `Node` model already represents a machine or runtime slot:

```text
Node {
  node_id
  runtime_provider: local | e2b | kubernetes | ci | other
  labels
  capabilities
  tool_inventory
  capacity
  drain_state
  last_heartbeat
  expires_at
}
```

The local supervisor registers itself as a node with
`runtime_provider = local` and capabilities such as `local-supervisor`
and `agent-process`. Future cloud/container workers can either register
as nodes when they run a Loom daemon, or stay external runtime artifacts
when they are API-backed execution services.

#### Run ownership

A generic runner is workspace-scoped, not machine-scoped. Only one node
may own a run at a time:

```text
Node A acquires run lease
Node A schedules tasks
Node A dies
Lease expires
Node B reconciles the same Run from durable state
Node B resumes, retries, or marks lost attempts
```

The run lease should use fencing so stale controllers cannot continue
writing after another node takes over.

#### Attempt placement

Each task attempt records where it ran:

```go
type TaskAttemptPlacement struct {
    RunID           string
    TaskID          string
    Attempt         int
    RuntimeProvider string // local, container, upstash-box, kubernetes, ci
    NodeID          string // set when execution is owned by a Loom node
    AgentName       string
    RequiredLabels  []string
    Capabilities    []string
}
```

Placement matches:

- runtime provider
- required capabilities (`agent-process`, `git`, `gh`, `docker`, etc.)
- node labels (`macos`, `linux`, `repo:backend`, `large-memory`)
- available capacity
- drain state
- secret/tool availability

Example:

```yaml
placement:
  runtime: local
  requires:
    capabilities: ["agent-process", "git", "gh"]
    labels: ["macos", "repo:backend"]
```

#### Repository preparation and branch

Placement only selects a node that can run the attempt. It does not
prove the node has the right repository state. Before `StartTaskAttempt`,
the runner must execute a `PrepareWorkspace` preflight on the selected
node and persist the result as a work artifact.

An epic should not pre-resolve repository state for all tasks. The simple
rule is:

```text
Blocked dependencies decide when a task is eligible.
Each TaskRun starts from the configured branch name.
```

For the first slice, keep branch handling deliberately boring:

- Default base is the configured branch name, usually the repo default
  branch.
- Downstream tasks remain `blocked` until their blocking tasks are
  `closed`.
- For code-producing tasks, the workflow should not close the task until
  the code is on that branch, for example after PR merge or a
  validated direct push. That makes dependents unblock from existing
  issue rules and start from the configured branch.
- Stacked branches and per-edge code parentage are later workflows, not
  MVP primitives.
- Each retry creates a new attempt from the same configured branch name.

The runner records branch/worktree artifacts and delivery state. The MVP
runner stays branch-name based.

Once a task run starts, its repo requirement is immutable:

```go
type RepoRequirement struct {
    RepoName       string
    CloneURL       string
    Remote         string
    BranchName     string // main, release/1.2
    SparsePaths    []string
    CredentialsRef string
}

type PreparedWorkspaceArtifact struct {
    RunID          string
    TaskID         string
    Attempt        int
    NodeID         string
    RepoName       string
    RepoPath       string // node-local cache path
    WorktreePath   string // node-local disposable attempt path
    Branch         string
    BranchName     string
    RemoteURLHash  string
}
```

Preparation flow:

1. A task becomes eligible when normal issue dependency rules remove its
   `blocked` state.
2. Pick a node from placement constraints. A `repo:<name>` label can
   mean the node has a warm cache, but the runner must still verify the
   repo on disk.
3. Ensure the repo cache exists or clone it using `credentials_ref`.
   Secrets stay in the runtime secret manager; logs store only a URL hash
   or provider repo ID.
4. Fetch the configured branch name.
5. Create a disposable branch/worktree from that branch:
   `git worktree add -b <attempt-branch> <path> <remote>/<branch_name>`.
6. Persist `PreparedWorkspaceArtifact` before starting the worker.

The current local worktree helper creates a worktree from a branch name
and can remain branch-based for the MVP. The generic runner should make
the source branch explicit so a task run does not depend on whatever
branch a node-local checkout happens to have selected.

Delivery controls when dependents unblock because it controls when the
task is closed:

- PR workflow: open or update a PR from the attempt branch to the
  configured branch name.
  Keep the issue blocked/open until the PR is merged when downstream work
  should see the code on that branch.
- Direct-main workflow: re-fetch the configured branch and refuse to push unless
  required checks passed and branch protection permits the write. Close
  the issue only after the push succeeds.
- Multi-repo workflow: prepare every required repo before starting the
  agent, or use a policy that allows partial preparation. The issue is
  not closed until every required repo artifact reaches its delivery
  state.

Preparation failures are retryable only when another node or clone
strategy may satisfy the requirement. If the branch does not exist,
credentials are missing, or the provider refuses fetching the branch, the
attempt should stop as `preflight_failed` rather than letting an agent run
from an unknown branch. If a node dies after creating a node-local
worktree but before pushing a portable branch, the attempt is `lost` and a
new attempt starts from the configured branch name.

#### Node-local vs portable artifacts

Worktree paths are node-local. Branches, PRs, patches, logs, and external
runtime IDs are portable.

```text
node-local:
  worktree_path: /Users/.../worktrees/backend/run-abc-auth-3-a1

portable:
  branch: run-abc-auth-3-a1
  base_branch: main
  pr_url: https://github.com/acme/backend/pull/42
  box_id: box_...
```

If a node dies before pushing a branch or recording a portable artifact,
the attempt should be marked `lost` and retried on a new attempt branch.
If the node already pushed a branch or opened a PR, another node can
resume from those external refs.

#### Service-agent placement

On-call, bug-triage, Datadog-monitor, and other watch-loop agents are
service agents placed on nodes. They should use the same ownership lease
pattern as supervised agents: one active owner per logical service agent
and scope.

```text
Node A owns datadog-monitor
Node A heartbeats ownership lease
Node A dies
Lease expires
Node B acquires ownership and resumes from event cursor
```

Service agents must persist their event cursor, dedupe keys, cooldowns,
and launched workflow IDs in shared storage, not in node-local files.

#### Command shape

The existing daemon command queue can start an agent with
`payload["task_id"]`. A generic runner should eventually use a typed
command with the full placement and idempotency context:

```text
StartTaskAttempt {
  run_id
  task_id
  attempt
  workflow_step_id
  agent_name
  runtime_provider
  placement
  idempotency_key
}
```

This avoids treating "create agent + start with task_id" as the only
dispatch mechanism and gives the scheduler room to place work on local
nodes, containers, cloud boxes, or future runtimes.

### Durable run state and idempotency

The generic runner needs state that is more specific than
`AgentSession`. Add a run/workflow ledger before adding retries:

```go
type Run struct {
    ID             string
    WorkspaceID    string
    Kind           string // epic-dag, priority-drain, service-agent, custom
    TargetID       string // epic id, label query, repo, incident id
    Status         string // running, complete, stuck, failed, cancelled
    PolicyJSON     []byte
    WorkflowName   string
    MaxConcurrency int
    LeaseOwner     string
    LeaseExpiresAt time.Time
}

type RunTask struct {
    RunID     string
    TaskID    string
    Status    string // pending, scheduled, running, delivered, closed, failed
    Attempt   int
    AgentName string
}

type WorkflowStep struct {
    RunID          string
    TaskID         string
    StepID         string
    Type           string
    Status         string
    Attempt        int
    IdempotencyKey string
    InputsJSON     []byte
    OutputsJSON    []byte
    ExternalRefs   map[string]string
}

type ActionLedger struct {
    IdempotencyKey string
    ActionType     string
    Status         string
    ResultJSON     []byte
}
```

Every mutating step must have an idempotency key. If a runner crashes
after creating a PR, replaying the step must find and return the
existing PR rather than creating another.

### Guardrails and risks

| Risk | Required mitigation |
|---|---|
| Duplicate workers after crash/retry | Run lease, exact-task claim, idempotency keys, action ledger. |
| Wrong dependency unblock timing | Only close the issue when the workflow intentionally wants dependents unblocked. |
| SDK code performs unsafe side effects | SDK emits IR; runtime executes typed, capability-checked actions. |
| Direct push to protected branch | Protected-branch policy, required checks, explicit approval gate. |
| Infinite review/fix loop | Max attempts and terminal `needs_review` fallback. |
| External drift (PR exists, CI reran, box deleted) | Persist external refs and reconcile from provider APIs. |
| Cost explosion from fanout | Max concurrency, max attempts, budget caps per run and service agent. |
| Multi-repo partial delivery | One artifact per repo; close only when all required artifacts complete. |
| Noisy service agents | Event dedupe, cooldowns, severity routing, human gates. |

### First concrete slice

The first generic runner slice should be:

```text
epic-dag policy
+ pr-review workflow
+ local daemon worker runtime
+ action ledger/idempotency
```

User command:

```bash
loom run start \
  --parent auth-epic \
  --policy epic-dag \
  --workflow pr-review \
  --max-concurrency 2
```

There is no `loom run` command. The shipped equivalent is:

```bash
loom epic run --parent auth-epic --workflow epic-runner --max-concurrency 2
```

`internal/cli/epic/run.go:74,76,81` — the `--policy` axis collapsed into
`--workflow`, because the policy *is* the workflow.

Acceptance scenario:

```text
A -> B -> C

1. A is ready, B and C are blocked.
2. Runner starts A on an exact-task ephemeral worker.
3. A produces a branch and PR, and A moves to review.
4. A remains review/open until the PR is merged.
5. The workflow closes A after merge.
6. Closing A clears B's blocked state through normal issue dependency rules.
7. B starts from the configured branch name.
8. Runner repeats until C closes and no ready/blocked/in-flight work remains.
```

This proves the full architecture without adding arbitrary SDK code,
auto-merge, direct-main delivery, service agents, or Datadog integration
in the first increment.

---

## Data model additions

### Domain (`internal/domain/agent.go`)

> **Proposed, built, then reversed.** The field below was added and later
> removed. Tombstone at `internal/domain/agent.go:53-55`: "OrchestratorSessionID
> was here historically as a cache of the lead-to-orchestration AgentSession
> join. AgentSession is the single source of truth; use
> `store.OrchestrationSessionIDFor`" (`internal/store/orchestration.go:57`).
>
> Attribution instead flows two ways, neither of them a column on `Agent`:
> the `LOOM_ORCHESTRATOR_SESSION_ID` env var, injected by `loom lead`
> (`internal/cli/agent/lead/lead.go:364`), by the web terminal
> (`internal/webui/handlers/terminal/agent_session.go:441`) and by the
> supervisor (`internal/cli/daemon/supervisor/spawn.go:193`); and the
> `AgentCommand` payload key `parent_session_id`
> (`internal/cli/agentdef/agentdef_cmd.go:175-181`). For epic-runner child
> work it rides as `parentSessionId` on the task-run request
> (`internal/workflows/builtin/epic-runner.ts:87,281`).

Add one field to `Agent`:

```go
OrchestratorSessionID string `json:"orchestrator_session_id,omitempty"`
```

No other domain changes. `Mode` already exists. `AgentSessionKindOrchestration`
already exists.

### Store (`internal/store/store.go` + drivers)

> **NOT BUILT.** There is no `Agents().ListByOrchestrator`. With the
> `OrchestratorSessionID` column gone there is nothing to filter on; the join
> goes the other way, from a lead to its session, via
> `store.OrchestrationSessionIDFor` (`internal/store/orchestration.go:57`).
> The `AgentSessions().List` reuse below is real.

Extend `AgentCreate` and `AgentUpdate` with the new field. Two new
filter methods:

- `Agents().ListByOrchestrator(ctx, workspaceKey, sessionID)` —
  enumerate workers attached to an orchestrator.
- `AgentSessions().List(ctx, workspaceKey, AgentSessionFilter{Kind: Orchestration, Status: Running})` —
  already supported via existing `Filter`. Reuse.

### CLI

`loom agentdef add` reads `LOOM_ORCHESTRATOR_SESSION_ID` from env. New
explicit flag `--orchestrator <session-id>` is also accepted (overrides
env). Empty → unattached agent (today's behavior).

**Shipped**, with one difference: the resolved id is not written to the agent
record — it is put on the queued `start` command's payload as
`parent_session_id` (`internal/cli/agentdef/agentdef_cmd.go:115,130-132,175-181`),
and only when `--task` pins a first cycle.

### Lead

`loom lead` on startup:

1. Generates `session-<uuid>`.
2. Calls `Store.AgentSessions().Create(...)` with
   `Kind = orchestration`, `AgentID = ""`,
   `Metadata = {"sprint": "", "tmux_session": <name>, "started_by_user": <actor>}`.
3. Sets env: `LOOM_ORCHESTRATOR_SESSION_ID=<session-id>`.
4. Starts a heartbeat goroutine: `Update(LastHeartbeat: now)` every 30 s
   while alive.
5. On exit (signal / error / clean): `Update(Status: completed,
   FinishedAt: now)`.

**Shipped**, except the metadata. `createLeadSession`
(`internal/cli/agent/lead/lead.go:334-352`) writes
`Metadata = {"actor": <$USER>, "lead_workdir": <dir>}` and passes the terminal
id as the first-class `TerminalID` field; there is no `sprint` or
`tmux_session` key. Registration is `registerLeadOrchestratorSession`
(`:291`), env injection `:364`, heartbeat `leadHeartbeatInterval = 30 *
time.Second` (`:41`, ticker at `:415`), finalizer `:377`.

### Supervisor

> **Shipped with different field names.** `AgentProcess` never gained
> `LastExitOK` or `LastClaimedTask`. The real branch is `stopAfterEphemeralTask`
> (`internal/cli/daemon/supervisor/restart.go:97-108`):
>
> ```go
> if ap.Entry.Mode != domain.AgentModeEphemeral || ap.LastExitCode != 0 ||
>    ap.LastError != nil || ap.AssignedTaskID == "" {
>     return false
> }
> ```
>
> `AssignedTaskID` is `internal/cli/daemon/supervisor/types.go:41`;
> `StopReasonEphemeralDone` is `types.go:86`; the desired-state stop is in
> `control_plane.go:123`.

`internal/cli/daemon/supervisor/restart.go::shouldRestart` gets one
new branch:

```go
if ap.Entry.Mode == domain.AgentModeEphemeral &&
   ap.LastExitOK && ap.LastClaimedTask != "" {
    s.setStopReasonDefault(ap, StopReasonEphemeralDone)
    s.markDesiredStateStopped(ap)  // so reconciler doesn't re-add
    return false
}
```

`AgentProcess` gains two fields:

```go
LastExitOK      bool
LastClaimedTask string
```

Set after each subprocess exit cycle.

### Webui handlers

> **NOT BUILT.** None of the three routes below exist; there is no
> `/orchestrators` handler anywhere under `internal/`.

Two new endpoints, both workspace-scoped:

| Method | Path | Returns |
|---|---|---|
| GET | `/api/workspaces/{ws}/orchestrators` | `[{session_id, started_at, last_heartbeat, terminal_id, sprint, workers: [LoomAgentStatus...]}]` |
| POST | `/api/workspaces/{ws}/orchestrators/{id}/workers` | `{name, task_id, role, backend, mode}` request → starts a worker; same code as `loom agentdef add` server-side |
| GET | `/api/workspaces/{ws}/orchestrators/{id}/timeline?window=3m` | `[{worker_name, task_id, started_at, status_changes:[{at, status}]}]` for the timeline strip |

`LoomAgentStatus` JSON gains:

```json
{
  "mode": "ephemeral|service",
  "orchestrator_session_id": "session-...",
  "last_claimed_task": "auth-3"
}
```

### Frontend types

> **NOT BUILT.** `src/api/agents/agents.ts` has no `Orchestrator` type and no
> `fetchOrchestrators` / `spawnWorker` / `fetchOrchestratorTimeline`.

`src/api/agents/agents.ts`:

```ts
type Orchestrator = {
  sessionId: string;
  startedAt: string;
  lastHeartbeat: string;
  terminalId?: string;
  sprint?: string;
  workers: LoomAgentStatus[];
  counts: { active: number; review: number; error: number; completed: number };
};
fetchOrchestrators(workspaceId): Promise<Orchestrator[]>;
spawnWorker(workspaceId, sessionId, req): Promise<LoomAgentStatus>;
fetchOrchestratorTimeline(workspaceId, sessionId, windowMin): Promise<TimelineRow[]>;
```

---

## States & lifecycles

### Orchestrator session

```
        loom lead starts
              │
              ▼
   ┌─────────────────────┐
   │ status: running     │  heartbeat every 30s
   │ kind: orchestration │
   └──────────┬──────────┘
              │
   no heartbeat 5m       lead exits cleanly         lead crashed (signal)
   ┌──────────┐         ┌──────────┐               ┌──────────┐
   │  stale   │  ──→    │ completed│               │   lost   │
   └──────────┘         └──────────┘               └──────────┘

(workers are NOT killed when their orchestrator goes away — they finish
 their assigned task on their own lifecycle)
```

### Ephemeral worker

```
   agentdef add --mode ephemeral
              │
              ▼
   reconciler picks up (≤ 30s)
              │
              ▼
   ┌─────────────────────┐
   │ supervised          │ — claims task via existing path
   └──────────┬──────────┘
              │
   ┌──────────┴──────────┐
   │                     │
   clean exit + claim    error / crash
              │                     │
              ▼                     ▼
   ┌─────────────────┐     ┌─────────────────┐
   │ ephemeral_done  │     │ retry per backoff│
   │ DesiredState =  │     │ until max_retries│
   │   stopped       │     │ then ephemeral_  │
   │ (no restart)    │     │ failed (stopped) │
   └─────────────────┘     └─────────────────┘
```

The error path *does* allow retries — ephemeral does not mean "one
attempt." It means "one successful task, then exit." The existing
restart-on-error behavior is preserved.

---

## Visual design tokens (reuse only)

| Token | Source | Use |
|---|---|---|
| Status dot colors | `--color-status-{working,review,done,error,idle}` (AgentCard.tsx) | Worker status dot |
| Avatar color | `getAvatarColor(name)` | Orchestrator avatar |
| Orange accent | `#c96442` (existing) | ORCHESTRATOR badge background |
| Dashed border | new `border-style: dashed` rule on `.agent-card--ephemeral` | Ephemeral indicator |
| Caveat font | already loaded for sketchy mocks | Not used in production UI; production uses existing IBM Plex |
| `[E]` badge | new — small superscript on avatar bottom-right | Ephemeral subscript |

No new fonts, no new color tokens. The visual differentiation is
border-style and a small badge.

---

## Edge cases & error states

1. **Orchestrator with no workers** — card shows "0 active" + dashed
   placeholder ("No workers yet — chat with this orchestrator or click
   Spawn worker to start one.").

2. **Orchestrator goes stale (no heartbeat 5m)** — card stays visible
   but greyed out, with "stale" badge. Workers still run. User can
   dismiss the orchestrator card from the panel (sets status =
   `cancelled`, removes from view; underlying workers untouched).

3. **Worker lost its orchestrator** — if `OrchestratorSessionID` points
   to a non-existent session, the worker still appears in the flat
   Agent Activity panel, just not under any orchestrator card.

4. **Two orchestrators, same worker name collision** — agentdef names
   are workspace-scoped and unique; the daemon already enforces this.
   Lead AI is told this in the prompt so it picks unique worker names
   (e.g., `bolt-auth-3-T1` if `bolt-auth-3` already exists).

5. **User closes the Terminal tab** — the tmux session keeps running
   server-side (this is existing behavior). The orchestrator session
   stays `running`. Reopening the Terminal tab reattaches the same
   tmux + same orchestrator session.

6. **User force-kills the lead process** — heartbeat stops. After 5m
   the session goes stale. Workers continue running independently.

7. **Worker crashes mid-task** — existing supervisor error handling
   kicks in: backoff, retry up to `max_retries`. If `max_retries` is
   exceeded the worker stops with `ephemeral_failed` (a new
   `StopReason`). The card shows red border + "failed" pill +
   `[debug]` button.

8. **Service-mode agent created from inside lead** — gets stamped with
   `OrchestratorSessionID` like an ephemeral worker, but doesn't auto-
   exit on task completion. Renders in the orchestrator card without
   the `[E]` badge — useful for "I want this long-running planner
   under my orchestrator."

---

## Phasing

### Phase 1 — Backend semantics (1–2 days) — SHIPPED

- `Agent.OrchestratorSessionID` field + store update
  — *shipped then reverted; see Data model additions → Domain*
- Supervisor ephemeral-exit branch + `StopReasonEphemeralDone`
  — *shipped, `restart.go:97-108`, `types.go:86`*
- `loom lead` registers/finalizes `AgentSession{Kind: orchestration}`
  — *shipped, `internal/cli/agent/lead/lead.go:291-392`*
- `loom agentdef add` reads env var, stamps field
  — *env read shipped; the id goes onto the start command payload, not the
  agent record (`agentdef_cmd.go:175-181`)*
- Unit tests for ephemeral exit, lead session lifecycle

### Phase 2 — API + status JSON (~1 day) — NOT BUILT

- Extend `LoomAgentStatus` with `mode`, `orchestrator_session_id`
- New endpoints: GET `/orchestrators`, POST `/orchestrators/{id}/workers`,
  GET `/orchestrators/{id}/timeline`
- Heartbeat-staleness logic for stale orchestrators

### Phase 3 — Monitor panel + AgentCard variant (~1 day) — NOT BUILT

- `OrchestratorsPanel` component
- AgentCard ephemeral variant (dashed border + `[E]` badge)
- AgentDetailPanel "Spawned by" lineage line

### Phase 4 — Swarm view + spawn dialog (~2 days) — NOT BUILT

- `/swarm` route + `SwarmPage`
- Embedded TerminalView attached to lead session
- Worker grid with full state machine
- Live timeline strip (reusing `TaskTimeline` retargeted to per-worker)
- Spawn worker modal + form
- NavRail conditional Swarm icon

### Phase 5 — Generic runner vertical slice (future) — OVERTAKEN

Shipped, under other names. `Run`/`RunTask` became `DriverRun`
(`internal/domain/platform.go:396`) and `TaskRun` (`:498`); the `epic-dag`
policy became the `epic-runner` Flue workflow
(`internal/workflows/builtin/epic-runner.ts`); exact-task dispatch with
idempotency became deterministic task-run ids (`epic-runner.ts:626-628`) plus
conflict-tolerant enqueue (`:296-302`). `WorkflowStep` and `ActionLedger` were
never built as types. Details:
[docs/design/epic-runner-workflow-architecture.md](../design/epic-runner-workflow-architecture.md).

- `Run`, `RunTask`, `WorkflowStep`, and `ActionLedger` persistence
- `epic-dag` policy backed by `Ready(parent)` / `Blocked(parent)`
- `pr-review` workflow IR template
- Local worktree worker-runtime adapter
- Exact-task worker dispatch with idempotency keys
- PR creation + review wait + issue close as the dependency-unblock signal
- E2E test: `A -> B -> C` epic where each closed task unblocks the next

Total ~5 days for phases 1–4. Phase 5 is deliberately separate and
should start only after the human-driven orchestrator/worker lifecycle is
shippable; phase 1+2 alone delivers real value (the backend semantics
finally mean something) even without the UI.

---

## Open questions

1. **Spawn-via-button vs spawn-via-chat parity** — should the modal
   accept an arbitrary prompt the AI uses, or always go through the
   built-in `task` role? Recommendation: built-in only for MVP.

2. **Lead AI prompt change** — should the lead system prompt
   explicitly mention the orchestrator concept and how to use `--mode
   ephemeral`? Recommendation: yes, small addition to
   `GenerateLeadPrompt` so the AI knows to prefer ephemeral for
   single-task work. Out of scope for the structural slice; ship the
   prompt change in phase 4.

3. **Cleanup of completed ephemeral agents** — keep the record
   forever, GC after 7 days, or delete on `ephemeral_done`?
   Recommendation: keep + mark `state = stopped`; let observability
   accumulate; never auto-delete.

4. **Cross-orchestrator worker visibility** — when Terminal #1 spawned
   a worker that's still running, should Terminal #2's panel show it?
   Recommendation: no — each panel scopes to its own session. The
   flat Agent Activity panel still shows everything.

5. **Per-orchestrator concurrency cap** — should there be a limit
   on how many simultaneous workers one orchestrator can spawn?
   Recommendation: not in MVP; the existing per-role
   `MaxConcurrency` already enforces an upper bound.

6. **Aether-style "reassign / retry / debug" inline action chips in
   chat** — these require the lead AI to render structured suggestions
   the UI can promote to buttons. Recommendation: out of scope for
   MVP; chat is plain text. Add structured-suggestion protocol later.

---

## Appendix — existing primitives reused

| Primitive | Path | What we reuse |
|---|---|---|
| `loom lead` | `internal/cli/agent/lead/` | The orchestrator chat itself |
| `loom agentdef add/rm` | `internal/cli/agentdef/agentdef_cmd.go` | Spawn/despawn primitive — already supports `--mode ephemeral`, `--parent` |
| Daemon reconciler | `internal/cli/daemon/daemon_reconciler.go` | Picks up new agentdefs in 30 s — works as-is |
| Per-agent supervisor goroutine | `internal/cli/daemon/supervisor/supervisor.go` | One restart-loop branch added; rest unchanged |
| `AgentSession.Kind = orchestration` | `internal/domain/control_plane.go:60` | First real consumer |
| `AgentMode = ephemeral` | `internal/domain/control_plane.go:8` | First real consumer |
| `AgentCard` | `internal/webui/frontend/src/components/AgentCard/` | Worker card; small CSS variant for ephemeral |
| `AgentDetailPanel` | `internal/webui/frontend/src/components/AgentDetailPanel/` | Worker drill-in; one new info line |
| `MonitorDashboard` | `internal/webui/frontend/src/components/MonitorDashboard/` | New `OrchestratorsPanel` slot at top |
| `TerminalView` | `internal/webui/frontend/src/components/TerminalView/` | Embedded in Swarm view's left pane (`components/EmbeddedTerminal/` for embedded use) |
| `TaskTimeline` | `internal/webui/frontend/src/components/ObservabilityDashboard/TaskTimeline.tsx` | Retargeted to per-worker for swarm timeline. As built it renders 24h completion buckets (`HourlyBucket[]`), not per-worker rows, so "retargeted" means rewritten. |
| Status dot tokens | `--color-status-*` CSS vars | Worker status colors |
| `IssueBackend.Ready/Blocked` | `internal/backend/issuebackend.go` | Future DAG runner frontier/stuck-set queries |
| `IssueBackend.Close` | `internal/backend/issuebackend.go` | Future workflow unblock signal after delivery succeeds |
| Daemon agent command queue | `internal/cli/daemon/daemon_command_poller.go` | Future exact-task dispatch via `payload["task_id"]` |
| `GitOps` | `internal/ops/gitops.go` | Future delivery actions: PR, pull/rebase, push/merge, status, diff |
| Worktree resolver | `internal/cli/workspace/worktree_repo.go` | Future attempt-scoped worktree/branch allocation |
| `Node` + node heartbeat | `internal/domain/control_plane.go`, `internal/cli/daemon/supervisor/supervisor.go` | Future machine placement, capacity, drain state, and liveness |
| Agent ownership leases | `internal/domain/control_plane.go` | Future service-agent and run-controller fencing |

The new code is small relative to the codebase. The big win is making
fields that already exist on the schema actually mean something to
users and operators.

---

## Related

- [docs/loom-glossary.md](../loom-glossary.md) — canonical dictionary. This
  doc's Glossary table is the longer prose form of the *orchestrator* /
  *worker* / *ephemeral* / *service* entries; the glossary wins on conflict.
- [docs/product/lead-agent-epic-runner-spec.md](lead-agent-epic-runner-spec.md)
  — canonical for the lead↔epic product rules (one epic per lead, conflict
  outcomes, `agent.parent` as the lock).
- [docs/design/epic-runner-workflow-architecture.md](../design/epic-runner-workflow-architecture.md)
  — the generic runner that phase 5 became.
- [docs/design/lead-runtime-delivery.md](../design/lead-runtime-delivery.md) —
  how work reaches a live orchestrator's conversation.
- [docs/design/epic-runner-lead-control.md](../design/epic-runner-lead-control.md)
  — historical design + validation record for the deleted Go epic runner.
- [docs/design/distributed-control-plane.md](../design/distributed-control-plane.md)
  — the *worker-as-process* sense of "worker".
