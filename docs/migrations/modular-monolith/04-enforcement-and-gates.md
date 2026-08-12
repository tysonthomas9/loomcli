# Enforcement, Validation, and Stop Conditions

- **Status:** The Phase 4 architecture slice is complete, with paired full gates and exact packaged-Desktop positive, fail-closed, and restart-persistence proof at Loom `f0011b248` with FleetDB `de89f0544`; Phase 5 has not started
- **Purpose:** Define the fitness functions that distinguish a real modular monolith from a folder reorganization
- **Migration:** [Modular Monolith Migration](README.md)

**Phase 4 status:** Analyzer `1.0.0` enforces all four release and seven tag/race profiles plus the all-files AST pass, rejects undeclared module roots/edges and cycles, and detects forbidden signature/type leakage. Workflow Catalog, Automation, Execution, and minimal Artifacts roots are active at `completed_phase: 4`, and the mutation ledger contains 61 required command-ID namespaces grouped by prefix: three `workflowcatalog.*`, 14 `automation.*`, four `artifacts.*`, and 40 `execution.*`. Those prefix counts are not aggregate-ownership counts; each ledger row's `aggregate_owner`, `coordinating_owner`, and `instance_owner` remain authoritative. The latest completed architecture check passes with 82 composite-Store files, 71 outside composition, 90 legacy handler-import exceptions, 251 primary direct-write rows across 272 sites, and a separate 10-row/11-site `internal/driver` digest ratchet. Runtime evidence names 86 components, 53 ticker sites, 58 managed components, and 105 in-scope non-test goroutine launch definitions. The source-bound Execution supervisor-disabled row passes against the paired Phase 4 FleetDB worktree; it remains a scoped Execution-lane proof, not the Phase 6 supervisor-deletion gate.

The current hardening source is committed through Loom `f0011b248` and
FleetDB `de89f0544`; their OpenAPI snapshots are byte-identical at SHA-256
`c87f72aaeef1d1967ab2a70f67650555c371c0d00b1e04073bfbc842666a318b`.
Both full gates pass. The exact packaged app contains all six supported
built-in workflows, passes deep strict signing verification after ad-hoc
resealing, completes a real-Codex Local Review run with transcript evidence,
fails closed to Blocked when Codex is unavailable, and preserves that state
across restart.

## Architecture source of truth

Phase 1 established the checks in `internal/archtest/testdata/capability-graph.yaml`, with `analysis-matrix.yaml`, `migration-baseline.json`, `direct-writes.yaml`, `mutation-ledger.yaml`, `runtime-components.yaml`, and `performance-baseline.yaml` beside it. Workflow Catalog, Automation, Execution, and Artifacts are now `active`, `completed_phase` is `4`, and the ledger records the complete current 61-namespace inventory selected through Phase 4 and its hardening delta. The baseline retains the immutable Phase 2 snapshot, the self-reference-free Phase 3 base-plus-diff measurement, and the Phase 3 post-commit audit. Its retained Phase 4 pre-commit snapshot remains explicitly provisional and is not rewritten. The appended `phase4-execution-validation-53cbe2577` snapshot remains the source, contract, gate, performance, source-bound, and packaged-product record for the exact source it names. The newer `phase4-reliability-validation-67c45972f` record remains immutable evidence for its named paired contract, FleetDB gate, packaged Desktop repository recovery/Terra coder, Podman/raw-browser journeys, exact-head architecture checks, and aggregate Loom gate. The final current-head closure is recorded in [Phase 4 evidence](09-phase-4-decisions-and-evidence.md#final-exact-packaged-builtin-closure) without rewriting either historical snapshot. The graph declares:

- every capability root;
- allowed capability-to-capability import, synchronous command/query, and durable event edges;
- `app` and `platform` restrictions;
- default-deny third-party imports for capability and named-workflow cores/adapters, plus the core standard-library infrastructure denylist;
- a conservative `completed_phase`, which makes legacy-path and direct-write expiry fail once the declared last permitted phase completes rather than when implementation merely begins;
- temporary legacy paths and their owners/expiry;
- the [Phase 0 baselines](00-phase-0-baseline.md) for broad Store use and type-resolved direct adapter writes.

The human ownership tables in [02-target-architecture.md](02-target-architecture.md) and the machine graph change together. An import exception without an ownership decision is not accepted.

The `internal/workflows` legacy-path expiry is explicitly extended from Phase 3 to Phase 5 under `workflow-distribution-lane`. It cannot truthfully expire in Phase 3: the package still mixes embedded source authoring/catalog duties with trusted global-runner resolution, and 16 checked-in Go callers remain. The graph records the rationale, Workflow Catalog and Execution replacement roots, and the exact sorted caller list; `TestLegacyWorkflowsExtensionMatchesCurrentCallers` fails if that evidence drifts. Phase 5 is the last permitted milestone under this review, not a silent waiver.

Phase 4 also freezes the remaining mixed `internal/driver` mutation sites as a
content-addressed legacy ratchet until Phase 6 completion. The baseline records exact
10 rows, 11 call sites, capability-owner distribution, and a SHA-256 digest. This is
containment evidence, not a claim that cross-capability driver code became
Execution-owned merely because Execution is now active; any drift fails until
the remaining lanes move to their actual owners or the Phase 6 path expires.

## Go dependency gates

1. Only a capability's public root may be imported by another capability.
2. Capability cores import no `webui`, `cli`, transport adapter, infrastructure implementation, unapproved third-party dependency, or denied standard-library infrastructure package.
3. Capability adapters may import their own public root and adapter subtree, approved platform mechanisms, and reviewed external dependencies; only a `fleetdb` adapter may import the shared FleetDB transport. They may not import another capability, including its public root.
4. `platform` imports no product capability.
5. `app/serve` may construct public roots/adapters but owns no product state or policy.
6. A named `app/<workflow>` core may call multiple public roots, its own packages/ports, approved platform mechanisms, and reviewed neutral dependencies, but no module internals, concrete adapters, repositories, or unapproved infrastructure. `app/serve` alone constructs and injects the app-local adapter for an atomic-command or coordination-state port.
7. Compile-time import and synchronous command/query graphs must both be acyclic.
8. Unknown module roots or edges fail by default.

Durable event cycles are permitted only when explicitly declared, idempotent, bounded by actor/hop/re-entry admission, and incapable of synchronous recursion. `go list` validates imports only; a manifest checker validates command/query/event edge kinds.

Use both:

- depguard for direct per-file restrictions; and
- a `go/packages`/AST analyzer across production, tests, and the finite variants in `analysis-matrix.yaml`, because depguard and simple grep cannot detect leaked signatures, embedded Store fields, or paths such as `A → B → forbidden C`.

The initial matrix is explicit rather than “all build tags”:

- untagged packages for the release targets `linux/amd64`, `linux/arm64`, `darwin/amd64`, and `darwin/arm64`;
- Linux/amd64 profiles for `integration`, `e2e`, `testbackend`, `issuebackend_e2e`, `container`, `playground`, and `race` source selection;
- an AST-only pass over every non-generated `.go` and `_test.go` file regardless of selected tags, so Windows/wasm and mutually exclusive files cannot hide a forbidden import or signature;
- `//go:build ignore` files excluded only through a named allowlist entry with a reason and expiry.

Adding a supported target or build tag updates the matrix in the same PR. The analyzer reports the exact target/tag profile for each violation.

The analyzer must reject:

- a composite Store field or parameter in a migrated production API;
- an exported capability signature leaking legacy `domain`, `store`, `webui/service`, adapter, or implementation types;
- new imports of legacy `internal/domain`, `internal/driver`, `internal/workflows`, `webui/service`, or `svcimpl` from a migrated slice;
- module tests that bypass public APIs by injecting the composite Store, except declared legacy/conformance fixtures.

Store the baseline as machine-readable data with commit SHA and analyzer version.

The handler depguard rule was corrected from singular `**/handler/**` coverage
to the current `**/handlers/**` and future
`**/internal/modules/*/httpapi/**` adapter paths. The enforced rule:

- cover existing plural handlers and future `modules/*/httpapi` adapters;
- direct migrated handlers to capability public APIs;
- ratchet legacy `webui/service` imports instead of institutionalizing that soon-to-be-retired layer;
- avoid making the first guardrail PR an unrelated big-bang service rewrite.

## Coupling ratchets

These ratchets were initialized from the [Phase 0 integration baseline](00-phase-0-baseline.md), checked into the machine-readable inventories, and are enforced on every extraction:

| Measure | Rule |
|---|---|
| Production `store.Store` references | Never increase; each migrated slice reaches zero |
| Composite Store outside composition/legacy adapters | Never increase; end target is zero |
| Direct persistence writes from HTTP/CLI adapters | Never increase; migrated slice reaches zero |
| Direct generic Lease/ActionLedger access outside owner-scoped adapters | Never increase; migrated slice reaches zero |
| Imports of another capability's internals/adapters | Always zero |
| Compile-time import and synchronous command/query cycles | Always zero |
| Compatibility facades past expiry | Always zero unless an explicit decision extends one |

Raw package count, file count, and fanout are diagnostic only. They must not be used as a substitute for mutation ownership or public-API compliance.

## Ownership and transaction gates

For every mutation command, the ledger records:

- aggregate owner;
- coordinating owner for any cross-aggregate intent;
- mechanism discriminator and per-instance owner for polymorphic records such as Lease;
- accepted authority classes;
- durable command(s) used;
- transaction or saga boundary;
- idempotency key;
- emitted semantic event/read-model impact;
- retry and restart behavior;
- negative and fault-injection tests.

Required properties:

1. Every mutable record instance has exactly one owner; a polymorphic table is mapped by discriminator rather than assigned to a generic shared module.
2. A non-owner may keep an ID or immutable projection, never mutate the owner's repository.
3. A cross-capability write uses one atomic fleet-db command or a documented recoverable process manager.
4. Fleet-db enforces atomicity, CAS, fencing, and persisted preconditions.
5. Loom capabilities enforce product policy and use-case invariants.
6. Adapter validation is never the only correctness check.
7. A process manager owns only a durable coordination record through its own port: idempotency key, step states, retry state, and terminal result.
8. Execution alone writes ActionLedger entries; Work Items and Source Control execute their own product commands and return an outcome reference.
9. Loom verifies the resource-specific owner and typed authority before its owner-scoped Lease adapter runs; fleet-db separately validates workspace, discriminator, resource, holder, token, fence, and transition. The shared service credential is not treated as proof of in-process capability identity.
10. A DriverRun Work Item claim returns one exact `ClaimActionID` generation. TaskRun request receipts retain it, and release, liveness, retry, and terminal commands must validate it without consulting mutable lineage as authority. A stale command never clears a successor generation.
11. Typed TaskRun terminal policy is backend-parity behavior: close closes/unassigns; successful non-close completion reviews/unassigns; cancelled or non-blocking failure opens/unassigns; retry exhaustion or explicit block blocks/unassigns. TaskRun/Lease/Work Item transition is atomic, while DriverStep repair and terminal-event convergence use their separately declared durable paths.
12. Artifact finalize and reference lost-response replay use the same deterministic command ID across a bounded 128-revision window ending at the current revision; the bound and divergent-reuse conflict behavior require tests.
13. Repository admission is a Work Items owner command, not a read-then-generic-update sequence: admission of created or batch-created tasks and stale ready-event handling must invoke one command that rechecks current state and atomically persists `blocked`, unassigned state with private `repository_required` authority whenever a non-epic task has no unambiguous repository. Repository assignment may reopen only that exact policy block, and caller-editable metadata cannot mint or clear its authority.
14. Retry recovers `repo_ref` from the immutable original TaskRun request receipt; it does not infer selection from a mutable claim. One normalized configured capacity is shared by the DriverRun executor and TaskRun workers, and cloned repository metadata records each repository's detected committed default branch unless an explicit branch is verified in every clone.

Atomic fleet-db commands require backend transaction, CAS-conflict, concurrent update, duplicate retry, and lost-response retry tests. Workflow approve/unapprove/activate and Execution claim/finalize use this test class as single durable commands.

For any flow implemented as a process manager, fault injection should terminate the process between every durable step of:

- agent provisioning;
- agent enable/pause and role change;
- binding disable/delete and grant revocation;
- any new multi-capability process manager.

Restart must converge without duplicate authority, orphaned configuration, or an unrecoverable in-progress record.

## Authority gates

Every mutation declares accepted authority classes. The test matrix includes operator, execution, session, webhook, system, wrong-workspace, unauthenticated, expired, and stale-fence callers where relevant.

Structural requirements:

- adapters construct authority only after the configured deployment trust
  check: server-owned local-open admission or external credential verification;
- workspace scope and principal are server-derived;
- operation classes are registered and unknown classes fail closed;
- all driver operations are classified before the driver-op gate is considered complete;
- execution, session, and operator authority cannot be converted into one another;
- webhook authority reaches only Automation ingestion;
- `SystemAuthority` is capability/action scoped, registered, reason-audited, and rejected by operator-only commands;
- connector secrets remain inside Connectors dispatch/materialization boundaries;
- TaskRun and PTY environment-audit tests prove operator/forge credentials are absent; PTY commands also prove the session fence and session scope.

Workflow approve/unapprove/activate and every grant-write path require negative tests on HTTP, CLI, and any internal entry surface. The standalone CLI must reach these commands through the configured management API, not by constructing authority or Store access locally. Its behavior slice also tests endpoint discovery, the prohibition on implicit host startup, unavailable-host failure, credential-free local/open mode, authenticated OIDC mode, non-interactive use, output/exit-code compatibility, and rollback.

## Runtime gates

- Every capability background component is registered with the runtime host.
- Modules do not launch unmanaged long-lived goroutines from constructors.
- Every in-scope non-test source `go` statement links to one or more named lifecycle components or carries an explicit bounded, request, command, or helper disposition with a nonempty reason; missing and stale links fail closed.
- Cadence, jitter, timeout, backoff, health, and cancellation are observable and testable.
- One failing reconciler does not stall unrelated reconcilers.
- Restart/recovery tests prove idempotency.
- Supervisor retirement requires the replacement for every retained feature and a green `make test-supervisor-disabled` backed by `test/modular-monolith/supervisor-disabled-matrix.yaml` before deletion.
- TaskRun and DriverRun fencing are each backend-assigned and monotonic, but remain in distinct namespaces and are never compared or converted across resource kinds.

## Frontend architecture gates

The feature graph must cover:

- static imports;
- dynamic imports;
- re-exports;
- runtime and type-only imports;
- production and test code.

Rules:

1. Cross-feature imports use a designated public entry.
2. A feature cannot import another feature's `api`, `model`, `state`, or `ui` internals.
3. `shared` cannot import `modules` or `app`.
4. Generated wire models are the explicit product-vocabulary exception and remain a leaf; `shared/api/client` contains transport/auth primitives only, while endpoint functions and feature mappers stay with the owner.
5. Cross-feature screens are composed in `app/screens`.
6. Runtime and semantic/type graphs contain no cycles.
7. A temporary “no new imports from legacy roots” rule ratchets the horizontal-folder migration.

`check:dir-size` currently reports five violations and is not included in `check:arch`:

- `src/utils`: 30 direct TS files;
- `src/hooks/workspace`: 24;
- `src/components/FileExplorer`: 22;
- `src/components/AgentDetailPanel`: 18;
- `src/hooks/ui`: 17.

Fix these or introduce a ratcheted baseline before wiring the check into `check:arch`; otherwise the architecture-gate change would make the existing branch fail immediately. Directory size remains a maintainability warning, not a capability definition.

Frontend proof per extracted route:

- module-boundary check;
- API mapper contract tests;
- route-level Vitest integration test;
- relevant Playwright journey/visual evidence;
- production build and chunk comparison;
- Terminal persistence/reconnection proof when Interaction or routing changes.

## Contract and product gates

### Per behavior-preserving structural slice

- No unintended CLI, HTTP, SDK, OpenAPI, or fleet-db behavior change.
- `make gate` passes in LoomCLI.
- Targeted Vitest tests, the targeted Playwright journey, and `npm run build` pass when frontend code changes.
- Relevant fleet-db storage and API contract suites pass.
- Cross-repo fleet-db OpenAPI and Loom vendored-spec guard pass together.
- `make local-mode-verify` passes when the slice affects composition,
  persistence, runtime, or UI integration. The proof counts only when the
  container run manifest matches the physical checkout and Compose project,
  names the exact FleetDB-created task IDs, and every accepted session starts
  at or after that manifest's `started_at`; preserved volume data is context,
  never proof for a new run.
- The smallest relevant E2E journey proves the changed capability through a real entry surface.

### Per contract, policy, or security slice

- The intended API/security behavior delta is stated with version-skew and rollback behavior.
- Fleet-db service/storage/API/OpenAPI tests and Loom handwritten-client/adapter tests pass.
- The paired PR records companion SHA and vendored-spec checksum.
- `GET /api/v1/capabilities` contract tests cover supported, missing-key, unsupported-version, and old-backend 404 responses for every supported Redis/Postgres and enabled/disabled route/configuration profile; a key is advertised only when the running deployment can execute it.
- Loom's exact required 21-key Phase 4 foundation list in the target architecture stays synchronized with its required-key constants; FleetDB's advertised manifest is a compatible superset because it retains the V1 review-handoff key for old clients and composes repository admission through the deployment profile. `agents.lifecycle_command_fencing.v1` requires client-generated-ID Create recovery, atomic node/stable-owner Ack binding, exact-replay completion, and owner-fenced `acked -> running -> terminal` transitions. `agents.lifecycle_command_ownership_fencing.v1` additionally requires every Ack and Complete to match the current logical-agent ownership lease and fencing generation, with only the documented no-live convergence exceptions; the deprecated `X-Actor` rollout bridge is HTTP-only and never relaxes storage. Existing umbrella keys cover the implemented method families: `execution.driver_run_work_item_claim.v1` requires both exact-generation claim and release, while `execution.driver_run_review_work_item_handoff.v2` independently proves the retained-generation Review handoff, including atomic canonical local-branch `external_ref` stamping and exact server-derived `task.review` lineage. The public request has no trigger-policy field. FleetDB alone may stamp the private `review_trigger_policy=suppress_successor` marker after proving the DriverRun, TriggerEvent, and Delivery lineage; Loom then suppresses the whole successor because the original event already fanned out to every matching binding. Caller-provided or legacy `suppress_self` metadata has no authority and must retain normal fanout. `execution.task_run_work_item_design.v1` independently proves the run-derived, owner-fenced planner design command rather than widening the older lease-fencing key. The three explicit terminal-recovery keys are not umbrella inventions: `execution.task_run_terminal_convergence.v1` requires the versioned pending-candidate query plus monotonic completion marker, `execution.terminal_driver_run_work_recovery.v1` requires the typed DriverRun recovery command, and `execution.terminal_driver_run_work_recovery_queue.v1` requires its durable claim/complete/retry queue. `work_items.repository_requirement.v1` requires Redis/Postgres parity for conditional repository blocking, private policy authority/recovery, atomic repository assignment, canonical replay, and commit-time dispatch readiness. The remaining TaskRun keys require Redis/Postgres parity for owner fencing and terminal Work Item policy. Do not add documentary generic release or terminal-policy capability names that do not exist in code and OpenAPI.
- New Loom fails readiness with the exact missing capability key against old fleet-db; no generic mutation fallback is allowed.
- Composition tests prove Loom derives required keys from its enabled slices, one low-level fleet-db transport client is shared by narrow capability/app adapters, and that client is never injected into a module or workflow core.
- Negative authority tests cover every entry surface.
- Relevant local-mode/E2E proof exercises the changed behavior, not only package compilation.

### Before supervisor deletion

- Full supervisor feature-parity ledger resolved.
- Every required row in `test/modular-monolith/supervisor-disabled-matrix.yaml` is green through `make test-supervisor-disabled`, including an assertion that no daemon process or control socket exists.
- Stale timeout and every launch-path heartbeat fix active.
- `LOOM_TASK_READY_EVENTS` and the TS execution leaf use their intended generic defaults.
- `LOOM_LOCAL_MODE_PLANE=ts` is replaced by explicit task-type arbitration.
- Preflight, error classification, retry/backoff, recovery, concurrency, epic transition, and periodic desired-state decisions are implemented or consciously retired.
- Builtin plan/task and all `agentdef` populations, including per-PR reviewers, are migrated.
- `AgentCommand`, daemon IPC, binding-proxy, legacy auth/attribution/cron, and other sunset paths have enforced tripwires.
- No runtime fallback silently re-enables supervisor code.
- No parity row is skipped or disabled without an owned blocker; existing daemon-control `t.Skip` cases are resolved before counting the matrix green.

### SDK and desktop when touched

These are not assumed to be covered by the root Go/frontend gate:

- SDK: `npm test`, deterministic generation diff, Go/operation-manifest parity, `npm pack --dry-run`, and API-surface compatibility.
- Desktop: frontend typecheck, `cargo test`, capability-schema validation, loopback URL/native-command security tests, and packaged sidecar/web-assets smoke proof before signing.

For the historical `67c45972f` repository-admission delta, its freshly built
package proves repository-free creation, blocked-card/zero-run admission,
public Repo admission, explicit repository selection, planning, a GPT-5.6
Terra coder run, transcript and exact diff, applied patch-back, and terminal
Closed convergence. Its Podman verifier and raw browser independently prove
fresh planner/coder design, transcript, diff, and zero supervisor artifacts.
The final `f0011b248` validation supersedes that current-state claim: both full
gates pass, the exact Desktop package embeds all six supported built-ins, Local
Review executes with a real-Codex transcript and authoritative handoff, and an
unavailable backend fails closed to durable Blocked state across restart.

## Performance and operability

The checked-in performance inventory records:

- `loom serve` startup time;
- Phase 2 authenticated workflow-approval p50/p95 from 30 retained samples;
- six observed FleetDB round trips per successful approval command, with the request mix retained in evidence rather than inferred from call sites;
- background-loop/reconciler counts;
- build/test/gate duration;
- frontend route chunk sizes for migrated routes.

Also record per slice:

- owner capability;
- changed non-generated production files;
- allowed inbound adapters/contracts;
- business-rule files changed outside the owner;
- Store/direct-write counts before and after;
- fleet-db round-trip and latency sampling procedure.

A module boundary that adds synchronous chatter without reducing broad dependencies is a regression. Phase 2 populated the pilot measurements from the authenticated management path in the checked-in `performance-baseline.yaml`; later phases compare against those observed values and must not substitute numbers from a legacy direct-Store flow.

## Outcome measures

After two completed backend slices, all three must improve:

1. composite-Store consumers decrease;
2. direct persistence writes from handlers/CLI decrease;
3. zero business-rule files change outside the owner capability; only declared adapters/contracts may accompany the slice.

Every completed extraction must reduce at least one legacy coupling count. Phase 2 replaced both target-path nulls only after the authenticated management boundary made them observable; later slices follow the same evidence-before-budget rule.

If those do not improve, stop and revisit the capability map before extracting more code.

## Pause conditions

Pause a slice when:

- its branch is behind an overlapping base change;
- the affected runtime path lacks a green characterization/E2E baseline;
- a required backend capability is unresolved;
- fleet-db capability negotiation is missing or would discover incompatibility only on first mutation;
- a cross-capability write has no atomic command or recovery proof;
- a privileged operation has no verified authority on every entry surface;
- the slice conflicts with supervisor retirement or custom-driver delivery without an agreed landing order.

## Reject or redesign conditions

Reject or redesign when:

- a slice is declared complete while most of its delivered value is only path movement; a pure-move commit is acceptable inside an approved stacked extraction when delegation, caller migration, proof, and tighter gates follow;
- the new module still injects `store.Store`;
- its public API mirrors storage CRUD;
- another capability imports its adapter, repository, or implementation;
- the backend slice creates a generic product-domain `common`, `shared`, `models`, or `services` bucket; frontend `shared` is allowed only under its transport/generated-contract and two-consumer/no-handwritten-product-vocabulary rules;
- it merges DriverRun and AgentSession persistence;
- it treats all fencing tokens as equivalent before normalization;
- it adds a process, `go.mod`, plugin boundary, or synchronous dependency cycle;
- a compatibility facade survives its declared expiry;
- after two pilots, ownership and feature-change locality have not measurably improved.

## Migration completion gate

The modular-monolith migration is complete only when:

- zero forbidden capability edges exist;
- migrated capability code has zero composite-Store use;
- all product mutations go through owner commands or a declared cross-aggregate coordinating command;
- polymorphic Lease instances and ActionLedger writes follow their declared owner/discriminator rules;
- standalone mutating CLI paths use the authenticated management API rather than direct Store/FleetDB access;
- the SDK runner's direct-FleetDB fallback is removed and absence of `LOOM_TASK_RUN_API_URL` fails closed;
- every cross-capability write is atomic or recoverable;
- every mutation has explicit authority coverage;
- lifecycle components are runtime-host managed;
- frontend feature boundaries are enforced;
- global horizontal business-logic buckets are removed or reduced to genuine transport/platform roles;
- supervisor code is deleted with the replacement matrix green;
- no extra service, deployment unit, Go module, or feature package was introduced.

---

[All migrations](../README.md) · [Migration overview](README.md) · Previous: [Migration plan](03-migration-plan.md) · [Phase 4 evidence](09-phase-4-decisions-and-evidence.md)
