# Modular Monolith Migration

- **Status:** INTERIM — Phase 4 implementation active; the current architecture check passes, while the paired contract, aggregate gates, source-bound proof, performance run, and packaged Desktop evidence remain pending
- **Date:** 2026-07-16
- **Scope:** `loom serve`, the operator CLI entry surfaces, the Vite frontend counterpart, and the fleet-db contracts those capabilities depend on
- **Provenance:** [Phase 0 integration baseline](00-phase-0-baseline.md), final [Phase 1 evidence](06-phase-1-decisions-and-evidence.md) at Loom `7e8a6dd2`, [Phase 2 evidence](07-phase-2-decisions-and-evidence.md) at Loom `84cccb761` with FleetDB `430dce8d9`, [Phase 3 evidence](08-phase-3-decisions-and-evidence.md) at core implementation commits Loom `7f95b9bf1` and FleetDB `f1c4e1119`, and the in-progress [Phase 4 record](09-phase-4-decisions-and-evidence.md) from Loom base `1353e2faf` with FleetDB base `f1c4e1119`
- **Related:** [Unified agent UX](../../design/2026-07-01-unified-agent-ux-proposal.md) · [Durable agent identity](../../design/2026-07-07-agent-identity-record.md) · [Workflow driver authoring](../../design/workflow-driver-authoring-guide.md)

## Decision summary

Migrate the application inside `loom serve` to a **capability-owned modular monolith**.

- Keep one Go module, one build, and—after supervisor retirement—one Loom application process.
- Keep fleet-db as the separate durability/control-plane service.
- Give every mutable aggregate one declared capability owner.
- Expose narrow command/query APIs from capability roots.
- Put persistence and transport implementations behind capability-owned ports and adapters.
- Enforce default-deny capability edges with acyclic compile-time import and synchronous command/query graphs; durable event cycles must be explicit, bounded, and idempotent.
- Extract one production use case at a time while delivering the existing unified-agent, custom-driver, and supervisor-retirement work.

This is not a package-count project. The pre-guardrail source snapshot already has 165 Go packages; the migration addresses broad dependencies, ambiguous write ownership, handler/CLI orchestration, partial cross-record writes, and authority checks enforced by convention.

## Reading order

| Document | Question |
|---|---|
| [00-phase-0-baseline.md](00-phase-0-baseline.md) | Which bases were integrated, what passed, and how the Phase 0 inventory closed? |
| [01-current-state.md](01-current-state.md) | What evidence says a migration is needed, and what must be re-measured? |
| [02-target-architecture.md](02-target-architecture.md) | Which capabilities own which records, and what dependencies are allowed? |
| [03-migration-plan.md](03-migration-plan.md) | In what order do we extract modules without blocking active work? |
| [04-enforcement-and-gates.md](04-enforcement-and-gates.md) | What automated rules, tests, metrics, and stop conditions make the boundary real? |
| [05-v5-integration-regression-closure.md](05-v5-integration-regression-closure.md) | Which integration regressions were closed, and what evidence is required before extraction starts? |
| [06-phase-1-decisions-and-evidence.md](06-phase-1-decisions-and-evidence.md) | Which decisions were approved, what Phase 1 proved, and what remains explicitly RED or not yet migrated? |
| [07-phase-2-decisions-and-evidence.md](07-phase-2-decisions-and-evidence.md) | What Workflow Catalog boundary was implemented, which invariants it owns, what passed, and which external proof remains? |
| [08-phase-3-decisions-and-evidence.md](08-phase-3-decisions-and-evidence.md) | Which Automation boundary, durable admission/dispatch contracts, runtime components, and compatibility decisions define Phase 3? |
| [09-phase-4-decisions-and-evidence.md](09-phase-4-decisions-and-evidence.md) | Which Execution and minimal Artifacts boundaries, replay classes, supervisor-disabled proof, and packaged Desktop evidence define Phase 4? |

## Scope boundaries

“Modular monolith” applies to the capability core hosted by `loom serve`. The approved target requires one product-mutation topology: standalone operator CLI commands become authenticated management-API clients rather than opening the composite Store directly. Under MM-7, those commands discover an explicitly configured endpoint, never start a host implicitly, fail closed when it is unavailable, authenticate locally, and migrate command family by command family while preserving output and exit-code compatibility. Local bootstrap/file-only commands remain platform tools outside product aggregate ownership.

| Artifact | Migration decision |
|---|---|
| fleet-db | Remains a separate service and owner of atomic durable transitions |
| Vite web UI | Remains one application; gains vertical feature modules |
| Desktop launcher | Remains a thin Tauri lifecycle/native adapter |
| `@loom/sdk` | Remains one published package with its current coarse exports |
| TypeScript workflow drivers | Remain the extension boundary; no Go plugin system |
| Coding backend processes | Remain execution processes behind module-owned runtime ports |

The migration does **not** introduce microservices, multiple `go.mod` files, feature npm packages, microfrontends, or a generic in-process event bus.

## Phase 0 through current Phase 4 status

The required base integration is complete and its validated code heads are pushed. The Phase 1 branch starts from Loom `122d4d79`, where `origin/v5` at `95e97289` is an ancestor; companion FleetDB is `8120c788`. The original divergence counts, semantic conflict ledger, gate results, and local-mode proof remain frozen in the [Phase 0 baseline](00-phase-0-baseline.md), while the machine-readable baseline records the refreshed heads and structural ratchets.

Phase 0 and Phase 1 are complete. The final Phase 1 Loom head is `7e8a6dd2`; it approved the capability graph, completed the direct-write, authority, transaction, runtime, and performance inventories, enforced all 11 declared build profiles plus all-files AST checks, added the characterization gate, and productized the supervisor-disabled proof contract. It also closed the stale-task default/clock and session-heartbeat reliability items and added fail-fast FleetDB capability negotiation for slices that declare required keys.

Phase 2 is complete with an active `internal/modules/workflowcatalog` root. Loom implementation head `84cccb761` and FleetDB `430dce8d9` implement the read API, typed operator commands, shared FleetDB adapter, management HTTP and CLI adapters, local credential mechanism, and Redis/Postgres lifecycle contract. The paired gates, checksum `4f50d5e0…ca19`, real route/CLI E2E, packaged Desktop journey, checkout-scoped deterministic local-mode integration, and measured p50/p95 plus round trips pass. The graph is ratcheted to `completed_phase: 2`, and the machine baseline includes the immutable Phase 2 validation snapshot. See the [Phase 2 evidence](07-phase-2-decisions-and-evidence.md).

Phase 3 source implementation establishes active `internal/modules/automation`, named webhook and system-event application workflows, durable Redis/Postgres admission, cron, retry, manual-dispatch, run-outcome, and generic await-notification operations, plus four named runtime-host registrations across Automation and Execution. Its security closeout makes generic await resolution and parent-run resumption one Execution-owned Redis/Postgres command, reserves the complete `run.finished` provenance lane across admission/live dispatch/historical catch-up, and negotiates the always-required `execution.await_atomic_resume.v1` deployment key before runtime loops start. The architecture graph is ratcheted to `completed_phase: 3`, and the mutation ledger records all 14 public Automation mutations alongside the three Workflow Catalog lifecycle commands. Architecture and focused Go checks, public route/CLI and signed-webhook E2Es, the pinned-count performance proof, the OpenAPI checksum, checkout-scoped generic plus webhook local-mode verifiers, the authenticated local-browser create/activate/manual-run/history journey, and the targeted Playwright local-operator suite pass. That UI run also drove the Redis/Postgres effective-trust parity correction and malformed-approval hardening. The final post-correction FleetDB gate passes at 78.0% coverage, and the final Loom aggregate gate passes against the exact paired FleetDB binary and matching source under a clean HOME/runtime environment. GPT-5.6 Terra review is separately not recorded because exporting the local screenshots to an external model awaits explicit informed approval; this is not a product failure. The baseline retains both the self-reference-free pre-commit measurement and a post-commit audit bound to Loom `7f95b9bf1` and FleetDB `f1c4e1119`.

Phase 4 implementation activates the Execution and minimal Artifacts
capability roots and moves the selected TaskRun and DriverRun owner-command
paths behind the paired FleetDB contract. The current architecture check
passes all 11 profiles plus the all-files AST check with four active roots, 56
required command-ID namespaces, Store `82/71`, 90 handler exceptions, 243
primary direct-write rows, 86 runtime components, and 103 goroutine
definitions. The namespace prefixes group the ledger entries; they do not
replace the per-entry aggregate and coordinating-owner declarations. The
separate `internal/driver` ratchet freezes its remaining 10 rows across 11
sites without relabeling their owners. The checked-in Execution
supervisor-disabled row is green, but that row proves only its declared
deterministic plan/coder lane; it does not authorize physical supervisor
deletion or stand in for the Phase 6 full matrix. The final paired contract,
aggregate gates, source-bound supervisor-disabled rerun, measured product
performance, implementation commit identities, and packaged Desktop evidence
remain pending. See the [Phase 4 record](09-phase-4-decisions-and-evidence.md).

## Approved architecture decisions

| ID | Outcome |
|---|---|
| MM-1 | Adopt the ten coarse capability owners, including Artifacts and the Workspace/Source Control split. |
| MM-2 | Require a server-issued 256-bit local operator credential stored in a mode-0600 runtime file; loopback location grants no authority. |
| MM-3 | Advertise a capability key only when the active Redis or Postgres deployment has parity; missing support fails readiness with no fallback. |
| MM-4 | Use Workflow Catalog approve/unapprove/activate as the first complete backend pilot after a behavior-neutral read seam. |
| MM-5 | Place public capability roots at `internal/modules/<capability>` with optional owner-private subpackages. |
| MM-6 | Expire compatibility facades after at most two migration waves unless an explicit reviewed extension records an owner. |
| MM-7 | Use an explicitly configured management endpoint, no implicit host startup, fail-closed unavailability, local authentication, and family-by-family compatibility rollout. |

These outcomes are enforced by `capability-graph.yaml` and `migration-baseline.json`. Changing one requires a reviewed graph/baseline change; an individual slice must not reverse it through incidental package movement.

---

[All migrations](../README.md) · Next: [Current-state evidence](01-current-state.md) · [Phase 4 evidence](09-phase-4-decisions-and-evidence.md)
