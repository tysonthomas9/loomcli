# Flue durable re-runs, resume, and retries — what they mean for agent sessions opened by a step attempt

Research for LOOMCLI-92 (wayfinder). Read-only survey of:

- flue runtime: `/Users/tyson/codebase/code-agents/agent-traces/flue/packages/runtime`
- loom durability store: `internal/workflows/builtin/flue-durability-store.mjs` (+ contract spec)
- fleet-db (branch `agent-traces-v2`): `internal/api/platform.go`, `internal/storage/platform.go`, `internal/storage/task_run_claim.go`, `internal/models/platform.go`
- loomcli driver: `internal/driver/` (task bridge, retry, sweeper, events)

All paths below are absolute or repo-relative to those roots; line numbers are from the working trees on 2026-07-19.

---

## 1. Facts

### 1.1 Flue has TWO different durability models: agent submissions (resumable) and workflow runs (NOT resumable)

**A "workflow" in flue is `agent + action`; the action's `run(context)` is plain user code.**
- `flue/packages/runtime/src/workflow-definition.ts:18-22, 54-85` — `defineWorkflow({ agent, action | run })`.
- `flue/packages/runtime/src/action.ts:126-147` — `runActionWithParsedInput` simply awaits `action.run(runContext)`. There is no step journal inside the action body; nothing memoizes intermediate results of the `run` function.

**Workflow-run persistence is one record per run, not a step journal.**
- `flue/packages/runtime/src/runtime/run-store.ts:5-16, 106-130` — `RunRecord {runId, workflowName, status active|completed|errored, startedAt, input, result?, error?}`; `createRun` is idempotent first-writer-wins (`INSERT OR IGNORE`, run-store.ts:107-115), `endRun` finalizes.

**An interrupted flue workflow run is FAILED on recovery — the action `run` code is never re-executed and never "replayed".**
- Cloudflare: `flue/packages/cli/src/lib/build-plugin-cloudflare.ts:386-399` — `handleFlueWorkflowFiberRecovered` calls `failRecoveredRun` with error `"Flue workflow execution was interrupted. Start a new workflow run explicitly if retry is appropriate."`
- `flue/packages/runtime/src/runtime/handle-agent.ts:390-439` — `failRecoveredRun` reconciles the run record/event stream (idempotent `createRun(recovery)`, `emitRunResume`, then `emitRunEnd({isError: true})`). It repairs bookkeeping; it does not restart user code.
- Node one-shot (what loom uses, §1.3): the process is single-invocation; a crash mid-run just loses the run (no resume machinery exists on that path).

**Agent submissions (prompts/dispatches into an agent session) are the durable, resumable unit.**
- Store contract: `flue/packages/runtime/src/agent-execution-store.ts`.
  - `AgentSubmission` (lines 32-50): `sequence, submissionId, sessionKey, kind dispatch|direct, input, status queued|running|settled, acceptedAt, attemptId?, inputAppliedAt?, startedAt?, error?, attemptCount, maxRetry, timeoutAt, ownerId?, leaseExpiresAt`.
  - Durability defaults (lines 21-26): `DURABILITY_DEFAULT_MAX_ATTEMPTS = 10`, timeout 1h, `LEASE_DURATION_MS = 30_000`.
  - `claimSubmission` (lines 245-256): atomic CAS queued→running, records `attemptId`, `ownerId`, `leaseExpiresAt`, **increments `attemptCount`**.
  - Per-submission **turn journal** (`AgentTurnJournal`, lines 100-117): `{submissionId, sessionKey, kind, attemptId, operationId, turnId, phase, revision, checkpointLeafId?, toolRequest?, streamKey?, streamConsumedAt?, committed, committedLeafId?}`. Phases: `before_provider → provider_started → tool_request_recorded → committed` (lines 94-98). One journal slot per submission, replaced in place per turn with `revision` increasing (`beginTurnJournal`, lines 169-175).
  - `replaceTurnJournalAttempt(attempt, nextAttemptId, lease?)` (lines 203-215): the recovery handoff — atomically moves a running submission and its uncommitted journal to a NEW attemptId, increments `attemptCount`, installs the new lease.
  - Leases: `renewLeases` heartbeat, `listExpiredSubmissions` (lines 300-312). Attempt markers (`insertAttemptMarker` etc., lines 289-298) are durable "attempt may still be live" evidence.
- Journal write points: `flue/packages/runtime/src/runtime/agent-submissions.ts:229-275` (`createSubmissionJournalCallbacks`), fed from `session.ts:446-470` (`emitTurnRequestAndStream` writes `before_provider`/`provider_started` with `streamKey = "${submissionId}:${turnId}:${attemptId}"`, session.ts:449-452).

### 1.2 Flue resume semantics: journaled-result replay vs. re-execution

- **Node coordinator** (`flue/packages/runtime/src/node/agent-coordinator.ts`):
  - Claim loop claims runnable submissions with `attemptId: crypto.randomUUID()` and a 30s lease (lines 246-261); heartbeat renews every 10s (lines 333-346); a periodic lease scan (15s) plus a startup `reconcileSubmissions()` (lines 373-378, 487-498) reconcile expired-lease submissions from a dead process.
- **Reconciliation decision tree** (`flue/packages/runtime/src/runtime/agent-submissions.ts:305-548`, `reconcileInterruptedSubmission`):
  1. Inspect persisted session state first. If the canonical response completed, **settle as success and return the persisted result — no provider work replays** (lines 316-349; `reconstructSubmissionResult` in `session.ts:747-775` rebuilds the PromptResponse "without replaying any provider work").
  2. Retry budget: `attemptCount >= maxRetry` → terminal failure (`SubmissionRetryExhaustedError` / interrupted-before-input variant, lines 350-379). Timeout → terminal (lines 381-394).
  3. Interrupted mid-stream (`provider_started`, uncommitted, stream not consumed): recover partial output from durable stream chunks, then claim a **replacement attempt** (new random attemptId) via `replaceTurnJournalAttempt` (lines 417-440).
  4. Provider unreached (`journal === null` or phase `before_provider`): "nothing observable happened, so a retry is safe" — replacement attempt (lines 441-471).
  5. Interrupted mid-tool-batch (`tool_request_recorded`): repair — preserve every recorded tool result, synthesize interrupted-markers for unresolved calls — then replacement attempt (lines 473-503). Explicit invariant: "completed calls are never re-executed" (lines 453-458; also `submission-state.ts:44-48` and `session.ts:2442-2449`).
  6. Otherwise terminalize with `SubmissionInterruptedError`, recording a `submission_interrupted` advisory in session history (lines 505-548, 737-782).
- **Resume classification** is a pure function over persisted session history: `flue/packages/runtime/src/submission-state.ts:110-206` (`classifySubmissionState`) with resume modes `input_only | tool_results | tool_results_partial | stream_continuation | transient_retry | overflow | aborted_partial` (lines 63-70).
- **What re-executes on resume**: `session.ts:2374-2488` (`runPersistedContextInput`) — `completed` (non-overflow) settles from history with no model call (line 2441); `resume` re-drives the agent loop *from the last durable message* (`runModelTurnWithRecovery` → `agentLoop.continue()`, lines 2447-2458). So an in-flight LLM turn IS re-invoked (a new provider call), but committed turns, recorded tool results, and completed responses are replayed from the store, never re-run.
- Idempotent admission: dispatch replay with the same id+payload returns the original submission/receipt; conflicting replay errors (`agent-execution-store.ts:229-242`; node/agent-coordinator.ts:66-90).

### 1.3 How loom embeds flue: one fresh flue process + fresh flue workflow-run per step attempt

- Every builtin loom runner (local-task-runner, daytona, github-review task runner, ...) is registered with `RunnerKindFlueWorkflow = "flue-workflow"` (`loomcli/internal/driver/register.go:27`) and executed by the bundled runner: `bundled_runner.go:110-119` sets `LOOM_TASK_RUNNER_KIND=flue-workflow`, and the host bridge launcher forks the built flue server in **one-shot IPC mode** (`task_bridge.go:584-599`: `FLUE_MODE=local`, `FLUE_CLI_TARGET='workflow'`, `FLUE_INTERNAL_CLI_IPC='1'`), then sends `{type:'invoke', requestId: request.task_run_id, payload: request}` (`task_bridge.go:629-634`).
- On the flue side, each IPC invoke mints a **brand-new flue workflow runId**: `flue/packages/cli/src/lib/build-plugin-node.ts:341-363` — "Local workflow execution accepts one invocation only", `const runId = generateWorkflowRunId()`. The flue runId is therefore per-invocation and uncorrelated with the loom `taskRunId` except through the request payload/env.
- The runner env carries the loom identity: `task_bridge.go:661-690` (`taskRunnerEnv`) — `LOOM_TASK_RUN_ID`, `LOOM_TASK_ID`, `LOOM_DRIVER_RUN_ID`, `LOOM_DRIVER_STEP_ID`, `LOOM_PARENT_SESSION_ID`, `LOOM_TASK_RUN_NODE_ID`, `LOOM_TASK_RUN_LEASE_ID`, `LOOM_TASK_RUN_LEASE_TOKEN`, `LOOM_TASK_RUN_FENCING_TOKEN`. **No attempt number is passed** (neither in `TaskExecRequest`, task_request.go:81-105, nor the env).
- **Durability store (Phase 0, not yet wired into the runner bundle)**: `loomcli/internal/workflows/builtin/flue-durability-store.mjs`:
  - Wraps flue's `sqlite()` adapter at a host path **keyed by the task-run id** (`taskRunDurabilityPath`, lines 30-39: `<base>/<taskRunId>.sqlite`; id from opts → `LOOM_TASK_RUN_ID` → `LOOM_ASSIGNED_TASK_ID`, lines 46-55).
  - Design intent stated in the header (lines 1-12): "The task-run id is stable across reclaim (internal/driver/task_scheduling.go keeps the id; only the lease token rotates), so a relaunched local runner re-opens the SAME store and flue's reconciler resumes the interrupted submission mid-turn instead of restarting from zero."
  - Verified note (lines 14-20): flue stores are SQL-driver parameterized; a cross-node backend should implement `SqlStorage` over fleet-db Postgres rather than reimplement the ~25 store methods.
  - Contract proof rides flue's own suite (`flue-durability-store.contract.spec.mjs:47-60`, `defineStoreContractTests` from `@flue/runtime/test-utils`). Only consumer today: `scripts/test-flue-durability-store.sh`. `internal/workflows/source_layout.go` does not materialize it as a runner `db.ts` yet.
- The loom **driver-run workflow layer** (the code that *requests* step TaskRuns) has its own re-entry model, distinct from flue's: on await-suspend "the runner must exit; **resume re-runs from the top**" (`loomcli/internal/driver/await_op.go:56-58`), with deterministic replay via dense `awaitIndex` slots (await_op.go:12-15, 38-44), deterministic child-run ids (`composition.go:72-84` — sha256 of `parentRunID + idempotencyKey|start-{n}`), and deterministic TaskRun ids in workflow code (`internal/workflows/builtin/github-review-agent.ts:29-32, 373-375` — `"task-run-" + slug(driverRunId) + "-" + slug(label)`; `epic-runner.ts:50-52, 272-276` — conflicts on re-issue mean "already exists from a previous pass", swallowed via `isConflictError`).

### 1.4 fleet-db / loomcli TaskRun attempt model (Platform v2)

- **TaskRun model has NO first-class attempt column**: `fleet-db/internal/models/platform.go:1102-1141` — identity is `TaskRunID` plus ownership fields `NodeID, LeaseID, FencingToken`, timing `NextEligibleAt, StartedAt, LastHeartbeat, FinishedAt`, and free-form `RuntimeMetadata`.
- **Attempt counter lives in `RuntimeMetadata["scheduler_attempt"]`**, written by loomcli:
  - `loomcli/internal/driver/task_retry.go:37-50` (`taskRunAttempt` reads it), `24-35` (`taskRunRetryDecision`: this attempt = `taskRunAttempt(claimed) + 1`; retry while `attempt < maxAttempts` on failed status), `120-135` (`schedulerMetadata` stamps `scheduler_state` retrying|blocked, `scheduler_attempt`, `scheduler_max_attempts`, last error).
  - Retry is requeue-in-place: `requeueClaimedTaskRun` (task_retry.go:52-74) → fleet-db `RequeueTaskRun` (`fleet-db/internal/storage/platform.go:2206-2260`) keeps the **same TaskRunID**, checks owner (`NodeID/LeaseID/FencingToken`, line 2214) and the lease-token hash (line 2217), returns status to queued with `NextEligibleAt` backoff (1s<<attempt, cap 30s — task_retry.go:89-104).
- **Every claim rotates ownership tokens**: `fleet-db/internal/storage/task_run_claim.go:189-201` (`ApplyTaskRunClaim`) sets `FencingToken = claim.ClaimedAt.UnixNano()` (unique-per-claim, monotonic) and a fresh `LeaseID`; the claim API hashes a caller-supplied lease token per claim (`fleet-db/internal/api/platform.go:2366-2389`; loomcli generates it in `task_scheduling.go:276-282`). Generic lease TTL 5m (`task_run_claim.go:10`).
- **Journal of attempts**: loom serve appends TaskRunEvents with **deterministic EventID `taskRunID#attempt#type`** (`loomcli/internal/domain/outbox.go:31-38`; `internal/driver/task_events.go:29-62`). Event types: claimed/requeued/completed/failed/cancelled. "Append is idempotent on this key, so replaying the same lifecycle transition for the same attempt never produces a duplicate row." The event row carries `Attempt`, `Status`, `SchedulerState`, `LeaseToken`, `NextEligibleAt` (requeue).
- **Stale-run recovery fails, it does not requeue**: `fleet-db/internal/storage/platform.go:1542-1611` (`RecoverStaleTaskRuns`) fails running TaskRuns whose heartbeat predates `staleBefore` (`errorClass=stale_task_run`), fails the linked driver step, and releases the underlying task issue. Driven server-side by `loomcli/internal/driver/stale_task_sweeper.go` (default max age 20m, lines 12-18; same store method as the manual `recover-stale-tasks` op — `internal/cli/driver/task_cmd.go:105`, `internal/infra/fleetdb/platform.go:431`).
- Outbox exactly-once on the completion path uses a status-scoped DedupeKey `"lead-task-message:"+epicID+":"+taskRunID+":"+status` (task_events.go:128-137) — attempt-agnostic.

### 1.5 Agent-session records opened for step attempts today

- **Task-bridge flue session — ONE session per TaskRunID across ALL attempts**: `loomcli/internal/driver/task_bridge_session.go:157-197` (`startFlueTaskSession`). SessionID is `"flue-" + req.TaskRunID` (`flueTaskSessionID`, lines 294-296). On re-entry `AgentSessions().Create` hits `domain.ErrAlreadyExists` (line 176) and the code **updates the existing record back to `running`**, merging metadata (lines 179-193). `finishFlueTaskSession` (lines 216-262) later stamps terminal status/exit code and overwrites `driver_runner_session_id` with the runner's inner session id (lines 225-229). Consequence: a retried step flips the same session record completed/failed → running, and the previous attempt's outcome, metadata, and inner-session linkage are overwritten. The `Attempt` field is NOT set on this path.
- **Session metadata linkage** (task_bridge_session.go:302-325): `task_id`, `task_run_id`, `driver_run_id`, `driver_step_id`, `parent_session_id`, `runner*`, `flue_session`, `flue_harness` — but nothing distinguishing the invocation.
- **AgentSession schema already has an attempt field** on both sides: loomcli `internal/domain/control_plane.go:90-111` (`Attempt int \`json:"attempt,omitempty"\``, line 101) and fleet-db `internal/models/control_plane.go:194-216` (line 206; validated non-negative at 785). Plumbed through `internal/store/control_plane_store.go:59`, `internal/infra/fleetdb/control_plane.go:105,118`, `internal/infra/memstore/control_plane.go:171`.
- **The daemon supervisor already models per-invocation sessions**: `internal/cli/daemon/supervisor/supervisor.go:497-535` — every (re)start of an agent process creates a **new** session (fresh random SessionID via `sessions.GenerateSessionID`, `internal/sessions/id.go:18-30`) with `AttemptNum/Attempt = restartCount`, plus a per-session lease (`sessionID + "-lease"`). This is the existing precedent for "one session record per invocation, attempt as a counter, stable task linkage via TaskID".
- **Artifacts are keyed by TaskRunID only**: `internal/driver/task_bridge_artifacts.go` — `transcript-<taskRunID>` (line 135), `logs-<taskRunID>` (line 158), `artifact-<taskRunID>-<i>` (line 43), `patch-<taskRunID>` (task_bridge.go:858), owner `task_run/<taskRunID>` (lines 90-91), `ArtifactsRef = "artifacts://" + taskRunID` (line 62). Create swallows `ErrAlreadyExists` and Finalize overwrites URI/hash (lines 99-115), so a retry's transcript silently replaces the previous attempt's.
- **Runner-side sessions**: `local-task-runner.ts` spawns a cold CLI process per invocation — "`--resume` is owned by the durability/resume path and is added there once session carry-forward exists — it is not part of a cold local run" (`internal/workflows/builtin/local-task-runner.ts:459-461`).
- **Flue-side session identity** (relevant once the durability store is wired): sessions are stored under `agent-session:["instanceId","harness","session"]` (`flue/packages/runtime/src/session-identity.ts:31-37`); external submissions always target the default session of the default harness (`agent-submissions.ts:819-832`, `SUBMISSION_SESSION_NAME = 'default'`, `adapter-helpers.ts:32`).

---

## 2. Answers to the sub-questions

### Q1 — On resume/retry, does step code re-execute or is the journaled result replayed?

Layered answer; both happen, at different granularities:

1. **Loom driver-run workflow code (epic-runner, github-review-agent, ...): always re-executes from the top** on resume (await_op.go:56-58). Effects are made idempotent, not journaled: deterministic TaskRun ids + conflict-swallow for enqueues, deterministic child-run ids for `workflows/start`, awaitIndex-slot replay for awaits (§1.3). A re-entered workflow re-*issues* everything and the store deduplicates.
2. **A step's TaskRun attempt: full re-execution.** Retry/requeue returns the same TaskRunID to queued; the next claim spawns a fresh runner process → fresh flue one-shot process → fresh flue workflow runId → (today) fresh cold agent CLI process. Nothing about the previous attempt's execution is replayed into the new one (no journaled result exists at this layer beyond the TaskRunEvent lifecycle journal).
3. **Inside flue (agent submissions): journaled-result replay first, bounded re-execution only for the interrupted tail.** A submission whose canonical response completed settles from the store with zero provider work (`reconstructSubmissionResult`); an interrupted submission resumes mid-turn — only the un-committed turn's provider call re-runs, completed tool calls are never re-executed (repair batch), partial streams are recovered from durable chunks. This only helps across process restarts **if the same store file is reopened** — which is exactly what the Phase-0 durability store (keyed by reclaim-stable taskRunId) provides, and which the default (`:memory:`/no db.ts) runner does not.
4. **Flue's own workflow-run function is neither replayed nor resumed**: an interrupted flue workflow run is terminally failed on recovery ("Start a new workflow run explicitly if retry is appropriate"). Loom's retry-by-requeue *is* that explicit new run.

### Q2 — How are attempts/executions of the same TaskRun distinguished?

- **fleet-db TaskRun row**: not distinguished structurally. Same `TaskRunID`; each claim installs a fresh `LeaseID`, fresh lease token (hash), and a unique monotonically-increasing `FencingToken = ClaimedAt.UnixNano()`. `RuntimeMetadata["scheduler_attempt"]` / `"scheduler_max_attempts"` / `"scheduler_state"` carry the counter (written at requeue/block time by loomcli; a claimed run's current attempt number is `scheduler_attempt + 1`).
- **TaskRunEvent journal**: attempts are first-class in the deterministic EventID `taskRunID#attempt#type`, one idempotent row per (attempt, lifecycle transition), carrying attempt, status, lease token, and (for requeues) NextEligibleAt.
- **Stale recovery**: recover-stale terminally fails the run (attempt distinguishable in the journal by the `taskRunFailed` event for that attempt with `errorClass=stale_task_run`); the sweeper, not workflows, owns this.
- **flue submissions**: attempts are fully first-class — `attemptId` (fresh UUID per claim and per reconciliation replacement), `attemptCount` vs `maxRetry` budget, `ownerId` + `leaseExpiresAt`, attempt markers, and the turn journal binding `(submissionId, attemptId, operationId, turnId, revision)` with per-attempt stream keys `submissionId:turnId:attemptId`. Ownership transfer is an atomic CAS (`replaceTurnJournalAttempt`); settlement CAS is first-terminal-wins.
- **flue workflow runs**: distinguished trivially — every invocation is a new runId (loom Node path: `generateWorkflowRunId()` per IPC invoke).

### Q3 — What identity/linkage do per-invocation agent sessions need to stay truthful across re-entry?

The current failure mode is concrete: `flue-<taskRunID>` is reused and mutated across attempts (§1.5), so one logical step yields either a single overwritten session (today's bridge) or, if the id were made random, ambiguous duplicates with no way to tell "retry of the same step" from "different step". To stay truthful a per-invocation session needs, simultaneously:

1. **A stable step key**: `TaskRunID` (reclaim-stable by design; the durability store and artifacts already key on it) plus `TaskID`, `DriverRunID`, `DriverStepID` for lineage.
2. **An invocation discriminator that matches the server's own attempt accounting**: the attempt ordinal (`scheduler_attempt`-derived, i.e. the same number the TaskRunEvent journal uses) is the human-aligned choice — the session then joins 1:1 against `taskRunID#attempt#*` journal rows. The `AgentSession.Attempt` field already exists end-to-end (loomcli domain, store plumbing, fleet-db model) and the supervisor already uses exactly this pattern (new SessionID + `Attempt = restartCount`).
3. **A collision-proof fencing witness**: the attempt ordinal alone is derivable but is written server-side at requeue time and is not currently delivered to the executor/runner (`TaskExecRequest` and `taskRunnerEnv` carry lease/fencing but no attempt). Two options, not mutually exclusive:
   - plumb the attempt into `TaskExecRequest`/`LOOM_TASK_RUN_ATTEMPT` (cheap: the bridge already has the claimed run in hand — `taskRunAttempt(claimed)+1`);
   - or use `FencingToken` (already in the request/env, unique per claim by construction) as the invocation witness, keeping the ordinal as display metadata. Flue's precedent is both: `attemptCount` for budget/ordinal + random `attemptId` for ownership.
4. **Deterministic per-invocation session id** so re-entry *within* one attempt (bridge crash between Create and Update, executor retry of the same claim) stays idempotent while distinct attempts get distinct records: e.g. `flue-<taskRunID>-a<attempt>` (or `…-<fencingToken>`). This preserves the existing ErrAlreadyExists-tolerant Create pattern but scopes it correctly. The prior attempt's session then keeps its terminal status/summary/exit code instead of being flipped back to running.
5. **Cross-layer linkage metadata**, so the session can be joined to what actually ran:
   - loom side: `lease_id`, `fencing_token`, `scheduler_attempt` in session metadata;
   - flue side (once durability lands): the flue store `sessionKey` (`agent-session:[instanceId, harness, session]`), flue workflow `runId` of the invocation, and — for submissions — `submissionId`/`attemptId`, since one loom step attempt maps to a fresh flue runId but potentially the SAME resumed flue submission (that is the whole point of the taskRunId-keyed store). A truthful model is: *flue session history is per-step (shared across attempts); loom AgentSessions are per-invocation and each records which flue attempt/run it hosted.*
   - runner side: `driver_runner_session_id` (inner CLI session) must be recorded per invocation, not overwritten on one shared record.
6. **Per-attempt artifact identity** for the same reason: `transcript-<taskRunID>` / `logs-<taskRunID>` currently make the last attempt silently win (Create ignores AlreadyExists, Finalize overwrites). Per-invocation sessions are only truthful if their transcript refs are also per-invocation (`transcript-<taskRunID>-a<attempt>`), or if attempt-overwrite is an explicit, recorded policy.
7. **A staleness terminalization path per invocation**: sessions opened by an attempt that dies must not stay `running` forever. The bridge already heartbeats the session while alive (task_bridge_session.go:194-213) and the control-plane model has `LastHeartbeat` + `expired` status; a per-invocation session left stale should be expired by the same server-side sweep philosophy as stale TaskRuns (fault policy is not workflow code — stale_task_sweeper.go:25-29), fenced by the invocation's lease/fencing token so a live retry can never be clobbered by the sweeper of a dead one.

Constraint worth flagging: attempts are only counted for the requeue path. `RecoverStaleTaskRuns` fails runs terminally (no attempt increment path), and quarantine/park flows sit above this — so "attempt ordinal" must be defined as "per claim of this TaskRunID" (fencing-token ordering), with `scheduler_attempt` as the retry-loop projection, if sessions are to be unambiguous under every recovery path.
