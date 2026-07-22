# Phase 4 Execution Decisions and Evidence

- **Status:** The Phase 4 architecture slice is complete; the current reliability hardening is bound to refreshed packaged Desktop, Podman/raw-browser, exact-head architecture, and aggregate Loom gate proof at Loom `67c45972f` with FleetDB `9ffa69f60`; Phase 5 has not started
- **Date:** 2026-07-21
- **Branch bases:** Loom `1353e2faf14ae121c93fe5eb92f779b56a2ad7ae`; FleetDB `f1c4e11199c2c7cdab52cce55899af4df328fbcb`
- **Implementation commits:** Loom `510391c60f17c6e9fc951c710a07a8ef8768b67f`, `45a73889ab456e974d9cd4346bcb8873be172438`, `8037205dadec12cb8ddc83edcf5509d3acf89652`, `a240215be482b1efe4731a1a24485d4a5ccb8b76`, and `53cbe257715d55770c77d508c23620389b9c9de1`; FleetDB `424492070a0f26e798eabad51b11ee4ea0b6b58c`, `758842a7e3a703470f7afcced437f46935b5a12f`, and `afb6887682f777b0e7093b5dcdff0a5e236777f9`
- **Architecture-analyzer and gate commits:** Loom `69e332697`, `02b62e5c7`, `88cb7f262`, `7d8118556`, and `e686b7a95`
- **Current validation heads:** Loom `67c45972f286f2f6c111fde9306720728dc6c4b4` (core reliability hardening at `ee971be22feb3c93096d599b7e3a62bff2cb0fa2`); FleetDB `9ffa69f6028969c03913c08c1159910fc772bd8b`
- **Current paired contract:** byte-identical FleetDB and Loom OpenAPI snapshots at SHA-256 `ebf2ec68fd5751fbb59747c7b3db7b66fe4f7f80f30cb7eead9b6b3fd35ccb9e`
- **Scope:** Minimal Artifacts lifecycle, Execution-owned DriverRun/DriverStep/TaskRun/worker/lease/await/recovery mutations, supervisor-disabled execution, SDK runner containment/publication, Work Items repository admission and retry placement, trigger-loop containment, and packaged Desktop proof

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

- TaskRun heartbeat, append-log, exact bound-Work-Item planner-design save, and
  finalize owner commands;
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
- TaskRun-owner-fenced, run-derived Work Item planner-design persistence,
  including managed design-artifact metadata and the Issue pointer;
- generic await resolution plus parent resume; and
- artifact lifecycle commands and immutable reference creation.

Replay semantics are command-specific; Phase 4 does not use “idempotent” as a
blanket synonym for returning an old response:

| Replay class | Commands | Required behavior |
|---|---|---|
| Immutable receipt | Artifact create/upload/finalize/reference; TaskRun request, claim/start, Work Item planner-design save, requeue, retry exhaustion, completion, and append-log; DriverRun submit, claim, finish, and child-start; queue completion | A stable request identity and fingerprint return the committed snapshot or receipt after a lost response, even when the live projection has advanced. Upload identity includes the content digest and cannot accept different bytes. Finalize and reference retry the same deterministic command ID through a bounded 128-revision window ending at the current revision to find a committed receipt. Divergent reuse of any identity conflicts. |
| Identity plus state convergence | Await register/resolve | Await identities prevent duplicate consumption while suspend/resume converges the current parent state. No second side effect is permitted merely to recreate an old response. |
| State or lease convergence | Child cascade, heartbeats, stale recovery/scans, Node operations, WorkerProfile operations, terminal DriverStep repair, terminal TaskRun convergence completion, and queue claim/retry | Retry rechecks current ownership, fence, freshness, claim deadline, or desired state. Terminal TaskRun convergence completion advances a monotonic versioned marker and replay returns the current marker without lowering it. A lost queue-claim response is recovered only after lease expiry; retry after a cleared claim may return an ownership conflict. Child-cascade identity freezes the affected IDs and intent while replay reports the descendants' current projections rather than stale snapshots. |

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

The service-authenticated planner-design command derives `TaskID` from the
immutable TaskRun request, requires the exact permission and advertised
capability, and atomically persists managed artifact metadata/content together
with the Issue design pointer. It returns the exact artifact reference and
replays by stable request identity. The transport rejects request envelopes
larger than 4 MiB and semantic design content larger than 512 KiB.

Redis event-sequence allocation uses `XINFO STREAM last-generated-id`, so
deleting the maximum stream entry cannot rewind the watermark. Deleting the
maximum fails closed with `ERROR_EVENT_WATERMARK` and zero writes; deleting
max-minus-one continues at the exact successor. Live Redis 7 proof also covers
lossless unsigned rollover from
`18446744073709551614-18446744073709551615` to
`18446744073709551615-0`. The corresponding Postgres transaction parity tests
pass.

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

After all three legs have succeeded or reached a durable skip decision,
Execution advances the TaskRun's typed `terminal_convergence_version` and
`terminal_converged_at` through a narrow FleetDB command. The periodic pass
pages only terminal TaskRuns below the required protocol version, so completed
history is not fetched and replayed every cadence. A partial leg or marker
failure leaves the run eligible after restart; a lost completion response can
replay the same command and observe the durable marker. Every new pass resets
the exclusive TaskRun-ID cursor, which discovers a newly terminalized ID even
when it sorts behind a prior page. Missing and older versions remain eligible
for upgrade backfill, and concurrent old/new completion calls may only raise,
never lower, the marker.

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

### Repository admission, placement, and retry hardening

A non-epic task without an explicit source repository is unambiguous only when
its workspace has exactly one registered repository. With zero or multiple
repositories, the Work Items owner performs one atomic repository-requirement
command. FleetDB rechecks the current Issue projection, repository count,
claim generation, live lock, and pending projections in the same Redis script
or Postgres transaction before moving an eligible task to `blocked` and
unassigning it. The result carries the canonical Issue and a commit-time
`dispatch_ready` decision. Loom's ready-event bridge and startup reconciliation
consume that result rather than dispatching from the stale journal payload;
deleted tasks and tasks that concurrently became claimed, terminal, ordinarily
blocked, or repository-assigned are suppressed.

Repository selection is a separate atomic Work Items command. The caller sends
a registered first-class repository `name`; FleetDB resolves and persists its
effective `source_repo_id` (falling back to the name). In the same commit it
reopens the Issue only when the Issue carries FleetDB's genuine private
`repository_required` authority. Other blocked Issues remain blocked, and a
lost assignment response can replay the already committed canonical Issue
without applying a second transition.

The policy authority is not caller-editable Issue metadata. Redis and Postgres
persist it separately, public issue creation/batch creation and metadata
set/remove endpoints cannot mint or remove the reserved `loom.block_reason`,
and generic status, close, defer, assign, or claim operations cannot bypass a
repository-required card. Delete clears the private record. Disaster recovery
reconstructs authority only from
the exact tagged block/assignment event sequence; an isolated public-looking
marker is insufficient. Trusted import remains an Admin-only restore boundary
and applies the same state machine.

TaskRun retry preserves placement independently of mutable claim state. The
reset path recovers `repo_ref` from the immutable original request receipt, so
a selected repository cannot disappear into a multi-repository ambiguity on a
later attempt. The DriverRun executor and TaskRun workers also register the
same once-normalized configured node capacity; registration order cannot
downgrade the process node to one slot.

First-class Repo create now commits the Repo projection, audit event, workspace
set/list membership, and global admission mapping in one Redis script or
Postgres transaction. Update and delete atomically reject any change that
would orphan an Issue reference, pending Issue projection, current TaskRun
placement, or immutable TaskRun origin. Deleting the sole implicit fallback is
also rejected while an open repo-less non-epic task is dispatch-ready. Replay
and recovery rebuild or remove mappings with compare-and-set ownership, and
cross-workspace name collisions fail closed without partial projection writes.
Workspace resolution accepts both first-class local handles and retained
legacy `org/repo` aliases.

Repository cloning no longer guesses `main` or copies one workspace-wide
default to every repository. Each clone records the committed branch selected
by its remote symbolic HEAD (with the clone's symbolic HEAD as the bounded
local/file-remote fallback). An explicit requested branch must resolve to a
commit in every clone, and an empty or unborn remote fails closed instead of
registering unrunnable metadata.

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
command. There is no tokenless, always-on global TaskRun lifecycle recovery
pass; the service-only TaskRun convergence pass repairs only terminal
projections and uses the durable versioned completion boundary above.
The healthy-parent pass runs immediately after claim and periodically only
after a successful parent heartbeat, stops and cancels the backend on an exact
fence conflict, and is drained before the parent is terminalized. Its cadence
is no faster than the owner heartbeat and otherwise one quarter of the stale
threshold, avoiding a durable no-op receipt on every heartbeat.

### Trigger-loop containment

Automation binding create and update reject any route or event pattern that
can match `internal.issue.*` unless its actor filter excludes `workflow`.
Runtime dispatch applies the same rule to unsafe legacy records and fails
closed, so a workflow-origin Issue mutation cannot recursively retrigger the
workflow that produced it. Exact routes and broad segment patterns are both
covered.

`@loom/sdk/runner` uses only the Loom task-run facade selected by
`LOOM_TASK_RUN_API_URL`. Direct FleetDB credentials and mutation routes are not
accepted. The facade derives the exact TaskRun owner envelope from the
lease-token-authenticated request and forwards only owner commands.
The local task runner recursively redacts inherited secrets from logs and
transcripts, scans patches, worktrees, and immutable commit objects for exact
credential bytes, and fails closed when patch capture is incomplete. It scans
the commit object that will be published, disables hooks, and pushes that exact
SHA rather than symbolic `HEAD`; a concurrent post-scan HEAD movement therefore
cannot publish unscanned content. A backend that already created a commit is
reused instead of forcing an empty follow-up commit.

TaskRun-scoped `loom data` permits only the exact `show` and design-only
`update` leaves. A composition-root guard enforces that allowlist before
backend resolution. The client uses a deterministic request identity and does
not accept a caller-supplied Task ID; notes, labels, parent, and sibling access
fail closed.

The prompt-agent maps `source_repo` into TaskRun `repoRef` and cannot complete
planning without a persisted nonblank design. Startup refreshes stale enabled
system-managed prompt-agent versions. Enabled prompt-agent digest drift or an
unavailable rebuild toolchain fails closed, while operator-managed active
versions are preserved. Generic built-ins retain their existing fail-open
policy.

## Compatibility and rollout

Loom readiness requires `execution.await_atomic_resume.v1` plus the complete,
exact 18-key Phase 4 foundation profile listed in the [target architecture](02-target-architecture.md#fleet-db-client-topology-and-compatibility)
before runtime loops start. A missing route, older FleetDB deployment, disabled
command family, or absent authority resolver fails closed; there is no legacy
multi-step mutation fallback or partial-profile mode. The existing keys are
umbrella parity gates rather than one key per method:
`execution.driver_run_work_item_claim.v1` requires both exact-generation claim
and release. The dedicated `execution.task_run_work_item_design.v1` key is
required because an older FleetDB can truthfully advertise TaskRun lease
fencing without exposing the new atomic design route; the other TaskRun keys
jointly cover the owner-fenced lifecycle and terminal Work Item policy. The
three additional advertised keys correspond to actual typed routes:
`execution.task_run_terminal_convergence.v1` for pending discovery and marker
completion, `execution.terminal_driver_run_work_recovery.v1` for the terminal
DriverRun recovery command, and
`execution.terminal_driver_run_work_recovery_queue.v1` for its durable queue.
`work_items.repository_requirement.v1` certifies the conditional repository
block and atomic repository-assignment recovery commands, including canonical
replay, private policy authority, and Redis/Postgres parity; Loom has no
generic Issue-update fallback for these race-sensitive transitions.
Phase 4 does not invent generic release or terminal-policy capability names
absent from Loom, FleetDB, and OpenAPI.

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
adapters. The current architecture pass reports four active roots, 60 required
command-ID namespaces, 82 composite-Store files, 71 outside composition, 90
legacy handler-import exceptions, 251 primary direct-write rows across 273
sites, 86 runtime components, 103 goroutine definitions, all six existing architecture-inventory
performance records measured, and zero pending decisions across all 11
declared profiles plus the all-files AST pass. The three
`workflowcatalog.*`, 14 `automation.*`, four `artifacts.*`, and 35
`execution.*` prefixes at the immutable snapshot, now 39 after the terminal-work
recovery delta, group IDs only; the ledger's aggregate, coordinating,
and instance owners remain authoritative. Phase 4 execution-path latency and
round-trip proof is separate from those fixed baseline categories. The real
artifact-backed 30-sample product test measured p50 `11.796 ms`, p95
`14.784 ms`, and exactly three Loom-to-FleetDB requests per sample (two
workspace-guard reads plus the one atomic design command), within its
`35 ms`/`75 ms` latency and three-request budgets. The architecture check
passes; the aggregate product gates are not inferred from that result.

### Architecture analyzer peak-memory fix

The architecture analyzer previously amplified peak memory by keeping three
repository-scale `go/packages` syntax/type graphs live concurrently for each
profile pass. Direct-write collection also requested dependency syntax and
`TypesInfo` even though it inspects only the requested adapter roots, and the
package suite repeated the complete 11-profile direct-write scan after the
authoritative repository test had already run it. This produced the observed
approximately 15 GiB peak and a SIGKILL during a fresh
`go test ./internal/archtest -count=1` run; inspection found no retained global
package graph, so the failure is classified as peak-memory amplification rather
than a classic retained-object leak.

The Phase 4 fix serializes repository-scale loads and splits every architecture
profile into three bounded views: import adjacency only for the transitive
dependency graph; `Types` and compiled-file selection for all requested
`./...` roots; and syntax plus `TypesInfo` only for directories whose selected
files contain a generic-mechanism selector candidate. The broad typed graph is
released before that focused semantic load begins. Direct-write analysis
likewise omits `NeedDeps` because it inspects only requested adapter roots.
Analyzer-owned `-p=2` flags bound Go compiler subprocess fan-out even though
the profile environment deliberately clears caller `GOFLAGS`. The package
suite also converts the duplicate checked-in 11-profile direct-write scan into
a cheap strict row/site manifest assertion; the authoritative repository test
remains responsible for matching observed calls to every baseline row and
count.

Regression tests freeze the single profile-load limit, typed-root versus
metadata-only versus focused-`TypesInfo` modes, direct-write mode, and compiler
cap. Existing typed receiver, tagged/race source, exported-signature, and
transitive-dependency fixtures remain green. New fixtures prove same-package
and external tagged test files reach the focused semantic load, repeated test
variants cannot duplicate a violation, malformed selected source remains
fail-closed, and the metadata graph still detects a test-only path through an
internal helper to a forbidden dependency.

The final hardening pass also replaces the former generated-file prefix check
with Go's canonical `ast.IsGenerated` rule, so only a complete
`// Code generated ... DO NOT EDIT.` marker before the package declaration can
exclude a file. Every configured direct-write adapter root must exist; missing
or inaccessible roots now stop analysis instead of silently reducing its
scope. The checked-in `scripts/rsswatch` runner, `make
check-architecture-memory` target, and dedicated CI job enforce a 2048 MiB
process-tree ceiling and terminate the watched process group on budget or
timeout failure.

Bounded local validation on 2026-07-20 sampled aggregate RSS for each test
process and its descendants every 250 ms and killed the process group at the
declared ceiling:

| Command | Result | Peak process-tree RSS | Hard ceiling |
|---|---:|---:|---:|
| Exact `7d8118556` `go test ./internal/archtest -count=1 -timeout=15m`, no `GOMEMLIMIT`, standard build cache | PASS in `132.652s` | `743.2 MiB` | `2048 MiB` |
| Exact `7d8118556`, same command with a fresh `GOCACHE` | PASS in `191.376s` | `1154.8 MiB` | `2048 MiB` |
| Exact `7d8118556` `go test -race ./internal/archtest -count=1 -timeout=15m` | PASS in `87.119s` | `1111.7 MiB` | `2048 MiB` |
| Exact `7d8118556` `go run ./scripts/archcheck check` | PASS, `11/11` profiles | `370.5 MiB` | `2048 MiB` |

Follow-up gate/analyzer commit `e686b7a95` applies the architecture RSS budget
to the normal aggregate-gate path, replaces its duplicate repository-scale
snapshot scan with a checked-in manifest assertion, and bounds gate worker
fan-out and process cleanup. The measurements in the table remain provenance
for exact commit `7d8118556`; they are not reattributed to the follow-up commit.

Summed descendant RSS conservatively double-counts shared pages, so these
figures are safe upper-bound evidence rather than heap profiles. `GOMEMLIMIT`
is defense in depth, not the fix: the checked-in 2048 MiB process-tree budget
is the primary regression proof.

## Slice locality and structural delta

This self-reference-free measurement is taken from Loom base
`1353e2faf14ae121c93fe5eb92f779b56a2ad7ae` through implementation commit
`53cbe257715d55770c77d508c23620389b9c9de1`. It takes the committed diff,
then excludes tests, architecture tooling, generated API clients, generated
workflow bundles, and the vendored OpenAPI fixture. The result is 135 changed
non-test, non-generated Go source files under that declared scope: 21 under the
Execution owner root, six under the Artifacts owner root, 15 in
`internal/app/serve` composition, 18 bounded `internal/driver`
compatibility/delegation files, and 75 contracts, persistence/transport
adapters, runtime or authority infrastructure, public CLI/WebUI entry adapters,
and the supervisor-disabled support command. The arithmetic is
`135 = 21 + 6 + 15 + 18 + 75`.

| Slice | Owner and changed Go source files | Allowed inbound adapters/contracts | Business-rule files outside the owner | Coupling delta |
|---|---|---|---|---|
| Execution plus minimal Artifacts | Execution: 21 owner-root files. Artifacts: six owner-root files. The remaining 108 files are composition, contracts, durable adapters, runtime/authority infrastructure, inbound adapters, bounded compatibility, or the support command. | `app/serve` composition; shared FleetDB and memstore adapters; driver, epic, workflow-management, and worker-profile CLIs; TaskRun, DriverRun, webhook, workflow, and execution-management HTTP adapters; SDK runner and built-in workflow callers; generated client/spec reported separately. | **No new cross-capability policy owner is introduced.** Eighteen changed `internal/driver` files delegate or remove Execution behavior while retaining explicitly owned legacy Automation, Interaction, Source Control, and Workflow Catalog lanes. Their current 10-row/11-site write set is frozen by owner and digest rather than relabeled as Execution. | Composite Store `88 -> 82`; outside composition `77 -> 71`; handler exceptions `91 -> 90`. The primary direct-write inventory changes `226/249 -> 243/265` rows/sites because 29 centralized `app/serve` adapter rows across 30 sites become visible while 12 legacy handler/CLI rows across 14 sites disappear. |

The runtime inventory changes from 85 to 86 named components while exact ticker
sites fall from 56 to 53 and goroutine launch definitions fall from 107 to
103. Managed components remain 58; foreground command-poll components rise
from three to four because the epic CLI now waits on the public DriverRun
projection rather than a direct Store path. Generated OpenAPI artifacts and
the vendored FleetDB specification are excluded from the 135-file locality
count and are instead bound by the paired SHA-256 checksum
`26ed930bc527c3815742c8b4c7a0ba5267bdc91c585ddc9f78483d9373303482`.
The measured real-process result is p50 `11.796 ms`, p95 `14.784 ms`, and three
Loom-to-FleetDB requests per artifact-backed planner-design save; no call-site
estimate is substituted for that measurement.

The locality and ratchet measurements are reproducible with:

```sh
BASE=1353e2faf14ae121c93fe5eb92f779b56a2ad7ae
IMPL=53cbe257715d55770c77d508c23620389b9c9de1
git diff --name-only --diff-filter=ACMR "$BASE" "$IMPL" \
  | rg '\.go$' \
  | rg -v '(_test\.go$|^internal/archtest/|^internal/backend/api/gen/|^internal/infra/fleetdb/testdata/|^internal/workflows/builtin-dist/|generated)' \
  | wc -l

jq '.ratchets.composite_store | [.max_production_files, .max_outside_composition]' \
  internal/archtest/testdata/migration-baseline.json
awk '/^  - file:/{rows++} /^    count:/{sites += $2} END{print rows, sites}' \
  internal/archtest/testdata/direct-writes.yaml
```

## Historical public-path and packaged Desktop proof

The acceptance sequence below is immutable evidence for Loom `53cbe2577` and
FleetDB `afb688768` only. It predates the repository-admission hardening delta
described above and must not be presented as refreshed package proof for the
current worktree. Refreshed package evidence for the hardening delta is
recorded separately below.

The earlier acceptance sequence used product entry surfaces, not hand-seeded
storage:

1. run the Execution-tagged supervisor-disabled matrix against the paired
   FleetDB source and observe fresh planner/coder transcripts plus one patch;
2. build the packaged `Loom Agents.app` with the exact paired FleetDB checkout;
3. through the packaged Desktop UI, use a clean attached repository and the
   real local Codex backend through the Terra test wrapper, create task
   `PHASE4-TERRA-FRESH-20260718-9`, let the packaged supervisor claim its
   planning run, approve the persisted design in Review, invoke the coder with
   the UI `Run now` action, and inspect the exact patch-back diff and terminal
   board state;
4. through Settings select and save unavailable Claude, run epic
   `PHASE4-TERRA-FRESH-20260718-6`, prove fail-closed behavior with no agent run
   or worktree, then restore and save Codex as the default; and
5. use agent-browser only for supplemental rendered-state inspection, with
   packaged Desktop interaction through Computer Use remaining authoritative.

The source-bound supervisor-disabled row is green against the Phase 4 FleetDB
worktree. It created fresh planner/coder tasks, verified planner design,
transcripts, coder diff metadata and patch content, asserted zero auto
agentdefs, daemon processes, and daemon control sockets, then cleaned the
checkout-scoped stack. The run used free host ports `8480`/`8482`/`8483`
because an unrelated local stack already owned the default proof ports.

The final packaged supervisor also reused its authenticated embedded FleetDB
runtime successfully: after daemon PID `55147` started it claimed tasks and
spawned the planner with zero subsequent `authentication failed` records. This
is the product-level regression proof for retaining the process-local
StoreHandle service credential in secondary CLI, driver, and epic clients.

The signed package at
`desktop/src-tauri/target/release/bundle/macos/Loom Agents.app` contains Loom
`53cbe2577` and the FleetDB sidecar built from `afb688768`; read-only provenance
checks report Loom binary SHA-256
`6f5111b719e7461281129f3f73b5820fd30a3510d246563b95edf44d3271fe88`,
FleetDB binary SHA-256
`5e5ffc32d0ca270c5bb64cce7a89eb59d437d96b9a9fe6f2dec7fbd86d7dde32`,
valid deep/strict code signing, and a healthy running local service whose exact
executable and build are `53cbe2577`.

Prompt agents expose run history and outcome details, not an interactive PTY or
worktree tab. The positive backend used the real local Codex CLI and existing
Codex authentication and therefore consumed a paid external service. Loom does
not expose a product model selector for this path, so the package used a
test-only wrapper that transparently invoked the real CLI with
`--model gpt-5.6-terra` while preserving authentication and exit behavior. The
archived 15-entry wrapper log contains 13 health/version checks and exactly two
real `exec` invocations; every entry records `model=gpt-5.6-terra`. The planner
invocation names task `-9` directly, and the coder invocation is correlated by
its stdin form, timestamp, and packaged-UI run record. This is test
instrumentation, not a Loom product setting.

The packaged positive journey completed planner run exit `0` in `59.6 s` and
coder run `automation-run-c6c9baf0d6bb8bbaf6782d41445d1e83` exit `0` in
`46.3 s`, with patch-back applied. The delivered worktree contains exactly one
untracked 67-byte file, `phase4-terra-proof-53cbe2577.txt`, whose sole line is
`Phase 4 packaged Desktop proof via GPT-5.6 Terra on Loom 53cbe2577.` followed
by one LF. The attached base repository remains clean and does not contain the
file. A read-only product query and the refreshed packaged task detail place
exact task `PHASE4-TERRA-FRESH-20260718-9` in the terminal `Closed` state.
Patch-back did not create a delivery branch, commit, push, or PR, so none is
claimed.

For the fail-closed case the packaged UI selected unavailable Claude while the
Codex/Terra setup remained installed. Running epic `-6` produced the exact
`local_backend_unavailable` error stating that backend `claude` was not
installed and directing the operator to install it or switch the Project
Default Backend. The Epic Runs tab reported `No agent runs recorded yet`; a
read-only filesystem check found no corresponding worktree and the attached
repository remained clean. The UI then restored Codex and showed it as
installed, authenticated, and the saved default.

Versioned local evidence is retained at
`artifacts/phase4/desktop-terra-final/20-53cbe2577-task-created.jpeg` through
`23-53cbe2577-exact-diff.jpeg`,
`25-53cbe2577-backend-unavailable.jpeg` through
`27-53cbe2577-backend-restored-codex.jpeg`,
`31-53cbe2577-task-closed-detail-refreshed.jpeg`, and
`artifacts/phase4/desktop-terra/terra-wrapper-invocations-53cbe2577-positive.log`.
These local artifacts remain deliberately untracked; the checked-in evidence
record and validation snapshot are the portable provenance.

### Split repository-admission package evidence

Packaged build `8b17cb261-phase4-repo-required-v3`, running at
`http://127.0.0.1:58237`, exercised the current repository-admission path
through the product UI. Task `PHASE4-TERRA-FRESH-20260718-12` was created
without a repository and appeared in the canonical board's Blocked column.
Selecting `hello-world` reopened it to Open. Its task worktree was then created
under `hello-world` from `origin/master`; the bundled Codex run completed in
`5m18s`, persisted the design, and moved the task to Review. The wrapper's
terminal record rendered `Completed` with outcome `skipped`.

That run exposed a transient create-response race: the create call returned an
Open projection before durable reconciliation changed it to Blocked. It also
predated the fix that verifies a recorded local-runtime PID's executable before
signalling it, which prevents an unrelated process that reused a stale PID
after reboot from being terminated.

Final packaged build `8b17cb261-phase4-repo-required-final`, running at
`http://127.0.0.1:61594`, contains both fixes. Creating
`PHASE4-TERRA-FRESH-20260718-14` without a repository returned canonical
`blocked` status in the synchronous POST response; an immediate GET also
returned Blocked, and the task workflow-runs query returned zero runs. The
packaged Desktop board and task detail rendered it in the Blocked column with
`Repo: No repo`. That task was deliberately left blocked. Consequently, the
final package does not prove repository-selection recovery or retry after both
fixes. The earlier v3 package evidence proves the canonical
repository-selection recovery and the real bundled-Codex run from
`origin/master`, while the final package binds only the synchronous admission
and stale-PID fixes to the rebuilt product. These are split evidence runs, not
a claim that the final package satisfies the complete Desktop closure row in
[the enforcement record](04-enforcement-and-gates.md).

The final runtime used service PID `50694`, serve PID `50696`, FleetDB PID
`50698`, and daemon PID `50702`. The running Loom executable reports build
`8b17cb261-phase4-repo-required-final`; its SHA-256 is
`16baced9dab5416bdfe351dc0c4a8e4f1b46958eb49fefbfaf7f4a531c09dfb9` and
matches the bundled executable sidecar. This final-package evidence does
not create a new immutable source validation snapshot.

## Current reliability validation

The refreshed package closes the split-evidence gap above against exact Loom
head `67c45972f286f2f6c111fde9306720728dc6c4b4`. It reports
`loom version dev (67c45972f)`; the bundled Loom executable SHA-256 is
`ffb4c7de517d66f84274ce8d269d279a24a79399eac9429d42a6a5e2ea5e1e57`,
and the packaged prompt-agent digest is
`a05189181463581fa9e2317f92e8a3e4a33220a5fe04c2346a7a398bf369134e`.
Actual packaged Desktop interaction through Computer Use produced this fresh
journey:

1. Create repository-free workspace `PHASE4-RELIABILITY-67C45972F` through
   the fixed product form, then create task
   `PHASE4-RELIABILITY-67C45972F-1`. The board and detail show Blocked,
   `Repo: No repo`, and zero agent runs.
2. Admit `https://github.com/octocat/Hello-World` through the product UI. The
   task remains Blocked until `hello-world` is selected on the task, then moves
   atomically to Open without a premature run.
3. Run the legacy planner. It completes exit zero, persists the design, and
   moves the task to Review. That planner used its normal configured Codex
   model and is **not** claimed as GPT-5.6 Terra.
4. After restarting with the transparent coder wrapper first on `PATH`, run the
   autonomous coder. The wrapper records
   `kind=exec model=gpt-5.6-terra prompt_mode=stdin`; the session completes exit
   zero in `58.566574s` via `local-cli-codex`, reports one file / one added line
   / zero removed lines, and exposes both transcript and exact diff. Patch-back
   is `applied` in the durable task worktree at head `7fd1a60b01f9`; the task
   converges to Closed. The added file is
   `phase4-reliability-proof-67c45972f.txt`, containing exactly
   `Phase 4 current packaged Desktop repository recovery proof on Loom 67c45972f.`
   followed by LF. This does not claim mutation of the base clone, a delivery
   branch, commit, push, or PR.

Read-only task/session provenance agrees with the UI: the task is closed,
bound to `hello-world`, and has persisted design; the coder session is
completed with exit zero, transcript, diff, `patch_back_status=applied`, and
the metrics above. Local screenshots, JSON captures, and the wrapper log remain
ignored under `artifacts/phase4/desktop-reliability-67c45972f/`; this checked-in
record and the machine snapshot are the portable evidence and do not depend on
those local files being committed.

The independent raw-browser proof used checkout-scoped run
`phase4-raw-workflows-67c45972f286-20260722t061455z` and compose project
`loomcli-phase4-raw-workflows-67c45972f286-20260722t061455z` on host-loopback
ports FleetDB `8580`, Loom API `8582`, and UI `8583`. The supported
`make local-mode-codex-workflows-up` target ran with the TS execution plane,
Codex CLI `0.144.4`, the configured live Codex home, and pinned Flue
`492bf47b9f3d6c379d00471523987b8fe9511f7d`. The verifier passed the exact fresh
planner task in Review with released ownership, persisted design, a completed
Codex exit-zero session and transcript; it passed the coder task in Closed with
a completed Codex exit-zero session, transcript, and exact
`local-mode-agent-output.txt` diff. It also asserted zero automatic agent
definitions, daemon processes, and daemon control sockets. This raw run's model
was not independently attested as Terra.

Agent Browser opened the same loopback UI without a launch code or operator
credential, rendered the fresh board, planner design and transcript, and coder
transcript and exact diff. Its console and page-error inspections were empty.
The ignored screenshots and run manifest under the corresponding
`artifacts/phase4/podman-raw-browser-*` directory are convenience captures, not
the portable source of truth.

The appended `phase4-reliability-validation-67c45972f` snapshot binds those
product journeys and the completed focused frontend (`32/32` Vitest, `2/2`
Playwright, changed-file ESLint, production build), focused backend, exact
paired-contract, FleetDB full-gate, and current architecture results to FleetDB
`9ffa69f6028969c03913c08c1159910fc772bd8b` and OpenAPI SHA-256
`ebf2ec68fd5751fbb59747c7b3db7b66fe4f7f80f30cb7eead9b6b3fd35ccb9e`.
The exact-head architecture check passed all 11 profiles plus the all-files AST
pass with Store `82/71`, 90 handler exceptions, 251 direct-write rows across
273 sites, four active roots, 60 mutation commands, 86 runtime components, 103
goroutine definitions, six of six measured performance records, and zero
pending decisions. The architecture package passed in `47.768s`, then passed
again in `46.926s` after the final gate row was promoted. The aggregate
Loom gate then passed against the exact paired FleetDB source and sidecar with
an isolated HOME, `GOMAXPROCS=4`, `GOMEMLIMIT=3GiB`, and one Vitest worker. Its
parallel Go and frontend lanes cover the complete checked-in gate; the record
does not infer that pass from the prior UI-independent source gate.

## Completion evidence

The baseline retains the appended
`phase4-execution-precommit-1353e2faf` snapshot as a **provisional historical
record** of the source state on which it was captured. Its recorded 53-command
architecture result, architecture-test duration, FleetDB head, and OpenAPI hash
are not rewritten as the implementation changes. It is superseded for current
source status by the live architecture check and must not be cited as the final
paired contract or product-gate record.

The immutable `53cbe2577` evidence includes the passing architecture check
with the counts above, byte-identical FleetDB/Loom OpenAPI snapshots at SHA-256
`26ed930bc527c3815742c8b4c7a0ba5267bdc91c585ddc9f78483d9373303482`, the
green source-bound supervisor-disabled row, and the 30-sample artifact-backed
product measurement (p50 `11.796 ms`, p95 `14.784 ms`, exactly three
Loom-to-FleetDB requests). FleetDB `make gate` passes, including race,
Redis/Postgres contracts, container E2E, harness evaluation, and 82.7% overall
coverage. Loom `make gate` passes against the exact paired FleetDB source and
freshly built binary, including architecture, Go, SDK, and frontend gates; the
frontend suite reported 8,572 passing and one expected skipped test (8,573
discovered). The implementation sequence is Loom `510391c60`, `45a73889a`,
`8037205da`, `a240215be`, and `53cbe2577`, with FleetDB `424492070`,
`758842a7e`, and `afb688768`. A concurrent gate in a separate
checkout initially corrupted the shared `/tmp/loom.coverage.out` with a sparse
NUL region after every preceding Go stage had passed; the final uncontended
run is the recorded gate result rather than that environment-collided parser
attempt.

The broad exploratory Playwright run is recorded but is not claimed as a Phase
4 gate: 172 passed, 41 failed, six were interrupted, 95 skipped, and 1,166 did
not run. The required targeted frontend and aggregate gates passed for that
snapshot. Its packaged Desktop positive Terra and unavailable-backend journeys
passed, Codex was restored as default, and that package was running and
healthy when captured. The appended
`phase4-execution-validation-53cbe2577` immutable snapshot binds these results
to Loom `53cbe257715d55770c77d508c23620389b9c9de1`, FleetDB
`afb6887682f777b0e7093b5dcdff0a5e236777f9`, and the final contract hash. The
subsequent evidence-only commit changes documentation and the append-only
baseline, not the validated product source.

The core reliability-hardening source is committed at Loom
`ee971be22feb3c93096d599b7e3a62bff2cb0fa2`; the current product-validation
head adds repository-free workspace creation at
`67c45972f286f2f6c111fde9306720728dc6c4b4`, paired with FleetDB
`9ffa69f6028969c03913c08c1159910fc772bd8b`. FleetDB `make gate` passes its
static analysis, race/unit matrix, integration pipeline (`200.984s`), Redis and
Postgres storage contracts, Postgres API integration (`11.641s`), aggregate
coverage (`81.4%` with all 28 enforced packages above the 50% floor), container
E2E (`99.309s`), and harness evaluation. The exact paired FleetDB binary used
by Loom has SHA-256
`3dbd0f4ab3748929707bf851fe5f046466316a53b2929a552c70db941cb3e3dc`.
Loom `make gate` passes at the immediately preceding core-hardening source under
a fresh HOME against that exact FleetDB source and binary, including Go,
architecture, SDK, built-in workflow, and frontend test/build gates. The exact
current-head architecture check passes all 11 profiles plus the all-files AST
pass with Store `82/71`, 90 handler exceptions, 251 primary direct-write rows
across 273 sites, four active roots, 60 required command-ID namespaces, 86
runtime components, and 103 goroutine definitions; the architecture package
passes in `47.768s` and its post-promotion rerun in `46.926s`. The paired
OpenAPI snapshots are byte-identical at SHA-256
`ebf2ec68fd5751fbb59747c7b3db7b66fe4f7f80f30cb7eead9b6b3fd35ccb9e`.

Those results plus the current validation section make the paired contract,
architecture, focused source, FleetDB gate, packaged Desktop, Podman verifier,
and raw-browser rows green. A lost successful Repo-create response still
converges as a documented `409` retry rather than an idempotency-receipt replay;
compensated Redis failures may consume a stream ID while leaving no visible
projection or event. The appended
`phase4-reliability-validation-67c45972f` snapshot binds the exact current
product and architecture proof instead of rewriting the historical snapshots.
Its aggregate Loom gate also passes under the bounded-memory profile. Phase 5
has not started.

---

[All migrations](../README.md) · [Migration overview](README.md) · Previous: [Phase 3 evidence](08-phase-3-decisions-and-evidence.md)
