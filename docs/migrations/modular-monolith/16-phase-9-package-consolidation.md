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
- **Wave 9.16 implementation:** `4c82d3781`
- **Wave 9.17 implementation:** `92eeb4423`
- **Wave 9.18 implementation:** `7c179fada`
- **Wave 9.19 implementation:** `c412c3f61`
- **Wave 9.20 implementation:** `d34fb0ed1`
- **Wave 9.21 implementation:** `fb16ce443`
- **Wave 9.22 implementation:** `22688c1c0`
- **Wave 9.23 implementation:** `ce388df2d`
- **Wave 9.24 implementation:** `015ff85ef`
- **Wave 9.25 implementation:** `9caddc7e5`
- **Wave 9.26 implementation:** `cfe542420`
- **Wave 9.27 implementation:** `14f4ee9ac`
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
  `modular-monolith-phase9-15-native-parser-ownership`, then
  `modular-monolith-phase9-16-local-session-archive`, then
  `modular-monolith-phase9-17-durable-terminal-launch`, then
  `modular-monolith-phase9-18-driver-run-contract`, then
  `modular-monolith-phase9-19-local-node-state`, then
  `modular-monolith-phase9-20-agents-bootstrap-composition`, then
  `modular-monolith-phase9-21-automation-owner-intents`, then
  `modular-monolith-phase9-22-handler-read-ports`, then
  `modular-monolith-phase9-23-connector-inbound-secrets`, then
  `modular-monolith-phase9-24-shallow-package-deletion`, then
  `modular-monolith-phase9-25-legacy-package-deletion`, then
  `modular-monolith-phase9-26-runtime-legacy-deletion`, then
  `modular-monolith-phase9-27-handler-port-deletion`
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

## Wave 9.16 result

The sixteenth slice deletes `internal/infra/sessionstoreadapter`, the
forwarding-only package whose twelve exported functions mirrored Sessions
store and session methods one for one. Its CLI consumers now cross a
Sessions-owned `Archive` interface expressed as lifecycle intents: begin a
session, capture a transcript, append or read event evidence, repair metadata
or an index entry, and apply or preview retention. Cleanup preview policy and
stale-index repair coordination moved behind that interface instead of being
copied into callers. Finalization owns a narrow consumer-side `localSession`
port, so it no longer depends on the deleted adapter or on public session
fields.

The slice also deletes `internal/sessions/eventstore`. Its single production
implementation is now private session event-log machinery, while Sessions
owns directory creation, append, deduplication, ordering, compaction, and read
projection. Backend acquisition and WebUI serving no longer import or open a
second storage package. The former packages are both retired roots; recreating
either path or importing it fails architecture validation.

The direct-write analyzer now accepts an exact Go file as an owner-adapter
root. `internal/sessions/archive.go` is therefore the precise Interaction
adapter seam: its eight Store mutations are owner-classified, while private
Sessions implementation calls are not mislabeled as caller persistence. No
production CLI caller directly references `sessions.Store`, `NewStore`, or its
session persistence methods. Exact direct persistence falls from 93 rows/102
sites to 89 rows/98 sites without adding a transitional disposition or raising
a threshold.

The exact package shape moves from 173 to 171 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 158 to 156,
one-file packages fall from 53 to 51, and one-or-two-file packages fall from 74
to 72. The implementation is `4c82d3781579878fc640b9b999a083ada7297c54`.

## Wave 9.16 validation

| Check | Result |
|---|---|
| Sessions archive/event-log behavior and every migrated CLI/WebUI consumer | PASS: Sessions, agent, automode, backends, cleanup, Doctor, hooks, finalization, and session coordination; automode completed in 47.760s |
| All production `internal/...` packages | PASS: every package compiled under the clean exact-pair environment |
| Retired roots, exact package/direct-write ratchets, exact-file analyzer regression, and affected lint | PASS: shape `171 / 15 / 156 / 51 / 72`; writes `89 / 98`; zero lint issues |
| Authoritative repository architecture snapshot | PASS in 388.83s at implementation `4c82d3781` |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: Go, frontend, race, coverage, dependency, contract, architecture, and build gates with sanitized Loom runtime variables, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The first authoritative invocation reached Go's default ten-minute test alarm
while removing its temporary profile cache; it produced no passing evidence.
The next invocation correctly exposed the stale checked-in 93-row summary and
then exhausted a temporary volume already occupied by disposable earlier
caches. The summary was tightened to 89, only Phase 9 temporary build/profile
caches were removed, and the unchanged full-profile proof passed with a
20-minute alarm. The subsequent aggregate gate passed without another source
correction, threshold change, allowlist expansion, compatibility path, or
fallback.

## Wave 9.17 result

The seventeenth slice deletes the executable terminal-launch compatibility
protocol. WebSocket attachment no longer reconstructs a command from a tab's
session name, supplies a launch for missing metadata, or treats a UUID-shaped
name as a special migration boundary. Generic tab creation now requires a
validated backend intent; the server derives and durably persists the exact
argv and environment envelope before the frontend mounts a terminal instance.
Every non-live attachment without that envelope fails closed. A backend-started
setup PTY remains attachable only while that exact in-process session exists;
after restart it is not replayed from a name.

The frontend likewise stops parsing terminal names for backend or agent
identity. Agent classification requires server-owned kind and agent ID
metadata, and restored colors/issue tabs use the persisted backend. Tab
creation, duplication, and issue seeding persist metadata before opening a
WebSocket, closing the race in which attachment could outrun the PUT. The dead
`trySeedOnConnect` no-op and its unused connection callback are deleted rather
than retained as extension points.

This is a compatibility-deletion wave, not a package-deletion wave. Exact
package shape remains `171 / 15 / 156 / 51 / 72`, and direct persistence
remains `89 / 98`. An architecture tombstone rejects the retired Go and
TypeScript symbols and all name-derived agent/backend classification fragments.

## Wave 9.17 validation

| Check | Result |
|---|---|
| Terminal launch/store/attach behavior | PASS: server-derived shell and five AI-backend envelopes, missing store/metadata/envelope rejection, invalid backend rejection, setup live-only attach, and persisted envelope consumption |
| Frontend terminal behavior | PASS: typecheck plus 169 focused tests covering persist-before-mount creation/restoration, issue seeding, agent classification, connection state, and metadata rollback |
| Generated contracts and affected lint | PASS: Go and TypeScript OpenAPI outputs current; zero affected Go lint issues |
| Retired compatibility tombstone and exact shape | PASS: name-derived launch/backend/agent inference and dead connect-seeding callbacks absent; exact shape `171 / 15 / 156 / 51 / 72` |
| Measured architecture guard | PASS: 11/11 profiles, 10 capability roots, 89 direct-write rows, 19 live legacy-handler allowances, and 1,224.1 MiB peak process-tree RSS under 2,048 MiB |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: Go, frontend, race, coverage, dependency, contract, architecture, and build gates with the matching `816b0b0c…7ef0` OpenAPI snapshot and bounded workers/memory |

The first aggregate attempt found two new lint issues and led to a smaller PUT
construction helper plus removal of a production-only test wrapper. The next
attempt intentionally failed the FleetDB freshness guard because only the
binary, not `FLEET_DB_REPO`, pointed at the Phase 7 companion. The final run
pinned both inputs to the exact companion checkout and passed without changing
a threshold, allowlist, contract snapshot, or compatibility behavior.

## Wave 9.18 result

The eighteenth slice deletes `internal/driver/runtypes`, a neutral 39-line DTO
package introduced only to break an import cycle. It owned no policy, storage,
protocol, or independently replaceable adapter. Driver now owns `RunRequest`
and `RunResult` directly. The Sandbox child package owns only its launcher port,
placement admission, and placement evidence; Driver projects a sandbox refusal
into the Driver-owned terminal result.

No equivalent neutral package or alias bridge replaces the deleted root. The
stale executor comment claiming a nil direct-store compatibility path is also
removed: the runtime already requires the Execution owner APIs and fails closed
when they are absent. Architecture guards reject both the deleted import root
and the retired compatibility claim.

The exact package shape moves from 171 to 170 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 156 to 155,
one-file packages fall from 51 to 50, and one-or-two-file packages fall from 72
to 71. Removing the bridge also lowers Driver import fanout from 19 to the
default ceiling of 18, so its exact exception is deleted rather than lowered.

## Wave 9.18 validation

| Check | Result |
|---|---|
| Driver and Sandbox behavior | PASS: complete package suites cover trusted/untrusted placement, launch, runtime result, cancellation, placement evidence, tokens, recovery, and worker flows |
| All production `internal/...` packages | PASS: every package compiled after deleting the bridge |
| Retired root, exact package shape, import fanout, and affected lint | PASS: shape `170 / 15 / 155 / 50 / 71`; Driver fanout 18 with no exception; zero affected lint issues |
| Authoritative repository architecture snapshot | PASS in 385.875s at implementation `7c179fada` |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: Go, frontend, race, coverage, dependency, contract, architecture, and build gates with the matching `816b0b0c…7ef0` OpenAPI snapshot and bounded workers/memory |

The first aggregate attempt exposed the now-stale Driver fanout exception. The
exception row was physically deleted, the focused guard passed at the normal
threshold, and the unchanged full paired gate then passed.

## Wave 9.19 result

The nineteenth slice deletes `internal/localnodeconfig`, a 42-line wrapper
whose only storage operations delegated directly to Bootstrap's state cache.
Bootstrap now exposes the machine-local runtime-provider read and atomic update
alongside the `WorkspaceLocalState.DefaultRuntimeProvider` field and transaction
it already owns. CLI, Driver, workflow preflight, terminal launch, workspace
coordination, and WebUI projections call that owner directly.

This does not move machine-local configuration into FleetDB or a capability
domain. Runtime-provider selection remains per-machine state in
`~/.loom/state.json`; the change only removes a false module boundary around
the existing adapter. Owner-level tests prove normalized round trips, blank-key
rejection, and preservation of unrelated workspace checkout and repository
state. The deleted root is guarded against return.

The exact package shape moves from 170 to 169 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 155 to 154,
one-file packages fall from 50 to 49, and one-or-two-file packages fall from 71
to 70.

## Wave 9.19 validation

| Check | Result |
|---|---|
| Bootstrap state-cache behavior | PASS: runtime-provider normalization, atomic round trip, blank-key rejection, and unrelated local-state preservation |
| Migrated consumers | PASS: CLI config, Driver, WebUI store adapter, workflow preflight, terminal launch, and workspace coordination complete suites |
| All production `internal/...` packages | PASS: every package compiled after deleting the wrapper |
| Retired root, exact package shape, import fanout, and affected lint | PASS: shape `169 / 15 / 154 / 49 / 70`; zero affected lint issues |
| Authoritative repository architecture snapshot | PASS in 390.489s at implementation `c412c3f61` |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: Go, frontend, race, coverage, dependency, contract, architecture, and build gates with the matching `816b0b0c…7ef0` OpenAPI snapshot and bounded workers/memory |

## Wave 9.20 result

The twentieth slice deletes both Agents bootstrap compatibility packages:
`internal/app/agentsbootstrap` and
`internal/infra/agentsbootstrapstore`. Workspace management no longer
constructs a private Agents service over the horizontal `store.RoleStore` and
`store.AgentServiceStore` interfaces. It owns a two-method consumer port, and
the existing serve application composition implements that port with the
canonical Agents capability and exact system authority. Both `loom serve` and
the standalone workspace-create command inject that owner command surface;
missing composition fails closed.

The startup PromptFile repair is now an Agents-owned command over the normal
revisioned Role port. Exact replay is read-only, a different non-empty value is
a conflict, and a concurrent empty-to-value update is resolved through the
Role CAS and authoritative winner read. The separate `BootstrapAPI`,
`BootstrapService`, `BootstrapStore`, `RolePromptRepairStore`, legacy
constructor, and memstore-only repair primitive are deleted rather than moved.
Architecture guards reject both retired roots and those compatibility symbols
if they return.

The exact package shape moves from 169 to 167 production packages. Module
packages remain 15, packages outside `internal/modules` fall from 154 to 152,
one-file packages fall from 49 to 47, and one-or-two-file packages fall from
70 to 68. Direct persistence falls from 89 rows/98 sites to 86 rows/95 sites
because all three bootstrap-adapter mutations disappeared. Workspace manager
import fanout falls from its exact exception of 18 to 14, so the exception row
is deleted entirely. The implementation is
`d34fb0ed12aae5e3476f07e332c80ef7e67de45c`.

## Wave 9.20 validation

| Check | Result |
|---|---|
| Agents owner, FleetDB adapter, application composition, serve adapter, workspace manager, CLI workspace, and memstore suites | PASS |
| All production `internal/...` packages | PASS: every package compiled after deleting both compatibility packages and the legacy constructor |
| Retired roots/symbols, exact package/direct-write ratchets, import fanout, and affected lint | PASS: shape `167 / 15 / 152 / 47 / 68`; writes `86 / 95`; workspacemgr fanout 14 with no exception; zero lint issues |
| Authoritative repository architecture snapshot | PASS in 387.091s at implementation `d34fb0ed1` |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with the matching `816b0b0c…7ef0` OpenAPI snapshot, sanitized Loom runtime variables, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The first authoritative invocation completed every repository profile and
exposed only the stale 89-row summary after the three adapter writes had been
deleted. The exact row/site snapshot was tightened to 86/95, and the complete
unchanged-source rerun passed. The subsequent full paired gate passed without
adding an allowlist, compatibility constructor, persistence receiver, fallback
path, or contract change.

## Wave 9.21 result

The twenty-first slice deletes the two remaining Automation composition
compatibility adapters and their tests. Trigger-binding routes no longer
reconstruct Connectors behavior over `store.TriggerBindingStore` and
`connectors.ManagementStore`, and they no longer reconstruct Agent identity
queries over `store.AgentServiceStore`. They consume the canonical Agents
identity queries and a new Connectors-owned `BindingLifecycle` intent directly.

Connectors now owns the complete operations for configuring one binding's
privileged signing secret and converging its grants to revoked. The service
validates canonical input and persisted grants, hides grant enumeration and
idempotent revoke handling, and exposes no secret result or persistence
interface. FleetDB and memstore implement the owner-private persistence port as
the two real adapters. The FleetDB adapter uses the existing trigger-binding
PATCH contract, while memstore preserves public redaction and exposes the
secret only through its privileged resolver.

Agent deletion retains a consumer-owned two-argument cleanup port instead of
importing the Connectors command vocabulary. Composition performs the single
translation into the owner command. This keeps the Agents handler at its exact
19-import ceiling while tightening `agentmodules` from 38 imports to 37. The
old `ConnectorCompatibility`, `UnattachedBindingIdentityChecker`,
`BindingGrantCompatibility`, `DeleteBindingAndRevokeGrants`, and store-backed
adapter symbols are physically absent and guarded against return. The duplicate
legacy schedule decorator is also deleted in favor of Automation's canonical
source-kind projection.

This is a compatibility-deletion wave, not a package-deletion wave. Exact
package shape remains `167 / 15 / 152 / 47 / 68`, and direct persistence
remains `86 / 95`. The FleetDB client call-site ratchet moves from 237 to 238
because the new owner adapter issues the already-inventoried PATCH route
directly rather than depending on the horizontal trigger-binding store. The
implementation is `fb16ce443`.

## Wave 9.21 validation

| Check | Result |
|---|---|
| Connectors owner behavior | PASS: canonical input validation, secret persistence, grant validation, idempotent revoke, and changed-row counting |
| FleetDB and memstore adapters | PASS: exact PATCH request/response contract, owner-error mapping, privileged secret round trip, public redaction, and missing-binding mapping |
| Trigger-binding, Agents, and route-composition suites | PASS |
| Retired files/symbols, package shape, direct-write policy, and import fanout | PASS: four compatibility files absent; shape `167 / 15 / 152 / 47 / 68`; writes `86 / 95`; `agentmodules` fanout 37; Agents fanout 19 |
| Measured architecture guard | PASS: 11/11 profiles, 10 capability roots, 19 live legacy-handler allowances, 86 direct-write rows, zero pending decisions, and 1,207.7 MiB peak process-tree RSS under 2,048 MiB |
| Aggregate `make gate` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with the exact paired source and binary, sanitized Loom runtime variables, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The first aggregate attempt exposed the lower `agentmodules` fanout and a new
Agents type dependency. Composition was narrowed instead of raising the
Agents exception, and the `agentmodules` ceiling was tightened to 37. The next
attempt found one test-only staticcheck simplification. The subsequent race run
reached the exact FleetDB client-call tripwire; the already-declared PATCH
route and new direct owner-adapter call were reviewed, and the count moved to
238. The final unchanged-source aggregate gate and measured architecture pass
both succeeded.

## Wave 9.22 result

The twenty-second slice removes two more horizontal persistence dependencies
from production HTTP adapters. The approval endpoint no longer accepts a
composite Store or reaches `AwaitStore`; its consumer-owned
`PendingAwaitQueries` port exposes only the pending-pattern projection required
for eligibility before the Automation-owned journal and Execution-owned
dispatch operations run. The trigger-binding endpoint likewise no longer
imports `store.DriverRunStore`, `store.DriverRunFilter`, or Store-owned ordering
helpers. Its `RunQueries` port expresses the exact bounded, newest-first
binding-history query used by the UI.

The existing WebUI read-projection module now implements the binding-history
join at the composition boundary. It is shared by trigger-binding routes and
Agents binding decoration, so filter translation and ordering policy are not
duplicated or moved into another handler. Production code contains no fallback
to the removed Store paths: missing ports preserve the existing inert or
fail-closed route behavior.

The exact live handler-import allowance falls from 19 to 17. Total package
shape remains 167 production packages, 15 module packages, and 152 packages
outside modules. Adding the second cohesive read-projection source file lowers
the one-file-package ceiling from 47 to 46; one-or-two-file packages remain 68.
Direct persistence remains `86 / 95`, and no FleetDB contract or client call
site changes. The implementation is `22688c1c0`.

## Wave 9.22 validation

| Check | Result |
|---|---|
| Approval, trigger-binding, Agents, route-composition, read-projection, and Driver suites | PASS |
| Removed handler dependencies and exact topology ratchets | PASS: production approval and trigger-binding packages contain no `internal/store` import; handler allowance `17`; shape `167 / 15 / 152 / 46 / 68`; writes `86 / 95` |
| Measured architecture guard at implementation `22688c1c0` | PASS: 11/11 profiles, 10 capability roots, 17 live legacy-handler allowances, 86 direct-write rows, zero pending decisions, and 1,247.9 MiB peak process-tree RSS under 2,048 MiB |
| Aggregate `make gate` at implementation `22688c1c0` against FleetDB `b71dec551` | PASS: all Go and frontend quality gates with the exact paired source and binary, sanitized Loom runtime variables, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The implementation review found that the new binding-history projection made
`readprojection` a two-file package. Its one-file ceiling was tightened to 46,
then both the repository-scale architecture transaction and aggregate paired
gate were rerun at the amended implementation head. No allowance was widened.

## Wave 9.23 result

The twenty-third slice deletes the executable TriggerBinding secret
compatibility plane across both repositories. A trigger binding no longer
accepts, stores, patches, redacts, resolves, or authorizes a webhook signing
secret. FleetDB removes the field from its public model, OpenAPI contract,
permission set, Redis/Lua persistence, Postgres reads and writes, and agent
provisioning snapshots. Migration 081 drops the live Postgres column; the
historical migration that introduced it remains immutable migration history,
not an executable compatibility path. The retired create field is rejected and
the retired secret endpoint returns 404.

Connectors is now the only owner of inbound signing material. Loom's webhook
verifier resolves the exact enabled route through a consumer-owned Automation
query port and resolves current or in-window previous secrets through the
Connectors secret source. A missing, disabled, deleted, ambiguous, or
unsupported route/connector fails closed with the same 401 as a bad signature.
There is no binding-secret fallback. Trigger-management JSON and CLI creation
also drop `secret` and `--secret`; callers provision a Connector independently
from the Automation binding.

This is complete-plane deletion rather than package deletion. Exact package
shape remains `167 / 15 / 152 / 46 / 68`, and direct persistence remains
`86 / 95`. Removing the webhook handler's Store dependency lowers the live
handler-import allowance from 17 to 16. Removing the FleetDB secret resolve and
binding-secret update calls lowers the exact client call-site ratchet from 238
to 236. No replacement facade, alias, dual-write, compatibility constructor, or
fallback resolver is retained. The Loom implementation is `ce388df2d`; its
paired FleetDB contract and migration implementation is `9c1859a`.

## Wave 9.23 validation

| Check | Result |
|---|---|
| FleetDB focused API, storage, authorization, and migration suites | PASS: retired field rejected, retired endpoint 404, migration 081 present, and both storage implementations compile and pass focused behavior tests |
| Loom Automation, Connectors, verifier, trigger-management, composition, CLI, memstore, and FleetDB adapter suites | PASS, including build-tagged webhook E2E compilation |
| Retired production symbols and exact contract snapshot | PASS: no binding secret field, resolver, endpoint, permission, CLI flag, compatibility verifier, or fallback remains; Loom's vendored OpenAPI is byte-identical to the paired FleetDB source |
| Measured architecture guard | PASS: 11/11 profiles, 10 capability roots, 16 live legacy-handler allowances, 86 direct-write rows, zero pending decisions, and 1,198.8 MiB peak process-tree RSS under 2,048 MiB |
| FleetDB aggregate `make gate` at `9c1859a` | PASS: static analysis, race/coverage, Redis and Postgres integration, migration/storage contracts, 80.8% total coverage, all 28 measured packages above the 50% floor, container E2E, crash/recovery, and harness evaluation against the explicit active Podman socket |
| Loom aggregate `make gate` at `ce388df2d` against FleetDB `9c1859a` | PASS: all Go and frontend quality gates with byte-identical OpenAPI SHA-256 `54a75342…65733`, exact companion binary SHA-256 `8f963829…7c0f0`, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

## Wave 9.24 result

The twenty-fourth slice removes three package boundaries that no longer pass
the boundary test while preserving their behavior in the owning package or
test surface:

- `internal/cli/serve/serveadapter/daytonabroker` had one production consumer,
  while its parent contained only a type alias and forwarding constructor. The
  real credential-isolating external adapter and its live proof now live in
  `serveadapter`; no provider behavior, port, or fail-closed check was removed.
- `internal/cli/serve/workspacemgr/workspacematerialization` had one consumer,
  its parent `workspacemgr`, plus duplicated forwarding helpers. The Git
  inspection, recovery, cancellation, worktree attachment, and rollback logic
  now live directly with the workspace materialization workflow.
- `internal/harness/fakeharness/mock` was a test executable, not a production
  package. It now lives under `internal/harness/fakeharness/testdata/mock`, and
  the package-shape scanner explicitly follows Go's `testdata` exclusion
  convention while the integration test continues to compile and execute it.

The three retired roots are in the cannot-return guard, and no imports target
them. Exact package shape falls from `167 / 15 / 152 / 46 / 68` to
`164 / 15 / 149 / 43 / 65`. Capability roots remain 10, live handler imports
remain 16, and direct persistence remains `86 / 95`. The implementation is
`015ff85ef`.

## Wave 9.24 validation

| Check | Result |
|---|---|
| Workspace materialization, Daytona host adapter, workflow-distribution authoring, backend, and harness suites | PASS |
| Exact package topology, retired-root, source-control reachability, and import-fanout ratchets | PASS: shape `164 / 15 / 149 / 43 / 65`; all three old roots absent; `serveadapter` remains at its exact approved fanout |
| Measured architecture guard | PASS: 11/11 profiles, 10 capability roots, 16 live legacy-handler allowances, 86 direct-write rows, zero pending decisions, and 1,165.1 MiB peak process-tree RSS under 2,048 MiB |
| Aggregate `make gate` at `015ff85ef` against FleetDB `9c1859a` | PASS: all Go and frontend quality gates with the exact paired source and binary, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The first aggregate run rejected a new twenty-eighth import on `serveadapter`.
The corrected implementation consumes Daytona runtime layout and bundle staging
through the already-imported workflow-distribution authoring adapter, removing
the extra edge instead of increasing the exact fanout exception. The complete
unchanged-source gate then passed.

## Wave 9.25 result

The twenty-fifth slice deletes four more production package boundaries and the
compatibility APIs that kept two of them alive:

- `internal/cli/serve/workspacemgr/admissionstore` was a Workspace-internal
  repository plane with one real consumer. Its journal now lives directly in
  `workspacemgr`; the broad four-store interface, forwarding journal, old
  constructor, and six `BuildStoreBacked*` compatibility wrappers are deleted.
  Serve and the workspace CLI compose the Workspace owner API explicitly.
- `internal/driver/taskworktree` was used only by its parent. Worktree
  preparation and stack lineage now live in `driver`; production receives the
  three Source Control ports explicitly instead of recovering two of them with
  type assertions. A lineage lookup error now fails closed rather than silently
  selecting the repository default branch.
- `internal/cli/clitest` and `internal/store/storetest` contained only shared
  test support. Both move under their owners' `testdata` trees, so Go continues
  compiling their tests while production topology no longer counts them.

All non-port journal methods made public by the former child package are now
private. `ResolveLocalRepositoryAdmission` remains public because it implements
the Source Control consumer-owned resolver port. The direct-write analyzer now
honors an exact declared receiver without treating every function in the same
mixed package as persistence. A regression proves that undeclared methods on
that receiver fail closed while unrelated package helpers remain outside the
classifier.

Exact package shape falls from `164 / 15 / 149 / 43 / 65` to
`160 / 15 / 145 / 42 / 61`. The retired-root guard prevents all four old paths
from returning. The source-backed direct-write inventory changes from `86 / 95`
to `94 / 112`: two forwarding-constructor rows disappear, while the analyzer
now sees every actual private journal mutation rather than hiding the journal
behind a package-wide declaration. This is stricter observation, not eight new
persistence operations. Live handler allowances remain 16, capability roots
remain 10, and no architecture exception is widened. The implementation is
`9caddc7e5`.

## Wave 9.25 validation

| Check | Result |
|---|---|
| Workspace admission, local journal recovery/fencing, task worktree/lineage, moved CLI/store test support, serve composition, Driver API, automode, and paired FleetDB contract suites | PASS |
| Exact topology, retired-root, Source Control reachability, exact-receiver default-deny, and direct-write ratchets | PASS: shape `160 / 15 / 145 / 42 / 61`; writes `94 / 112`; all four old package roots and all six wrapper constructors absent |
| Measured `make check-architecture` | PASS: 11/11 profiles, 10 capability roots, 16 live legacy-handler allowances, 94 direct-write rows, 107 reviewed mutation commands, 71 runtime components, 80 goroutine launches, all six performance rows measured, zero pending decisions, and 1,159.4 MiB peak process-tree RSS under 2,048 MiB |
| Aggregate `make gate` against FleetDB `9c1859a` | PASS: all Go and frontend quality gates with the exact paired source and binary, `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The initial aggregate attempts caught eight moved-import formatting changes and
a three-line `funlen` overage after explicit Source Control port injection. The
final source formats the imports and extracts task-worker construction; the
linter reports zero issues and the uninterrupted aggregate rerun passes. No
lint exception or topology allowance was added.

## Wave 9.26 result

Wave 9.26 deletes executable compatibility behavior that remained inside the
160-package topology. It does not rename those paths or preserve their policy in
test-only copies:

- Execution now owns DriverRun and TaskRun lifecycle mutation through its typed
  APIs. Driver runtime, webhook ingestion, Workflow HTTP, retry/recovery, and
  await completion callers no longer reconstruct the retired direct-Store
  request/run/event implementations.
- Automation owns internal-event admission, matching, scheduling, trigger
  delivery, and provenance. The parallel trigger-route repository and
  dispatcher plane, old runtime cron/delivery/internal-source implementations,
  and duplicate FleetDB/memstore fanout fixtures are deleted.
- Workflow Catalog owns builtin authoring and refresh. The old native/builtin
  authoring implementation is gone; tests seed only the narrow catalog
  projections they consume instead of copying the deleted production policy.
- Task workflow history now projects canonical DriverRuns directly through
  immutable `SourceRef` lineage rather than joining through the retired
  TriggerDelivery representation or consulting Interaction shadows.
- The issue-journal bridge requires durable cursors plus current Work Items
  lookup, startup-ready reconciliation, and commit-time repository admission.
  Missing ports fail composition; there is no journal-only dispatch or
  in-memory cursor branch. FleetDB's retired `source_repo` journal alias is no
  longer interpreted.
- Runtime discovery no longer accepts a Node executable beside `loom`, and
  repository selection no longer aliases a selector to an unrelated remote
  basename. Explicit overrides, the packaged Desktop runtime, PATH installs,
  exact repository identity, and the Work Items-owned single-repository
  admission rule remain supported contracts rather than migration fallbacks.

The implementation deletes 41 files and changes 217 total files, with 3,008
insertions and 15,647 deletions: a net removal of 12,639 lines. New test support
is consumer-scoped and delegates to the same Execution or Workflow Catalog
contracts as production. Architecture tombstones prevent the deleted runtime
implementations, selector helpers, TriggerRoute surface, and test-only legacy
copies from returning.

Package shape remains exactly `160 / 15 / 145 / 42 / 61`. This is intentional:
the wave removes 12,639 net lines and several live horizontal planes from
packages that still own other durable adapters. Direct persistence remains
`94 / 112`, the live handler allowance remains 16, and all ten capability roots
remain active. No package, handler, topology, or direct-write allowance grows.
The implementation is `cfe542420`; the paired FleetDB contract source is
`9c1859a`.

## Wave 9.26 validation

| Check | Result |
|---|---|
| Full production `internal/...` compile | PASS after deleting the old Driver, Automation runtime, trigger-route, and Workflow Catalog implementations |
| Automation, Execution, serve composition, Workflow Catalog authoring, memstore/FleetDB adapters, WebUI app/handlers/hooks/projections/session coordination, trigger CLI, and full Driver suites | PASS against paired FleetDB `9c1859a` |
| Webhook E2E-tag build | PASS; the deleted router integration fixture is not required to compile the current module boundary |
| Exact retired-surface, topology, package-shape, and default-deny guards | PASS: shape `160 / 15 / 145 / 42 / 61`; writes `94 / 112`; 16 live handler allowances; deleted selectors and compatibility implementations absent |
| Measured `make check-architecture` | PASS under the 2,048 MiB process-tree ceiling with all 11 profiles, ten capability roots, all six performance records measured, and zero pending decisions |
| Aggregate `make gate` | PASS against FleetDB source `9c1859a` and a freshly built paired binary, with `GOMAXPROCS=4`, one Go test worker, two Vitest workers, and a 3 GiB Go soft memory limit |

The gate was run after the final fail-closed journal composition change and the
Node/repository selector deletions. `git diff --check` was clean before the
implementation commit. No generated frontend output, runtime state, or test
report was added to the source tree.

Later waves must continue deleting executable legacy model, projection, and
Store fallback paths. Reaching 160 packages is a progress metric, not the Phase
9 completion criterion.

## Wave 9.27 result

Wave 9.27 removes the remaining WebUI-handler access to the horizontal Store,
backend, and FleetDB planes. Agents, Roles, TaskRun, Driver API, Terminal, and
Workflows now consume exact capability or presentation ports. Application
composition supplies the real adapters; handler tests supply consumer-local
adapters through the same interfaces. There is no handler fallback to the old
persistence path.

Execution now owns the TaskRun and DriverRun queries and mutations used by
serve and the Driver/TaskRun handlers. Work Items owns blocked-state and
repository-required projections. Agents owns Role and Agent records. Workspace
owns workspace identity and path lookup. Terminal's four-method state query is
consumer-owned because it combines those owners with the orchestration-session
store at the composition seam; it does not expose a repository interface to
the handler.

The deleted surfaces include the old Driver task-scheduling implementation and
the prompt-agent create response compatibility layer. The characterization
matrix no longer points at deleted Driver or Automation-runtime tests: Workflow
Catalog directly proves version-scoped approval, while Automation directly
proves authority-derived event identity, hop-depth rejection, and replay after
an Execution owner handoff.

Package shape remains exactly `160 / 15 / 145 / 42 / 61` because the handler,
capability, and composition packages are still real modules. The measurable
ownership surface tightens instead: composite Store files fall from 15 to 14;
production handler legacy imports fall from 16 to 0; direct persistence falls
from `94 / 112` to `90 / 108`; named runtime components fall from 71 to 70;
and in-scope goroutine launch definitions fall from 80 to 79. All ten
capability roots and all 107 reviewed mutation commands remain enforced. The
implementation changes 96 files with 2,390 insertions and 1,674 deletions; the
added code is typed capability/presentation contracts, composition adapters,
and boundary tests, not a renamed compatibility implementation.

## Wave 9.27 validation

| Check | Result |
|---|---|
| Handler legacy-import and composition ratchets | PASS: zero production imports of `internal/store`, `internal/backend`, or `internal/fleet` below `internal/webui/handlers`; composite Store `14 / 14`; outside-composition Store `0 / 0` |
| Capability-owned characterization matrix | PASS: all 6 authoritative rows, including Workflow Catalog approval and Automation admission/replay/hop-cap proofs |
| Exact topology, package-shape, direct-write, runtime, LOC, package-size, and import-fanout guards | PASS: shape `160 / 15 / 145 / 42 / 61`; writes `90 / 108`; runtime `70 / 79`; no exception increased |
| Measured architecture guard in the pre-final aggregate attempt | PASS: 11/11 profiles, ten capability roots, 107 reviewed mutation commands, all six performance records measured, zero pending decisions, and 1,188.7 MiB peak process-tree RSS under 2,048 MiB |
| Aggregate `make gate` | PASS with FleetDB source `9c1859a` and a freshly built binary from that exact checkout pinned explicitly, `GOMAXPROCS=4`, two Go package workers, one Vitest worker, and a 2 GiB Go soft memory limit |

The first aggregate run reached the race suite and correctly rejected the stale
112-site direct-write assertion plus an unrelated generic sibling FleetDB spec.
After lowering the source-backed ratchet to 108 and pinning the paired source,
the next run rejected the older globally installed FleetDB binary. The final
run pinned both paired source and freshly built paired binary and passed every
Go and frontend quality gate. No contract snapshot, architecture exception, or
compatibility path was changed to make the gate pass.

---

[Migration overview](README.md) · [Phase 8 consolidation](15-phase-8-consolidation-and-evidence.md)
