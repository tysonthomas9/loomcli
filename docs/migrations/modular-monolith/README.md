# Modular Monolith Migration

- **Status:** Proposed
- **Date:** 2026-07-14
- **Scope:** `loom serve`, the operator CLI entry surfaces, the Vite frontend counterpart, and the fleet-db contracts those capabilities depend on
- **Provenance:** [Phase 0 integration baseline](00-phase-0-baseline.md), validated against Loom `09f071d0a` and FleetDB `7f7104b9`
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

This is not a package-count project. Loom already has 163 Go packages; the migration addresses broad dependencies, ambiguous write ownership, handler/CLI orchestration, partial cross-record writes, and authority checks enforced by convention.

## Reading order

| Document | Question |
|---|---|
| [00-phase-0-baseline.md](00-phase-0-baseline.md) | Which bases were integrated, what passed, and what Phase 0 work remains? |
| [01-current-state.md](01-current-state.md) | What evidence says a migration is needed, and what must be re-measured? |
| [02-target-architecture.md](02-target-architecture.md) | Which capabilities own which records, and what dependencies are allowed? |
| [03-migration-plan.md](03-migration-plan.md) | In what order do we extract modules without blocking active work? |
| [04-enforcement-and-gates.md](04-enforcement-and-gates.md) | What automated rules, tests, metrics, and stop conditions make the boundary real? |

## Scope boundaries

“Modular monolith” applies to the capability core hosted by `loom serve`. The target has one verified product-mutation path: standalone operator CLI commands become authenticated management-API clients rather than opening the composite Store directly. MM-7 must settle endpoint discovery, optional local-host startup, unavailable-server behavior, local authentication, and scripting compatibility before that behavior changes. Local bootstrap/file-only commands remain platform tools outside product aggregate ownership.

| Artifact | Migration decision |
|---|---|
| fleet-db | Remains a separate service and owner of atomic durable transitions |
| Vite web UI | Remains one application; gains vertical feature modules |
| Desktop launcher | Remains a thin Tauri lifecycle/native adapter |
| `@loom/sdk` | Remains one published package with its current coarse exports |
| TypeScript workflow drivers | Remain the extension boundary; no Go plugin system |
| Coding backend processes | Remain execution processes behind module-owned runtime ports |

The migration does **not** introduce microservices, multiple `go.mod` files, feature npm packages, microfrontends, or a generic in-process event bus.

## Phase 0 status

The required base integration is complete and its validated code heads are pushed. Before integration, Loom was 40 commits ahead and 25 behind `origin/v5`; FleetDB was 35 ahead and 8 behind `origin/main`. Those historical divergence counts, the semantic conflict ledger, the corrected contract checksum, gate results, and local-mode proof are frozen in the [Phase 0 baseline](00-phase-0-baseline.md).

Phase 0 remains in progress because the machine-readable capability graph and the direct-write, authority, transaction, loop, and performance inventories are not complete. Characterization and guardrail work may now proceed; package moves still wait for the affected ownership decisions and a refreshed overlap check. Base integration does not approve the proposed architecture.

## Proposed decisions still requiring review

| ID | Decision |
|---|---|
| MM-1 | Confirm the ten-capability map, including Artifacts and the Workspace versus Source Control split |
| MM-2 | Select the explicit operator-authority model for local/open-mode use |
| MM-3 | Build Redis/Postgres parity for the required fleet-db record families and commands, or declare the supported backend boundary |
| MM-4 | Confirm Workflow Catalog approve/unapprove/activate as the first complete backend pilot |
| MM-5 | Confirm `internal/modules/<capability>` as the physical root |
| MM-6 | Confirm the two-migration-wave maximum for compatibility facades |
| MM-7 | Approve the mutating-CLI management topology: endpoint discovery, optional local host startup, unavailable-server behavior, local/open-mode authentication, automation compatibility, and rollout |

The migration is `Proposed` until these are resolved. Individual slices must not silently settle an open decision through incidental package movement.

---

[All migrations](../README.md) · Next: [Current-state evidence](01-current-state.md)
