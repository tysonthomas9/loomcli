# Dynamic Workflow Runner Proposal

**Status:** Draft v2 (supersedes v1 of 2026-06-09)
**Date:** 2026-06-09
**Branch:** `feat/dynamic-workflows-v5`
**Related:**
- `docs/design/fleetdb-agent-platform-v2-proposal.md` (branch `codex/pr-57-epic-runner-customizable`)
- `docs/design/native-flue-driver-integration.md` (branch `codex/pr-57-epic-runner-customizable`)
- `docs/design/flue-daytona-runtime-proposal.md` (branch `docs/flue-daytona-runtime-proposal-v5`)
- `docs/product/lead-agent-epic-runner-spec.md` (branch `codex/pr-57-epic-runner-customizable`)
- `docs/design/distributed-control-plane.md`
- fleet-db branch `codex/test6-remaining-platform-proposals` (platform entities, verified implemented; see Appendix A)

## Purpose

Define how Loom runs dynamic workflows: TypeScript programs — epic
runners, trigger workflows, and long-running background agents —
executed by Flue, recorded in FleetDB, and invoked from Loom's CLI and
web UI.

This v2 replaces the v1 draft after source-level verification of Flue
and FleetDB (Appendix A) and two architectural corrections:

1. **Plane assignment.** Loom is the control plane. FleetDB is the data
   plane. Flue is the execution plane. v1 wrongly placed active
   behaviors (cron, dispatch, run lifecycle) in FleetDB and invented a
   fourth component (a standalone claim-loop runtime) that this design
   deletes.
2. **Durability model.** v1 had no durability story; an interim
   proposal added Inngest-style memoized steps. Both are wrong for this
   system's workloads. Durability comes from three existing layers:
   Flue's durable submission store for in-flight agent work, the
   reconciler pattern for orchestration state that already lives in
   FleetDB, and ActionLedger idempotency keys for effectively-once side
   effects. (Step memoization remains a possible later SDK addition; it
   is additive and out of scope.)

## The Three Planes

```text
┌─────────────────────────────────────────────────────────────────┐
│ CONTROL PLANE — Loom (Go: daemon locally, loom server in cloud)  │
│   decides and initiates everything:                              │
│   reconcilers (epic runner wakes), schedule firing (cron),       │
│   trigger routing policy, run lifecycle, event capture,          │
│   supervision of the execution plane, CLI + web UI               │
└──────────────┬──────────────────────────────┬───────────────────┘
               │ writes/queries/watches       │ invokes (push, SDK)
┌──────────────▼───────────────┐  ┌───────────▼───────────────────┐
│ DATA PLANE — FleetDB         │  │ EXECUTION PLANE — Flue server  │
│   never initiates anything:  │  │   purely reactive:             │
│   issues/epics/tasks,        │  │   workflows (one-shot run()),  │
│   Driver/DriverVersion,      │  │   agent instances (persistent  │
│   DriverRun/TaskRun records, │  │   sessions), durable           │
│   TriggerEvent admission,    │  │   submissions + turn journal,  │
│   leases, fencing tokens,    │  │   sandboxes, tools, models     │
│   idempotency, concurrency   │  │                                │
│   invariants, event streams  │  │                                │
└──────────────────────────────┘  └────────────────────────────────┘
```

Rules that keep the planes honest:

- FleetDB has no clock and no background dispatchers. It enforces
  *atomic invariants* next to the data — claim/lease/fencing,
  idempotency dedupe, concurrency admission (`one_active_per_epic`) —
  the way a database enforces constraints, and exposes watch streams.
- Flue executes only when invoked. There are no autonomous loops,
  timers, or daemon agents in the Flue runtime (verified). Anything
  that "wakes up" does so because Loom invoked it.
- Loom is the only component with initiative. All clocks, all routing
  decisions, all run lifecycle management, all supervision.

## Flue Execution Model (verified)

The design leans on five verified properties of Flue:

1. **Single-invocation start.** One SDK call starts an agent or
   workflow: `client.agents.invoke(name, id, {message, session})`
   (HTTP sync or SSE stream), a WebSocket `prompt()`, or
   `client.workflows.connect(name).invoke(payload)`. The invocation
   may be short (a triage decision) or long (an hour-long agentic
   session); per-agent `durability.timeout` tunes the ceiling
   (default 60min).
2. **Instances are persistent state, not processes.** An agent
   instance (`<agent>/<id>`) is named, durable session history in a
   `SessionStore` — no TTL, stable across restarts. "Long-lived agent"
   means an instance whose sessions accumulate over many invocations,
   not a resident process.
3. **Flue already has a durable work queue.** The
   `AgentSubmissionStore` provides claims with 30s leases and
   heartbeat renewal, a per-turn journal with stream-chunk recovery,
   max-10 retries, and recovery of unsettled submissions on restart —
   pluggable to Postgres. Loom does not rebuild any of this.
4. **Built bundles are servers.** `flue build --target node` emits a
   self-contained `dist/server.mjs` (Hono HTTP server + a one-shot IPC
   CLI mode). Nothing is importable; the bundle is invoked by running
   it. Workflows/agents are fixed per bundle at build time.
5. **Gaps Loom must cover:** no cancel API for queued dispatches
   (turn-boundary abort exists on shutdown; closing a streamed
   connection is the caller's lever); direct agent invocations do not
   persist events (workflow runs do, but Node's run store is
   in-memory); `dispatch()` is server-side only, not in the public
   SDK.

Consequence: **the execution plane is just the built Flue server.**
There is no `loom-workflow-runtime` shim, no claim loop, no per-run
subprocess manager. Loom pushes invocations into Flue over the SDK and
owns everything around them.

## Workflow Kinds

| | Epic runner | Trigger workflow | Background agent |
|---|---|---|---|
| Flue primitive | Agent instance per epic (`epic-runner/EPIC-123`) | Workflow run (one invocation per event) | Agent instance per service (`<service>/<id>`) |
| Who drives | Loom reconciler, woken by FleetDB mutation events | Loom trigger router, after FleetDB admission | Loom scheduler (cron fires) and trigger router |
| Invocation shape | Short wake: "advance this epic" — re-query frontier, act, return | One `run(ctx)` per event | Scheduled or routed prompts; an invocation may run long |
| Durable state | Epic/tasks in FleetDB (re-derivable); conversational memory in the instance's sessions | None beyond the run record + ledger | Session history (SessionStore); service record in FleetDB |
| Record in FleetDB | `DriverRun` per wake (cheap, auditable), `TaskRun` per spawned child | `TriggerEvent → TriggerDelivery → DriverRun` | `AgentService` + `DriverRun` per delivered invocation |
| Concurrency | `one_active_per_epic` admission in FleetDB | Per-binding policy (`queue`, `forbid`, `replace`, …) | One prompt per session (Flue) + `max_instances` (FleetDB) |

### Epic runner = reconciler with memory

The epic runner is a level-triggered reconciler, not a long-lived run:

```text
loom daemon watches FleetDB mutations (existing SSE/long-poll)
  on epic-relevant event (task closed, run requested, timer):
    admission: create DriverRun for epic E (one_active_per_epic dedupes)
    invoke flue agent instance epic-runner/E: "advance the epic"
      TS handler: query ready frontier via SDK
                  start TaskRun for each ready child (idempotency key = task id)
                  if frontier empty and children done: close epic (ledger action)
      return summary
    loom records events/usage/result on the DriverRun, completes it
```

Correctness comes from idempotency, not journaling: every action
carries an idempotency key, the frontier is re-derived from FleetDB on
every wake, and a crashed wake is simply re-run. Because each wake
resumes the same Flue instance, the agent also carries conversational
memory of its prior decisions across the whole epic — a reconciler
that remembers *why*.

Child `TaskRun`s are executed by Loom's existing coding-agent
machinery. The seam (TaskRun ↔ supervisor/issue-claim) is design work
for Phase 1 (see Open Decisions #4).

### Trigger workflows

```text
external source → trigger-route ingest → FleetDB admission
  (atomic: TriggerEvent + DriverRun(queued) + TriggerDelivery,
   idempotency by delivery/event id, concurrency by binding policy)
loom observes the queued run (mutation stream) → invokes the bound
  flue workflow with the event payload → streams events → completes run
```

FleetDB's trigger-route endpoint (verified implemented,
`POST /api/v1/{ws}/trigger-routes/{route_key}`) stays the *admission*
point — that is data-plane work because admission must be atomic with
storage. Loom owns routing *policy*: which bindings exist, signature
config, and (cloud mode) optionally fronting the public endpoint.
Retry-on-failed-delivery and replay are Loom behaviors over FleetDB
records.

### Background agents

An `AgentService` (FleetDB record: kind, desired_state, schedule,
max_instances, restart policy) maps to one Flue agent instance. Loom:

- fires schedules (Loom owns the clock; FleetDB stores `schedule_id`
  config) as deterministic events — `schedule:<service>:<fire_time>` —
  through normal admission, so missed ticks are visible records;
- routes bound trigger events to the service;
- delivers each as an invocation to the instance, recorded as a
  `DriverRun`;
- reconciles desired state: `stopped` → stop delivering;
  `running` → resume. There is no resident process to supervise —
  "stopping" a background agent means stopping deliveries.

Long individual invocations (a lead agent working for an hour) are
supported by Flue's durability settings and survive Loom restarts:
Flue's submission store finishes or retries the turn; Loom reconciles
the run record afterward from FleetDB side effects.

## Run Lifecycle and Event Capture

Loom holds the SSE/WebSocket stream for every invocation it starts and
is the system of record for what happened:

```text
loom: create DriverRun (status=queued→running) in FleetDB
loom: invoke flue (stream mode), forward events as they arrive:
        → run log appends (batched) in FleetDB
        → usage capture
        → UI live tail via loom serve (existing SSE patterns)
flue: executes; TS code emits side effects ONLY via SDK → ActionLedger
loom: on terminal event, complete DriverRun (output, summary, error class)
```

Failure semantics are at-least-once with idempotent effects:

- Loom dies mid-stream → the Flue invocation continues and the session
  is saved; on restart Loom finds the orphaned `DriverRun`, reconciles
  from FleetDB side effects (ledger entries, TaskRuns), and either
  marks it complete or re-invokes (idempotency keys absorb the rerun).
- Flue dies mid-turn → its submission store recovers or retries the
  turn on restart; Loom's stream errors and the run is retried per
  policy.
- Cancellation: Loom closes the streamed connection and marks the run
  cancelled; for a wedged invocation the lever is restarting the Flue
  process (local: daemon restarts the child; cloud: instance restart).
  Queued Flue dispatches cannot be cancelled — therefore Loom only
  uses direct streamed invocations, never Flue-side dispatch, so that
  it always owns the connection.

## Authoring, Bundles, and Registration

A workflow project is a **normal Flue project** (per the native Flue
driver integration note — no generated shims, Flue owns build):

```text
workflows/                         # new TS workspace in this repo
  packages/
    workflow-sdk/                  # @loom/workflow-sdk
      src/
        fleet.ts                   # scoped FleetDB client (run-scoped creds)
        actions.ts                 # ActionLedger writes, idempotency-keyed
        tasks.ts                   # TaskRun start/query (lease plumbing hidden)
        context.ts                 # payload/event schemas, run metadata
  examples/
    epic-runner/                   # flue project: agents/epic-runner.ts
    github-triage/                 # flue project: workflows/triage.ts
    standup-reporter/              # flue project: agents/reporter.ts (cron service)
  template/
    app.ts                         # loom app.ts seam: auth middleware, /healthz
```

`app.ts` is the sanctioned extension seam: the bundle template adds
Loom-scoped auth middleware and a health endpoint to the generated
server without forking Flue.

Registration (cloud / production path, verified contract from the
native integration note):

```text
flue build --target node
loom workflow register --flue-dist ./dist --name epic-runner [--activate]
  → stage bundle in content-addressed store, compute digest
  → FleetDB DriverVersion (immutable): runtime=flue-node, server_ref,
    bundle_digest, source provenance, SDK/runtime versions, capabilities
  → --activate flips Driver.active_version_id after validation
```

Same digest → idempotent; new digest → new version; in-flight runs
stay pinned to their version. **Gap:** FleetDB stores only
bundle_ref/digest metadata today — the content store must be built by
generalizing the existing artifact content store
(`PUT /artifacts/{id}/content` + finalize with SHA-256, verified
present).

## Local Mode vs Cloud Mode

Identical contracts, different topology. The control plane is the loom
**daemon** locally and the loom **server** in cloud; the
FleetDB-facing and Flue-facing protocols are byte-identical.

| | **Local mode** | **Cloud mode** |
|---|---|---|
| Control plane | loom daemon | loom server |
| Data plane | embedded fleet-db (already spawned by loom as a localhost HTTP child; URL discoverable from the embedded runtime info) | deployed fleet-db, Postgres backend |
| Execution plane | daemon-spawned, daemon-supervised Flue child process: `flue dev` from source (hot reload, ephemeral dev DriverVersions) or `node dist/server.mjs` | Flue bundle deployed as a service (Node containers, or Flue's native Cloudflare target — per-agent Durable Objects give single-writer instances for free) |
| Workflow code | run from source; registration optional (dev versions auto-stamped) | uploaded immutable bundles, digest-verified, explicit activation |
| Flue stores | in-memory (acceptable: Loom reconciles; dev restarts lose only in-flight turns) | Postgres adapter (later: FleetDB-backed SessionStore/SubmissionStore — the interfaces are small: save/load/delete + submission contract) |
| Trigger ingest | synthetic events: `loom workflow trigger <route> --payload ...` + a test-event panel in the UI; tunnel optional | public ingress with HMAC verification and rate limits (fronted by loom server or fleet-db directly — Open Decision #2) |
| Credentials | local trust | brokered scoped tokens per run/service |
| Isolation | trusted local code | service boundary now; container/Daytona sandboxes for untrusted code later (Flue sandbox connectors) |
| UX | `loom workflow dev` / auto-start with daemon — one command, like the rest of local Loom | normal deploy pipeline |

The local model deliberately mirrors `temporal server start-dev`,
`inngest dev`, and `npx trigger.dev dev`: a dev server that collapses
the topology without changing any contract.

## Invocation Surfaces

### CLI (`loom workflow ...`, Go, thin FleetDB/control-plane clients)

```text
loom workflow dev                          # local: supervise flue-from-source + watch
loom workflow register --flue-dist ./dist --name <n> [--activate]
loom workflow list | show <name> | activate <name>@<ver>
loom workflow run <name> [--epic <id>] [--payload-json ...]
loom workflow runs [<name>] | logs <run-id> [--follow]
loom workflow bind <name> --source github --events issues.opened ...
loom workflow trigger <route-key> --payload-json ...   # synthetic event
loom workflow service create|start|stop|show <name>
```

### Web UI (new Workflows view in loom serve)

- Workflows list: drivers, active version, bindings, last run.
- Run detail: live event tail, child TaskRuns, ledger entries, usage,
  error class.
- Services: desired vs observed state, start/stop, recent invocations.
- Run Epic button on an epic (admission-guarded by
  `one_active_per_epic`; disabled state reflects an active run; shows
  "no execution plane connected" when the Flue endpoint is unhealthy).
- Test-event panel (local): post synthetic payloads to a route.

## Phasing

Each phase ships one workflow kind end-to-end on infrastructure the
next kind reuses.

**Phase 1 — Epic runner (local mode).**
SDK core (`fleet`, `actions`, `tasks`); daemon: Flue child supervision,
mutation-watch reconciler, run lifecycle + event capture; CLI dev/run/
runs/logs; minimal UI (runs list + detail + Run Epic); epic-runner
example; TaskRun↔agent-supervisor seam decided and stubbed end-to-end.
E2E: `TestE2E_EpicRunner_ReconcilerAdvancesAndClosesEpic`, crash-resume
variant (kill loom mid-wake; reconcile completes without duplicate
TaskRuns), `one_active_per_epic` rejection.

**Phase 2 — Trigger workflows + the clock.**
Trigger router (observe admitted runs → invoke), binding management
CLI/UI, synthetic-event tooling, delivery retry/replay policy in Loom,
schedule firing (Loom-owned cron → admission). github-triage example.
E2E: duplicate-delivery dedupe, failed-delivery retry, missed-tick
visibility.

**Phase 3 — Background agents.**
AgentService reconcile (desired state → delivery control), service
CLI/UI, standup-reporter example, long-invocation handling (timeout
tuning, orphan-run reconciliation). E2E: service stop halts
deliveries; loom restart mid-invocation reconciles the run.

**Phase 4 — Cloud hardening.**
Bundle content store (generalize artifact store) + register/activate;
loom server deployment of control plane; Flue service deployment
(Node or Cloudflare target); Postgres Flue stores; public trigger
ingress + HMAC + rate limits; scoped credential broker; sandbox
isolation for untrusted workflow code.

## Open Design Decisions

1. **DriverRun-per-wake granularity (epic runner).** One run per
   reconcile wake is auditable but chatty for busy epics.
   Alternative: one logical run per epic with `DriverStep` per wake.
   Recommendation: run-per-wake in Phase 1 (simpler lifecycle), revisit
   with real volume.
2. **Cloud ingress front.** FleetDB exposes trigger routes directly
   (fewer hops, but data plane is internet-facing) vs loom server
   fronting (control plane owns ingress policy, FleetDB stays
   private). Recommendation: loom server fronts in cloud; local mode
   posts straight to embedded fleet-db.
3. **Dev-mode registration.** Ephemeral auto-stamped dev versions vs
   no registration at all locally. Recommendation: auto-stamp
   (`dev-<ts>`) so run records always pin a version, even in dev.
4. **TaskRun ↔ existing agent machinery.** The supervisor claims
   issues; TaskRuns are control-plane records. Options: (a) daemon
   translates claimed TaskRuns into agent spawns (TaskRun is the
   source of truth); (b) TaskRun creation also creates/claims the
   issue and existing flow is untouched. Must be decided in Phase 1
   design review before the epic runner is real.
5. **FleetDB-backed Flue stores.** Worth building (one storage system)
   vs Postgres adapter forever (exists today). Defer to Phase 4.
6. **Step memoization.** Add `step.run()` journaling on `DriverStep`
   later for genuinely multi-stage pipelines? Additive; revisit when a
   concrete workflow needs it.

## Acceptance Criteria (Phase 1)

- `loom workflow dev` starts (or attaches to) the daemon, spawns the
  Flue child from the example project source, and survives Flue
  crashes (supervised restart, runs retried).
- Run Epic (UI) / `loom workflow run epic-runner --epic E` creates an
  admission-checked `DriverRun`; the reconciler invokes the
  `epic-runner/E` Flue instance; child `TaskRun`s appear; the epic
  closes when exhausted; every side effect went through the
  ActionLedger with an idempotency key.
- A concurrent second run request for the same epic is rejected by
  FleetDB admission, and the UI reflects it.
- Killing loom mid-wake and restarting completes the epic without
  duplicate TaskRuns or duplicate ledger applications.
- Killing the Flue child mid-turn results in a retried wake and an
  eventually-consistent epic; the interrupted run records a clear
  error class.
- All state shown in CLI/UI is read from FleetDB; Loom never queries
  the Flue server for state (only live event streams it captures
  itself).

## Appendix A — Source-Verified Facts (2026-06-09)

| Fact | Where verified |
|---|---|
| Flue bundles are non-importable Hono servers with a one-shot IPC CLI mode; workflow set fixed at build time | `flue/packages/cli/src/lib/build-plugin-node.ts` (generated entry; `FLUE_CLI_TARGET=workflow`, "accepts one invocation only") |
| Flue has no autonomous loops/timers; agents are reactive; instances = persisted sessions (SessionStore, no TTL) | `flue/packages/runtime/src/types.ts:889` (SessionStore), runtime survey |
| Flue durable submissions: 30s leases + heartbeat, turn journal, stream-chunk recovery, 10 retries, 60min timeout, restart recovery; Postgres adapter exists | `flue/packages/runtime/src/agent-execution-store.ts:18-23,117-180`, `flue/packages/postgres/` |
| Flue SDK: `agents.invoke` (sync/SSE), agent WebSocket prompts, `workflows.connect().invoke`, `runs.*`, read-only admin routes; no cancel API; `dispatch()` not in SDK; direct-agent events not persisted; Node run store in-memory | `flue/packages/sdk/src/client.ts:53-114` |
| FleetDB platform entities implemented with tests on the branch: Driver, DriverVersion, TriggerBinding/Event/Delivery, DriverRun (claim/heartbeat/finish/recover-stale), DriverStep, TaskRun (+logs), AgentService, WorkerProfile, ActionLedger | `fleet-db/internal/api/platform.go`, `internal/storage/platform*.go`, registered in `cmd/fleet-db/main.go:488` |
| Trigger-route ingest is `POST /api/v1/{ws}/trigger-routes/{route_key}`; admission inline (event + queued run + dispatched delivery), idempotency-keyed, **not transactional** across the three writes | `fleet-db/internal/api/platform.go:63,1067-1175` |
| No bundle upload/fetch endpoints (metadata only); artifact content store exists to generalize (`PUT .../artifacts/{id}/content` + SHA-256 finalize) | `fleet-db/internal/api/control_plane.go`, `internal/service/artifact_content.go` |
| No cron/scheduler in FleetDB (only archive/retention/compaction sweeps); `AgentService.ScheduleID` is stored config only | repo-wide grep |
| Loom's embedded fleet-db is a spawned localhost HTTP child with a runtime lock + discoverable URL | `loomcli/internal/bootstrap/embedded.go`, `internal/infra/fleetdb/client.go:43` |
