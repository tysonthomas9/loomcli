# Flue Session Model

**Status:** Decided -- implementation not started.  
**Date:** 2026-07-20  
**Map ticket:** LOOMCLI-86  
**Relation to v1 observability:** `docs/design/agent-observability.md`
ships first. This is the post-v1 flue-plane session model and supersedes the
bridge-minted 1:1 flue session mechanics only when the migration slice lands.

The vocabulary of record is `CONTEXT.md` "Flue session model"
(`CONTEXT.md:71-104`). This spec uses **Agent Invocation**, **Invocation Key**,
**Attempt**, and **Parent Session** exactly as defined there and does not
redefine them.

## The invariant

**TaskRun 1-N AgentSession; AgentSession 1-1 Agent Invocation attempt.**
A TaskRun can produce zero, one, or many AgentSessions. Each AgentSession
records exactly one Agent Invocation attempt within one TaskRun Attempt. An OS
process is one implementation of an Agent Invocation; one in-process Flue
harness prompt call is another (LOOMCLI-105).

**Leaves create sessions at invocation time.** The leaf opens a session when it
invokes an agent and closes it when that invocation settles. The host bridge no
longer creates a session around every TaskRun. Its role becomes session
reconciliation: closing registered-but-unclosed sessions truthfully after the
TaskRun outcome is known (LOOMCLI-97).

**No deterministic session exists.** Deterministic work spawns no agent and
opens no session. The run remains observable through TaskRun and driver-run
records only (LOOMCLI-106).

## Motivating defects

**Preflight noise.** The v1 bridge creates `flue-<taskRunID>` sessions before
the leaf decides whether it will invoke an agent. At test cadence this produces
roughly 300 preflight `flue-task-run-*` sessions per day with no transcripts
(`internal/driver/task_bridge_session.go` `startFlueTaskSession`).

**Judge transcripts missing.** Judge work shells out from the eval leaf but the
leaf does not own session creation or transcript upload, so the judge process
does not produce a judge AgentSession transcript.

**`eval_cost` all zeros.** Usage and cost extraction live in each leaf result
path. The session lifecycle and transcript/cost capture are not shared at the
Agent Invocation boundary.

**1:1 cannot represent multi-invocation runs.** One `flue-<taskRunID>` record
cannot distinguish attempts, parallel or sequential invocations, judge
invocations, or future subagent invocations. It also overwrites retry evidence
across attempts (LOOMCLI-92).

## Scope

**Flue plane only.** This spec changes flue TaskRun leaves and the flue bridge
path. The daemon plane is out of scope for this map; the daemon supervisor
already creates one session per process start and remains the precedent, not
the migration target.

**Daemon-adoptable contract.** The lifecycle authority is a store primitive, not
a taskrunapi-only feature. The taskrunapi operations are the wire projection of
the same descriptor and finalize primitives so the daemon supervisor can adopt
them later without rework (LOOMCLI-87).

**Strictly post-v1.** v1 agent observability ships first. This spec records the
post-v1 model and the migration slices that update v1 e2e assertions.

## Placement & connectivity basis (LOOMCLI-91)

**Leaves are host-placed.** "Daytona placement" means
`RunnerPlacement=flue` and `SandboxPlacement=daytona`; the leaf process is still
a host-side `node` child of `loom-serve` (`task_scheduling.go:208-219`,
`task_bridge.go:355-401`). `daytona-task-runner.ts` creates and drives the
Daytona sandbox from that host process.

**Every leaf has taskrunapi at spawn.** The bridge exports
`LOOM_TASK_RUN_API_URL` plus the leaf's lease token and fenced identity
(`task_bridge.go:661-709`). Both local and Daytona leaves call taskrunapi from
their first instruction for task reads, runtime credentials, and artifacts
(`sdk/runner.js:34-61`, `sdk/runner.js:411-419`).

**Driver ops are credential-unreachable by design.** Leaves can see
`LOOM_DRIVER_RUN_ID` as data, but they do not receive `LOOM_RUN_TOKEN`,
`LOOM_DRIVER_API_URL`, the parent lease quad, or fleet-db credentials. The
driver-op verifier requires the parent workflow credential; leaves cannot pass
it even when the loopback endpoint is reachable (LOOMCLI-91,
`docs/research/remote-leaf-connectivity.md`).

**Session ops live on taskrunapi.** `session-open` and `session-close` use the
lease-authenticated taskrunapi channel. No remote-placement fallback is needed:
the leaf is host-side, and sandbox code still receives no Loom credential.

## Attempt & durability semantics (LOOMCLI-92, LOOMCLI-89 §5)

**Attempt means one claim of a TaskRun.** A TaskRun keeps the same TaskRunID
across reclaims. Each claim rotates lease ownership and installs a monotonic
`FencingToken = ClaimedAt.UnixNano()`. Attempt ordinals are dense per claim;
fencing order is the truth when an ordinal projection drifts.

**Re-execution is layered.** Driver-run workflow code re-runs from the top on
resume and relies on deterministic ids and conflict swallowing. A requeued
TaskRun attempt fully re-executes: fresh runner process, fresh one-shot Flue
workflow runId, same TaskRunID. Inside Flue, completed agent submissions can
settle from the durability store with no provider work, but that durability
store is not wired into the runner bundle yet
(`docs/research/flue-reentry-sessions.md`).

**The journal is Attempt-first-class.** TaskRunEvents use deterministic ids of
the form `taskRunID#attempt#type` (`internal/domain/outbox.go:31-38`). Agent
sessions must join the same Attempt projection.

**Terminal recovery paths mint no Attempt.** Stale failure, quarantine, and park
do not claim the TaskRun, so they mint no Attempt. A post-quarantine re-claim is
the next Attempt.

**Today's shared-session mutation is the defect.** The bridge currently reuses
one `flue-<taskRunID>` session and mutates it back to `running` on re-entry,
then overwrites the prior attempt's status, metadata, transcript linkage, and
`driver_runner_session_id` (`internal/driver/task_bridge_session.go:157-262`).
Per-invocation ids and per-invocation artifacts remove that overwrite class.

## Agent-exec contract (LOOMCLI-87, LOOMCLI-88, amended)

**Store primitive first.** The contract authority is a store-level lifecycle
primitive over AgentSession:

- `Open(descriptor) -> sessionRef`
- `Finalize(sessionRef, outcome)`

The taskrunapi operations are a thin HTTP projection of those primitives.
Wire payload fields equal store descriptor fields.

**Wire op set.** v1 adds only:

- `session-open { invocationKey, backend, model, parentSessionId?, kind?,
  tags?, metadata{} } -> { sessionId, attempt }`
- `session-close { sessionId, status, exitCode?, summary?,
  usage{tokens,cost}?, transcriptRef?, metadata{} }`

There is no `session-report` op in v1. Usage is crash-lossy: if the leaf dies
before reporting usage, the reconciler can stamp the session failed but cannot
recover cost that never reached Loom. **Revisit trigger:** add a checkpoint or
report op only if crash-lossy cost proves material.

**Descriptor.** The full amended descriptor is:

- `invocationKey` -- required Invocation Key.
- `backend` -- required backend label.
- `model` -- required or explicit unknown model label, as the backend can
  truthfully provide.
- `parentSessionId?` -- optional Parent Session id.
- `kind?` -- enum, default `task`; includes `judge` (LOOMCLI-94).
- `tags?` -- optional leaf-declared slug list (LOOMCLI-99 decision 7).
- `metadata{}` -- structured metadata, with reserved keys in "Identity,
  linkage & metadata".

**Process form helper.** The LOOMCLI-88 process prototype maps to a process
form of agent-exec. The helper owns capture; the leaf owns argv. Process-level
failures return in the result and do not throw. The helper throws only for
caller bugs (`AgentExecSpecError`). Bounded `session-open` retries default to
two; if open still fails, the agent runs and the result reports
`session.degraded` plus leaf `runtimeMetadata.observability_degraded`.

**Process form shape.** The process form carries:

- Session descriptor: `invocationKey`, `backend`, `model?`,
  `parentSessionId?`, `metadata?`, plus the amended `kind?` and `tags?`.
- Process owned by the leaf: `argv`, `cwd?`, `env?`, `stdin?`,
  `timeoutMs?`, `live?`.
- Capture owned by the helper: `transcript?: stream-json|minimal|none`,
  `redactSecrets?`, `openRetries?`, `close?: auto|deferred`.

It returns `exitCode`, `timedOut`, `spawnError`, `stdout`, `stderr`,
`durationMs`, canonical transcript `entries`, `usage`, and
`session:{id, attempt, transcriptRef, opened, closed, degraded}`; deferred
close returns `finalize?`. Leaves may keep returning entries and usage in leaf
results during the cutover, but the shared helper is the source of session
capture. Redaction is declared by the leaf, not inferred.

**Close ownership.** `close: auto` lets the helper compute and send the
helper-computed outcome of the process form's invocation. `close: deferred`
sends no automatic close; the leaf must call `finalize` on every return path so
domain-specific outcomes such as `codex_no_findings` are recorded. A deferred
leaf crash is handled by the session reconciler.

**Server-composed identity.** The server composes:

`flue-<taskRunID>-a<attempt>-<invocationKey>`

The leaf never supplies the session id and does not receive attempt plumbing as
authority. The server stamps the Attempt and fencing witness.

**Open idempotency.** `session-open` is idempotent on
`(taskRunID, attempt, invocationKey)`. Re-opening with an identical descriptor
returns the same session. Re-opening the same key with a conflicting descriptor
is a structured non-retryable error; a buggy leaf must not silently share one
record between concurrent agents (LOOMCLI-89 §3).

**Liveness.** Session liveness is inherited from the task-run lease. There is
no per-session heartbeat. A session is live iff its parent TaskRun lease is
live; unclosed sessions are reconciled after the TaskRun settles or goes stale.

**Close semantics.** First terminal close wins. A replayed close with the same
outcome is a no-op success. A conflicting later close returns a structured
non-retryable error and may be recorded only as advisory metadata; the settled
record is not mutated. This CAS must land before the reconciler writes stamps.

**Auth.** Both ops use the existing lease-token bearer plus
`X-Loom-Task-Run-*` identity headers, verified by the same fenced store checks
as other taskrunapi ops. No new credential is introduced.

**Transcript artifact.** Transcript bytes ride the existing artifact
declare/upload/finalize surface, including the 64MB raw-content route.
Artifact ids are per invocation:

`transcript-<taskRunID>-a<attempt>-<invocationKey>`

`session-close` references the finalized artifact as `transcriptRef`.
Missing usage is represented as unknown/null, not as zero.

**Prototype mapping proof.** The LOOMCLI-88 prototype fit all three first
movers: `local-task-runner` collapses its exec/parse/redact/usage block to one
call with Invocation Key `agent`; `github-review-task-runner` gains a
session/transcript with Invocation Key `review` and deferred close; the
session-eval judge gains transcript plus real eval cost with Invocation Key
`judge`; preflight simply never calls the helper and opens no session.

**Contract obligation: agent invocations only.** agent-exec opens sessions for
Agent Invocations only. It never opens a session for deterministic command
capture, including sandbox clone/checkout/diff commands (LOOMCLI-106). Server
enforcement of "is this process agentic?" is rejected as unverifiable; the
server can verify fenced TaskRun ownership, not the semantic nature of argv.

**Terminalization race.** `session-open` must reject opens that land after the
TaskRun is terminal. This closes the open-in-flight-during-reconciliation race;
stragglers are left to the reconciliation loop only if they committed before
terminalization (LOOMCLI-97 race constraint).

**Disjoint invocation forms.** Process leaves and harness leaves must use
disjoint API forms with disjoint validation, such as `exec.process(argv...)`
and `exec.invoke(run...)`, or an equivalent explicit mode discriminator.
An optional `argv` field on one API form is explicitly not acceptable. Naming
is settled in the implementation slice. The process form preserves "helper
owns capture, leaf owns argv"; the invoke form owns open/collect/close around
an in-process prompt call while the leaf owns the prompt function (LOOMCLI-105).

## Identity, linkage & metadata (LOOMCLI-89)

**TaskRunID is first-class.** AgentSession gains first-class `TaskRunID`, and
the list API gains `task_run_id` as a query parameter. This mirrors the
AgentInboxMessage precedent. `driver_run_id` stays metadata because driver-run
grouping joins through TaskRuns; promoting it later remains cheap.

**Invocation fields are explicit.** `invocation_key` and `attempt` are explicit
API fields. Clients never parse session ids for current rows. Legacy
`flue-<taskRunID>` rows may degrade via metadata fallback until migration.

**Metadata stamped at open.**

- Server-stamped: `fencing_token`, `driver_run_id`, `driver_step_id`.
- Reserved leaf-optional: `flue_run_id`, `flue_session_key`,
  `flue_submission_id`.

**Metadata stamped at close.**

- `driver_runner_session_id` -- the inner CLI/backend session id, per
  invocation.

**Dropped as redundant.**

- `lease_id` -- `fencing_token` is the claim witness.
- `scheduler_attempt` -- first-class Attempt carries the ordinal.
- metadata copies of `task_id` and `task_run_id` -- first-class fields carry
  them.

**Invocation Key validation.** Invocation Key is a strict slug:
`[a-z0-9][a-z0-9-]{0,63}`. Invalid keys are rejected at open because the key
embeds in session and artifact ids. The key names the Agent Invocation; it does
not sequence it. `StartedAt` orders invocations within an Attempt.

**Parent Session has one causal meaning.** `parent_session_id` is the session
from whose execution this session was spawned: workflow-to-leaf today,
agent-to-subagent later. It may be empty when the spawner has no session. Run
grouping is TaskRunID's job; Parent Session never doubles as run linkage.

**Attempt is pinned.** The claim that opens the session fixes the Attempt.
The session joins `taskRunID#attempt#*` journal rows 1:1. The reconciler
finalizes a dead claim's sessions under that claim's Attempt. Stale failure,
quarantine, and park mint no Attempt because they do not claim the run.
Whether the server derives the ordinal from `scheduler_attempt + 1` or from a
first-class claim counter is left to implementation; the invariant is the
contract.

## Session kinds & judge exclusion (LOOMCLI-94)

**Judge sessions are never eval candidates.** Judge transcripts appear in
Traces for debugging, but no judge judges a judge. Re-inclusion requires a
deliberate future decision with a judge-shaped rubric and a prompt_version
bump.

**Mechanism: `kind=judge`.** Add `AgentSessionKindJudge = "judge"` to the
session kind enum. The existing candidate predicate `kind == task` remains
unchanged, so judge sessions are excluded with zero predicate change. Manual
rejudge already rejects non-task sessions, and the doctor transcript repair
check stays task-scoped.

**Leaf-declared kind.** `kind` is optional on the descriptor and defaults to
`task`. Builtin eval workflows (`session-eval-agent`,
`session-eval-task-runner`) declare `kind=judge` on any session they open. The
forget-risk is covered by the migration assertion that judge sessions carry
`kind=judge`.

**Judge linkage metadata.** The judge leaf stamps `judged_session_id` and
`judge_prompt_version` at session open. LOOMCLI-99 additionally gives the eval
record a first-class `judge_session_id`.

**Rejected alternatives.** Metadata driver-identity negative filter was
rejected because it adds a join and has the same silent-inclusion failure mode
LOOMCLI-63 avoided. Run-linkage joins at candidate time were rejected because
they break the pure-metadata candidate predicate. Server-derived kind from the
workflow registry was rejected because it couples taskrunapi to workflow names
and splits kind assignment across planes. Reusing `maintenance` or `ad_hoc`
was rejected because nothing mints them today and `judge` is self-describing.

## No agentic marker (LOOMCLI-106)

**No marker exists.** "Deterministic exec session" is definitionally empty
post-cutover. A session records an Agent Invocation by construction, including
failed spawns. A deterministic path opens no session and leaves TaskRun and
driver-run records only.

**The charting constraint dissolves.** The runtime choice is session-or-no
session, not a marker value. There is no residual `kind=exec`,
`agentic=true`, or scheduler-declared flag.

**Contract obligation, not server inference.** agent-exec adopters must not use
session capture as a generic process wrapper. Server-side enforcement of
agent-vs-deterministic is rejected as unverifiable; fenced task-run ownership is
the enforceable boundary.

**Synthetic fixture carve-out.** The hidden `daemon seed-transcript` helper
(`seed_transcript_cmd.go`) writes synthetic AgentSessions directly for tests.
Those records are test plumbing and sit outside the invariant's claims.

**Corollaries.** Traces defaults remain the LOOMCLI-99 defaults: implicit
`kind != judge`, nothing else. Leaf-declared tags have no reserved
`agentic`/`deterministic` vocabulary. A future non-agentic session producer
would violate this contract and force a re-decision, not a quiet tag addition.
The truth condition is post-cutover: LOOMCLI-98 deletes the bridge mint and
adds the preflight-no-session / judge-kind assertions; LOOMCLI-99 lands the
judge-hidden default.

**Rejected alternatives.** New kind values for non-agentic sessions, reserved
tag conventions, server-side machine enforcement, and leaving the constraint as
a Daytona-only comment were all rejected.

## Session reconciler (LOOMCLI-97)

**Name.** The role is the session reconciler. Code names:
`TaskRunSessionReconciler` for the bridge-side component and "session
reconciliation loop" for the server backstop. "Janitor" was rejected by Tyson;
"finalizer" and "sweeper" were rejected for existing vocabulary collisions.

**Discovery is hybrid.** Both halves land in v1:

- In-process open-callback registry for live visibility on the serve-hosted
  bridge path (`internal/webui/handlers/driverapi/module.go:588`).
- Store query authority at TaskRun finish: non-terminal AgentSessions for
  `(task_run_id, attempt)`.

Standalone exec bridges have no callback channel and rely on store-only
discovery. Correctness always comes from the store query. Required store work:
first-class TaskRunID plus Attempt and non-terminal filters
(`internal/store/control_plane_store.go:64` lacks them today).

**Evidence-truthful stamp mapping.** The reconciler never fabricates
`completed`; synthetic completions would poison the eval candidate pool
(`internal/evals/evals.go:198-220`).

- Run completed + session unclosed -> `failed`, errorClass
  `agent_session_unclosed`.
- Run cancelled -> `cancelled`, errorClass `driver_cancelled`.
- Run failed -> `failed`, with the run error carried in metadata.

Every stamp carries `finalized_by` provenance and a summary. Leaf-closed
sessions are untouchable. Multi-session partial completion follows naturally:
only the unclosed remainder is stamped.

**Run outcome untouched.** Reconciler findings never demote the TaskRun.
Observability failure must not trigger retry storms. The bridge path merges
`unclosed_sessions: N` into `TaskExecResult.RuntimeMetadata` before the
worker's terminal write. Later terminal updates reject changes, so the
breadcrumb is omitted outside that path
(`internal/infra/memstore/platform_task_run.go:310-312`).

**Synchronous and best-effort.** The bridge reconciler runs before the normal
bridge path persists the TaskRun terminal status
(`internal/driver/task_request.go:654-687`). Reconciler errors are logged and
recorded, never promoted into the run result; this removes today's `finishErr`
promotion (`internal/driver/task_bridge.go:256`).

**Leaf self-complete is forbidden on bridge paths.** taskrunapi `complete` /
`sdk/runner.js completeRun` is not allowed for bridge-run flue leaves. IPC
result is the only completion path. The op remains for non-bridge topologies,
whose sessions are owned by the reconciliation loop. No current flue leaf calls
the op.

**Backstops both land.**

- Serve-side session reconciliation loop: lists non-terminal sessions, loads
  the parent TaskRun, and settles sessions whose parent is terminal. Each loop
  stamp carries `finalized_by` provenance, a summary, and a swept-by marker.
- Same-pass stale hook: when `RecoverStaleTaskRuns` fails a dead run, its
  sessions are stamped `failed` / `stale_task_run` in that pass.

Both are Attempt- and fence-checked. The split is required because
`StaleTaskSweeper` only visits running driver runs
(`internal/driver/stale_task_sweeper.go:71-92`) and cannot catch
terminal-parent orphans.

**Transcript salvage is narrow.** The reconciler may finalize
uploaded-but-unfinalized per-invocation transcript artifacts via store-level
artifact ops and link them as `transcript_ref` with
`transcript_partial: true`. It does not salvage mid-agent crashes because the
prototype upload is one post-exit PUT
(`internal/webui/handlers/taskrunapi/artifacts.go:225`).
**Revisit trigger:** only if agent-exec streams transcripts incrementally.

**Race constraints.**

- `session-open` verifies the live lease and fence and rejects opens landing
  after TaskRun terminalization.
- Every reconciler, loop, and stale-hook stamp is Attempt- and fence-checked,
  so Attempt N never touches Attempt N+1.
- Concurrent reconciler/loop/leaf close writes are safe only through the
  first-terminal-wins CAS from LOOMCLI-87.

Research and review: `docs/research/session-reconciler-codex-review.md`.

## Preflight observability & doctor `eval_loop` (LOOMCLI-96)

**Driver-run records are sufficient.** The per-tick record of truth is the
`session-eval-agent` DriverRun: status and error_class are written at finish
(`internal/driver/executor.go:353-359`). The preflight TaskRun keeps executing;
only its accidental session record disappears.

**Verifier already reads driver-runs.** `test/local-mode/verify-evals.sh:209-220`
polls `/driver-runs?driver_id=session-eval-agent` and matches `error_class`.
No preflight session is consumed by the verifier.

**No dashboard or rollup dependency.** Preflight rows have no transcript_ref and
are already outside the eval candidate pool
(`internal/evals/evals.go:198-221`). Their removal from Traces does not hide a
data-plane signal that any v1 consumer reads.

**Specified gap: eval loop health.** Operators lose the accidental heartbeat of
preflight rows accumulating in Traces. The replacement is a doctor check named
`eval_loop`.

**Doctor check contract.**

1. Read the `cron.session-eval-agent` trigger binding via
   `TriggerBindings().GetByRouteKey` (`internal/store/platform_store.go:260`,
   fleet-db client `internal/infra/fleetdb/platform.go:189`). Binding absent or
   `Enabled=false` -> Pass, informational "evals not provisioned". This matches
   the opt-in model: `loom evals enable` setting `Enabled=true` is the
   operator consent act.
2. Read recent `session-eval-agent` DriverRuns with `DriverRunFilter{DriverID}`.
   Compute latest client-side over a bounded list because fleet-db ordering is
   not contractual.
3. Stale tick -> Warn when the second scheduled fire after the latest run's
   `started_at` has passed, plus grace. Formulate forward from the last run
   because `robfig/cron/v3` exposes `Next()` only
   (`internal/trigger/cron.go:13`).
4. Failed latest run -> tiered classes:
   `eval_backend_unsupported` is informational; all other classes
   (`eval_backend_unavailable`, `judge_error`,
   `eval_candidate_list_failed`, `stale_driver_run`, and future classes) are
   Warn.
5. Never StatusFail. Evals are observability, not the critical path.
6. This is doctor's first data-plane check. Reuse the CLI store bootstrap
   pattern from evals commands (`internal/cli/evals/evals_cmd.go:60`). If no
   workspace resolves, informational skip.

**Rejected alternatives.** A dashboard eval-health card is UI work beyond this
spec-only map and can be revisited if doctor is insufficient. API-only health
was rejected because a dead eval loop would remain silently invisible.

## Traces display (LOOMCLI-99)

**Flat list.** The Traces list remains one row per session. It is not grouped by
run. New columns:

- Run -- task-run short id.
- Attempt -- slim dedicated Attempt column.
- Invocation -- Invocation Key.

Run-less daemon sessions render `-` in all three. `task_run_id`,
`invocation_key`, and `attempt` are explicit API fields; the client never parses
session ids. Legacy rows degrade through metadata fallback: Run may display
metadata `task_run_id`, while Attempt and Invocation render `-`.

**Score columns.** The compact eval cell is replaced by one column per score
dimension in both the list and run view. The list response includes
`score_dimensions`, computed server-side over the full filtered range, not just
the returned page. Rows missing a dimension render `-`. Today's eval wire shape
and backend validation remain the canonical four dimensions until the
map-shaped score/rationale schema migration lands; that migration is a
slice-level enabler, not a re-decision.

**Run navigation.** Clicking a run id navigates to
`traces/runs/<taskRunId>` under the workspace route. The workspace fallback
currently redirects unknown routes to kanban, so this route must be added
explicitly. The list filter also gains a `task_run_id` URL parameter with a
clearable `Run: <id>` chip. Clearing the chip returns to plain `/traces`. The
run view and filtered list cross-link with "view in list" and "All traces"
affordances. There is no free-text run filter.

**Run view v1.** Header: run id, task link, run status, attempt count, totals.
Status, tokens, and duration come from the TaskRun row; files changed are summed
over the run's sessions. Below the header is the run's session table, using the
same columns minus Run and ordered:

`attempt ASC, started_at ASC, invocation_key ASC, session_id ASC`

Selecting a session opens the detail pane embedded below the table.

**Detail placement and tabs.** The list view shows no detail by default. Click a
trace to open a closable right-side panel; Expand navigates to the run view with
that session selected. Expand is absent for run-less daemon sessions. The run
view fixes detail below the session table. Detail tabs are ordered:

`Eval | Transcript | Diff | Judge`

Eval is the default for `kind=task`; Transcript is the default for kinds that
are never candidates. There is no layout toggle.

**Degraded states.** TaskRun present with zero sessions -> header plus empty
state. Sessions present but TaskRun missing -> sessions-derived header plus
"task run record missing" notice.

**Judge linkage.** `SessionEval` gains `judge_session_id`
(`internal/domain/session_eval.go`), stamped by the eval agent at write time.
The subject session's Judge tab renders exactly that judge session's transcript,
joined from the displayed eval record. It is disabled when no eval record
exists. The Eval tab cross-links to the Judge tab. On rejudge, the old record is
deleted by existing rejudge semantics; the Judge tab empties with that record
until the new eval lands.

**Judge detail.** A judge session's own detail exposes a forward "Judged
session" link as an explicit detail field, not client-side metadata scraping.
The re-judge button is hidden for `kind=judge`; backend validation already
rejects non-task rejudges. Eval-record routes also live in fleet-db, so
`judge_session_id` lands on both sides. Today's builtin eval judge does not mint
a judge AgentSession; this UI is post-migration.

**Judge rows hidden by default.** The default list applies implicit
`kind != judge`. Selecting Kind=Judge, or a `kind=judge` URL, reveals them. A
judge row appears only in its own eval run's view, which shows that run's
sessions unfiltered, and in the list when the kind filter reveals it. A worker
run's view never contains judge rows. The Judge tab joins cross-run and is
unaffected. The Kind filter adds "Judge" and backend validation accepts it.

**Tags.** Sessions gain optional leaf-declared tags on the descriptor. Traces
shows a Tags column with clickable pills and repeatable AND-composed filter
chips: `?tag=a&tag=b`.

**Rejected alternatives (tags).** Eval `error_taxonomy_tags` as the tag/filter
source was rejected because it requires an eval-record join and only judged
sessions would be findable. The both-sources variant, combining leaf tags with
eval taxonomy tags, was also rejected.

**Timeline deferred.** Timeline visualization is explicitly deferred from this
map.

**Rejected alternatives.** Grouped list with run headers, runs-as-primary rows,
free-text run filter, meta-cells-only and run-strip drill-ins, metadata reverse
query for the judge embed, union-of-loaded-rows score columns, rubric/config
endpoint, hardcoded score columns after the schema migration, and
show-all-kinds-by-default were rejected.

Mock: <https://claude.ai/code/artifact/d50575ea-4f86-4ee4-aa34-b4878efce364>  
Review: `docs/research/traces-multiinvocation-codex-review.md`.

## Sandbox-run agents: the harness invoke form (LOOMCLI-105)

**Daytona uses the harness shape.** The Daytona leaf's agent loop runs in the
host leaf process through Flue `createAgent`; the sandbox is the fs/exec tool
backend. The exec-adapter is not remote-process capture.

**Invoke form.** agent-exec grows an invocation-shaped form around an
in-process prompt call. It opens the session at prompt start, collects
transcript entries from the in-process event collector, reads usage from
structured `response.usage`, uploads the transcript, and closes the session at
prompt end. The reconciler backstop is unchanged.

**One prompt, one session.** Each prompt call gets its own stable Invocation
Key. One prompt equals one Agent Invocation equals one AgentSession. Missing
usage is stamped unknown/null, never zero.

**Ordering.** Session close and transcript upload must complete before TaskRun
completion because taskrunapi lease verification rejects terminal runs.

**No deterministic sandbox sessions.** Clone, checkout, diff, and other
deterministic sandbox commands never open sessions. This is the LOOMCLI-106
contract obligation satisfied by construction.

**Honest trade.** Harness trades pull's sandbox-loss window for host-memory
loss: transcript entries live in host memory until the prompt returns. Leaf
crash or OOM mid-prompt loses them. This is accepted under the crash-lossy
usage/transcript stance; the reconciler stamps `failed` /
`agent_session_unclosed`.

**Pull rejected.** Pull is not logically impossible, but durable in-sandbox
upload needs a taskrunapi credential the Daytona runner deliberately treats as a
leak. Without that credential it accepts evidence-loss or failed-close
semantics. That trade is rejected.

**Proxy deferred.** Proxy is the right shape only if a future leaf truly runs an
agent CLI inside a sandbox. It remains additive later through the process form.

**Prototype pointer.** The logic prototype is captured on branch
`prototype/loomcli-105-exec-adapter`, commit `65b8e605`. Run
`node sdk/prototypes/exec-adapter/demo.mjs --tour` on that branch. The open
implementation slice is LOOMCLI-136.

Review: `docs/research/exec-adapter-codex-review.md`.

## Migration & rollout (LOOMCLI-98)

**Hard cutover.** `startFlueTaskSession` and its finalize/heartbeat/artifact
stamping machinery (`internal/driver/task_bridge_session.go`) are deleted.
Sessions exist only where a leaf calls agent-exec. There is no manifest
capability flag and no runtime fallback mint.

**Why no fallback.** A finish-time fallback cannot distinguish an un-migrated
leaf from a helper-adopted leaf that deliberately opened no session. It would
resurrect the preflight noise this map exists to kill.

**Un-adopted leaves.** Leaves not migrated to agent-exec, such as openshell or
custom runners, produce no AgentSessions. Their runs remain visible as TaskRuns
and driver runs.

**One landing.** The three prototype-validated leaves migrate and the legacy
mint is deleted as a single merged unit:

- `session-eval-task-runner` / eval workflows, with `kind=judge` for judge
  invocations and no session for preflight.
- `local-task-runner`.
- `github-review-task-runner`.

No interim dual state, duplicate sessions, capability flag, skip-list, or
assertion churn lands.

**Daytona is in scope, not gating.** Daytona tolerates a temporary session gap
between the hard cutover and LOOMCLI-136. That gap closes when the harness
invoke form lands.

**verify-evals assertions updated in the same landing.**

- Judge sessions carry `kind=judge`.
- Preflight TaskRuns produce no sessions.
- No legacy unsuffixed `flue-<taskRunID>` session ids exist post-landing.
- `eval_cost.total_tokens > 0` on the codex flow; the plain flow stays
  type-only.

**Existing checks that survive.** `find_completed_task_session_with_ref` and
verify-local-mode's per-task session/transcript checks survive unchanged
because they match on status/kind, not id shape.

**Doctor transcript_ref check unchanged.** `transcript_ref_backfill` remains a
daemon-plane repair. It only fires when a session id resolves in a local
`sessions.Store` with a native transcript file on disk
(`doctor_checks_transcript_ref.go` `localNativeTranscript`). Flue sessions
missing `transcript_ref` are crash-lossy by contract and not doctor-repairable;
the check stays task-scoped per LOOMCLI-94.

## Implementation slices

1. **AgentSession identity, filters, and schema base.**  
   Scope: add first-class `task_run_id` and `invocation_key`; expose
   `task_run_id` filter; add Attempt and non-terminal filtering needed by the
   reconciler; add `kind=judge`; add leaf-declared tags; keep legacy metadata
   fallback for old rows.  
   Depends on: none.  
   Implements: Identity, judge kind, Traces flat-list data source, reconciler
   store authority.  
   Repos: fleet-db sibling and loomcli.

2. **Session lifecycle primitives and first-terminal CAS.**  
   Scope: store-level `Open(descriptor)` and `Finalize(sessionRef, outcome)`;
   server-composed ids; identical-descriptor idempotency; conflicting
   descriptor structured error; first-terminal-wins close guard; terminal-run
   open rejection; transcript artifact reference shape.  
   Depends on: slice 1.  
   Implements: agent-exec contract, Attempt pinning, reconciler CAS
   prerequisite.  
   Repos: fleet-db sibling and loomcli.

3. **taskrunapi session ops and SDK process form.**  
   Scope: lease-authenticated `session-open` / `session-close`; TaskRunClient
   agent-exec process form; open retry/degrade behavior; auto/deferred close;
   helper-owned transcript capture/redaction/upload and usage extraction;
   descriptor fields `kind`, `tags`, `metadata`.  
   Depends on: slice 2.  
   Implements: LOOMCLI-87/88 process contract and judge/tag descriptor
   amendments.  
   Repos: loomcli.

4. **Session reconciler and backstops.**  
   Scope: `TaskRunSessionReconciler`; in-process open callback registry;
   store-query finish reconciliation; serve-side reconciliation loop;
   same-pass stale hook; transcript_partial salvage; `finishErr` promotion
   removal; bridge-path self-complete prohibition; `unclosed_sessions`
   breadcrumb.  
   Depends on: slices 1-3.  
   Implements: Session reconciler.  
   Repos: loomcli, with store/filter support from fleet-db already in slice 1.

5. **One-landing flue cutover.**  
   Scope: migrate `session-eval-task-runner`, `local-task-runner`, and
   `github-review-task-runner` to agent-exec; delete
   `startFlueTaskSession`/`finishFlueTaskSession` machinery; remove legacy
   bridge minting; update `test/local-mode/verify-evals.sh` assertions; keep
   doctor transcript_ref behavior unchanged. This is one slice by LOOMCLI-98
   constraint.  
   Depends on: slices 1-4.  
   Implements: Migration & rollout, no-marker truth condition, preflight noise
   removal, judge transcript creation, eval_cost nonzero regression guard.  
   Repos: loomcli.

6. **Doctor `eval_loop` check.**  
   Scope: add the data-plane doctor check using trigger binding and
   `session-eval-agent` DriverRuns; implement stale, tiered error-class, and
   informational skip semantics.  
   Depends on: v1 eval infrastructure; independent of the cutover except for
   the operator-surface motivation.  
   Implements: Preflight observability.  
   Repos: loomcli.

7. **Traces backend and eval schema enablers.**  
   Scope: expose explicit run/attempt/invocation fields; implement
   `score_dimensions` over the full filtered range; add the map-shaped
   score/rationale schema migration required for true dynamic dimensions;
   add `SessionEval.judge_session_id`; add run-view data endpoint and
   `task_run_id` URL/API filter behavior.  
   Depends on: slice 1 for session identity; slice 5 for post-migration judge
   sessions to exist, though the backend can degrade legacy rows.  
   Implements: Traces list, score columns, run navigation, judge linkage.  
   Repos: fleet-db sibling and loomcli.

8. **Traces frontend.**  
   Scope: flat list columns; score dimension columns; run route
   `traces/runs/<taskRunId>`; list chip; right-side list detail; embedded run
   detail; Eval/Transcript/Diff/Judge tabs; judge hidden default and Kind=Judge
   reveal; tag column and AND filters; degraded states.  
   Depends on: slice 7.  
   Implements: Traces display.  
   Repos: loomcli.

9. **LOOMCLI-136 Daytona invoke-form migration (already open).**  
   Scope: implement the disjoint invoke form; migrate
   `internal/workflows/builtin/daytona-task-runner.ts`; test deterministic
   sandbox commands opening no session, one prompt equals one Invocation Key,
   missing usage remains unknown, and upload-failure behavior.  
   Depends on: the real taskrunapi session ops and CAS from slices 2-3; follows
   the hard cutover and closes the temporary Daytona session gap.  
   Implements: Sandbox-run agents.  
   Repos: loomcli.

## Out of scope

**Daemon-plane migration.** The daemon supervisor can adopt the store primitive
later, but this map does not unify it. **Revisit trigger:** a second
daemon/flue divergence bug; the supervisor finalize-ordering transcript bug of
2026-07-19 was the first.

**Retention / TTL.** Session accumulation remains a fleet-db platform effort.
This map removes the largest noise source but does not build retention.

**v1 shipping interaction.** v1 agent observability ships first and unchanged.
Only the migration slice updates v1 e2e assertions and notes the unchanged
doctor transcript_ref check.

**Deferred items from resolutions.** Timeline visualization, a session
checkpoint/report op, proxy adapter, judge re-inclusion in eval candidates or
score-dimension rollups, dashboard eval-health card, API-only eval-health
surface, spend ledger, and automatic transcript repair for crash-lossy flue
sessions remain out of this spec unless their revisit triggers fire.

## Decision log index

| Ticket | Where recorded here | Research / review source |
| --- | --- | --- |
| LOOMCLI-86 | Header, invariant, motivating defects, scope, migration, out of scope | LOOMCLI-86 map description (tracker) |
| LOOMCLI-87 | Agent-exec contract, session reconciler, implementation slices | LOOMCLI-87 resolution comment; feeds LOOMCLI-88/97/98 |
| LOOMCLI-88 | Agent-exec process helper, migration first movers, implementation slices | LOOMCLI-88 resolution comment; prototype `prototype/agent-exec-loomcli-88` commit `44f66012` |
| LOOMCLI-89 | Attempt semantics, identity/linkage/metadata, Traces data fields | `CONTEXT.md:71-104`; `docs/research/flue-reentry-sessions.md` |
| LOOMCLI-91 | Placement & connectivity, agent-exec auth surface, Daytona harness | `docs/research/remote-leaf-connectivity.md` |
| LOOMCLI-92 | Attempt & durability semantics, shared-session defect, artifact identity | `docs/research/flue-reentry-sessions.md` |
| LOOMCLI-94 | Session kinds & judge exclusion, Traces judge linkage, migration assertions | LOOMCLI-94 resolution comment |
| LOOMCLI-96 | Preflight observability and doctor `eval_loop` | LOOMCLI-96 resolution comment |
| LOOMCLI-97 | Session reconciler, CAS/race constraints, implementation slices | `docs/research/session-reconciler-codex-review.md` |
| LOOMCLI-98 | Migration & rollout, verify-evals assertion changes, doctor inertness | LOOMCLI-98 resolution comment |
| LOOMCLI-99 | Traces display, score schema enabler, judge tabs, tags | `docs/research/traces-multiinvocation-codex-review.md`; mock artifact link above |
| LOOMCLI-105 | Invariant sharpening, disjoint invoke form, Daytona harness | `docs/research/exec-adapter-codex-review.md`; prototype branch `prototype/loomcli-105-exec-adapter` commit `65b8e605` |
| LOOMCLI-106 | No agentic marker, agent-invocation-only obligation, fixture carve-out | `docs/research/agentic-marker-codex-review.md`; `CONTEXT.md:73-83` |
| LOOMCLI-136 | Open Daytona implementation slice reference | LOOMCLI-105 resolution; `docs/research/exec-adapter-codex-review.md` |
