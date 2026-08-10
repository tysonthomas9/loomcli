# Phase 9 Package Consolidation

- **Status:** In progress
- **Base:** Phase 8 documentation head `1fc9d887c517fad60728afdfcf3c28375d84ece3`
- **Wave 9.1 implementation:** `da9105472`
- **Working branch:** `modular-monolith-phase9-01-types-ratchet`
- **Purpose:** Reduce the residual package surface toward 160 production Go
  packages without weakening capability ownership, consumer-owned ports, or
  independently replaceable adapters.

## Decision

Phase 9 continues the Phase 8 shrink-only discipline. It does not merge the ten
capability owners, introduce a generic shared business-logic package, or count
package deletion as success when the same abstraction survives under a new
name. A package is removed only when its durable boundary reason is absent or
its behavior can be absorbed by its declared owner or a real external adapter.

The deletion test for every wave is:

1. the old package and import path are gone;
2. duplicated policy or representation hops are gone rather than relocated;
3. the owner public surface stays no larger than the use cases require;
4. real external adapters remain behind owner- or consumer-defined ports; and
5. architecture, focused behavior, aggregate, and product proofs remain green.

## Planned waves

| Wave | Target | Intended reduction |
|---|---|---|
| 9.1 | Retire the horizontal `internal/types` model plane | Remove dead product models and mappings; move live Work Items policy to its owner |
| 9.2 | Collapse residual legacy model and repository planes | Replace cross-capability repositories with owner ports and delete unused horizontal interfaces |
| 9.3 | Fold shallow composition packages | Move constructor-only and one-consumer forwarding seams into their composition root |
| 9.4 | Deepen remaining adapter seams | Keep protocol, credential, platform, and independently replaceable adapter boundaries; remove mapping-only siblings |
| 9.5 | Reproduce the full product proof | Run aggregate gates and packaged journeys before declaring the lower package target complete |

The target of 160 is directional until each candidate passes the boundary and
deletion tests. Ownership and replaceability take precedence over a round
number.

## Wave 9.1 result

The first slice classifies every production consumer of `internal/types` and
finds no remaining reason for it to be a production package:

- FleetDB already owns private wire structs and now projects them directly to
  the established backend compatibility DTOs;
- Work Items owns molecule/sort validation; the sole live direct-blocker
  classification remains with its CLI task-selection consumer;
- WebUI health, issue, Git, and claim handlers use Work Items or backend
  projections directly; and
- Fleet transport fixtures remain test-private rather than recreating a public
  product model.

The exact package-shape ratchet moves as follows:

| Shape measure | Phase 8 | Wave 9.1 | Change |
|---|---:|---:|---:|
| Production packages under `internal/` | 189 | 188 | -1 |
| Packages under `internal/modules/` | 17 | 17 | 0 |
| Packages outside `internal/modules/` | 172 | 171 | -1 |
| One-file packages | 67 | 67 | 0 |
| One-or-two-file packages | 89 | 89 | 0 |

`internal/types` is added to the retired-horizontal-root architecture guard, so
both recreating the directory and importing the old path fail. The refreshed
exact inventory separately rejects any unreviewed replacement package.

## Wave 9.1 validation

| Check | Result |
|---|---|
| Work Items classification and CLI blocker-policy characterization | PASS |
| FleetDB adapter conversion and behavior packages | PASS |
| Affected WebUI Fleet, issue, health, Git, and application packages | PASS |
| Measured `make check-architecture` | PASS: 11/11 profiles, 10 roots, 98 direct-write rows, zero pending decisions; peak process-tree RSS 1,254.8 MiB under 2,048 MiB |
| Full `go test ./internal/archtest -count=1 -timeout=15m` | PASS in 618.070 seconds |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: both Go and frontend quality lanes |

The aggregate gate initially rejected a new nineteenth import on
`internal/cli`. The final tree removes that cross-package edge instead of
raising or waiving the exact fanout ceiling; the uninterrupted rerun passes.

Later waves must update this document with the selected package candidates,
the boundary reason retained or removed for each, exact shape changes, and
same-head evidence.

---

[Migration overview](README.md) · [Phase 8 consolidation](15-phase-8-consolidation-and-evidence.md)
