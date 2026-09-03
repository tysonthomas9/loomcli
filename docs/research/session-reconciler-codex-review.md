# Codex adversarial review: session reconciler (LOOMCLI-97)

> Second-opinion review by OpenAI Codex CLI (gpt-5.5, xhigh, explore mode) of the
> LOOMCLI-97 decisions, run 2026-07-20 during the wayfinder grilling session.
> Prompt covered the six draft decisions + directed race/ordering/naming questions.
> Verdicts were adjudicated by Tyson: registry kept hybrid in v1 (overriding the
> deferral advice), loop+stale-hook both in v1, self-complete forbidden on bridge
> paths, salvage kept narrowly scoped. See LOOMCLI-97 resolution comment.

**Verdicts**
1. Discovery/registry: **SOUND-WITH-CHANGES**. Store-as-truth is right; the callback registry should be optional, not part of correctness. The store lacks `TaskRunID`, attempt/fence, non-terminal filters, and terminal CAS today (`internal/store/control_plane_store.go:64-109`).
2. Stamp mapping: **SOUND-WITH-CHANGES**. The mappings are defensible, especially “never fabricate completed”, but they require first-terminal-wins support that does not exist in `AgentSessions().Update` today (`internal/infra/memstore/control_plane.go:236-275`).
3. Run outcome untouched: **SOUND-WITH-CHANGES**. Do not demote the run, but the `RuntimeMetadata` breadcrumb only works on the normal bridge path before `Finish`/`Complete`; terminal runs reject later completion/finish updates (`internal/driver/task_request.go:654-687`, `internal/infra/memstore/platform_task_run.go:297-346`, `internal/infra/memstore/platform_task_run.go:428-461`).
4. Synchronous best-effort finalizer: **SOUND-WITH-CHANGES**. The ordering holds for normal `ExecuteTask` returns, but not for leaf `complete`, bridge crash, or hard cancellation. Also today `finishErr` can become the task error, contrary to this decision (`internal/driver/task_bridge.go:249-258`).
5. Server-side sweep: **FLAWED as stated**. A stale-run hook fits the stale path, but “one sweep” does not cover already-terminal runs because `StaleTaskSweeper` only visits running driver runs (`internal/driver/stale_task_sweeper.go:71-92`).
6. Transcript salvage: **SOUND-WITH-CHANGES**. Keep only as narrow best-effort for “uploaded but not finalized”; it does not salvage mid-agent crashes because upload is one PUT after process exit in the prototype (`sdk/prototypes/agent-exec/agent-exec.mjs:131-143`, `internal/webui/handlers/taskrunapi/artifacts.go:225-255`).

**Per-Decision Detail**
1. The authority must be the store. The current taskrunapi has no session ops yet, only `get`, `heartbeat`, `log-append`, `complete`, credential, and artifact ops (`internal/webui/handlers/taskrunapi/module.go:105-116`). The current `AgentSessionFilter` cannot list by `task_run_id` or `attempt`, and only supports one `Status` value (`internal/store/control_plane_store.go:64-86`). If session identity includes attempt, the store must expose attempt/fencing filters or the reconciler risks touching the wrong retry attempt; the re-entry notes say TaskRun has no first-class attempt column and attempt currently lives in runtime metadata (`docs/research/flue-reentry-sessions.md:67-75`).

2. The mapping is directionally right because current eval selection consumes terminal task sessions with transcript refs (`docs/design/agent-observability.md:76-84`, `internal/evals/evals.go:198-220`). Synthetic `completed` would pollute that pool. The missing piece is CAS: `AgentSessionUpdate` has no expected-status or terminal guard (`internal/store/control_plane_store.go:88-99`), and memstore update is last-writer-wins (`internal/infra/memstore/control_plane.go:236-275`). Leaf-closed sessions are only safe if `session-close` becomes a real first-terminal-wins operation.

3. “Run outcome untouched” matches the worker flow: `ExecuteTask` returns first, then the worker completes, requeues, or finishes the TaskRun (`internal/driver/task_request.go:654-687`). But a finalizer can only inject `unclosed_sessions` into `TaskExecResult.RuntimeMetadata` before that terminal write. Store `Finish` and `Complete` replace runtime metadata during terminal transition (`internal/infra/memstore/platform_task_run.go:334`, `internal/infra/memstore/platform_task_run.go:506-525`), and terminal runs reject later finish/complete updates (`internal/infra/memstore/platform_task_run.go:310-312`, `internal/infra/memstore/platform_task_run.go:443-445`). If the leaf uses `complete`, the breadcrumb needs a separate fenced metadata patch op or it must be omitted.

4. The claimed ordering is true only on the main bridge path: `ExecuteTask` defers `finishFlueTaskSession`, and the worker persists the TaskRun result only after `ExecuteTask` returns (`internal/driver/task_bridge.go:249-258`, `internal/driver/task_request.go:654-687`). It is false when a leaf calls taskrunapi `complete`, which terminalizes through `TaskRuns().Complete` before the bridge returns (`internal/webui/handlers/taskrunapi/module.go:447-464`, `sdk/runner.js:309-355`). Cancellation is mixed: the embedded runner maps `SIGINT`/`SIGTERM` to cancelled 130/143 (`internal/driver/task_bridge.go:653-658`), but a bridge process crash skips defers entirely, so the sweep is mandatory.

5. The stale-run part belongs near `StaleTaskSweeper`, which is explicitly serve-side fault recovery and runs whenever serve has a store (`internal/cli/serve/serve_loops.go:25-47`). But the current sweeper lists only running driver runs, then calls `RecoverStaleTaskRuns` (`internal/driver/stale_task_sweeper.go:71-92`). That cannot catch “parent run already terminal, finalizer missed”. Split this into two mechanisms: a stale-run recovery hook for recovered TaskRun IDs, and an independent session reconciler that lists non-terminal task sessions, loads their parent TaskRun, and settles sessions whose parent is already terminal.

6. Salvage is only worth keeping if it is cheap and explicitly scoped. The artifact content route reads a single PUT body and uploads it after lease verification (`internal/webui/handlers/taskrunapi/artifacts.go:225-255`), while the prototype declares, uploads, finalizes, then closes after the agent process exits (`sdk/prototypes/agent-exec/agent-exec.mjs:131-178`). Also finalize validates content exists in memstore (`internal/infra/memstore/terminal_artifact.go:256-271`). So this only helps the crash-between-upload-and-close/finalize window, not the common mid-agent crash window.

**A. Race Windows**
- `session-open` in flight while bridge queries sessions: real. If the open commits after the query but before TaskRun terminalization, the finalizer misses it. The backstop catches it; tighter fencing requires `session-open` to verify the current lease/fence (`internal/webui/handlers/taskrunapi/module.go:248-260`).
- `session-close` racing finalizer stamp: real but closed by future CAS. Current store is unsafe because `Update` is last-writer-wins (`internal/infra/memstore/control_plane.go:236-275`).
- Leaf close after finalizer stamped failed: CAS makes this a no-op/conflict. Correctness depends on only running the bridge reconciler after the runner process has exited (`internal/driver/task_bridge.go:261-302`).
- Leaf `complete` before leaf `session-close`: real. `complete` can terminalize the TaskRun via taskrunapi (`internal/webui/handlers/taskrunapi/module.go:447-464`), so a later session-close must either be allowed against the same fenced invocation or be left to the reconciler.
- Retry attempt N+1 while attempt N reconciliation runs: dangerous unless every query/stamp is fenced by attempt/fencing token. TaskRun claim rotates fencing on each claim (`internal/infra/memstore/platform_task_run.go:249-260`), while AgentSession listing cannot filter by attempt today (`internal/store/control_plane_store.go:64-86`).
- Stale sweep racing bridge finalizer: acceptable only with CAS; both writers may stamp the same non-terminal session.
- Artifact upload/finalize racing transcript salvage: real. The finalizer should use store-level artifact state, not taskrunapi, because taskrunapi artifact ops verify the TaskRun lease (`internal/webui/handlers/taskrunapi/artifacts.go:87-133`, `internal/webui/handlers/taskrunapi/artifacts.go:197-223`).

**B. Hybrid Registry**
Argument for: the main serve path constructs the bridge executor in-process, so a callback can provide low-latency live visibility without polling (`internal/webui/handlers/driverapi/module.go:575-608`).

Argument against: standalone exec paths construct the bridge executor outside serve and do not set `APIBaseURL`, so an in-process callback is not generally available (`internal/cli/driver/exec_cmd.go:120-165`, `internal/cli/driver/exec_cmd.go:180-225`). It also adds a second lifecycle cache while correctness still requires store queries.

Pick: defer the registry. Build store query + CAS first. Add callback later only as an event/cache for live UI latency.

**C. Ordering**
Normal bridge path: yes, synchronous finalization before TaskRun terminal persistence holds because `ExecuteTask` returns before the worker calls complete/requeue/finish (`internal/driver/task_request.go:654-687`).

Exceptions: leaf `complete` terminalizes early (`internal/webui/handlers/taskrunapi/module.go:447-464`); bridge crash skips defer; hard cancellation may bypass the JS cancelled result. Also change today’s behavior where session finish failure can become the execution error (`internal/driver/task_bridge.go:254-258`).

**D. Sweep Placement**
Do not put all of this inside `RecoverStaleTaskRuns`. The current sweeper is a running-driver-run recovery loop (`internal/driver/stale_task_sweeper.go:71-92`), and the store recovery result only returns counts and TaskRun IDs (`internal/store/platform_store.go:484-516`). Use that path for stale-dead runs, but add a separate session reconciler for terminal-parent cleanup.

**E. Daemon Adoptability**
No fundamental conflict. The daemon supervisor already creates one session per process start with an attempt number (`internal/cli/daemon/supervisor/supervisor.go:480-545`) and has startup orphan/stale cleanup precedent (`internal/cli/daemon/supervisor/supervisor.go:147-170`, `internal/sessions/stale.go:41-60`). The caution is naming and API shape: make this a reusable session reconciler/CAS operation, not a bridge-only finalizer.

**F. Naming**
Best fit: `TaskRunSessionReconciler`.

Other acceptable names:
- `SessionOutcomeReconciler`
- `RunSessionReconciler`
- `UnclosedSessionCloser`
- `TaskRunSessionSettler`

Avoid `finalizer`: artifacts already use `ArtifactFinalize` and taskrunapi `artifact-finalize` (`internal/store/control_plane_store.go:216-238`, `internal/webui/handlers/taskrunapi/artifacts.go:197-223`), and stack publishing has `finalizeStackNode` (`internal/driver/task_worktree_resolver.go:225-249`). Avoid `sweeper`: stale task and delivery sweepers are established background loops (`internal/driver/stale_task_sweeper.go:25-30`, `internal/trigger/delivery_sweeper.go:39-68`). “Reconciler” best matches the store-truth behavior without overloading artifact or stale-recovery vocabulary.