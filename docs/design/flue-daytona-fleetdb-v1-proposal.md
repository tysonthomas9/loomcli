# Flue-Daytona FleetDB TaskRun V1 Proposal

**Status:** V1 proposal for review
**Date:** 2026-06-03
**Related:**
- `docs/design/flue-daytona-runtime-proposal.md`
- `docs/design/distributed-control-plane.md`
- `docs/product/session-artifact-contract.md`
- `docs/product/local-mode-product-spec.md`
- `docs/product/orchestrator-worker-model.md`
- `docs/api.md`

## Purpose

This document defines the V1 control-plane contract for running Loom task
agents with:

- FleetDB as the source of truth for epics, task dependencies, claims,
  sessions, leases, and artifacts;
- Flue as the agent backend/harness;
- Daytona as a remote runtime provider for isolated filesystem and shell
  execution.

The proposal focuses on the parts that were underspecified in the
runtime proposal:

- how results flow back to FleetDB;
- how local embedded FleetDB differs from cloud/server FleetDB;
- how pull-based task ownership and push-assisted wakeups fit together;
- which edge cases V1 must handle before scale-out is trustworthy.

## Five-Agent Review Synthesis

This V1 draft was reviewed against five areas: FleetDB/control-plane
correctness, local-vs-cloud deployment, push-vs-pull scheduling,
artifacts/observability, and security/failure recovery. The main review
findings folded into this document are:

- FleetDB dependency ownership is the right core model, but ready-frontier
  scheduling needs a stricter server query than interactive ready views.
- Existing session-oriented leases are not sufficient unless they become
  task-run-scoped and fenced on every mutation.
- Completion must be idempotent and must validate durable artifacts before it
  changes task status, because task close unlocks downstream dependencies.
- Local mode and cloud mode are different products: local uses a host runner
  bridge and patch-back, while cloud uses server-visible artifacts and remote
  TaskRun APIs.
- Push is useful for latency, but polling, leases, and server-side compare-and-
  set remain the authority.

## Non-Negotiable Rules

1. FleetDB owns the epic dependency graph and the ready frontier.
2. A runner executes one exact leased task attempt.
3. Runner success alone does not unlock downstream work.
4. Task closure in FleetDB is the default dependency unlock signal.
5. Pull owns authority: claim, lease, heartbeat, completion, and artifact
   registration.
6. Push only wakes workers, streams logs/status, and refreshes UI.
7. Remote Daytona artifacts must become server-visible before cleanup.
8. Local patch-back is a developer path, not the cloud source of truth.
9. Scheduling, task claim, run creation, and run lease acquisition are one
   atomic server operation.
10. Every mutating run endpoint is fenced by `lease_id` plus a monotonic
    `fencing_token`.
11. A task cannot close until required artifacts are durable and readable by
    the control plane.
12. A runner that loses its run lease must fail-stop and must not mutate task
    status.

## V1 System Fit

```text
                         +----------------------+
                         |      User / UI       |
                         +----------+-----------+
                                    |
                                    v
                         +----------------------+
                         | Persistent Lead Agent|
                         | planning / review    |
                         +----------+-----------+
                                    |
                                    v
+------------------------------------------------------------------+
|                    FleetDB / Loom Control Plane                  |
|                                                                  |
|  +---------+     +------------------+     +-------------------+  |
|  |  Epic   | --> | Tasks + depends  | --> |  Ready Frontier   |  |
|  +---------+     +------------------+     +---------+---------+  |
|                                                     |            |
|  +-----------+   +------------------+               |            |
|  | TaskRuns  |   | Leases/Fencing   | <-------------+            |
|  +-----------+   +------------------+                            |
|                                                                  |
|  +------------------------------------------------------------+  |
|  | Sessions / logs / transcripts / patches / commits / tests  |  |
|  +------------------------------------------------------------+  |
+-------------------------------+----------------------------------+
                                |
                                v
+------------------------------------------------------------------+
|                      Loom Runtime Scheduler                      |
|                                                                  |
|  policy: epic-dag / critical-path / gated-dag                    |
|  rule: only lease open, unblocked tasks from ready frontier       |
|  limits: concurrency, budget, provider capacity, repo scope       |
+-------------------------------+----------------------------------+
                                |
                                v
+------------------------------------------------------------------+
|                    Ephemeral Task Execution                      |
|                                                                  |
|  +-------------+    +--------------+    +---------------------+  |
|  | Task Runner | -> | Flue Backend | -> | Flue Harness/Agent  |  |
|  +-------------+    +--------------+    +----------+----------+  |
|                                                     |            |
|                                                     v            |
|                                          +-------------------+   |
|                                          | Daytona Sandbox   |   |
|                                          | /workspace/project|   |
|                                          +-------------------+   |
+-------------------------------+----------------------------------+
                                |
                                v
+------------------------------------------------------------------+
|                         Result Back to FleetDB                   |
|                                                                  |
|  heartbeat -> logs/events -> artifacts -> CompleteRun             |
|  CompleteRun validates lease and applies close/block/review       |
|  closed upstream task updates FleetDB ready frontier              |
+------------------------------------------------------------------+
```

## V1 Control Loop

The V1 loop is dependency-driven:

```text
while epic run is active:
  ready = FleetDB.SchedulerReadyFrontier(parent_epic, filters)
  scheduler picks open, unblocked, unassigned tasks from ready
  scheduler atomically creates TaskRun, reserves task, and creates lease
  scheduler starts or wakes Flue-Daytona runner
  runner accepts exact task attempt with lease token
  runner executes in Daytona
  runner uploads logs, transcript, usage, patch/commit/test artifacts
  runner calls CompleteRun
  server validates lease and writes final state
  task close updates FleetDB dependency projection
  scheduler re-queries ready frontier
```

The runner must not call `loom data ready` and choose a sibling task once
it has an exact `task_id`. Work selection belongs to FleetDB and the Loom
scheduler.

V1 needs a scheduler-specific ready query. The existing user-facing ready
views may include `review`, `in_progress`, or locally-filtered items for
interactive workflows. The scheduler query must be server-side and stricter:

```text
status = open
dependencies = closed
blocked = false
assigned = false
active_issue_lock = false
active_nonterminal_TaskRun = false
```

The ready result should include a dependency projection version and task
version. Lease acquisition and completion use these versions as compare-and-
set preconditions so stale ready-frontier caches cannot start or close the
wrong task.

## Result Flow Contract

### 1. Schedule

Input:

```text
parent_epic
role/filter policy
repo scope
priority/budget/concurrency limits
runtime provider policy
```

FleetDB query:

```text
SchedulerReadyFrontier(parent_epic, filters)
```

Atomic scheduler operation:

```text
ScheduleRun(parent_epic, task_id, scheduler_id, placement, close_policy)
```

Server preconditions:

```text
task status is open
task dependencies are closed
task is not blocked
task has no active issue claim or assignment
task has no active nonterminal TaskRun
dependency projection version matches scheduler input
task version matches scheduler input
capacity reservation can be acquired
```

Server output:

```text
TaskRun {
  run_id
  task_id
  workspace_key
  parent_epic
  agent_id
  role
  backend = flue
  runtime_provider = daytona
  attempt
  close_policy
  dependency_projection_version
  task_version_at_start
  status = queued | leased | starting
}
```

`ScheduleRun` is the only operation that claims or reserves the task for
automated execution. The runner accepts an assigned run; it must not perform a
normal issue claim against FleetDB. If the scheduler crashes after creating the
run but before starting the runner, lease expiry releases the capacity
reservation and the task becomes schedulable again according to retry policy.

### 2. Lease

The scheduler acquires a run lease before a runner can mutate state.

```text
TaskRunLease {
  lease_id
  run_id
  task_id
  session_id
  holder_node_id
  holder_worker_id
  token
  fencing_token
  expires_at
  status = active | released | expired | revoked
}
```

V1 should prefer an explicit `TaskRunLease` or a generic
`Lease(resource_type=task_run)` record. Reusing the existing session-oriented
`AgentLease` store is only acceptable if the lease is hard-bound to
`run_id + task_id + session_id`, every renewal checks active and unexpired
state, and every final mutation uses a server-side compare-and-set against the
current `lease_id` and `fencing_token`.

Every mutating endpoint must include:

```text
run_id
lease_id
fencing_token
request_id
```

The server must reject stale mutations before applying side effects. This
includes heartbeat, event append, artifact registration, completion, release,
and cancellation acknowledgement.

Lease loss is fail-stop:

1. if heartbeat reports `lease_lost`, `expired`, `revoked`, or a newer
   fencing token, the runner stops the provider-side command;
2. the runner may upload fenced partial diagnostics only;
3. the runner must not close, block, reopen, or otherwise mutate the task;
4. replacement attempts must use a new run/lease and must not reuse stale
   sandbox state unless the server explicitly attaches it for recovery.

### 3. Start Runner

The runner receives exact task context:

```json
{
  "workspace_key": "ACME",
  "run_id": "run-123",
  "session_id": "sess-123",
  "task_id": "ACME-42",
  "lease_id": "lease-123",
  "lease_token": "secret-token",
  "node_id": "daytona-node-1",
  "agent_name": "worker-acme-42",
  "role": "task",
  "backend": "flue",
  "runtime_provider": "daytona",
  "repo_remote_url": "git@github.com:org/repo.git",
  "repo_branch": "feature/acme-42",
  "base_ref": "abc123",
  "sandbox_cwd": "/workspace/project",
  "sync_strategy": "server-artifacts",
  "loom_server_url": "https://loom.example.com",
  "fleetdb_url": "https://fleetdb.example.com",
  "control_plane_mode": "loom-server",
  "credentials_ref": {
    "control_plane": "run-token-ref",
    "git": "git-credential-ref",
    "packages": "package-registry-ref",
    "artifacts": "artifact-upload-ref",
    "daytona": "provider-ref"
  }
}
```

The real `task_id` must come from the scheduler or `LOOM_ASSIGNED_TASK_ID`.
It must not be inferred from `agent_name`.

Control-plane URL rule:

- In cloud V1, the runner should call Loom TaskRun APIs via
  `LOOM_SERVER_URL`; Loom owns run, lease, artifact, session, and task
  mutations and may call FleetDB internally.
- If a deployment exposes FleetDB directly through `LOOM_FLEET_DB_URL`, the
  proposal must define that as a different mode where FleetDB owns the same
  TaskRun APIs directly.
- Do not split one run so some mutations go to Loom server and others go
  directly to FleetDB.

Credential rule:

- control-plane credentials are run-scoped, short-lived, and limited to the
  assigned workspace/run endpoints;
- git, package registry, artifact storage, Daytona provider, and
  control-plane credentials are separate references;
- tokens must be redacted before logs, transcripts, events, or diagnostics are
  persisted;
- retained failed sandboxes must scrub or revoke credentials before a human can
  inspect them.

### 4. Execute

Runner duties:

- create or attach Daytona sandbox;
- hydrate repo under `/workspace/project`;
- verify `base_ref` or expected branch state;
- start Flue harness with `sandbox=daytona`;
- stream runner and Flue events;
- heartbeat the run lease and read desired run state;
- stop provider-side commands when cancellation or lease loss is observed;
- upload artifacts before sandbox cleanup.

Runner output should be NDJSON:

```text
runner_started
sandbox_created
repo_hydrated
log
flue_event
usage
test_result
patch_ready
artifact_uploaded
final
```

Heartbeat response should include:

```text
lease_state = active | lease_lost | expired | revoked
desired_state = running | cancel_requested
current_fencing_token
heartbeat_deadline
```

Cancellation is not push-only. Push may deliver a fast cancellation hint, but
heartbeat is the authoritative pull-visible check. When cancellation is
requested, the runner acknowledges with a fenced mutation, sends provider kill
or interrupt, uploads allowed partial artifacts, and finishes as `cancelled`.
Cancelled runs do not close tasks.

Persisted event envelope:

```text
event_id
workspace_key
run_id
session_id
task_id
seq
source = runner | flue | server | scheduler
runner_timestamp
server_timestamp
severity
type
message
artifact_id optional
redaction_status
```

Event append must be idempotent by `event_id` or `(run_id, seq)`. The UI/API
needs replay through `GET /runs/{run}/events?after_seq=N`; SSE is a transport,
not the source of truth.

### 5. Upload Artifacts

V1 artifact rows should be run-scoped. Either introduce first-class
`TaskRunArtifact` records, or define `run_id == session_id` for the
compatibility implementation and add `run_id` to artifact metadata and indexes.
Artifacts must be listable by run and fenced by lease.

Artifact state machine:

```text
declared -> uploaded -> checksum_verified -> committed
                         \-> rejected
```

V1 artifact types:

```text
patch
diff_summary
log
transcript_or_flue_events
usage
test_result
commit_or_pr
sandbox_metadata
failure_diagnostics
```

Each artifact must include:

```text
workspace_key
run_id
session_id
task_id
agent_id
lease_id
fencing_token
type
uri
artifact_id
idempotency_key
size_bytes
checksum
durability_status
visibility_status
redaction_status
redactor_version when applicable
metadata
```

In cloud mode, artifact `uri` must be server-visible. Daytona-local paths
are not acceptable after sandbox cleanup.

Cloud upload protocol:

1. runner declares artifact with type, size, checksum, and idempotency key;
2. server returns an upload target or object-storage policy;
3. runner uploads bytes;
4. server verifies checksum, ACL, redaction status, and readability;
5. server returns committed artifact IDs;
6. `CompleteRun` references artifact IDs, not arbitrary local paths.

Required artifacts for a successful close:

- patch or commit/PR artifact, unless the task is explicitly no-change;
- log or transcript/Flue event artifact;
- usage artifact, with `unknown` explicitly represented if unavailable;
- test artifact when the close policy requires tests;
- sandbox metadata.

Per-type schema minimums:

- patch: repo, base ref, head ref, changed files, binary/untracked markers,
  patch object ref, and apply strategy;
- commit/PR: commit SHA, branch, remote, PR URL when present, push result, and
  merge/readiness state;
- test: command, cwd, start/end times, exit code, pass/fail/skipped summary,
  and full log artifact ID;
- usage: backend, model, tokens, cost/pricing metadata, and unknown-vs-zero
  fields;
- diagnostics: failure class, provider error, sandbox ID, retained-until, and
  scrub status.

Cloud V1 rejects unredacted sensitive artifact types. Logs, transcripts, Flue
events, test output, and diagnostics must be redacted before upload or marked
as non-persistable raw streams according to policy.

The UI must be able to read artifacts after Daytona cleanup through
server-mediated APIs such as `GET /runs/{run}/artifacts` and
`GET /artifacts/{artifact}/content`, or through scoped signed URLs issued by
the server.

### 6. CompleteRun

Completion is one authoritative operation:

```text
CompleteRun(
  run_id,
  lease_id,
  fencing_token,
  completion_id,
  final_run_status,
  artifact_manifest,
  task_status_policy,
  preconditions
)
```

Server responsibilities:

1. validate `lease_id`, lease token, and current `fencing_token`;
2. reject completion if the lease expired, was revoked, or moved to another
   runner;
3. validate task version, dependency projection version, and current task
   status preconditions;
4. validate that required artifact IDs are committed, checksum-verified,
   server-visible, and attached to this run/lease;
5. update AgentSession or TaskRun status;
6. atomically promote artifacts from uploaded/verified to committed;
7. apply task status policy:
   - close;
   - move to review;
   - mark blocked;
   - reopen with needs-revision;
   - fail run without changing task terminal state;
8. release lease and capacity reservation;
9. emit mutation/timeline event once;
10. let FleetDB dependency projection update ready frontier.

Idempotency:

- `completion_id` is generated by the runner and stable across retries;
- if the same `completion_id` is replayed with the same payload, the server
  returns the previously committed result;
- if the same run receives a different terminal completion, the server returns
  conflict;
- dependency unlock and timeline emission happen once.

Run status is separate from task status:

```text
Run: queued -> leased -> starting -> running
Run terminal: completed | failed | blocked | cancelled | expired | stale

Task: open | in_progress/reserved | review | blocked | closed | needs-revision
```

Only a terminal run with a successful `task_status_policy=close` can close the
task and unlock downstream dependencies. `failed`, `cancelled`, `expired`, and
`stale` runs do not close tasks by default. A `blocked` run may move the task to
blocked, but it must not unlock dependents.

`/api/workspaces/{ws}/fleet/done/{worker}` is not enough for this V1
contract. It records a short-lived worker result and releases a Redis
claim, but it does not close issues, persist artifacts, or enforce a run
lease/fencing contract.

## Local User Mode vs Cloud Mode

| Area | Local user FleetDB | Cloud/server FleetDB |
|---|---|---|
| Control plane | Embedded/local FleetDB, usually loopback | Shared reachable Loom/FleetDB service |
| Scheduler owner | Local Loom process or daemon | Loom server scheduler |
| Runner location | Local process plus remote Daytona sandbox | Remote task runner and Daytona sandbox |
| Daytona access to FleetDB | Forbidden by default; host runner bridges task/result data | Runner calls `LOOM_SERVER_URL` TaskRun APIs with scoped credentials |
| Artifacts | Patch sync back locally, then existing finalizer records diff | Server-visible artifact storage is source of truth |
| Good for | Phase 2 developer validation | Production scale-out |
| Main risk | Remote sandbox cannot reach laptop embedded FleetDB | Auth, quotas, leases, artifact durability |

### Local V1 Path

```text
local Loom selects/claims task
local Loom creates session/lease
host Flue runner creates Daytona sandbox
host runner passes task content and repo context into sandbox
Daytona executes task
sandbox returns patch/log/test artifacts to host runner
host runner applies patch to clean local worktree or isolated attempt worktree
host runner records local diff/session and closes task in local FleetDB
task close in local FleetDB unlocks downstream work
```

Use this to validate the Flue-Daytona execution model. Do not make this
the production scale-out path.

Local mode rules:

- only the host/local Loom process talks to embedded FleetDB;
- do not pass loopback FleetDB URLs or local FleetDB credentials into Daytona;
- preflight requires a clean worktree or an isolated local attempt worktree;
- verify `base_ref` before applying patch-back;
- if patch-back conflicts, preserve the remote patch artifact, do not overwrite
  user changes, and do not close the FleetDB task;
- local-to-cloud migration is a deployment boundary unless a later phase
  explicitly imports embedded FleetDB state and local artifacts into cloud.

### Cloud V1 Path

```text
Loom server owns scheduler
FleetDB provides ready frontier
server creates TaskRun/lease
runner executes exact task in Daytona
runner uploads server-visible artifacts
CompleteRun writes FleetDB state
task close unlocks downstream work
```

Cloud mode must not depend on a developer-local worktree. Patch-back can
exist as an optional UX affordance, but server-visible artifacts are the
source of truth.

Cloud mode rules:

- `LOOM_SERVER_URL` is the runner control-plane endpoint in the default V1
  design;
- arbitrary `file://`, Daytona-local, or laptop-local artifact URIs are
  rejected;
- sandbox cleanup waits for server-confirmed artifact durability;
- credentials are run-scoped and separated by purpose;
- failed sandbox retention requires retention TTL, ACLs, and credential scrub
  status.

## Pull vs Push

### Pull Owns Authority

Use pull for:

- worker/node registration;
- ready frontier query;
- atomic `ScheduleRun` task reservation;
- run lease acquisition;
- run lease heartbeat;
- artifact registration;
- task status mutation;
- task close/block/review;
- run completion;
- cancellation desired-state polling;
- retry and recovery decisions.

Pull state also owns node and worker liveness:

```text
NodeHeartbeat(node_id, capacity, running_runs, ttl)
WorkerHeartbeat(worker_id, node_id, accepted_runs, ttl)
RunHeartbeat(run_id, lease_id, fencing_token, ttl)
```

Node liveness may inform placement and cleanup, but it must not extend a run
lease without the run lease heartbeat. If a node heartbeat is healthy but a run
lease heartbeat is stale, the run lease still expires and the runner must
fail-stop.

Retries are explicit:

```text
attempt = 1..N
retryable_failure = provision | transient_network | lease_expired_before_start
terminal_failure = policy_denied | invalid_task | repeated_patch_conflict
backoff = configured per workspace/provider
```

Exhausted retries leave the task open, blocked, or needs-review according to
policy; they do not silently close the task.

### Push Assists Latency

Use push for:

- "work may be available" wakeups;
- UI refresh/SSE;
- log streaming;
- transcript and Flue event streaming;
- cancellation hints;
- "ready frontier changed" notifications.

A dropped push must only add latency. It must never lose work, create
duplicate leases, or allow a stale runner to complete.

Scheduler tick contract:

- each active workspace/epic has periodic ready-frontier polling even when no
  push arrives;
- wakeups are deduped by workspace, epic, and frontier version;
- lease acquisition revalidates task readiness and capacity atomically;
- V1 should define a maximum scheduling latency target after dropped push
  based on poll interval plus jitter;
- multiple schedulers require either a single elected leader per workspace or
  database-backed atomic capacity reservations.

Capacity reservation is part of `ScheduleRun`. Dimensions include workspace
concurrency, repo concurrency, provider capacity, budget, role/model limits,
and sandbox quota. Reservations release on terminal completion, explicit
release, or lease expiry.

## Dependency Unlock Semantics

FleetDB dependency projection is the only V1 unlock mechanism.

```text
A -> {B, C} -> D

A closes       => B and C may become ready
B closes only  => D remains blocked
C closes too   => D may become ready
```

Close policy decides when dependencies unlock:

| Workflow | Close task when |
|---|---|
| direct patch | patch accepted and tests pass |
| PR workflow | PR merged, if downstream needs merged code |
| review workflow | review gate approves, if downstream can start before merge |
| blocked/failure | do not close; downstream remains blocked |

Do not add a second hidden unlock flag in runner metadata.

Close must be a server-side transaction with preconditions:

```text
current task version == TaskRun.task_version_at_start or allowed transition
dependency projection version is current or safely refreshable
current task status allows requested close/review/block transition
run lease is current and unexpired
required artifacts are committed and readable
completion_id is idempotent
```

If a user manually closes, reopens, blocks, or edits the task mid-run, the
completion path must either apply an explicitly allowed merge policy or reject
the stale completion without unlocking additional downstream work.

## V1 API Shape

The exact endpoint names can change, but V1 needs this shape:

```text
GET  /api/workspaces/{ws}/epics/{epic}/scheduler-ready-frontier
POST /api/workspaces/{ws}/runs/schedule
POST /api/workspaces/{ws}/runs/{run}/heartbeat
POST /api/workspaces/{ws}/runs/{run}/events
GET  /api/workspaces/{ws}/runs/{run}/events
POST /api/workspaces/{ws}/runs/{run}/artifacts/declare
POST /api/workspaces/{ws}/runs/{run}/artifacts/{artifact}/finalize
GET  /api/workspaces/{ws}/runs/{run}/artifacts
GET  /api/workspaces/{ws}/artifacts/{artifact}/content
POST /api/workspaces/{ws}/runs/{run}/complete
POST /api/workspaces/{ws}/runs/{run}/release
POST /api/workspaces/{ws}/runs/{run}/cancel
POST /api/workspaces/{ws}/nodes/{node}/heartbeat
POST /api/workspaces/{ws}/workers/{worker}/heartbeat
```

All `POST /runs/{run}/...` mutations include `lease_id`, `fencing_token`, and
`request_id`. `complete` also includes `completion_id` and committed artifact
IDs/checksums.

Compatibility path:

- implement V1 initially using existing `AgentSession` and `Artifact` records
  only if `run_id` mapping is explicit and artifacts can be listed by run;
- avoid reusing `AgentLease` for task-run authority unless it is hard-bound to
  run/task/session and validates active, unexpired fencing before renewal or
  final mutation;
- do not extend legacy `/fleet/done/{worker}` as the authoritative
  completion path;
- keep `/fleet/*` only as compatibility or bootstrap until the TaskRun
  protocol replaces it.

## Edge Cases V1 Must Handle

### Lease And Ownership

- runner completes after lease expired;
- runner completes after another runner acquired a new lease;
- heartbeat succeeds for node but run lease heartbeat fails;
- network partition between runner and server;
- duplicate `CompleteRun` retry after timeout;
- `CompleteRun` response is lost after the server committed;
- same run receives conflicting terminal completions;
- stale runner appends events or artifacts after replacement run starts;
- scheduler crash after lease but before runner start;
- runner crash after task reservation but before artifacts;
- run lease renews after expiry because the store validates token but not
  active/unexpired state;
- capacity reservation leaks after scheduler or runner crash.

### Dependency Graph

- upstream closes while scheduler has stale ready frontier cache;
- dependency cycle exists;
- task changes from open to blocked while runner is starting;
- task is manually closed or reopened by a user mid-run;
- downstream should wait for merge, not just patch creation;
- review tasks remain open and should not unlock dependents;
- ready query includes interactive statuses that are not scheduler-ready;
- dependency projection version changes between schedule and completion;
- manual close/reopen races with automated completion.

### Artifact Durability

- patch upload succeeds but CompleteRun fails;
- CompleteRun succeeds but artifact upload was incomplete;
- sandbox cleanup runs before artifacts are durable;
- large binary patch exceeds API/object size limit;
- transcript contains secrets and needs redaction;
- partial logs exist after runner crash;
- artifact is stored but UI cannot read it after sandbox cleanup;
- artifact checksum differs after upload;
- artifact ID belongs to a different run or stale lease;
- redaction fails for transcript, logs, Flue events, tests, or diagnostics;
- `run_id` cannot be mapped to session/artifact rows.

### Local vs Cloud

- Daytona cannot reach local embedded FleetDB;
- local patch-back conflicts with user changes;
- local worktree dirty before remote run starts;
- server-visible artifact URI points to local-only path;
- local mode accidentally passes embedded FleetDB credentials into Daytona;
- local patch applies against the wrong `base_ref`;
- local-to-cloud migration needs state import or explicit fresh boundary;
- runner writes some mutations to Loom server and others directly to FleetDB;
- cloud runner lacks git credentials or package registry credentials;
- scoped token expires mid-run.

### Runtime/Sandbox

- Daytona provision fails;
- repo clone fails;
- base ref does not exist in remote;
- runner uses wrong cwd;
- Flue local/default sandbox accidentally selected instead of Daytona;
- cancellation does not stop a provider-side command immediately;
- cancellation arrives by push but is missed until heartbeat;
- sandbox is retained on failure and leaks credentials in files;
- cleanup runs before artifact finalize response is persisted;
- replacement attempt reuses stale sandbox state accidentally.

### Push/Pull

- push wakeup dropped;
- duplicate push wakeups;
- polling interval is too long and dropped push creates unbounded latency;
- cancellation push delivered late;
- UI stream disconnects while run continues;
- log stream backpressure;
- mutation stream order differs from artifact upload order;
- multiple schedulers race without DB-backed atomic scheduling;
- node heartbeat healthy while run heartbeat expired.

## V1 Test Plan

### Result Flow E2E

```text
TestE2E_FlueDaytonaTaskRun_CompleteRunPersistsArtifactsAndClosesTask
```

Assert:

- exact `task_id` is passed to runner;
- run/session/lease created before execution;
- runner uploads logs, usage, patch, sandbox metadata;
- CompleteRun validates lease;
- task closes;
- downstream ready frontier updates after close.

### Fencing And Idempotency E2E

```text
TestE2E_FlueDaytonaTaskRun_RejectsStaleLeaseAndReplaysCompletion
```

Assert:

- stale runner cannot append artifacts, events, or completion after lease loss;
- replacement run can complete without accepting stale mutations;
- duplicate `CompleteRun` with the same `completion_id` returns the stored
  result;
- conflicting terminal completion returns conflict;
- dependency unlock and timeline event occur once.

### Artifact Durability E2E

```text
TestE2E_FlueDaytonaTaskRun_RequiresDurableArtifactsBeforeClose
```

Assert:

- incomplete artifact upload blocks task close;
- checksum mismatch rejects artifact finalize;
- committed artifacts are readable through server/UI API after Daytona cleanup;
- local-only or Daytona-local URIs are rejected in cloud mode;
- unredacted sensitive artifacts are rejected or marked non-persistable.

### Dependency DAG E2E

```text
TestE2E_FlueDaytonaScheduler_DrivesEpicDAG
```

Graph:

```text
A -> {B, C} -> D
```

Assert:

- only A and independent ready tasks run first;
- B/C do not run before A closes;
- D does not run before both B and C close;
- failed or blocked B does not unlock D.

### Local Mode E2E

```text
TestE2E_FlueDaytonaLocal_PatchBackThenFleetDBClose
```

Assert:

- local Loom owns claim;
- Daytona cannot assume embedded FleetDB access;
- patch syncs back before local finalization;
- local FleetDB task close unlocks downstream work;
- dirty or conflicting local worktree preserves patch artifact and does not
  close the task.

### Cloud Mode E2E

```text
TestE2E_FlueDaytonaCloud_ServerVisibleArtifactsNoLocalWorktree
```

Assert:

- runner uses `LOOM_SERVER_URL`;
- artifacts are server-visible;
- local filesystem paths are rejected as final artifact URIs;
- sandbox cleanup happens after artifact durability.

### Push/Pull E2E

```text
TestE2E_FlueDaytonaScheduler_DroppedPushDoesNotLoseWork
```

Assert:

- dropped wakeup only delays execution;
- polling still claims ready work;
- duplicate push does not create duplicate lease;
- stale runner completion is rejected.

### Cancellation E2E

```text
TestE2E_FlueDaytonaTaskRun_CancelStopsProviderCommandWithoutClosingTask
```

Assert:

- cancellation is visible through heartbeat even if push is dropped;
- runner acknowledges cancellation with current fence;
- provider-side command is stopped or times out through a defined grace path;
- partial artifacts are persisted according to policy;
- task remains open/blocked/review according to cancellation policy, not
  closed.

## Open Questions For Review

1. Should V1 introduce explicit `TaskRun` tables, or map V1 onto
   `AgentSession` plus explicit `run_id == session_id` compatibility first?
2. Should V1 introduce a new `TaskRunLease`, or a generic
   `Lease(resource_type=task_run)` table, instead of adapting `AgentLease`?
3. What is the smallest `CompleteRun` payload that still carries
   `lease_id`, `fencing_token`, `completion_id`, committed artifact IDs,
   task/dependency preconditions, final run status, and task status policy?
4. Should remote runners use `loom data` with scoped credentials in V1, or
   should V1 start with narrow TaskRun/artifact/event tools?
5. How should artifact storage be implemented for cloud V1: FleetDB blob,
   object storage, repo branch/commit refs, or hybrid?
6. What is the default close policy for PR-based workflows?
7. How do we recover artifacts from a runner that lost its lease but still
   has useful partial output?
8. How long should failed Daytona sandboxes be retained, and who can
   attach to them?
9. Do we keep the existing `/fleet/*` API as compatibility, or mark it
   legacy once TaskRun APIs exist?
10. Is local-to-cloud migration a fresh deployment boundary, or must V1 import
    embedded FleetDB state and local artifacts into cloud?
11. Does V1 assume one scheduler leader per workspace, or do we require
    database-backed capacity reservations from day one?
