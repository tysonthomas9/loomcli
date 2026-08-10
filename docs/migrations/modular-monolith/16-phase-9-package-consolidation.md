# Phase 9 Package Consolidation

- **Status:** In progress
- **Base:** Phase 8 documentation head `1fc9d887c517fad60728afdfcf3c28375d84ece3`
- **Wave 9.1 implementation:** `da9105472`
- **Wave 9.2 implementation:** `ec263bfa3`
- **Wave 9.3 implementation:** `a4333c31a`
- **Wave 9.4 implementation:** `bfb1ac9e8`
- **Wave 9.5 implementation:** `72870b85b`
- **Wave 9.6 implementation:** `830541155`
- **Wave 9.7 implementation:** `9722a8bed`
- **Stacked branches:** `modular-monolith-phase9-01-types-ratchet`, then
  `modular-monolith-phase9-02-shallow-seams`, then
  `modular-monolith-phase9-03-legacy-planes`, then
  `modular-monolith-phase9-04-adapter-seams`, then
  `modular-monolith-phase9-05-artifact-adapter`, then
  `modular-monolith-phase9-06-connectors-adapter`, then
  `modular-monolith-phase9-07-legacy-runtime`
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
| 9.2 | Fold shallow seams with no durable boundary reason | Make reusable E2E helpers test-only and move one-consumer validation to its enforcing consumer |
| 9.3 | Collapse residual legacy model and repository planes | Replace cross-capability repositories with owner ports and delete unused horizontal interfaces |
| 9.4 | Deepen remaining adapter seams | Keep protocol, credential, platform, and independently replaceable adapter boundaries; remove mapping-only siblings |
| 9.5 | Retire duplicate owner-adapter layers | Remove forwarding/error-remapping packages that add no independently replaceable boundary |
| 9.6+ | Continue complete-plane and shallow-seam deletion | Apply the deletion test to each selected candidate; do not retain fallback facades |
| Final | Reproduce the full product proof | Run aggregate gates and packaged journeys before declaring the lower package target complete |

The target of 160 is directional until each candidate passes the boundary and
deletion tests. Ownership and replaceability take precedence over a round
number.

Package shape and `capability-graph.yaml`'s `legacy_paths` list are necessary
ratchets, not a complete legacy-code inventory. A green architecture gate does
not prove that runtime compatibility constructors, DTO bridges, aliases, or
fallback mutation paths are absent unless those surfaces are independently
enumerated. Phase 9 is therefore incomplete while any known transitional
production path remains. Ordinary defaults, error selection, and protocol
version handling are not legacy merely because their implementation uses the
word "fallback"; each candidate must be classified by behavior and deletion
impact.

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

## Wave 9.2 result

The second slice removes two packages whose abstractions fail the boundary
test:

- `internal/backend/backendtest` was a reusable, build-tagged E2E suite used
  only by one `internal/cli` test file. Its implementation now lives beside
  that tagged test, so it remains shared across all four backend modes without
  compiling as a production package.
- `internal/pathsec` had one live validator and one entirely unused sensitive
  path classifier. The live diff-path traversal rule is now private to
  `sourcecontrolcoord`, its sole enforcing consumer; the dead classifier and
  its tests are deleted.

The review retained small packages that still have a boundary reason,
including external adapter translation in `artifactcatalog`, scoped system
authority in `agentsbootstrap`, shared HTTP protocol parsing, driver
cycle-breaking contracts, and isolated Git worktree materialization. No owner,
port, external adapter, or public production API was added.

The exact package-shape ratchet now moves as follows:

| Shape measure | Phase 8 | Wave 9.1 | Wave 9.2 | Phase 9 change |
|---|---:|---:|---:|---:|
| Production packages under `internal/` | 189 | 188 | 186 | -3 |
| Packages under `internal/modules/` | 17 | 17 | 17 | 0 |
| Packages outside `internal/modules/` | 172 | 171 | 169 | -3 |
| One-file packages | 67 | 67 | 65 | -2 |
| One-or-two-file packages | 89 | 89 | 87 | -2 |

Both retired roots are guarded against recreation or re-import, and the exact
inventory rejects an unreviewed replacement package.

## Wave 9.2 validation

| Check | Result |
|---|---|
| Source-control path validation and ordinary CLI packages | PASS |
| `issuebackend_e2e` tagged CLI compilation | PASS |
| Retired-root and exact package-shape tests | PASS |
| Measured `make check-architecture` | PASS: 11/11 profiles, 10 roots, 98 direct-write rows, zero pending decisions; peak process-tree RSS 1,168.5 MiB under 2,048 MiB |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: both Go and frontend quality lanes; the already-passed measured architecture lane was not duplicated inside the aggregate run |

The first aggregate attempt reached generated-API staleness after all earlier
lanes passed, then the sandbox denied DNS access to the Go module proxy. The
approved uninterrupted rerun passed; this was an execution-environment
failure, not a source failure.

## Wave 9.3 result

The third slice retires the duplicate Connectors model, repository, and mapping
planes. `internal/modules/connectors` now owns the redacted connector, grant,
call-record, validation, and privileged inbound-secret persistence shapes.
FleetDB and memstore implement its `ManagementStore` directly, while webhook
verification declares a two-method consumer-owned secret source rather than
depending on the full owner port.

The following compatibility layers are deleted rather than renamed:

- `internal/domain/connector.go`;
- `internal/store/connector_store.go` and its unimplemented placeholder; and
- the mapping-only `internal/infra/connectorscatalog` package.

The composite Store now exposes one Connectors-owned management port instead
of three horizontal connector/grant/audit repositories. CLI, WebUI, agent,
driver, PR-review, and webhook composition use that owner port directly. The
two real adapters remain independently replaceable: the FleetDB HTTP adapter
translates transport errors to Connectors-owned categories, and memstore
retains the in-process contract implementation. Public connector projections
cannot carry credential or inbound-secret material; privileged secret reads
remain behind the owner persistence port and narrow inbound verifier seam.

The exact package-shape ratchet now moves as follows:

| Shape measure | Phase 8 | Wave 9.1 | Wave 9.2 | Wave 9.3 | Phase 9 change |
|---|---:|---:|---:|---:|---:|
| Production packages under `internal/` | 189 | 188 | 186 | 185 | -4 |
| Packages under `internal/modules/` | 17 | 17 | 17 | 17 | 0 |
| Packages outside `internal/modules/` | 172 | 171 | 169 | 168 | -4 |
| One-file packages | 67 | 67 | 65 | 64 | -3 |
| One-or-two-file packages | 89 | 89 | 87 | 86 | -3 |

Retirement guards reject recreation of the catalog root and the three deleted
model/repository files. The ownership inventory also drops five obsolete
catalog write rows, reducing exact direct persistence writes from 98 to 93 and
sites from 107 to 102. Removing the Store dependency from the connector HTTP
module lowers legacy handler imports from 26 to 25; exact import-fanout
ceilings fall from 42 to 41 for WebUI composition and from 19 to 18 for PR
review.

## Wave 9.3 validation

| Check | Result |
|---|---|
| Connectors owner, FleetDB, memstore, provider, CLI, and affected WebUI behavior suites | PASS |
| Paired FleetDB OpenAPI snapshot and adapter contract | PASS against companion `fleet-db-modular-monolith-phase7`; no contract change required |
| Retired-root, retired-file, exact package-shape, and direct-write ratchets | PASS |
| Measured `make check-architecture` | PASS: 11/11 profiles, 10 roots, 93 direct-write rows, 25 legacy handler imports, zero pending decisions; peak process-tree RSS 1,263.4 MiB under 2,048 MiB |
| Aggregate `make gate` against the paired FleetDB binary | PASS: all Go and frontend quality gates |

The aggregate gate exposed three stale exact expectations after behavior was
already green: two import-fanout ceilings and the direct-write row/site count.
All were lowered to the observed values; no ceiling, threshold, or allowlist
was widened.

## Wave 9.4 result

The fourth slice deletes the duplicate Artifact model and repository plane.
`internal/modules/artifacts` is now the sole owner of Artifact records and its
command and query ports. FleetDB and memstore implement those ports directly;
runtime composition receives the owner query API from `ArtifactsCapability`
instead of discovering it through the horizontal composite Store.

The following compatibility surfaces are physically deleted:

- `domain.Artifact`;
- `store.ArtifactStore`, `ArtifactContentReader`, upload DTOs, and
  `Store.Artifacts()`; and
- the mapping-only `internal/infra/artifactcatalog` package.

No fallback repository or compatibility facade replaces them. The existing
FleetDB command adapter is deepened with the owner query implementation rather
than adding another shallow package, while memstore remains the second real
adapter and exposes only the owner ports. Retirement guards reject the old
files, exact type and method names, and any attempt to expose Artifact queries
again from `internal/store` or `internal/domain`.

The exact package-shape ratchet now moves as follows:

| Shape measure | Phase 8 | Wave 9.1 | Wave 9.2 | Wave 9.3 | Wave 9.4 | Phase 9 change |
|---|---:|---:|---:|---:|---:|---:|
| Production packages under `internal/` | 189 | 188 | 186 | 185 | 184 | -5 |
| Packages under `internal/modules/` | 17 | 17 | 17 | 17 | 17 | 0 |
| Packages outside `internal/modules/` | 172 | 171 | 169 | 168 | 167 | -5 |
| One-file packages | 67 | 67 | 65 | 64 | 63 | -4 |
| One-or-two-file packages | 89 | 89 | 87 | 86 | 85 | -4 |

The direct-write inventory remains at 93 rows and the legacy handler-import
inventory remains at 25. This wave removes a repository plane and one package
without relabeling existing writes or weakening an import boundary.

## Wave 9.4 validation

| Check | Result |
|---|---|
| Artifacts owner, FleetDB, memstore, driver, WebUI, and session-projection suites | PASS |
| Retired-file, retired-type, exact package-shape, package-size, and direct-write ratchets | PASS |
| Measured `make check-architecture` | PASS: 11/11 profiles, 10 roots, 93 direct-write rows, 25 legacy handler imports, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, and zero pending decisions; peak process-tree RSS 1,201.7 MiB under 2,048 MiB |
| Aggregate `make gate` against the paired FleetDB source and binary | PASS: all Go and frontend quality gates |

The first aggregate attempt correctly rejected a temporary twenty-sixth file
in the FleetDB adapter package. The final implementation folds the query port
into the existing Artifact adapter, returns the package to its exact 25-file
ceiling, and passes without raising a threshold or adding an allowlist.

## Wave 9.5 result

The fifth slice removes the second, forwarding-only Artifact FleetDB adapter.
The composition-owned FleetDB bridges already map the external transport DTOs
and now implement the owner-defined `artifacts.Store` and
`artifacts.SessionStore` ports directly. The deleted
`internal/modules/artifacts/fleetdb` package only delegated the same methods
and remapped the same errors a second time; it was neither an independently
replaceable protocol adapter nor an owner policy boundary.

The removal deletes three files and 339 net lines without creating a
replacement package. Artifacts remains one of the ten capability roots, while
the removed module subpackage is added to the retired-root guard. Its unused
generic-lease allowance is also deleted, and the exact `internal/app/serve`
import-fanout exception tightens from 33 to 32.

The exact package-shape ratchet now moves as follows:

| Shape measure | Phase 8 | Wave 9.1 | Wave 9.2 | Wave 9.3 | Wave 9.4 | Wave 9.5 | Phase 9 change |
|---|---:|---:|---:|---:|---:|---:|---:|
| Production packages under `internal/` | 189 | 188 | 186 | 185 | 184 | 183 | -6 |
| Packages under `internal/modules/` | 17 | 17 | 17 | 17 | 17 | 16 | -1 |
| Packages outside `internal/modules/` | 172 | 171 | 169 | 168 | 167 | 167 | -5 |
| One-file packages | 67 | 67 | 65 | 64 | 63 | 63 | -4 |
| One-or-two-file packages | 89 | 89 | 87 | 86 | 85 | 84 | -5 |

## Wave 9.5 validation

| Check | Result |
|---|---|
| Artifacts and Interaction composition suites plus all-`internal` compilation | PASS |
| Retired-root, exact package-shape, direct-write policy, import-fanout, and package-size ratchets | PASS |
| Measured `make check-architecture` | PASS: 11/11 profiles, 10 roots, 93 direct-write rows, 25 legacy handler imports, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, and zero pending decisions; peak process-tree RSS 1,202.6 MiB under 2,048 MiB |
| Aggregate `make gate` against the paired FleetDB source and binary | PASS: all Go and frontend quality gates |

The first aggregate attempt stopped because the deleted import lowered
`internal/app/serve` fanout below its checked-in exact exception. The final
tree lowers the exception to the observed 32 and passes; no threshold or
allowlist was widened.

## Wave 9.6 result

The sixth slice deletes the duplicate Connectors grant transport and adapter
plane. The FleetDB and memstore connector catalogs already implement the
Connectors management port; they now directly implement the owner's narrower
`ConnectorGrantStore` port as well. Source Control receives that port from the
concrete FleetDB client, with no neutral DTO bridge or second error-remapping
adapter between composition and the owner.

The following transitional surfaces are physically deleted:

- `internal/modules/connectors/fleetdb`, including its duplicate wire DTOs and
  error translation;
- the second connector-grant transport DTO and command plane in
  `internal/infra/fleetdb`;
- the composition-owned FleetDB grant bridge; and
- `connectors.NewWithGrants` plus the Git-only
  `NewSourceControlCapability` constructor, which could compose a service
  whose grant command only failed later at runtime.

`connectors.New` now requires grant persistence at composition time. There is
no production Git-only fallback constructor or replacement adapter package.
The FleetDB contract guard falls from 239 to 237 exact HTTP call sites, and the
`internal/app/serve` import-fanout exception tightens from 32 to 31.

The exact package-shape ratchet now moves as follows:

| Shape measure | Phase 8 | Wave 9.1 | Wave 9.2 | Wave 9.3 | Wave 9.4 | Wave 9.5 | Wave 9.6 | Phase 9 change |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Production packages under `internal/` | 189 | 188 | 186 | 185 | 184 | 183 | 182 | -7 |
| Packages under `internal/modules/` | 17 | 17 | 17 | 17 | 17 | 16 | 15 | -2 |
| Packages outside `internal/modules/` | 172 | 171 | 169 | 168 | 167 | 167 | 167 | -5 |
| One-file packages | 67 | 67 | 65 | 64 | 63 | 63 | 62 | -5 |
| One-or-two-file packages | 89 | 89 | 87 | 86 | 85 | 84 | 83 | -6 |

## Wave 9.6 validation

| Check | Result |
|---|---|
| Connectors owner, Source Control composition, FleetDB, memstore, and all-`internal` compilation | PASS |
| Retired-root, retired-constructor, exact package-shape, direct-write policy, import-fanout, and package-size ratchets | PASS |
| Measured `make check-architecture` | PASS: 11/11 profiles, 10 roots, 93 direct-write rows, 25 legacy handler imports, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, and zero pending decisions; peak process-tree RSS 1,223.1 MiB under 2,048 MiB |
| Aggregate `make gate` against the paired FleetDB source and binary | PASS: all Go and frontend quality gates |

The first aggregate attempt found that the deleted composition import lowered
`internal/app/serve` fanout from 32 to 31. The exact exception was lowered and
the uninterrupted rerun passed. No threshold, allowlist, package, or fallback
facade was added.

## Wave 9.7 result

The seventh slice deletes dead source-compatibility APIs rather than carrying
them behind deprecation comments or test-only legacy implementations. It
physically removes 11 compatibility functions or values across Interaction,
Automation, Workflow Distribution, Driver, Webhooks, Trigger Bindings, and the
web store adapter. The Workflows HTTP module constructor now accepts only its
typed `Config`; its store-only `any` constructor path is gone.

The obsolete Driver stale-task sweeper implementation tests are also deleted.
Characterization now exercises the Execution owner's recovery API directly,
including exact parent ownership, fencing, replay safety, and the 20-minute
default cutoff. This removes 448 lines of tests for an implementation that no
longer owns the behavior instead of preserving a second recovery model for
test coverage.

An architecture guard now rejects handwritten production APIs with
`Deprecated:` declarations, and explicit tombstones prevent the 11 removed
symbols and the two deleted legacy test files from returning. The slice removes
499 net lines while leaving the exact package shape and import fanout unchanged:
182 production packages, 15 module packages, 167 packages outside modules, 62
one-file packages, and 83 one-or-two-file packages.

The architecture metric named `legacy handler imports` still reports **25**.
Those are not historical labels: they are an exact allowlist of live handler
imports into the horizontal `store` or `backend` surfaces. Likewise, active
Driver shared-token/header authentication fallback remains runtime legacy.
Phase 9 is not legacy-free until those executable paths are deleted and their
allowlists require zero.

## Wave 9.7 validation

| Check | Result |
|---|---|
| Interaction, Driver, Serve, Automation, Webhooks, Trigger Bindings, Workflows, Workflow Distribution, store adapter, and all-`internal` compilation | PASS |
| Retired-source-API, typed-constructor, handwritten-deprecation, package-shape, direct-write policy, and exact import-fanout ratchets | PASS |
| Measured `make check-architecture` on the implementation source | PASS: 11/11 profiles, 10 roots, 93 direct-write rows, 25 live legacy-handler allowances, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, and zero pending decisions; peak process-tree RSS 1,223.1 MiB under 2,048 MiB |
| Characterization matrix after replacing the obsolete Driver sweeper row with Execution-owner recovery tests | PASS: all 6/6 rows |
| Aggregate `make gate` against the paired FleetDB source and binary | PASS: all Go and frontend quality gates |

The first aggregate attempt correctly rejected the characterization row that
still named the deleted Driver sweeper test. After moving that row to the
Execution owner, the uninterrupted aggregate rerun passed. A later duplicate
RSS-measured architecture invocation stalled in its measurement wrapper and
was interrupted; it does not replace the completed same-source architecture
pass above.

Later waves must update this document with the selected package candidates,
the boundary reason retained or removed for each, exact shape changes, and
same-head evidence.

---

[Migration overview](README.md) · [Phase 8 consolidation](15-phase-8-consolidation-and-evidence.md)
