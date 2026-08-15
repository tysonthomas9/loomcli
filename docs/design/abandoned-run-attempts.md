# Abandoned runs: recording the attempt when a run is killed

## Symptom

A daemon-supervised run that is killed mid-flight — daemon force-exit, SIGKILL,
machine death — leaves **no trace on the task it held**: no comment, no label,
no error class, no checkpoint. Every consumer of "how many attempts has this
task had" counts only attempts that ended by *reporting* something, so a killed
attempt is invisible: an attempt ceiling (e.g. a workspace's
`max_integration_attempts`) can never be reached, and the task is re-claimed and
re-run from scratch, forever.

Secondary symptom: the control-plane `agent_session` row of a killed run stays
`running` for good, because only the exit path ever finishes it.

## The mechanism

`internal/cli/daemon/supervisor/abandoned_run.go`. When a daemon takes ownership
of an agent (or claims a task) it reconciles the `agent_session` rows a previous
run left unfinished, writes durable evidence onto the task, and closes the row
out. It runs in a **different process** from the one that was killed, so the
kill cannot defeat it.

### The evidence source

A session row is created *before* the run starts
(`createControlPlaneAgentSession`) and finished *only* by the exit path
(`completeControlPlaneAgentSession`). Therefore a row with

* `Kind == task`,
* `FinishedAt == nil`, and
* a non-terminal status (`starting` or `running`)

is exactly a run that ended without reporting an outcome. Nothing new has to be
persisted.

### The authority to declare a run dead

Liveness is **server-arbitrated, never derived from comparing timestamps across
hosts** (the same rule `ownership.go` follows; the row's `LastHeartbeat` is
written exactly once, so its age is not a liveness signal). There is no
staleness clock anywhere in this path. Two entry points, two existing arbiters:

| entry point | arbiter | scope |
|---|---|---|
| `recordAbandonedRunsForAgent` (top of the supervise cycle, after ownership is acquired) | the agent ownership lease — if we hold it, no other daemon runs this agent | every unfinished row for that agent |
| `recordAbandonedRunsForTask` (after `claimTask`, before this run's own row is created) | fleet-db's per-issue claim lock | every unfinished row for that task, from any agent or node |

An acquire that fell through to "continuing without ownership guard" leaves the
lease token empty and is skipped: without the lease there is no proof of
exclusivity.

### The pipeline (at-least-once, deduped to exactly-once)

For each selected row, in this order:

1. `ListComments(task)` — if a comment already carries this row's marker, skip
   to step 4.
2. `AddComment(task, …)` — the human/agent-readable record.
3. `AddLabel(task, loom:attempt:<agent>=N)` where `N = max(existing counters for
   this agent) + 1`; superseded lower counters are then removed best-effort.
4. Latch the row: `Status: failed`, `FinishedAt: now`,
   `ErrorClass: abandoned_run`, `ExitCode: -1`.

Latching **last** is deliberate: the failure this exists to kill is "the attempt
silently vanishes", so the pipeline is at-least-once, and step 1 collapses the
retry to exactly-once in practice. A failure at step 2 or 3 leaves the row
unlatched and the whole thing retries on the next cycle or next claim. Once
latched, a row can never be selected again.

## The contract

### `loom:attempt:<agent>=N`

The mechanical attempt counter, shaped like the existing `review-cycle=N`
(`domain.DefaultCycleLabelPrefix`). fleet-db label values forbid only `,` and
`;`, so `:` and `=` are legal.

* **Per agent, not global.** A task is claimed by planner, critic, coder, tester
  and integrator in turn; a single global counter would charge the integrator
  for the planner's kills. An integrator-scoped budget must read an
  integrator-scoped counter.
* **Max, not sum.** `recordedAttempts` takes the highest counter present, so a
  leftover counter from a crashed cleanup is harmless.
* **Strict parsing.** `loom:attempt:x=1.5` and `loom:attempt:x=0` parse to 0
  rather than being rounded into an attempt count.

### `abandoned_run` error class

The session row's terminal `ErrorClass`, alongside `ExitCode: -1`. It is the
latch: its presence means the evidence pass for that row is complete.

### `loom-abandoned-run:<session-id>`

The last line of the evidence comment, and the dedupe key. Anything that
rewrites or truncates these comments breaks exactly-once and produces duplicate
records — visible and harmless, but noisy.

## What this deliberately does not do

* **It records; it never enforces.** No attempt ceiling lives in loomcli. The
  bug is that the evidence does not exist; the fix is to make it exist. Policies
  that read the task — an agent prompt, a human, `loom data show` — decide what
  to do with it, and must count a `loom:attempt:<agent>=N` label the same as N
  reported failures.
* **It does not touch the task quarantine ledger** (`quarantine.go`). That
  ledger is in-memory and per-supervisor — it dies with the daemon, which is the
  exact incident here — and its verdict (`blocked`) is a much heavier hand than
  "record the attempt".
* **It does not change how tasks are re-claimed.** A killed task still gets
  another attempt; it just stops being a free one.
* **It does not depend on the agent cooperating**, on a checkpoint, a yield
  file, or a clean daemon exit. A yielded run still runs the exit path, so its
  row is finished and is never selected.

## Limits

* **Requires a control plane.** With `ControlStore == nil`, `WorkspaceID == ""`,
  or no issue backend, the recorder logs at debug and does nothing; there is no
  second evidence store. All PUPPET repos run `issue_backend: fleetdb`.
* **Terminal tasks get no evidence.** fleet-db's `ValidateModifiable` rejects
  label writes on `closed`/`tombstone` issues, so those rows are latched only.
  `blocked` and `deferred` are *not* terminal and do get the evidence.
* **A row with no task id** (killed before the claim) is latched only — there is
  nothing to write to.
* **Bounded per pass.** At most `maxAbandonedPerPass` rows per reconcile, oldest
  first; the remainder is picked up on the next pass, and counters still advance
  deterministically. Entry point 1 runs once per agent per daemon lifetime.
* Nothing in this path is fatal: a total failure of the recorder leaves exactly
  the old behavior.
