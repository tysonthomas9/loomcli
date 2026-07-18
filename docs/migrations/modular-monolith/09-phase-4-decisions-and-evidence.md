# Phase 4 Execution Decisions and Evidence

- **Status:** INTERIM — Phase 4 implementation active and the current architecture check passes; paired contract, aggregate gates, source-bound proof, performance, implementation commits, and packaged Desktop evidence remain pending
- **Date:** 2026-07-16
- **Branch bases:** Loom `1353e2faf14ae121c93fe5eb92f779b56a2ad7ae`; FleetDB `f1c4e11199c2c7cdab52cce55899af4df328fbcb`
- **Scope:** Minimal Artifacts lifecycle, Execution-owned DriverRun/DriverStep/TaskRun/worker/lease/await/recovery mutations, supervisor-disabled execution, SDK runner containment, and packaged Desktop proof

## Locked decisions

### Ownership and public dependencies

Phase 4 activates `internal/modules/execution` as the sole Loom owner for
DriverRun, DriverStep, TaskRun, TaskRunEvent, ActionLedger, execution-scoped
worker state, run leases, awaits, retries, and recovery. It activates the
minimal `internal/modules/artifacts` lifecycle needed by running TaskRuns.
Execution may call Artifacts through its public API; Artifacts does not import
Execution implementation code and binds every operation to an independently
issued, action-specific Execution authority.

Work Items remains the sole product-policy owner for Issue status, assignee,
lock, and readiness semantics. Execution coordinates the FleetDB commands that
span run records and the exact Work Item generation; an `execution.*` command
ID does not relabel the Work Item aggregate as Execution-owned.

DriverRun and TaskRun remain different fenced aggregates. A shared FleetDB
lease mechanism does not make their owner tuples interchangeable. Raw lease
tokens are credential material, are excluded from public JSON, logs, results,
and authority values, and are compared only by the authoritative persistence
command using their stored hash. Both fence values are backend-assigned and
monotonic, but DriverRun and TaskRun use distinct namespaces/sequences; values
must not be compared or converted across resource kinds.

### Minimal Artifacts surface

The Phase 4 Artifacts API exposes the task-run-owner-scoped methods `Create`,
`Upload`, `Finalize`, `Reference`, `Get`, `List`, and the composed
`CreateContent` helper. The `Create` method is admitted by the
`artifacts.declare` action and persists through the create command; the public
method is not named `Declare`. Create/finalize commands use durable request
identities and fingerprints. References are immutable and deterministic.
General artifact search, unrelated UI/query features, and the remaining
cross-product artifact scope stay assigned to Phase 7.

### Execution command surface

Execution exposes intent-oriented application APIs rather than a storage CRUD
facade. Preflight and exit classification are non-mutating operations; the
production mutation surface is:

- TaskRun heartbeat, append-log, and finalize owner commands;
- DriverRun submit, child-start, child-cascade, claim, heartbeat, finalize,
  stale recovery, await registration, and atomic await resolution;
- DriverRun-coordinated Work Item claim and release, bound to the returned
  exact `ClaimActionID` generation;
- TaskRun request, system claim, owner-fenced requeue, retry exhaustion,
  terminal convergence, and parent-scoped stale-child recovery;
- worker-node register/heartbeat/drain and operator-owned WorkerProfile
  lifecycle;
- durable await-notification and DriverRun-outcome claim/complete/retry queues.

Every public mutation has one exact action and authority class. System actions
are minted only for registered runtime components. Operator commands use the
authenticated management boundary. Running DriverRun and TaskRun commands use
resource-, node-, lease-, token-, fence-, workspace-, and action-bound
execution envelopes.

### Atomicity, fencing, and lost-response replay

FleetDB owns atomic cross-record transitions. The Loom module validates intent,
authority, and returned envelopes but does not reproduce persistence rules.
Redis scripts and Postgres transactions must provide equivalent behavior for:

- TaskRun request plus DriverStep creation;
- DriverRun Work Item claim/release and TaskRun generation handoff;
- TaskRun claim/start, requeue/reset, retry exhaustion, and terminal repair;
- DriverRun claim/finalize, child start/cascade, and stale-child recovery;
- worker registration, heartbeat, drain, and WorkerProfile reference safety;
- append-log sequence allocation and request replay;
- generic await resolution plus parent resume; and
- artifact lifecycle commands and immutable reference creation.

Replay semantics are command-specific; Phase 4 does not use “idempotent” as a
blanket synonym for returning an old response:

| Replay class | Commands | Required behavior |
|---|---|---|
| Immutable receipt | Artifact create/upload/finalize/reference; TaskRun request, claim/start, requeue, retry exhaustion, completion, and append-log; DriverRun submit, claim, finish, and child-start; queue completion | A stable request identity and fingerprint return the committed snapshot or receipt after a lost response, even when the live projection has advanced. Upload identity includes the content digest and cannot accept different bytes. Finalize and reference retry the same deterministic command ID through a bounded 128-revision window ending at the current revision to find a committed receipt. Divergent reuse of any identity conflicts. |
| Identity plus state convergence | Await register/resolve | Await identities prevent duplicate consumption while suspend/resume converges the current parent state. No second side effect is permitted merely to recreate an old response. |
| State or lease convergence | Child cascade, heartbeats, stale recovery/scans, Node operations, WorkerProfile operations, terminal DriverStep repair, and queue claim/retry | Retry rechecks current ownership, fence, freshness, claim deadline, or desired state. A lost queue-claim response is recovered only after lease expiry; retry after a cleared claim may return an ownership conflict. Child-cascade identity freezes the affected IDs and intent while replay reports the descendants' current projections rather than stale snapshots. |

TaskRun completion receipts created before request fingerprints were persisted
retain the legacy same-TaskRun replay rule after upgrade: the same workspace,
completion ID, and TaskRun ID return the stored receipt, while cross-TaskRun
reuse conflicts. FleetDB leaves their fingerprint empty rather than fabricating
a v1 identity because old receipts do not contain every v1 fingerprint input,
including the lease-token hash and command-scoped runtime metadata. This is a
bounded compatibility exception; every new completion stores a nonempty v1
fingerprint and rejects divergent reuse.

Lease validation occurs inside the same Redis script or Postgres transaction as
the mutation, so expiry, revocation, reclaim, and concurrent
drain/profile-reference changes cannot race a preflight check.

### Work Item generation and terminal policy

The DriverRun claim receipt returns the exact `ClaimActionID` for the current
Work Item generation. TaskRun request stores that identity in its immutable
request receipt, and subsequent liveness, requeue, retry-exhaustion,
completion, and release paths recover the generation from that receipt. Once a
TaskRun is bound, the generic DriverRun release path conflicts. Ordinary Issue
lifecycle mutations also conflict while the typed generation is active.

The terminal command validates the exact generation and retires only its own
actor lock/generation. A successor generation wins: the stale TaskRun can
terminalize its own Execution state but cannot clear or rewrite the successor
Work Item. For the generation it still owns, FleetDB applies these policies in
the same TaskRun/Lease/Work Item transition:

- close intent produces `closed` and unassigned;
- successful non-close completion produces `review` and unassigned;
- cancelled or non-blocking failed completion produces `open` and unassigned;
- retry exhaustion or explicit block produces `blocked` and unassigned.

Retry exhaustion persists which branch actually committed. Its immutable action
response reference distinguishes an exact-generation block from a preserved
successor (or a defensively missing Issue), and the response derives
`issue_blocked` from that receipt on every replay. The response's Issue
projection is current and nullable; it is not the proof that the original
command blocked the Work Item.

The ActionLedger response reference is the canonical durable outcome receipt;
`issue_blocked` is derived from it when the response is reconstructed. The
TaskRun completion receipt binds the same Action ID and request fingerprint but
does not duplicate the environment-derived Work Item outcome in an unrelated
field. Consequently, corrupting an ActionLedger response from one valid outcome
value to the other is corruption of the authoritative receipt, not a divergent
retry that a second outcome copy is expected to repair or outvote.

DriverStep terminal repair and the terminal TaskRun event/notification are
separate durable convergence legs; the completion command does not claim they
are part of the same atomic transaction.

`needs_revision` is a typed host protocol rather than text scraping. The local
runner exposes a temporary, out-of-repository `LOOM_TASK_OUTCOME_FILE` and
accepts only exact version-1 JSON with disposition `needs_revision` and a
bounded nonempty summary after a clean backend exit. It records a cancelled,
non-retryable TaskRun with `errorClass=task_needs_revision` and
`task_outcome=needs_revision`; the prompt-agent host adds `needs-revision` and
leaves the Work Item open/unassigned for replanning. Malformed outcome data
fails closed, and a nonzero exit or fatal stream error takes precedence over a
valid file. Ordinary failed execution remains blocked instead of being reopened
into a spend loop.

### Runtime and supervisor-disabled operation

Execution lifecycle work is registered with the runtime host rather than
started by module constructors. The serve-owned task worker and reconcilers use
the Execution APIs and system-authority resolver for the Phase 4-owned mutation
operations; those operations do not fall back to the composite Store. This is
a scoped claim, not a statement that all legacy `internal/driver`, handler, or
composition call sites are Store-free: the current ratchet remains `82/71`.
The Execution-tagged supervisor-disabled matrix is the Phase 4 parity gate.
Physical supervisor deletion remains Phase 6 work after Agents and Interaction
replace their remaining lanes.

Recovery preserves owner fencing. A healthy DriverRun invokes the
owner-fenced stale-child command with its raw lease token kept inside the
transport. System DriverRun recovery may fail a stale or crashed parent; the
durable outcome path then converges its descendants through the child-cascade
command. There is no tokenless, always-on global TaskRun recovery pass.

`@loom/sdk/runner` uses only the Loom task-run facade selected by
`LOOM_TASK_RUN_API_URL`. Direct FleetDB credentials and mutation routes are not
accepted. The facade derives the exact TaskRun owner envelope from the
lease-token-authenticated request and forwards only owner commands.

## Compatibility and rollout

Loom readiness requires `execution.await_atomic_resume.v1` plus the complete,
exact 13-key Phase 4 foundation profile listed in the [target architecture](02-target-architecture.md#fleet-db-client-topology-and-compatibility)
before runtime loops start. A missing route, older FleetDB deployment, disabled
command family, or absent authority resolver fails closed; there is no legacy
multi-step mutation fallback or partial-profile mode. The existing keys are
umbrella parity gates rather than one key per method:
`execution.driver_run_work_item_claim.v1` requires both exact-generation claim
and release, while the TaskRun keys jointly cover the owner-fenced lifecycle
and terminal Work Item policy. Phase 4 does not invent separate release or
terminal capability names absent from Loom, FleetDB, and OpenAPI.

AgentCommand and AgentSession remain transitional records because their
identity and interactive lifecycle ownership is Phase 5/6 scope. Their
retention does not authorize Execution callers to mutate them.

`loom driver register` and the embedded-workflow registration helper still
reach the legacy Driver/DriverVersion authoring implementation. This is the
reviewed `MM-LEGACY-WORKFLOWS` Workflow Catalog authoring lane, not an
Execution mutation. Its machine-recorded expiry remains Phase 5; the physical
`internal/driver` containment exception remains `MM-LEGACY-DRIVER` until Phase
6 completion.

The Phase 4 audit also found that the Phase 2
`MM-2-LEGACY-DRIVER-LIFECYCLE-PATCH` description was too narrow: the new
Workflow Catalog lifecycle HTTP and CLI clients use the intent routes, but
native Flue registration still activates through generic `DriverStore.Update`,
and an untrusted re-registration uses that update to demote trust. Built-in
workflow startup and `loom driver register --activate` are current production
callers, so immediate Fleet-side removal would break supported registration.
The Phase 4 audit therefore records an explicit MM-6 extension for
`workflow-catalog-lane` through Phase 5. The replacement is
the Workflow Catalog authoring/authority API plus authenticated management
transport; Phase 5 must then remove the generic Fleet lifecycle fields,
approval deltas, Loom activation write, and compatibility tests.

This extension is not hidden under the broader Phase 6 `internal/driver`
expiry. The architecture gate freezes the Workflow Catalog owner share at four
legacy-driver rows/four sites, verifies the deprecated `active_version_id` and
`approved_version` markers in the vendored FleetDB OpenAPI, and fails once
`completed_phase` reaches 5 if any of those writes or markers remain. The
compatibility lane stays permission-gated, same-driver/version-validated,
digest-checked, and atomic until removal; Developer authority remains denied.

## Architecture activation and legacy disposition

The machine-readable mutation ledger enumerates the exact public Execution and
Artifacts mutations by method and route. Architecture tests freeze that list,
reject fictional generic commands, require the public FleetDB OpenAPI contract,
and forbid new direct Store or FleetDB mutation paths outside declared owner
adapters. The current architecture pass reports four active roots, 55 required
command-ID namespaces, 82 composite-Store files, 71 outside composition, 90
legacy handler-import exceptions, 243 primary direct-write rows, 86 runtime
components, 103 goroutine definitions, all six existing architecture-inventory
performance records measured, and zero pending decisions across all 11
declared profiles plus the all-files AST pass. The three
`workflowcatalog.*`, 14 `automation.*`, four `artifacts.*`, and 34
`execution.*` prefixes group IDs only; the ledger's aggregate, coordinating,
and instance owners remain authoritative. Phase 4 execution-path latency and
round-trip proof is separate from those fixed baseline categories and remains
pending. The architecture check passes; the paired contract and aggregate
product gates are not inferred from that result.

## Slice locality and structural delta

This self-reference-free interim measurement is taken from Loom base
`1353e2faf14ae121c93fe5eb92f779b56a2ad7ae` plus the current Phase 4 pre-commit
diff. It takes the union of tracked changes and non-ignored untracked files,
then excludes tests, architecture tooling, generated API clients, generated
workflow bundles, and the vendored OpenAPI fixture. The result is 121 changed
non-test, non-generated Go source files under that declared scope: 21 under the
Execution owner root, six under the Artifacts owner root, 15 in
`internal/app/serve` composition, 18 bounded `internal/driver`
compatibility/delegation files, and 61 contracts, persistence/transport
adapters, runtime or authority infrastructure, public CLI/WebUI entry adapters,
and the supervisor-disabled support command. The arithmetic is
`121 = 21 + 6 + 15 + 18 + 61`.

| Slice | Owner and changed Go source files | Allowed inbound adapters/contracts | Business-rule files outside the owner | Coupling delta |
|---|---|---|---|---|
| Execution plus minimal Artifacts | Execution: 21 owner-root files. Artifacts: six owner-root files. The remaining 94 files are composition, contracts, durable adapters, runtime/authority infrastructure, inbound adapters, bounded compatibility, or the support command. | `app/serve` composition; shared FleetDB and memstore adapters; driver, epic, workflow-management, and worker-profile CLIs; TaskRun, DriverRun, webhook, workflow, and execution-management HTTP adapters; SDK runner and built-in workflow callers; generated client/spec reported separately. | **No new cross-capability policy owner is introduced.** Eighteen changed `internal/driver` files delegate or remove Execution behavior while retaining explicitly owned legacy Automation, Interaction, Source Control, and Workflow Catalog lanes. Their current 10-row/11-site write set is frozen by owner and digest rather than relabeled as Execution. | Composite Store `88 -> 82`; outside composition `77 -> 71`; handler exceptions `91 -> 90`. The primary direct-write inventory changes `226/249 -> 243/265` rows/sites because 29 centralized `app/serve` adapter rows across 30 sites become visible while 12 legacy handler/CLI rows across 14 sites disappear. |

The runtime inventory changes from 85 to 86 named components while exact ticker
sites fall from 56 to 53 and goroutine launch definitions fall from 107 to
103. Managed components remain 58; foreground command-poll components rise
from three to four because the epic CLI now waits on the public DriverRun
projection rather than a direct Store path. Generated OpenAPI artifacts and
the vendored FleetDB specification are excluded from the 121-file locality
count and will instead be bound by the final paired checksum. FleetDB round-trip and
latency evidence remains pending the real-process product run; no call-site
estimate is substituted for that measurement.

The locality and ratchet measurements are reproducible with:

```sh
BASE=1353e2faf14ae121c93fe5eb92f779b56a2ad7ae
{ git diff --name-only --diff-filter=ACMR "$BASE"; git ls-files --others --exclude-standard; } \
  | sort -u \
  | rg '\.go$' \
  | rg -v '(_test\.go$|^internal/archtest/|^internal/backend/api/gen/|^internal/infra/fleetdb/testdata/|^internal/workflows/builtin-dist/|generated)' \
  | wc -l

jq '.ratchets.composite_store | [.max_production_files, .max_outside_composition]' \
  internal/archtest/testdata/migration-baseline.json
awk '/^  - file:/{rows++} /^    count:/{sites += $2} END{print rows, sites}' \
  internal/archtest/testdata/direct-writes.yaml
```

## Public-path and packaged Desktop proof

The acceptance sequence uses product entry surfaces, not hand-seeded storage:

1. run the Execution-tagged supervisor-disabled matrix against the paired
   FleetDB source and observe fresh planner/coder transcripts plus one patch;
2. build the packaged `Loom Agents.app` with the exact paired FleetDB checkout;
3. through the packaged Desktop UI, clone a real repository, select the Codex
   backend, create a ready task and prompt agent, start it, and observe run
   state, transcript, diff, patch-back (or a branch only when that delivery
   mode actually produces one), and completion evidence;
4. repeat through the UI with an unavailable backend and prove fail-closed
   behavior with no fabricated patch or successful run; and
5. use a dedicated agent-browser profile only as supplemental rendered-state
   and screenshot evidence, with packaged Desktop interaction remaining the
   authority for the acceptance journey.

Prompt agents expose run history and outcome details, not an interactive PTY or
worktree tab. A public HTTPS/SSH clone uses patch-back delivery, so its evidence
must not fabricate a Branch field. The positive backend is a real local Codex
CLI using the user's existing Codex authentication and may consume a paid
external service. Loom does not expose a product model selector for this path,
so the acceptance setup may place a test-only PATH wrapper ahead of `codex`.
That wrapper must transparently invoke the real local Codex CLI with
`--model gpt-5.6-terra`, preserve real authentication and exit behavior, and
record enough invocation evidence to prove Terra was requested. This is test
instrumentation, not a Loom product setting. The fail-closed case removes that
wrapper and any usable Codex binary (and unsets an explicit `LOOM_CODEX_BIN`)
rather than simulating a successful backend.

## Completion evidence

The baseline retains the appended
`phase4-execution-precommit-1353e2faf` snapshot as a **provisional historical
record** of the source state on which it was captured. Its recorded 53-command
architecture result, architecture-test duration, FleetDB head, and OpenAPI hash
are not rewritten as the implementation changes. It is superseded for current
source status by the live architecture check and must not be cited as the final
paired contract or product-gate record.

Current interim evidence is limited to the passing architecture check with the
counts above. The final paired FleetDB/Loom OpenAPI checksum, full architecture
test package rerun, FleetDB and Loom aggregate gates, source-bound
supervisor-disabled run, measured FleetDB request/latency samples,
implementation commit hashes, packaged Desktop screenshots, and
positive/negative run envelopes remain pending. A new immutable validation
snapshot is appended only after those facts and their real artifact paths
exist; no final hash, measurement, or path is predeclared here.

---

[All migrations](../README.md) · [Migration overview](README.md) · Previous: [Phase 3 evidence](08-phase-3-decisions-and-evidence.md)
