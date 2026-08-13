# Unified Agent Platform: the Workflow Plane Supersedes the Role Plane

**Status:** Implemented through Phase 4 (Phases 1–3 + Phase-4 platform/packaging
landed and real-run verified; Phase 4's EVENT lane is red pending task #36 —
see "Adversarial vet"; Phase 5 not started). Doc is an append-only design
record: later sections correct earlier ones.
**Date:** 2026-07-01 (v2); addenda 2026-07-02
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
3. **Shared telemetry vocabulary (Phase U/U0, current owner).** Artifacts owns
   the one canonical transcript event contract used by both execution planes
   (`internal/modules/artifacts/transcript_contract.go`). The former
   `internal/sessions` copy has been retired.
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

**All three gaps CLOSED (2026-07-01, same day).** (a) Workspace-global runner
resolution: fail-closed injected resolver — a runner is globally resolvable
iff declared by the ACTIVE version of a TRUSTED builtin; the runner executes
under its owner's trust and bundle, never the caller's
(`internal/driver/global_runner.go`, `internal/workflows/global_runner.go`).
(b) `loom.tasks.claim({taskId})` — targeted claim over the same ready view and
actor-scoped lease as claim-ready; not-ready/raced → 409. (c) `loom.roles.get`
— run-token-authenticated read returning the Role + prompt body via the roles
module's shared loader; prompt-agent now materializes `input.roleName` at
dispatch. Proven live full-circle: UNTRUSTED HTTP-registered
`prompt-agent-custom` resolved the `docs-assistant` role prompt, claimed
SANDBOX-4 by id, dispatched local-task-runner via global resolution
(`runner_ref` pinned to bug-fix-agent's trusted version), completed, task
closed; re-fire at the closed task surfaced the honest conflict.

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

## Architecture vet (2026-07-02) — what the real-run suite's reds revealed

The aether real-run suite (R1–R12, `aether-test-framework/docs/UNIFIED-AGENTS-REAL-RUN-SUITE.md`,
evidence cells committed at aether `1f868eb`) validated the unified-agent
architecture end-to-end with real codex runs: prompt-as-data, global runner
resolution under the OWNER's trust, claim-by-id, role-as-shared-config, and
the unified UI all held under adversarial cases (untrusted caller, claim
conflicts, genuine failures). The four reds, examined together, are NOT four
independent bugs — they cluster into two structural issues, one piece of
deliberately-priced debt, and one recurring toil class.

### Miss 1 — dispatch payload assembly is per-source, not a pipeline stage

Binding run-input merging exists in three call sites: the CronScheduler
(`internal/trigger/cron.go` dispatchTick), the frontend Run-now
(`WorkflowAgentDetail.handleRunNow`), and internal dispatch (MISSING — the
R11.B red, `internal/trigger/internal_source.go` Emit). Every future trigger
source must remember to merge, in whatever language it is written. Correction:
ONE merge point in the dispatch path, applied per delivery leg (fan-out
correct: each leg gets its own binding's run-input); the frontend copy is then
deleted. The truly correct home is fleet-db's leg builder (which owns run
creation) — blocked by Miss 2, so the loomcli-side consolidation carries an
explicit "eventual home: fleet-db dispatchTriggerRouteLeg" marker. Task #36
must be fixed at THIS altitude, not by adding a fourth call site.

### Miss 2 — the loomcli↔fleet-db boundary has no contract enforcement

One pattern, five sightings this week: the server lacks the trigger-binding
DELETE route the client fully implements (R10, found by E2E not by any
contract check); run_input smuggled through `source_config_ref` because
fleet-db strict-decodes unknown fields and schema growth is a cross-repo
change; the ready-queue RPC lacks the `Type` param storage already supports
(part of S1-G1); `external_ref` was write-dead and read-dead until P0-3.
Intra-repo contracts are frozen and tested (driverapi op list, @loom/sdk
surface); the CROSS-repo seam has nothing. Correction: a fleet-db↔loomcli
contract suite (or generate both sides from the shared spec), landed together
with task #34's DELETE route so the next verb gap cannot appear silently. The
`source_config_ref` transport is tagged migration-pending: when the binding
schema next evolves, run_input becomes a real field and the wedge is removed.

### Priced-in debt made visible — two planes, one queue, no arbitration (R8)

The plan lane claiming designless bugs before the bug-fix workflow is the
interim state the locked build order accepted: until Phase 5, both planes
compete for one ready queue, first-claim-wins, with hardcoded role
preferences. Consequence for sequencing: S1-G1 (route bugs away from the plan
lane) is a PHASE 5 DESIGN INPUT — task-type arbitration between planes should
be designed once, not patched as a one-off router rule now and redesigned at
supervisor retirement.

### Miss 3 (minor, recurring) — stack capability is implicit

S2.A's fast-fails, the bare 500 when binding-create's builtin self-heal lacks
the build toolchain (task #38), toolchain staging, vault key, backend auth:
"what can this environment run" is scattered across compose overrides and
scripts, and every gap surfaces as a runtime failure. Correction: a capability
manifest per agent kind (needs: build toolchain / connector X / backend auth)
checked at binding-create and surfaced by `loom doctor`.

### Re-sequenced fix plan

1. Task #36 at pipeline altitude: consolidate the three merge sites into one
   per-leg merge (+ unscope the issue-journal bridge). Flips R11.B.
2. Task #34 (fleet-db DELETE) lands WITH the first cross-repo contract guard.
   Flips R10.
3. S1-G1 folds into the Phase 5 arbitration design (which plane owns which
   task types) rather than shipping twice.
4. Task #38 grows into the capability-manifest/doctor item rather than a
   one-off error-message fix.

## Adversarial vet (fable agent, 2026-07-02) — corrections to the architecture vet

An independent adversarial review verified every claim above against the code
and the suite evidence. The architecture held (prompt-as-data, owner-trust
runner pinning, lease/fence claims, failure health all confirmed in code and
live evidence). The ARCHITECTURE-VET SECTION ABOVE contains errors, corrected
here; this section supersedes it where they conflict.

### Corrections to Miss 1 (dispatch assembly)

The finding stands (two merge sites exist, internal dispatch has none) but the
proposed fix — a loomcli-side per-leg merge — CANNOT BE BUILT: fleet-db owns
leg creation (`fleet-db/internal/api/platform.go dispatchTriggerRouteLeg`
threads ONE shared payload across all legs; memstore's leg code is test-only
and panics in production). The honest fixes: (a) PRIMARY — parse-and-merge
`source_config_ref` per leg inside fleet-db's `dispatchTriggerRouteLeg` (the
binding is in-hand there; NO schema change; batch with #34's DELETE route —
"blocked by Miss 2" was an excuse, fleet-db must be touched anyway); or (b)
interim loomcli-only — derive unique per-binding internal routes (the cron
`WithDerivedRoute` model) so `Emit` dispatches once per binding with its
merged payload. The frontend Run-now copy cannot simply be deleted either:
Run-now is a direct by-driver run-create with no binding in scope; absorbing
it needs a binding-scoped run-now endpoint.

### Corrections to Miss 2 (contract gap)

- The "ready RPC lacks Type" sighting was WRONG as a cross-repo gap: the seam
  supports `type` end-to-end; the real gap is loomcli-internal (the claim-ready
  driver op never exposes/sets `ReadyOpts.Type`).
- SIXTH sighting found: fleet-db stamps `trigger_binding_id` on every
  trigger-dispatched run; loomcli's `domain.DriverRun` silently drops it on
  decode — which also answers Open Question 3 (per-binding run attribution:
  the server already attributes; the client must stop discarding it).
- Fix emphasis inverted: fleet-db maintains a 10k-line `api/openapi.yaml` that
  ALSO lacks the DELETE (the handwritten client invented a verb the spec never
  had). Primary fix = generate or spec-verify the loomcli fleetdb client from
  that spec (loomcli already runs codegen elsewhere); a bespoke contract suite
  would be a second spec to drift.

### Correction to the arbitration deferral — the hazard is live BOTH ways

Deferring the arbitration DESIGN to Phase 5 stands, but total deferral is
wrong: (1) the plan lane steals designless bugs today (golden S1 red, standing
`REGRESSION` gate noise); (2) sharper and unnamed above — `bug-fix-agent`
claims BLIND via claimReady with no type filter and does NOT release the claim
when it discovers a non-bug (parks arbitrary tasks under its lease until TTL).
Ship two non-preemptive mitigations NOW, loomcli-only: release-on-skip in
bug-fix-agent, and `type` exposure on the claim-ready op. The Phase-5 design
question remains open; these just stop the bleeding.

### New findings (ranked)

1. **(security, should-fix) Untrusted caller → trusted host runner via
   prompt.** An untrusted, container-sandboxed driver can dispatch the
   globally-resolved `local-task-runner` with fully caller-controlled
   `input.taskPrompt` — and that runner executes codex ON THE HOST with
   patch-back and task-close (the suite's R4 proves the lane). "Prompt = data"
   is wrong in effect here: the prompt is the program for the runner's brain;
   instruction injection buys host-side file mutation. Options: restrict
   untrusted callers' global-runner dispatch to operator-authored role prompts
   (roles.get) rather than free-form taskPrompt; trust-gate exec-task; or
   explicitly accept for single-operator deployments — but it must be priced.
2. **(security, should-fix) Actor-keyed locks with caller-supplied actor.**
   Task claim/release locks are keyed by the actor STRING, and every driver op
   accepts a body actor — presenting a victim's label is lock takeover/release
   (cross-agent task theft in one op call). Fix: derive actor server-side from
   run/binding identity; body actor becomes display-only.
3. **(should-fix) claim-by-id false-409 window:** `ClaimTask` scans only the
   first `limit` (default 100) ready entries while the router uses 10,000 for
   the same crowding reason; busy workspaces will get silent no-ops for
   genuinely ready tasks, indistinguishable from honest conflicts.
4. **(should-fix with growth) Unbounded run fetches ×2:** `listWorkflowRuns`
   and `bindingRunHealth` both fetch a driver's FULL run history per call
   (documented local-mode tradeoffs). Fix in the same fleet-db batch: server
   newest-N ordering + a `trigger_binding_id` list filter.
5. **(notes)** `source_config_ref` wedge needs a source-kind guard + size cap
   while it lives; internal-event emission lets any run fire other agents'
   bindings (spend amplification — budget-policy territory); `roles.get`
   exposes every role prompt to every run — document "no secrets in prompts."

### Coherence fixes (applied with this addendum)

Status header corrected (was "Proposal" long after implementation). Open
Question 1 (task-ready event shape) is CLOSED — decided + implemented in
`issue_journal_bridge_task_ready.go`. Open Question 3 is CLOSED by finding
#2 above (decode `trigger_binding_id` + add the list param). Phase-4-full
acceptance is explicitly NOT met until #36: every green run in evidence is
cron/run-now; the chosen event lane is the red one. The by-design reds must
be separated from the aether suite's gate verdict or the gate reads as a
permanent false REGRESSION.

## Config-by-reference (2026-07-02, Tyson) — supersedes both vets' run_input fix plans

Both fix plans above (loomcli per-leg merge; fleet-db per-leg merge) optimize
the wrong transport: config BY VALUE, copied into run payloads by every
dispatcher — which is why merge sites multiply, and why landing the merge in
fleet-db would entrench the `source_config_ref` wedge as server semantics.

Long-term shape: **config BY REFERENCE, resolved at runtime from run
provenance.**

- Trigger-dispatched runs already carry `trigger_binding_id` (fleet-db stamps
  it; the loomcli client must stop dropping it).
- New driver op `binding.config`: the server resolves the binding from the
  VERIFIED run's provenance (connectors.go pattern — same server-side
  derivation principle as the actor-lock security fix), returns the binding's
  config. The op is the wedge's ONE reader.
- `prompt-agent` reads config at start: binding config → roleName →
  `roles.get` → prompt. Event payloads carry only EVENT data (taskId, tick) —
  which internal dispatch already delivers, so R11.B flips with NO fleet-db
  dispatch change.
- Frontend Run-now merge dies via the binding-scoped run-now endpoint (stamps
  binding provenance on manual runs — needed regardless). Cron's merge becomes
  legacy-compat, deleted after migration. `loom workflow run` without a
  binding keeps explicit-input semantics.
- End-state alignment: binding/service REFERENCES a Role; behavior config
  lives on the Role record (`AgentService.RoleName` already models this).
  Long-term the binding config shrinks toward the role pointer and the
  `source_config_ref` wedge becomes a real field or disappears.
- Trade-off, accepted: config resolves at RUN time (late binding) — which is
  what Decision 2 wants; the task-run still records the resolved prompt for
  audit.

The fleet-db batch therefore SHRINKS to: DELETE route (+ openapi.yaml),
server newest-N run ordering + `trigger_binding_id` list filter, and
spec-verifying the loomcli client (which also picks up `trigger_binding_id`
decode). The run_input merge item drops out of fleet-db entirely.

## Phase 4.5 — TS-plane parity (2026-07-07, Tyson) — the missing phase before Phase 5

A full-loop E2E browser test (matrix + verdicts:
`2026-07-03-unified-agent-ui-test-matrix.tsv`) exposed a sequencing hole in
this proposal: Phases 1–4 delivered the unified *surfaces*, but Phase 5
assumes a TS-plane *behavioral parity* that does not exist. Observed: the
autonomous loop (task → auto-plan → approve → auto-code) closes only on the
Go plane; the TS plane cannot wear the builtin roles at all
(`prompt_agent_missing_prompt` — builtin roles are seeded with no prompt
body), cannot win a pickup race (cron-only UI bindings vs the daemon's
seconds-fast claims), cannot publish locally (`stackpublish` is GitHub-only),
has no review→reopen→fix lane, and offers no live transcript. Phase 5's
acceptance is untestable in this state.

Phase 4.5 closes that gap. Full plan with verified root causes, workstreams,
decisions, and acceptance mapping: `2026-07-07-e2e-feature-parity-plan.md`.
Workstreams: WS1 builtin-role TS-contract prompt bodies + planner
close-suppression/design handoff; WS2 event-driven pickup
(`internal.task.ready` bindings from the UI, role-aware claim gating,
interim `LOOM_LOCAL_MODE_PLANE=ts` toggle); WS3 local publish (TS
local-branch delivery + Go LocalForge); WS4 live transcript + per-binding
run attribution; WS5 local review lane (`task-run-diff` op +
`local-review-agent`); WS6 UI fixes; WS7 capability-based tab convergence.
All loomcli-side; no fleet-db changes.

**Phase-5 entry criteria (= Phase 4.5 exit):** the E2E matrix rerun is green
with Go role-agent seeding disabled — a UI-created TS planner auto-plans a
new task within seconds, approval hands off to the TS coder, the change
publishes to the local origin, the review agent comments and reopens, the
coder fixes, and every stage is watchable live per agent per task.

**What Phase 5 still owns after 4.5:** flipping `LOOM_DAEMON_LEAF=ts`
default-on; the shared prompt-body rendering contract (Decision 2's full
end-state — one body both planes compose from); the real task-type
arbitration design (WS2c's toggle is the priced interim); AgentService for
lead/interactive; supervisor retirement once preflight/concurrency/restart/
PTY parity is proven.

### Decision — durable agent identity (Tyson, 2026-07-07)

An agent is a FIRST-CLASS RECORD with its own lifecycle (create / enable /
disable / delete), modeled on the identity the Go plane already has (the
agentdef row exists independently of any process, task, or claim). Bindings,
driver versions, and role references are CONFIGURATION ATTACHED TO the agent
— replaceable without changing who the agent is. The TS plane converges
toward the existing agent-identity model; identity is never the trigger
plumbing.

Consequences:
- Run history, grants, and budget attribute to the AGENT; `trigger_binding_id`
  remains dispatch-level provenance beneath it. Config churn (trigger swap,
  driver-version upgrade, binding delete+recreate) happens under a stable
  identity — the id-reuse/history-adoption ambiguity found in the wave-1 live
  run stops existing.
- Deleting the agent is the meaningful lifecycle event (revoke grants,
  archive history); deleting a binding is detaching config.
- `AgentService` (`internal/domain/platform.go:147-168`) is the shape: the
  IDENTITY RECORD comes forward now for background agents too; the
  desired-state CONTROLLER (restart policy, instances, PTY attach) stays
  Phase 5.
- Create-agent flows create an AGENT (identity + role ref + trigger config),
  not a bare binding. Interim until the record lands: the binding row is the
  identity PROXY for TS agents, and the agent-scoped runs surface is rooted
  on it (`GET /trigger-bindings/{id}/runs`), becoming a sub-resource of the
  agent record later — this is why the runs surface must not stay rooted on
  the driver.
