# Unified Agent Platform: the Workflow Plane Supersedes the Role Plane

**Status:** Proposal (v2 — reframes v1's "map workflow agents into role-agent
views"; the v1 UI work survives unchanged as Phases 1–3, but as the primary
agent surface rather than a bridge between two permanent planes)
**Date:** 2026-07-01
**Related:** `docs/design/create-agent-redesign.md`,
`docs/design/2026-06-07-trigger-workflow-proposal.md`,
`docs/design/2026-06-07-agent-service-driver-version-proposal.md`,
`docs/design/agent-run-visibility-plan.md`,
`docs/design/fleetdb-agent-platform-v2-proposal.md`,
`docs/design/workflow-driver-authoring-guide.md`

## Summary

Loom has two agent subsystems: **role agents** (daemon-supervised processes
driven by a prompt + task filter) and **workflow agents** (trigger-driven
TypeScript drivers). v1 of this proposal unified them at the presentation
layer. v2 goes further, because the platform already supports it: **the
workflow plane supersedes the role plane.** A role agent is the generic
task-runner driver *configured with a prompt* — prompt is data, not code —
triggered by internal task events instead of a poll loop, or supervised as a
long-running `AgentService` when interactive.

This is convergence, not invention: Phase U ("execution-leaf unification") has
already shipped a shared transcript vocabulary and a flag-gated path where the
daemon's execution leaf delegates to the same bundled TS task-runner the
driver plane uses. This proposal sequences the UX and packaging work to land
on that trajectory, so every screen we build is for the end-state plane.

End state: one agent concept = **driver (behavior) + trigger (binding or
desired-state service) + permissions (grants/budget)**. "Role" stops being an
execution plane and becomes a *behavior configuration record*.

## Evidence the platform is already converging

All verified in-tree 2026-07-01:

1. **Shared execution leaf (Phase U, flag-gated).** With `LOOM_DAEMON_LEAF=ts`
   the daemon's execution leaf runs the agent via the bundled TypeScript
   task-runner — "the same runner the driver host-bridge uses… so both planes
   share ONE execution + telemetry path. The Go daemon SUPERVISOR is
   untouched" (`internal/cli/agent/tsruntime/tsruntime.go:22-29`). Default-off
   today.
2. **Prompt is already data.** `local-task-runner.ts` composes its backend CLI
   invocation from a prompt with explicit precedence: `LOOM_TASK_RUN_PROMPT`
   (the daemon leaf's exact composed role prompt) > `input.taskPrompt` (custom
   workflows pass the prompt as workflow input) > the runner's generic prompt.
   In-source: *"prompt = data, brain stays custom"*
   (`internal/workflows/builtin/local-task-runner.ts:125-140`).
3. **Shared telemetry vocabulary (Phase U/U0).** Transcript event types are
   blessed in one place "so both execution planes share ONE event vocabulary"
   (`internal/sessions/transcript/event.go:36`).
4. **Task-runner drivers are builtins already**: `local-task-runner`,
   `daytona-task-runner`, `openshell-task-runner`, `github-review-task-runner`
   (`internal/workflows/workflows.go`, `internal/workflows/builtin/`). The
   runner execFiles the real backend CLIs (claude/codex/opencode/gemini/
   cursor) over a prepared worktree and fails closed — no synthetic completes.
5. **Internal events reach the trigger plane.** The issue journal bridge emits
   `issue.created` / `issue.closed` style trigger events
   (`internal/trigger/issue_journal_bridge*`), and `source_kind=internal`
   bindings with normalized event types exist
   (`internal/trigger/internal_source.go:101`). Event-driven task pickup is
   plumbing, not research.
6. **`AgentService` models the long-running case.** Desired-state services
   with `RoleName`, `MaxInstances`, `RestartPolicy`, `BudgetPolicy`,
   `Permissions`, `EventSources` (`internal/domain/platform.go:147-168`), and
   the companion proposal points services at immutable DriverVersions
   (`2026-06-07-agent-service-driver-version-proposal.md`).
7. **Concurrency/lease semantics live on the driver plane**:
   `TriggerBindingConcurrencyPolicy`, and the run executor's claim / lease /
   fencing / heartbeat / stale-recovery (`internal/driver/executor.go`) — a
   superset of the supervisor's ownership + slot model.

## End-state model

One plane. Every agent is:

| Axis | Options |
|---|---|
| **Behavior** | generic task-runner driver configured with a prompt (**prompt agent** — today's role) · custom TS driver, versioned + approved (**scripted agent** — today's workflow) |
| **Trigger** | internal event binding (e.g. task-ready — replaces the 30s poll) · cron binding · webhook/event binding · `AgentService` desired-state (long-running / interactive) |
| **Permissions** | connector grants · budget policy · tool allow/deny carried in behavior config |

Consequences worth stating explicitly:

- **Editing a prompt agent's prompt stays a textarea.** The prompt is
  configuration on the binding/service, not TypeScript. Only scripted agents
  are edited as TS (`WorkflowSourceModal`). The v1 worry "edit the prompt
  means editing TS" dissolves.
- **Event-driven task pickup is an upgrade, not a compromise.** Binding on
  task-ready events gives immediate dispatch vs today's 30s poll; cron's
  1-minute floor never enters the picture.
- **The `Role` record survives as the behavior-config object** referenced by
  services/bindings (`AgentService.RoleName` already models exactly this).
  What gets superseded is the role *subsystem* — the supervisor poll loop and
  the Go-only execution leaf — not the role *data*.
- **Runs become the activity record for every agent.** A role agent's task
  execution is a `DriverRun` with a transcript (U0 vocabulary), the same as a
  workflow fire.

## What supersede requires (delta beyond what exists)

A. **Prompt-agent packaging.** Creating a "role-shaped" agent produces a
   binding (background worker) or AgentService (interactive) whose config
   carries the role behavior (prompt, model, backend, task filter, tools),
   dispatched through the existing task-runner via `input.taskPrompt` /
   `LOOM_TASK_RUN_PROMPT`. No new runtime.
B. **Task-ready internal events.** Extend the issue-journal bridge (or task
   store outbox) to emit a task-ready event; a prompt-agent binding matches it
   and the run claims the task. Claim races are settled by the existing task
   lease, exactly as concurrent supervisors are today.
C. **AgentService controller maturity** for interactive/lead agents (spawn,
   restart policy, PTY attach). Owned by the AgentService proposal; this doc
   only sequences against it.
D. **Migration path.** (1) `LOOM_DAEMON_LEAF=ts` default-on — same supervisor,
   shared leaf, lowest risk. (2) New background roles created as prompt-agent
   bindings. (3) Migrate builtin `plan`/`task` roles. (4) Lead/interactive to
   AgentService. (5) Retire the supervisor.
E. **Observability parity.** The run transcript/stream replaces the PTY for
   background work; a live PTY remains only where a live interactive process
   exists (AgentService agents).

## UX: one surface, built once (v1 mapping, upgraded rationale)

No new views. Workflow agents render through the surfaces role agents already
use — and after convergence these are simply *the* agent surfaces:

| Existing surface | Today (role agent) | Driver-plane agent |
|---|---|---|
| Sidebar row (`AgentSection`) | name + status dot → detail | same row, clickable; dot = running / idle · next fire / failing / off |
| Detail route `/ws/{ws}/agents/{name}` | agent by name | binding/service by id; resolver decides |
| Terminal tab | PTY session | run transcript + live SSE stream; idle shows "next fire at …" (PTY only for interactive services) |
| Info tab | role, repo, stats, Edit configuration | driver + version + trigger + run stats; Edit = prompt textarea (prompt agents) or `WorkflowSourceModal` (scripted) |
| Git / Diff / Files tabs | agent worktree | rendered only when the agent has a worktree capability; run artifacts (patches, PRs) link from run history |
| Start / Stop / Restart | agentcontrol | Run now / Enable / Disable (bindings); desired-state controls (services) |
| Task history | tasks completed | run history |

**Capability-based tabs, not kind-based views** — the page renders what the
agent *has* (worktree, PTY, runs), never branches on what it *is*. Precedent:
`AgentsPage` already consumes workflow-run statuses for epic-runner
(`isTerminalWorkflowRunStatus`). `AutomationsModal` is absorbed and retired.

## Gap map (the near-term work list, verified 2026-07-01)

| Gap | Status |
|---|---|
| Runs list API | **missing** — `DriverRunStore.List` exists (`internal/store/platform_store.go`), no HTTP endpoint; nothing can show run history without it |
| Run history / live view in UI | per-run `GET /runs/{id}` + `/events` + `/stream` exist; wired only into the epic-runner issue panel |
| Failure surfacing | failed runs invisible; sidebar dot = on/off only |
| Schedule visibility | backend serializes `schedule`; frontend `TriggerBinding` type omits it (`src/api/workflows/workflows.ts`) |
| Next fire time | computed inside `CronScheduler` only, never exposed |
| Binding PATCH / DELETE | absent over HTTP (CLI has `update`; nothing has delete) — agents can be disabled but never removed |
| Detail page | none; rows not clickable; `AutomationsModal` is a parallel surface |
| Run now / cancel / retry | run-now exists (AutomationsModal only); cancel/retry absent |
| Event-triggered bindings in sidebar | invisible — `AgentSection` filters `source_kind === "cron"` only |
| Grants / budget visibility | grants provisioned per binding, never shown; no budget analog surfaced |
| Custom workflow authoring from UI | absent (custom *role* template exists) |

## Phased delivery

**Agreed execution order (Tyson, 2026-07-01):** Phase 1 → a minimal Phase-4
**spike** (prompt-agent proof, defined below) → Phases 2–3 (the detail UI then
renders both agent kinds from day one) → remainder of Phase 4 → Phase 5.

### Phase 1 — plumbing

Runs list endpoint (`GET /api/workspaces/{ws}/workflows/{name}/runs?status=&limit=`,
thin handler over `DriverRunStore.List`; `DriverRun` already carries `run_id`,
`status`, `summary`, `error_class`, `started_at`, `finished_at`, source
provenance). Computed `next_fire_at` on binding list (reuse the scheduler's
cron parser). Frontend `schedule`/`schedule_timezone` type fields.

*Acceptance:* runs endpoint returns S1's cron-fired runs in a live stack;
binding JSON shows `schedule` + `next_fire_at`.

### Phase 2 — the unified detail

Clickable sidebar rows for **all** bindings (not just cron); sidebar regroups
to *Interactive / Autonomous* (decision 5); route resolution (agent store
first, binding id second); capability-based tabs; run history + live SSE
stream; Run now / Enable / Disable; Edit via `WorkflowSourceModal`.

*Acceptance (live click-through, not just build):* clicking the S2 row opens
the detail; history shows past runs; Run now appears and streams live; Edit
opens the source modal; role-agent detail unchanged.

### Phase 3 — management parity + cleanup

Binding PATCH/DELETE + UI (edit cadence, rename, remove); failing status dot;
retire `AutomationsModal`.

*Acceptance:* change S1's cadence from the detail and observe the next fire
honor it; delete a binding and watch it leave the sidebar (grants revoked);
one failed run turns the dot amber, a second consecutive failure turns it
red.

### Phase 4 — prompt-agent packaging (convergence begins)

**Spike (runs right after Phase 1, before the detail UI):** flag-gated, no UI
— one binding whose config references a `Role` (decision: Role is the shared
behavior-config object), fired manually (Run-now / `loom workflow run`),
dispatching local-task-runner with the role's prompt as `input.taskPrompt`;
the run claims a real task with the existing task lease and completes it with
**no supervisor involvement**. Proves prompt-as-config + claim + transcript
before any UI is built on top.

Full phase: a Create-Agent path that produces a binding whose config carries
the role reference (prompt/model/backend/task-filter), dispatching the
existing local-task-runner with `input.taskPrompt`. **Task-ready internal
events** (issue-journal bridge extension, `source_kind=internal`) + binding
match replace the manual fire — decision: events, not cron polling; claim
races are settled by the existing task lease. Detail-page Edit for prompt
agents is a prompt textarea (config PATCH on the Role), not TS.

**Spike result (2026-07-01): PASSED.** `prompt-agent` builtin
(`internal/workflows/builtin/prompt-agent.ts`) ran live: claimed a real ready
task via the task lease, dispatched `local-task-runner` with the role prompt
verbatim as `input.taskPrompt`, real codex execution (61k input tokens),
1 file changed + patch-back applied, task auto-closed, run visible through
the Phase-1 runs API — zero supervisor involvement (driver-run executor +
task worker only, lease/fence/heartbeat). One Go change was needed:
registering the workflow as a builtin, because of gap (a) below. Gaps to
close for the full phase:

- (a) **Sibling-runner resolution is builtin-only**: `resolveDriverRunner`
  matches only the calling driver version's manifest, and the HTTP
  `createWorkflowVersion` path passes no runners and no `DeriveRunners`, so a
  custom driver can never dispatch `local-task-runner`. Fix: resolve builtin
  task-runners workspace-globally by name (or let the HTTP path declare
  runner specs blessed at approve time).
- (b) **No claim-by-task-id**: `tasks.claimReady` pulls queue order only;
  targeting a specific task means claim-and-release loops that race. Needed
  for event-driven pickup.
- (c) **No role-read surface in the driver SDK** (`loom.roles.*` missing):
  the prompt must be passed as input. Decision 2 ("one prompt edit updates
  every agent") needs `roles.get` from workflows or dispatch-time
  materialization of the role prompt into the run payload.

*Acceptance:* a prompt agent created from the UI claims and completes a real
task end-to-end with **no daemon supervisor involvement**, and its run +
transcript appear in the same detail view as any workflow run.

### Phase 5 — role-plane retirement (coordinated with Phase U / AgentService)

`LOOM_DAEMON_LEAF=ts` default-on; migrate builtin `plan`/`task` roles to
prompt-agent bindings; lead/interactive agents to `AgentService`; supervisor
deprecated once parity is proven (preflight, concurrency, restart, PTY).

*Acceptance:* a workspace runs S1, S2, and a migrated `task` role with the
supervisor disabled; all three are managed identically in the UI.

## Non-goals

- Fully specifying the runtime convergence internals — Phase U's own work owns
  the execution leaf; the AgentService proposal owns the service controller.
  This doc sequences UX + packaging against them.
- Forcing interactive/lead agents onto AgentService before its controller is
  ready; they stay supervised until Phase 5.
- Webhook/event binding *creation* UX (visibility only, Phase 2).

## Decisions (Tyson, 2026-07-01)

1. **Build order:** interleaved — Phase 1 → Phase-4 spike → Phases 2–3 →
   remainder of Phase 4 → Phase 5.
2. **Role record:** `Role` stays as the shared behavior-config object
   referenced by bindings/services (aligns with `AgentService.RoleName`); one
   prompt edit updates every agent wearing the role. Role becomes a template,
   not an execution plane.
3. **Steering:** background prompt agents are watch-only — live transcript
   stream + cancel, no stdin into a run. Interactive agents remain a separate
   long-running kind (AgentService, Phase 5); PTY support stays out of the
   run executor.
4. **Task pickup:** task-ready internal events via the issue-journal→trigger
   bridge (`source_kind=internal`), not cron polling; the run claims the task
   with the existing task lease.
5. **Sidebar grouping:** by interaction mode, not plane — *Interactive*
   (agents you talk to, e.g. lead) on top, *Autonomous* (background roles,
   scheduled, event-driven) below. Replaces the current regular / Background /
   Scheduled-workflows three-way split.
6. **Delete semantics:** deleting a binding revokes its connector grants;
   recreating from a template re-provisions them. No orphaned credentials.
7. **Failure surfacing:** one failed run turns the status dot amber (tooltip
   shows the error); two consecutive failures turn it red with a "failing"
   label.

## Open questions

1. **Task-ready event shape:** which journal/outbox event(s) exactly map to
   "task became ready"?
2. **`GET /agents` contract:** when bindings/services appear in the unified
   list with a `kind` discriminator, what must daemon/CLI consumers tolerate?
3. **Per-binding run attribution:** `DriverRunFilter` filters by driver;
   extend with `BindingID` or join through `TriggerDeliveries`?
