# Modular Monolith Migration

- **Status:** Reviewed — Phase 1 complete; Phase 2 not started
- **Date:** 2026-07-15
- **Scope:** `loom serve`, the operator CLI entry surfaces, the Vite frontend counterpart, and the fleet-db contracts those capabilities depend on
- **Provenance:** [Phase 0 integration baseline](00-phase-0-baseline.md), refreshed at Loom `122d4d79` and FleetDB `8120c788`
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

## Phase 0 and Phase 1 status

The required base integration is complete and its validated code heads are pushed. The Phase 1 branch starts from Loom `122d4d79`, where `origin/v5` at `95e97289` is an ancestor; companion FleetDB is `8120c788`. The original divergence counts, semantic conflict ledger, gate results, and local-mode proof remain frozen in the [Phase 0 baseline](00-phase-0-baseline.md), while the machine-readable baseline records the refreshed heads and structural ratchets.

Phase 0 and Phase 1 are complete. Phase 1 approved the capability graph, completed the direct-write, authority, transaction, runtime, and performance inventories, enforced all 11 declared build profiles plus all-files AST checks, added the characterization gate, and productized the supervisor-disabled proof contract. It also closed the stale-task default/clock and session-heartbeat reliability items and added fail-fast FleetDB capability negotiation for slices that declare required keys.

No capability package has been extracted: the architecture report still records zero `internal/modules/*` roots, all three Workflow Catalog ledger commands are Phase 2B specifications, and migrated workflow-approval latency and FleetDB round trips remain explicit nulls. The supervisor-disabled execution row is intentionally RED and cannot count as parity proof. See the [Phase 1 evidence](06-phase-1-decisions-and-evidence.md).

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

[All migrations](../README.md) · Next: [Current-state evidence](01-current-state.md) · [Phase 1 evidence](06-phase-1-decisions-and-evidence.md)
