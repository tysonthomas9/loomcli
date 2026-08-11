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
- **Wave 9.8 implementation:** `3fdcd3669`
- **Wave 9.9 implementation:** `520e8a9f8`
- **Wave 9.10 implementation:** `97df6fd92`
- **Wave 9.11 implementation:** `9e0306f37`
- **Wave 9.12 implementation:** `a7cc649d7`
- **Wave 9.13 implementation:** `2eccf26e0`
- **Wave 9.14 implementation:** `05bbf3ef3`
- **Wave 9.15 implementation:** `2ee3dfa2a`
- **Stacked branches:** `modular-monolith-phase9-01-types-ratchet`, then
  `modular-monolith-phase9-02-shallow-seams`, then
  `modular-monolith-phase9-03-legacy-planes`, then
  `modular-monolith-phase9-04-adapter-seams`, then
  `modular-monolith-phase9-05-artifact-adapter`, then
  `modular-monolith-phase9-06-connectors-adapter`, then
  `modular-monolith-phase9-07-legacy-runtime`, then
  `modular-monolith-phase9-08-driver-auth`, then
  `modular-monolith-phase9-09-handler-ports`, then
  `modular-monolith-phase9-10-prreview-ports`, then
  `modular-monolith-phase9-11-shallow-runtime`, then
  `modular-monolith-phase9-12-shallow-vocabulary`, then
  `modular-monolith-phase9-13-explicit-context`, then
  `modular-monolith-phase9-14-session-ownership`, then
  `modular-monolith-phase9-15-native-parser-ownership`
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

## Wave 9.8 result

The eighth slice deletes the Driver authentication compatibility plane. The
workspace-scoped Driver API, hidden Driver runtime client, SDK runtime client,
and executor-to-workflow launch now use exactly one credential: a signed,
run-scoped `LOOM_RUN_TOKEN` bound to workspace, run, node, lease, and fencing
generation.

The following executable fallback surfaces are physically removed:

- the node-wide `LOOM_DRIVER_API_TOKEN` configuration and shared Bearer-token
  comparison;
- the `LOOM_DRIVER_LEGACY_AUTH_ENV` switch;
- caller-supplied node, lease, lease-token, and fencing identity headers,
  flags, environment parsing, and runtime-client options;
- the SDK `apiToken` option and legacy authentication surface manifest; and
- executor downgrade behavior when token signing or TTL resolution fails.

Any `X-Loom-Driver-*` identity header is rejected even when it agrees with the
signed claims. Missing or invalid tokens return an unauthenticated response;
expired tokens retain the distinct terminal `token_expired` response. A
claimed run whose executor cannot mint its token fails with `driver_auth`
before the workflow launcher is called. A malformed operator-provided signing
key prevents serve configuration from being built. When no operator key is
configured, serve creates one cryptographically random per-process key; that
is key-custody selection for the same signed-token scheme, not an alternate
authentication path.

An architecture tombstone now rejects every removed authentication symbol in
handwritten production Go. The slice removes 279 net implementation and
contract lines. Package shape remains unchanged at 182 production packages,
15 module packages, 167 packages outside modules, 62 one-file packages, and 83
one-or-two-file packages. The 25 live handler imports into horizontal
`store`/`backend` surfaces also remain unchanged and are the next deletion
target; no allowlist or baseline was widened.

## Wave 9.8 validation

| Check | Result |
|---|---|
| Driver API, hidden Driver CLI, serve composition, full Driver executor, and all-`internal` compilation | PASS |
| SDK runtime and frozen surface | PASS: 72/72 Node tests plus TypeScript typecheck |
| Token-only rejection, missing/malformed signing material, invalid TTL, pre-launch fail-closed, retired-symbol, and step-9 regression tests | PASS |
| Measured `make check-architecture` | PASS: 11/11 profiles, 10 roots, 93 direct-write rows, 25 live legacy-handler allowances, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, and zero pending decisions; peak process-tree RSS 1,196.5 MiB under 2,048 MiB |
| Aggregate `make gate` against the paired FleetDB source and binary | PASS: all Go and frontend quality gates |

The first aggregate attempt found a three-line `funlen` overage in serve
composition and a redundant Driver runtime-context struct literal. The final
tree extracts capability wiring, uses the direct type conversion, and passes
the targeted linter and uninterrupted aggregate rerun. No lint threshold or
exception was added.

## Wave 9.9 result

The ninth slice removes three live presentation dependencies on the horizontal
`backend` plane. Git graph and blocked-list delivery now consume a handler-owned
query port backed by Work Items `List`, `Get`, and `Blocked` projections. Health
statistics and workspace readiness consume the separate Work Items
`StatsQueries` port. `/api/config` receives only a backend-name function from
composition and can no longer read `LOOM_ISSUE_BACKEND` as a parallel discovery
path.

`Stats` and `Blocked` intentionally remain separate narrow query interfaces
rather than growing the broad Work Items `API`. The composition root retains
the concrete owner service and hands each delivery adapter only its required
read authority. Blocked-list delivery calls the direct owner query, preserving
the prior single-purpose read instead of invoking the richer Kanban projection
and its ready/deferred joins.

The obsolete `WorkspaceOpsModule.issueBackendFn` field,
`WithIssueBackendFn` setter, conditional wiring, backend import, old
backend-based handler constructors, and 106-line graph backend test double are
physically deleted. A tombstone test prevents those symbols and the config env
fallback from returning. The exact live handler-import ratchet falls from 25 to
22; no exception is widened. Package shape remains 182 production packages, 15
module packages, 167 packages outside modules, 62 one-file packages, and 83
one-or-two-file packages. The slice adds 54 net non-test/non-architecture lines
to establish the owner projections and typed ports; it does not claim a package
count reduction.

## Wave 9.9 validation

| Check | Result |
|---|---|
| Work Items owner, Git, health, auth config, issue, onboarding, app composition, and all-`internal` compilation | PASS |
| Removed-handler-symbol and config-fallback tombstones, read-only persistence classification, and exact handler-import ratchet | PASS |
| Measured `make check-architecture` within the aggregate gate | PASS: 11/11 profiles, 10 roots, 93 direct-write rows, 22 live legacy-handler allowances, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, and zero pending decisions; peak process-tree RSS 1,172.9 MiB under 2,048 MiB |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with `GOMAXPROCS=4`, two Go test workers, one Vitest worker, and a 3 GiB Go soft memory limit |

The first aggregate invocation accidentally selected the older
`fleet-db/unified-agents` sibling at `8120c78`; its OpenAPI source predates the
Phase 7 companion and correctly failed Loom's vendored-spec freshness guard.
No contract was copied backward. Rebuilding from the documented clean
`fleet-db-modular-monolith-phase7` worktree at `b71dec551` passed the focused
contract package and the uninterrupted aggregate rerun.

## Wave 9.10 result

The tenth slice removes all three remaining PR-review handler dependencies on
horizontal backend and repository planes. The HTTP adapter now owns its private
pull-request wire shapes instead of importing generated backend DTOs. Repository
membership and reviewer materialization consume a two-method, consumer-owned
`WorkspaceQueries` port backed by the Workspace owner. The PR-review module no
longer receives `store.Store`, reaches into workspace/repository collections,
or constructs Connector owner implementations.

The application root now builds Connector dispatch, management, and credential
sealing from one durable adapter and one vault, then injects those owner
interfaces. This deletes on-demand vault reconstruction in the handler and
prevents the sealer used for new credentials from drifting from the vault used
for synchronization and dispatch. The old eleven-argument PR-review constructor,
`prReviewStore`, `connectorManagementStore`, and forwarding-only
`newPRReviewRouteModule` function are physically absent; there is no alternate
constructor or compatibility facade.

The exact live handler-import ratchet falls from 22 to 19. Import fanout falls
from 41 to 40 for `internal/webui/app` and from 18 to 15 for
`internal/webui/handlers/prreview`; both exact exceptions were tightened.
Package shape remains 182 production packages, 15 module packages, 167 packages
outside modules, 62 one-file packages, and 83 one-or-two-file packages. This
slice removes executable horizontal edges and a shallow composition seam; it
does not claim a package-count reduction.

## Wave 9.10 validation

| Check | Result |
|---|---|
| Full PR-review handler behavior, connector composition, and all-`internal` compilation | PASS |
| Removed backend DTO/store imports, obsolete constructor path, and exact fanout/handler-import ratchets | PASS: no production PR-review import of `internal/backend`, `internal/store`, or `internal/infra/connectorsvault`; exact live handler allowance is 19 |
| Authoritative repository architecture snapshot | PASS in 408.53s: 11/11 profiles, 10 capability roots, 93 direct-write rows, 19 live legacy-handler allowances, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, and zero pending decisions |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with `GOMAXPROCS=4`, two Go test workers, one Vitest worker, and a 3 GiB Go soft memory limit |

The first aggregate attempt failed closed because its exact import-fanout
exceptions still recorded the pre-change values. Tightening app `41 -> 40` and
PR-review `18 -> 15` made the focused fanout check and uninterrupted aggregate
rerun pass. No threshold was raised and no exception was added.

## Wave 9.11 result

The eleventh slice deletes the horizontal `internal/runtimepreflight` package
and moves its sole live policy into the Workflows HTTP delivery adapter that
decides whether an epic workflow may enqueue the local task runner. The
Workflows adapter owns a narrow `BackendHealthQuery` port and its readiness
projection. Serve composition maps the existing backend operations result into
that consumer-owned model; Workflows does not import CLI packages or the
operations model.

The deleted package's global test hook and package-wide `TestMain` health stub
are also gone. Tests inject the Workflows port per module, and production fails
closed when composition omits it, when the selected provider is unknown, when
its CLI is absent, or when authentication is missing. The configured local
runtime provider remains authoritative, with Codex as the ordinary default;
explicit non-local runners bypass this local-only gate.

The exact package shape moves from 182 to 181 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 167 to 166,
one-file packages fall from 62 to 61, and one-or-two-file packages fall from 83
to 82. Direct persistence remains exactly 93 rows across 102 sites, and the
live legacy-handler allowance remains 19. `internal/runtimepreflight` is now a
retired root, so neither recreating it nor importing it can pass architecture
validation.

`internal/infra/sessionstoreadapter` was evaluated in the same slice but was
not mechanically bypassed. That experiment exposed 24 direct session-store
writes outside the permitted ownership boundary. The change was discarded
rather than replacing a facade with direct persistence. Its deletion requires
a complete local-session port migration in a later wave, followed by physical
removal of the adapter in that same wave; it is not an accepted compatibility
layer in the Phase 9 target.

## Wave 9.11 validation

| Check | Result |
|---|---|
| Workflows preflight behavior, composition adapter, exact package shape, retired root, direct-write policy, import fanout, and all-`internal` compilation | PASS |
| Lint, dependency guard, and control-plane topology guard | PASS: zero issues; exact fanout restored without raising an exception |
| Authoritative repository architecture snapshot | PASS in 413.79s at the final implementation tree |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with `GOMAXPROCS=4`, two Go test workers, one Vitest worker, and a 3 GiB Go soft memory limit |

The aggregate proof required environment corrections but no source correction:
the first invocation omitted the temporary Go build cache, the second selected
an empty module cache while network access was unavailable, and the third
sandboxed invocation reached architecture step 8 before its RSS monitor was
denied `/bin/ps`. The final approved invocation reused the populated module
cache, placed build output under `/tmp`, allowed the bounded RSS monitor, and
passed uninterrupted. An earlier direct WebUI-to-CLI health import was also
rejected by the dependency guard; the final tree uses the consumer-owned port
and composition adapter described above.

## Wave 9.12 result

The twelfth slice deletes three one-file vocabulary packages that had no
independently replaceable implementation or durable protocol seam:

- `internal/authmode` contained only the `open` and `oidc` wire values and a
  two-value validator. Deployment trust-mode vocabulary now lives with the
  existing platform Authority owner.
- `internal/backendnames` contained only the `claude` and `codex` provider
  names. Provider identity now lives with the existing platform Runtime owner.
- `internal/cli/backendapi` existed only to carry optional backend interfaces
  and value shapes between the CLI package and its backend adapters. The CLI
  consumer now owns those contracts directly; the backend adapters implement
  them without a third package hop.

The slice does not introduce replacement compatibility packages, alternate
constants, environment fallbacks, or generic shared vocabulary. The three old
roots are physically absent and are included in the retired-horizontal-root
guard, so recreating a directory or importing an old path fails architecture
validation.

The exact package shape moves from 181 to 178 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 166 to 163,
one-file packages fall from 61 to 58, and one-or-two-file packages fall from 82
to 79. Direct persistence remains exactly 93 rows across 102 sites, and the
live legacy-handler allowance remains 19. The change therefore removes three
shallow seams without relabeling persistence or weakening a capability edge.

## Wave 9.12 validation

| Check | Result |
|---|---|
| Authority trust-mode behavior, CLI backend capability behavior, provider consumers, and all-`internal` compilation | PASS |
| Retired-root, exact package-shape, direct-write, import-fanout, lint, and dependency guards | PASS: all three roots absent; exact shape `178 / 15 / 163 / 58 / 79`; zero lint issues |
| Authoritative repository architecture snapshot | PASS in 544.35s at implementation `a7cc649d78a46f2bf3b4118263bd2bcc19c3b864` |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with sanitized Loom runtime variables, `GOMAXPROCS=4`, two Go test workers, one Vitest worker, and a 3 GiB Go soft memory limit |

The final aggregate invocation passed without a source correction. It reused
the populated temporary Go build cache and ran outside the filesystem sandbox
only because the architecture RSS monitor requires `/bin/ps`. No threshold,
allowlist, fallback, or compatibility facade was added.

## Wave 9.13 result

The thirteenth slice deletes the process-global `internal/runtimectx` package
and the ambient context-provider path in `internal/events`. CLI commands now
thread their Cobra or request context into store opening, workspace resolution,
session persistence, event emission, monitor collection, and worker execution.
The deleted root is included in the retired-horizontal-root guard, and a
dedicated tombstone rejects `RootContext`, `SetRootContext`, and
`SetContextProvider` if handwritten production source attempts to restore
them.

Automode no longer opens FleetDB internally to rediscover workspace metadata
for prompt construction. Planner, task, custom-agent, and remote-worker
composition resolve their prompt inputs before entering the loop and supply a
required prompt function. A missing prompt or monitor collector fails fast;
there is no default prompt builder, ambient workspace lookup, nil-context
substitution, or legacy constructor. The event bus and session store similarly
require an explicit context at construction, and HTTP monitor handlers pass the
request context through workspace-scoped collection.

The exact package shape moves from 178 to 177 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 163 to 162,
one-file packages fall from 58 to 57, and one-or-two-file packages fall from 79
to 78. The implementation commit removes 166 net repository lines and does not
add a replacement context package or compatibility shim. Direct persistence
remains exactly 93 rows, and the live legacy-handler allowance remains 19;
those unresolved edges are later deletion work rather than part of this slice.

## Wave 9.13 validation

| Check | Result |
|---|---|
| Events, sessions, session-store adapter, cmdstore, automode, agent, monitor, metrics, worker, session coordination, and all-`internal` compilation | PASS |
| Retired ambient-context APIs and exact package shape | PASS: `internal/runtimectx` absent; exact shape `177 / 15 / 162 / 57 / 78` |
| Authoritative repository architecture snapshot | PASS in 536.182s at implementation `2eccf26e0`; every remaining architecture test passed separately in 295.958s |
| Measured architecture guard in the aggregate gate | PASS: 11/11 profiles, 10 roots, 93 direct-write rows, 19 live legacy-handler allowances, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, and zero pending decisions; peak process-tree RSS 1,213.5 MiB under 2,048 MiB |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with sanitized Loom runtime variables, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The first aggregate attempt exposed one static-analysis defect in the new nil
context rejection test; using a typed nil preserved the fail-fast assertion
without violating `SA1012`. A later attempt exhausted the host temporary volume
during race coverage. Clearing only disposable Go build/test artifacts and
serializing package workers removed the disk spike without weakening the gate.
Another diagnostic run selected the generic FleetDB checkout at `8120c78` and
correctly failed the vendored-spec freshness guard. The final proof rebuilt the
binary from the documented Phase 7 companion at `b71dec551`; its OpenAPI
SHA-256 exactly matched the vendored `816b0b0c…7ef0` snapshot, the focused route
contract passed, and the aggregate rerun completed without a source correction.

## Wave 9.14 result

The fourteenth slice deletes the shallow native-transcript dispatcher and
Codex forwarding package under `internal/sessions/transcript`. Session
ownership now includes selecting the recorded backend parser, while the Codex
translation maps the external harness-wrapper representation directly into the
existing canonical session event. The subagent transcript reader uses the same
Sessions entry point rather than importing a parallel dispatcher.

The deleted packages are guarded as retired roots. No generic parser port or
replacement facade was introduced: these parsers are local implementation
choices with no independently replaceable runtime boundary. The previous
unknown-backend behavior silently selected the Claude wire format; it is gone.
An unrecognized recorded backend now returns an explicit error, preserving the
fail-closed runtime policy.

The exact package shape moves from 177 to 175 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 162 to 160,
one-file packages fall from 57 to 55, and one-or-two-file packages fall from 78
to 76. The implementation removes 129 net repository lines, including the two
production packages and their duplicate package-level tests.

## Wave 9.14 validation

| Check | Result |
|---|---|
| Sessions, WebUI session coordination, misc-handler bridge, platform-runtime, and all-`internal` compilation | PASS |
| Claude, Codex, and OpenCode parsing plus unknown-backend fail-closed behavior | PASS at the Sessions owner surface |
| Retired-root, exact package-shape, import-fanout, topology, and affected lint guards | PASS: both roots absent; exact shape `175 / 15 / 160 / 55 / 76`; zero affected lint issues |
| Authoritative repository architecture snapshot | PASS in 433.841s at implementation `05bbf3ef3` |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with sanitized Loom runtime variables, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The full gate used the exact Phase 7 companion checkout and binary whose
OpenAPI SHA-256 matches Loom's vendored `816b0b0c…7ef0` snapshot. The final
race-and-coverage pass ran sequentially; no threshold, allowlist, compatibility
path, or fallback was added to obtain the green result.

## Wave 9.15 result

The fifteenth slice completes native-transcript parser ownership by deleting
the remaining Claude and OpenCode subpackages. Claude parsing now delegates
directly from Sessions to the harness-wrapper parser, and the shared event
mapping is private to Sessions. OpenCode's wire representation and event mapper
are likewise owner-private.

The deleted OpenCode package exposed export DTOs, file-reading helpers, and
modified-file extraction that had no production consumer. The deleted Claude
package exposed serialization, truncation, modified-file extraction, and
subagent-ID extraction with no production consumer; its only live parse path
was already a wrapper delegation. None of those unused APIs was relocated.
The retained surface is the one Sessions use case: parse the recorded native
transcript into canonical events.

The exact package shape moves from 175 to 173 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 160 to 158,
one-file packages fall from 55 to 53, and one-or-two-file packages fall from 76
to 74. The implementation deletes 621 net repository lines and guards both
removed roots against return.

## Wave 9.15 validation

| Check | Result |
|---|---|
| Sessions behavior and all-`internal` compilation | PASS |
| Claude, Codex, and OpenCode owner-surface parsing, including OpenCode tool-result and nil-state regressions | PASS |
| Retired-root, exact package-shape, and affected lint guards | PASS: both roots absent; exact shape `173 / 15 / 158 / 53 / 74`; zero affected lint issues |
| Authoritative repository architecture snapshot | PASS in 521.168s at implementation `2ee3dfa2a` |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with the matching `816b0b0c…7ef0` OpenAPI snapshot, sanitized Loom runtime variables, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The first architecture invocation was stopped because its execution wrapper
yielded without returning a handle, making a trustworthy result impossible to
capture. The same deterministic command was restarted with a retained handle
and passed. The complete aggregate gate then passed without a source
correction, threshold change, allowlist expansion, compatibility path, or
fallback.

Later waves must update this document with the selected package candidates,
the boundary reason retained or removed for each, exact shape changes, and
same-head evidence.

---

[Migration overview](README.md) · [Phase 8 consolidation](15-phase-8-consolidation-and-evidence.md)
