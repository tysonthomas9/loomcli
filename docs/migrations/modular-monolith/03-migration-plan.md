# Migration Plan

- **Status:** Phase 6 complete, including source, paired-service, daemon-free,
  signed packaged-Desktop UI, and fresh real-Codex acceptance
- **Strategy:** Incremental vertical extraction aligned with active product work; no standalone big-bang reorganization
- **Migration:** [Modular Monolith Migration](README.md)

## Sequencing principles

1. Integrate the current bases before committing characterization tests, enforcement code, or package moves.
2. Keep behavior-preserving structure changes separate from security, policy, schema, or API behavior changes.
3. Move one production use case through every applicable inbound surface: durable command, capability API, adapters, and end-to-end proof. An existing UI may prove the new backend seam without moving frontend files; physical frontend extraction is its own slice.
4. Preserve existing CLI, HTTP, SDK, OpenAPI, and fleet-db behavior during structural steps.
5. Delete code scheduled for retirement rather than polishing or relocating it.
6. Use temporary facades only as stranglers with an owner, removal issue, and expiry.
7. Recompute the capability graph and migration residual after every slice.

The migration is an implementation method for the unified-agent, custom-driver, interactive-runtime, and supervisor-retirement work. It must not become a fourth competing roadmap.

## Workstream mapping

| Capability work | Existing delivery lane |
|---|---|
| Workflow Catalog and Automation | Custom-driver authoring, trigger admission, trust/operation gating |
| Execution and runtime host | Task-ready execution, reliability invariants, supervisor retirement |
| Agents and Interaction | Unified durable identity, chat/PTY convergence, prompt rendering |
| Source Control materializer | Credential removal and workspace materialization |
| Work Items | Claim/release/finish semantics as touched by execution work |
| Connectors | Grant/authority changes as touched by agent and source-control flows |
| Artifacts | Transcript, diff, patch, and upload lifecycle as touched by Execution/Interaction |

After Phase 0, these are dependency lanes rather than one serial queue:

| Slice | Hard prerequisites |
|---|---|
| Catalog read seam | Phase 0, ownership graph, and characterization baseline |
| Catalog mutations | Operator-authority decision plus atomic fleet-db commands |
| Automation | Catalog public API plus admission characterization |
| Execution replacement | Phase 0, heartbeat/stale-detection reliability fixes, and the minimal Artifacts create/finalize/reference seam |
| Agent/Interaction migration | Post-`v5` identity model, session/lead characterization, and minimal Source Control + Connectors broker APIs |
| Supervisor deletion | Execution parity, identity bridge, `agentdef` migration, daemon-dependency removal, and disabled matrix |
| Frontend feature slice | Stable backend public API for that feature |

## Phase 0 — integrate and re-baseline

This phase is a hard gate.

**Recorded status:** Complete. Steps 1 through 6 remain proven by the immutable integration record and were refreshed for the Phase 1 source at Loom `122d4d79` and FleetDB `8120c788`, with matching OpenAPI snapshots and current gates. Phase 1 completed step 7 with type-resolved direct-write, authority/transaction, named-runtime, and performance inventories and completed step 8 across this migration folder. See the [Phase 0 integration baseline](00-phase-0-baseline.md) for revisions, conflict resolutions, commands, evidence, and the explicit not-yet-migrated performance values.

1. Fetch and record the exact fleet-db base, Loom `v5`, and both branch-head SHAs.
2. Merge `origin/main` into the companion fleet-db branch; resolve migrations and `api/openapi.yaml` there first.
3. Merge `origin/v5` into LoomCLI; resolve Role, agent, terminal, and platform changes against the final fleet-db shape.
4. Copy the final fleet-db spec snapshot into Loom and make the vendored checksum/contract guard green.
5. Run fleet-db `make gate`.
6. Run Loom `make gate`, `make test-fleetdb-supervisor`, and `make local-mode-verify`; archive the results separately because the latter two prove the current supervisor path, not its absence.
7. Regenerate package/import, Store, direct-write, authority, transaction, loop, and performance inventories.
8. Replace every dated value in this migration folder, not only [01-current-state.md](01-current-state.md).

Store the commands, environment, expected skips, commit SHAs, spec checksum, results, and artifact paths in a checked-in baseline manifest. The historical `docs/design/2026-07-03-unified-agent-ui-test-matrix.tsv` is evidence, not an executable gate.

The Phase 0 snapshot had no clean-checkout supervisor-disabled matrix and correctly recorded **RED / harness absent**. Phase 1 checked in `test/modular-monolith/supervisor-disabled-matrix.yaml` plus `make test-supervisor-disabled`; at that milestone its stable `deterministic-plan-coder` row remained intentionally **RED** under `execution-reliability-lane`. Phase 4 supplies the deterministic TS leaf, ordered public-API seeding, daemon-free local-mode path, and zero-daemon assertions needed to turn that one row green. It remains an Execution-lane proof, not the Phase 6 full supervisor-deletion matrix.

Documentation and test design were refined before this phase. The first committed code change was the base integration, followed only by the compatibility fix required to make the merged runtime pass; no pre-merge “mechanical” package move was made.

## Phase 1 — parallel guardrail and reliability lanes

After Phase 0, these two lanes run independently. Catalog work does not wait for Execution reliability fixes.

**Completed:** The `modular-monolith-phase1` branch contains the behavior-neutral guardrails, approved MM-1 through MM-7 outcomes, completed Phase 0 inventories, characterization gate, productized RED supervisor-disabled contract, and the bounded reliability fixes below. No `internal/modules/*` capability root was introduced, so this milestone does not claim the Phase 2 pilot.

### Architecture guardrail lane — complete

Establish the migration controls without moving product behavior:

1. Resolve the open decisions in [README.md](README.md) and mark the migration documents reviewed.
2. Check in the aggregate-owner matrix and machine-readable capability graph.
3. Add a mutation/authority/transaction ledger for commands being migrated.
4. Add forbidden-import and cycle checks with a ratcheted legacy baseline.
5. Ratchet composite `store.Store` consumers and direct handler/CLI persistence writes.
6. Add post-merge characterization tests around workflow approval, trigger admission, agent provisioning, execution recovery, and supervisor policy.
7. Productize `test/modular-monolith/supervisor-disabled-matrix.yaml` and `make test-supervisor-disabled`; its execution rows may remain red until Phase 4, but setup and failure reporting must be deterministic.

### Execution reliability lane — complete

Phase 1 closed the reliability prerequisites without restructuring their capability ownership:

- `serve` now sources the stale-task sweeper's twenty-minute default instead of overriding it with five minutes;
- stale recovery uses the earlier of a monotonic projection and the live wall clock, so forward jumps cannot mass-age records and backward jumps conservatively protect fresh post-jump heartbeats until the new clock advances;
- standalone leads, serve-hosted task sessions, and supervisor-owned AgentSession/AgentLease records have explicit heartbeat tests; and
- caller-declared FleetDB capability requirements are checked during store startup with typed failures for an old 404 endpoint, an unsupported manifest revision, or missing keys; empty requirements preserve legacy startup.

MM-3 still requires Redis/Postgres parity before a Phase 2 capability key is advertised. Phase 1 supplies the fail-fast Loom readiness seam; it does not implement or publish the Workflow Catalog capability on FleetDB.

The four upward edges found by the legacy plane scan can be fixed when their owners are touched. They are not prerequisites for the capability pilot unless they obstruct its graph.

## Phase 2 — first backend pilot: Workflow Catalog

The pilot is intentionally narrower than the entire driver subsystem and must prove more than folder isolation.

**Completed:** The Phase 2 branches start from Loom `7e8a6dd2` and FleetDB `8120c788`; implementation heads are Loom `84cccb761` and FleetDB `430dce8d9`. The public Workflow Catalog root, typed authority, shared FleetDB adapter, Redis/Postgres lifecycle commands, FleetDB and Loom HTTP routes, capability negotiation, and management CLI are implemented. Paired gates, negative cases, contract checksum, real route/CLI and packaged UI journeys, checkout-scoped deterministic local-mode integration, and both target-path measurements pass. The graph is ratcheted to `completed_phase: 2`; the exact ledger is in [07-phase-2-decisions-and-evidence.md](07-phase-2-decisions-and-evidence.md).

### 2A — establish a read seam

- Introduce Workflow Catalog public queries such as `GetVersion` and `ResolveEffectiveVersion`.
- Wrap the existing persistence implementation behind catalog-owned ports.
- Route HTTP readers through the catalog API. Convert standalone CLI readers under MM-7 in a compatibility slice that removes direct Store reads and proves explicit discovery and unavailable-host behavior.
- Keep wire responses and observable behavior unchanged.

This is a useful structural checkpoint, not a completed pilot.

### 2B — prove mutation, authority, and atomicity

Under the approved MM-2 operator/open-mode authority model:

1. Add fleet-db intent commands for `ApproveVersion`, `UnapproveVersion`, and `ActivateVersion`.
2. Atomically validate driver/version ownership and validation status, preserve unrelated `Driver.Metadata`, and use an expected revision or equivalent CAS.
3. Implement the service/storage operation for Redis and Postgres, plus concurrency, CAS-conflict, duplicate/lost-response retry, and unrelated-metadata-update tests.
4. Add the fleet-db API route, auth permission, API tests, and `fleet-db/api/openapi.yaml` contract; publish a versioned `workflow_catalog.version_lifecycle.v1` key through `GET /api/v1/capabilities` only when the active backend, routes, and configuration make all three commands usable.
5. Add the Loom handwritten client method and capability adapter; update `clientRoutes`, `expectedClientCallSites`, the vendored spec snapshot, and `internal/infra/fleetdb/contract_guard_test.go`. Composition creates one low-level fleet-db client and injects a narrow Workflow Catalog adapter rather than constructing another client. Named application workflows use the same client only through an injected app-local implementation of their own atomic-command/coordination port.
6. Make Loom derive required capability keys from enabled slices, negotiate them during readiness, and report explicitly when the endpoint is absent or a key/version is unsupported. Test Redis/Postgres and enabled/disabled route/configuration profiles so a static binary-level key cannot pass falsely.
7. Route HTTP mutations through one Workflow Catalog command API; keep the existing Workflow UI wire behavior unchanged and prove its approval journey with targeted route-level E2E.
8. Under MM-7, convert the standalone workflow CLI in a separate contract/behavior slice; remove its direct Store mutation path rather than letting CLI code construct `OperatorAuthority`, and test explicit endpoint discovery, the prohibition on implicit host startup, unavailable-host failure, local authentication, and existing script output/exit-code compatibility.
9. Add negative HTTP and CLI tests for execution, session, webhook, unauthenticated, and wrong-workspace callers.
10. Remove any temporary legacy-authority adapter.

The paired PRs record their companion SHA and spec checksum. Fleet-db lands/deploys first and remains compatible with old Loom. New Loom fails readiness clearly against old fleet-db and never falls back to generic whole-record `UpdateDriver`, which would recreate the lost-update defect. Generated-client adoption is a separate migration; the current contract guard proves route/snapshot presence, not schema semantics or atomicity.

The pilot is complete only when it proves capability ownership, durable atomicity, typed authority, version-skew behavior, cross-repo contract discipline, and multiple inbound adapters.

Agents is deliberately not first: it currently sits at the collision point of incoming Role changes, AgentService, TriggerBinding, grants, terminal sessions, and all active workstreams.

## Phase 3 — Automation and custom-driver lanes

**Architecture activated:** The public Automation root, its 14 mutation
contracts, named ingestion/event workflows, durable FleetDB operations, and
runtime-host components are implemented and the graph is ratcheted to
`completed_phase: 3`. Architecture checks, focused suites, public E2Es,
performance sampling, the contract checksum, and checkout-scoped generic plus
webhook local-mode verifiers and the authenticated browser
create/activate/manual-run/history journey pass. The final post-correction
FleetDB and Loom aggregate gates also pass; see the [Phase 3 evidence](08-phase-3-decisions-and-evidence.md).
Source activation alone is not a substitute for that proof. GPT-5.6 Terra review is
not recorded because exporting the local screenshots to an external model
awaits explicit informed approval; this is not a product failure.

- Extract TriggerBinding, Event, Delivery, cron, and webhook application APIs.
- Centralize actor filtering, hop depth, and idempotency in one admission command.
- Register Automation schedules and retry components with the runtime host.
- Resumable SSE is not implemented, validated, or claimed by Phase 3. Existing SSE behavior remains unchanged. The work is assigned to a separately reviewed behavior-changing follow-up branch/PR that must define reconnect and replay semantics, persistence and offset ownership, version skew, rollback, and end-to-end proof. Partial SSE plumbing does not count toward Phase 3 completion.
- Deliver new custom-driver lanes through the module API rather than pausing product work for a directory rewrite.
- Keep Workflow Catalog immutable-version/trust policy separate from Automation matching/delivery policy.
- Close the generic await split-commit hazard with one Execution-owned
  resolve-and-resume command, Redis/Postgres parity, and an always-required
  `execution.await_atomic_resume.v1` readiness key. Stable Await/event
  identities must converge a suspended parent after a lost response; no legacy
  resolve-then-resume fallback is allowed.

## Phase 4 — Execution replacement and supervisor-disabled operation

**Complete:** Execution and the minimal Artifacts lifecycle are active in the
capability graph. The architecture check, byte-identical paired OpenAPI
contract, FleetDB and Loom aggregate gates, source-bound supervisor-disabled
rerun, measured artifact-backed design path, and exact packaged Desktop
positive/fail-closed journeys pass at validated product heads Loom
`53cbe2577` and FleetDB `afb688768`. The original Phase 4 pre-commit snapshot
remains an immutable provisional historical record and
`phase4-execution-validation-53cbe2577` remains immutable for the source it
names. The appended `phase4-reliability-validation-67c45972f` snapshot binds
the later recovery/repository hardening to refreshed packaged Desktop,
Podman/raw-browser, paired-contract, FleetDB-gate, focused-source, and
exact-head architecture proof at Loom `67c45972f` with FleetDB `9ffa69f60`;
its aggregate Loom gate also passes under the bounded-memory profile. Phase 5
has not started.
See the
[Phase 4 record](09-phase-4-decisions-and-evidence.md).

- Extract the minimal Artifacts `Create`, `Upload`, `Finalize`, `Reference`, `Get`, `List`, and composed `CreateContent` API before Execution starts writing through it; leave unrelated artifact UI/query scope for Phase 7.
- Establish intent-oriented ports for preflight, claim, launch, classify, recover, and finalize.
- Move DriverRun, DriverStep, TaskRun, worker, lease, await, and recovery invariants behind Execution commands.
- Keep backend-assigned DriverRun and TaskRun fences monotonic but in distinct namespaces; do not expose one interchangeable cross-resource Lease vocabulary.
- Bind DriverRun Work Item claim, TaskRun request, liveness, release, and terminal transitions to the exact immutable `ClaimActionID` generation. A stale TaskRun may never clear or rewrite a successor generation.
- Apply the typed terminal policy in the same FleetDB transition as the TaskRun, Lease, and owned Work Item mutation: close intent closes/unassigns; successful non-close completion moves to review/unassigned; cancelled or non-blocking failed completion opens/unassigns; retry exhaustion or explicit block leaves blocked/unassigned. DriverStep repair and terminal TaskRun-event convergence remain separate durable convergence paths.
- Register session/lease heartbeats for every launch path.
- Preserve the shipped SB3/SB4 sandbox gates while closing specifically named host-exec credential and placement gaps in separate behavior-change PRs.
- Port required supervisor restart/backoff, exit classification, concurrency, and resume behavior into the new owners. Do not move `AgentCommand` wholesale into Execution: batch ready-task dispatch bypasses it, while retained lead/session/interactive command delivery remains transitional Interaction scope through Phase 6.
- Run the Execution-tagged rows of `make test-supervisor-disabled`; the full matrix remains a Phase 6 gate after Agents/Interaction land.
- Remove `@loom/sdk/runner`'s direct-FleetDB mutation fallback. Phase 4 requires `LOOM_TASK_RUN_API_URL`; absence of the Loom owner facade fails closed rather than accepting FleetDB credentials or choosing a second mutation topology.
- Preserve Fleet owner fencing during recovery. A healthy DriverRun uses its owner-fenced stale-child command; a stale or crashed DriverRun is failed by system DriverRun recovery and its durable outcome cascade converges descendants. Phase 4 does not add a tokenless global TaskRun recovery sweep.
- Treat `needs_revision` as a typed runner outcome, not a magic stdout phrase. The backend may write only the exact versioned JSON disposition to the runner-owned `LOOM_TASK_OUTCOME_FILE`; after a clean exit the host records a cancelled, non-retryable TaskRun with `task_outcome=needs_revision`, adds the `needs-revision` label, and leaves the Work Item open/unassigned for another planning pass. Malformed outcomes fail closed, and a nonzero exit or fatal stream error takes precedence.

Do not move the supervisor into `internal/modules`. Characterize it, implement replacements in Execution/Interaction/runtime, and prove supervisor-disabled operation. Physical deletion waits for Phase 6.

## Phase 5 — Agents and Interaction

**Complete.** Agents,
Interaction, AgentProvisioning, and the minimal Source Control and Connectors
seams are active. The dedicated all-`internal` sole-writer scan finds zero
direct AgentSession, TerminalSession, AgentLease, or inbox mutations outside
Interaction and persistence adapters, and the graph is ratcheted to
`completed_phase: 5`. See the
[Phase 5 evidence](11-phase-5-decisions-and-evidence.md) for the exact
authority/retry contracts and green paired/package gates.

Begin only after the post-`v5` identity/Role model is stable. First establish the minimal Source Control materialization API and Connectors credential-broker API needed by this phase; leave their remaining UI/query/provider scope for Phase 7.

- Establish Agents as the sole owner of durable Agent identity, Role reference, and desired state.
- Separate identity operations from terminals, Git, PR, worktree, and execution operations currently grouped under `AgentService`.
- Move prompt/scripted agent provisioning into the named `AgentProvisioning` process manager.
- Make provisioning idempotent and restart-recoverable across Role, Agent, Binding, and Grant writes. If `EnsureRole` is independently committed and an unused Role is acceptable, record that outcome explicitly in the process-manager contract.
- Extract chat, PTY, inbox, and session lifecycle into Interaction.
- Give interactive child processes a fenced `SessionAuthority` and a filtered environment.
- Keep AgentSession and batch-run persistence distinct; expose a combined activity query only.
- Migrate `agentdef`, including per-PR reviewer rows, and remove lead/terminal dependencies on daemon IPC and `AgentCommand` task spawning.
- Move private-repository checkout/materialization behind the Source Control + Connector credential-broker seam before removing credentials from interactive/task environments.

## Phase 6 — final supervisor deletion gate

**Complete.** Loom `02daec339` and FleetDB `51b8a493` physically
retire the enumerated paths and pass their full gates. The authoritative
seven-row Phase 6 matrix runs with exact test discovery plus mandatory
`run`/`pass` evidence and rejects every skip. The fresh supervisor-disabled
stack passes planner/coder design, transcript, diff, zero-agentdef, zero-daemon,
and zero-socket assertions. The exact signed package separately completed a
UI-created real-Codex planner/coder task with both transcripts, local-branch
delivery, a one-file diff, and Review convergence. See the
[Phase 6 evidence](13-phase-6-decisions-and-evidence.md).

Delete the supervisor only after Phases 4 and 5 are green. The parity ledger explicitly enumerates and retires:

- `internal/cli/daemon/supervisor`;
- daemon control/IPC and the exact `rpc` calls no longer used;
- `webui/daemon` integration;
- `AgentCommand` paths replaced by Execution/Interaction commands;
- `DaemonProfile` and supervisor-only configuration;
- builtin plan/task supervisor defaults and task-type arbitration toggles;
- legacy role-agent, binding-proxy, auth, attribution, and cron compatibility paths.

Required proof includes stale-timeout and all-launch-path heartbeat fixes, `LOOM_TASK_READY_EVENTS` generic default, TS execution-leaf default, replacement of `LOOM_LOCAL_MODE_PLANE=ts` with explicit arbitration, preflight/error classification/retry/recovery/concurrency/epic decisions, periodic desired-state reconciliation, `agentdef` migration, and a full matrix with no unowned `t.Skip` or disabled rows.

## Phase 7 — remaining capabilities and frontend slices

- Complete the remaining Work Items, Workspace, Source Control, Connectors, and Artifacts scope as active product changes touch it; do not recreate seams already required by Phases 4 and 5.
- Use Observability as the lower-risk first frontend feature slice; defer Source Control UI movement until the credential-broker seam is stable.
- Move a full UI route—view, hooks, API mapping, state, components, and tests—behind one feature public entry.
- Preserve Terminal's always-mounted route behavior while Interaction moves.
- Replace global frontend API/hooks/stores/contexts/components/type barrels incrementally.
- Reduce `WorkspaceViewContext` to explicit app-level composition and feature-local state.
- Reduce `App.tsx` to routing, providers, and shell composition.

Do not combine desktop/SDK distribution or security changes with bulk frontend path movement.

## Strangler mechanics

### Type ownership

When moving an owned model:

1. Move the canonical type into the owning capability.
2. Temporarily alias the legacy `domain`/`entity`/`types` name to the capability type.
3. Ensure the new capability does not import the legacy package, avoiding a cycle.
4. Migrate consumers through the public capability API.
5. Remove the alias within the declared expiry.

Do not create a second mutable DTO and mapper unless the legacy representation is a true transport contract.

### Persistence

- Wrap existing sub-stores behind consumer-owned ports first.
- Construct one shared low-level fleet-db HTTP/auth/retry/connection-pool client in composition and wrap it with narrow capability-owned adapters; do not inject that client into modules or build one client per capability.
- Move business defaulting, routing derivation, and transition rules out of persistence.
- Add a fleet-db service command before splitting any currently atomic operation across ports.
- Keep fleet-db HTTP as the sole runtime persistence path. In-memory implementations are test-only fakes/conformance fixtures; converge those fixtures capability by capability rather than building ten runtime clients.

### Compatibility facades

Every facade or alias records:

- introduction PR and owning capability;
- callers still using it and a machine-readable remaining-call-site list;
- replacement API and removal issue;
- last permitted milestone/PR.

Approved limit: remove it within two subsequent migration waves. The graph-gate fixture fails when the declared milestone completes while callers remain. A facade that needs longer requires an explicit reviewed extension with an owner; aliases and legacy imports ratchet alongside Store references.

### Commit and review shape

Prefer this sequence where possible:

1. characterization tests;
2. public API and ports;
3. behavior-preserving delegation;
4. caller migration;
5. separately reviewed policy/schema/security change;
6. facade deletion and tighter enforcement.

Keep pure path moves separate from behavior changes so Git history remains reviewable and bisectable. A pure-move commit is allowed only inside an approved stacked extraction; a slice cannot be declared complete until delegation, caller migration, tests, and tighter enforcement land.

## Completion

The migration completes when:

- all capability-owned mutations enter through owner public commands or a declared cross-aggregate coordinating command;
- standalone mutating CLIs use the authenticated management API rather than direct Store/FleetDB paths;
- no capability code receives the composite Store;
- global `domain`, `store`, `webui/service`, and `svcimpl` packages no longer serve as business-logic centers;
- supervisor code is deleted;
- compile-time import and synchronous command/query graphs are default-deny and cycle-free; any durable-event cycle is explicitly declared, bounded, and idempotent;
- every cross-capability write is atomic or recoverable;
- direct SDK/FleetDB mutation fallbacks are removed and missing Loom owner facades fail closed;
- frontend feature boundaries are enforced;
- the validation requirements in [04-enforcement-and-gates.md](04-enforcement-and-gates.md) are green.

---

[All migrations](../README.md) · [Migration overview](README.md) · Previous: [Target architecture](02-target-architecture.md) · Next: [Enforcement and gates](04-enforcement-and-gates.md) · [Phase 4 evidence](09-phase-4-decisions-and-evidence.md)
