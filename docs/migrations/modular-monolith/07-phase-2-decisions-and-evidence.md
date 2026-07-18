# Phase 2 Workflow Catalog Decisions and Evidence

- **Status:** Complete
- **Date:** 2026-07-16
- **Loom branch/base:** `modular-monolith-phase2` from `7e8a6dd2d76bf9cddd0d8a6610a3d91046fe1433`
- **FleetDB branch/base:** `modular-monolith-phase2` from `8120c788ccc78477a61cfba591fe0445c580ab77`
- **Implementation heads:** Loom `84cccb76123d7531a881988ca0b1e9db49e17677`; FleetDB `430dce8d9fcc9c48bc9d52613b78403b8aae19d4`
- **OpenAPI SHA-256:** `4f50d5e0b98cabea2a904e5713bf13c838fd05e0c91d967e1d7a1b6528c9ca19` for both FleetDB source and Loom vendored snapshot at those companion heads
- **Scope:** Workflow Catalog reads plus approve, unapprove, and activate across core, authority, Redis/Postgres durability, FleetDB API, Loom HTTP, and standalone CLI

## Milestone decision

The working branches establish Workflow Catalog as the first active capability root. The implementation is intentionally one vertical slice: callers enter through the catalog API, mutations require typed operator authority, FleetDB owns each atomic durable transition, and every Fleet-backed Loom adapter for the slice shares the one low-level FleetDB client created by composition.

Phase 2 is complete. The paired source, contract checksum, Redis/Postgres parity, full repository gates, real Loom route/CLI E2E, packaged Desktop UI proof, checkout-scoped deterministic local-mode integration, and target-path measurements satisfy the migration's source-defined pilot gates. `capability-graph.yaml` is ratcheted to `completed_phase: 2` with `workflowcatalog` active, and `migration-baseline.json` retains the immutable Phase 2 validation snapshot.

## Public capability surface

`internal/modules/workflowcatalog` owns the canonical `Driver` and `DriverVersion` models. It exposes the complete Phase 2 `API` plus narrow `EffectiveVersionResolver` and `RequestedVersionResolver` query ports; `API` embeds both resolvers:

`EffectiveVersionResolver` contains only `ResolveEffectiveVersion`, for consumers such as Automation that need the active trusted version. `RequestedVersionResolver` contains only `ResolveRequestedVersion`, for operator preview flows. The complete `API` embeds both and adds the remaining reads and lifecycle commands below.

| Operation | Contract |
|---|---|
| `GetDriver` | Resolve exact durable ID first, then legacy name; always enforce workspace ownership. |
| `ListDrivers` | Return workspace-scoped defensive copies in persistence-defined stable order. |
| `GetVersion` | Return one exact workspace-scoped version. |
| `ListVersions` | Resolve the driver by ID/name and return only versions owned by it, with the driver revision in the same result. |
| `ResolveEffectiveVersion` | System-only pure query. Resolve only the activated version, require passed validation, and report approval/effective trust without accepting a caller-selected version ID. |
| `ResolveRequestedVersion` | Operator-only preview query. Resolve one explicit validated version, including an inactive version, without changing activation or approval state. |
| `ApproveVersion` | Require `OperatorAuthority`, passed validation, exact ownership, and expected revision. |
| `UnapproveVersion` | Require `OperatorAuthority`, exact ownership, and expected revision; passed validation is deliberately not required. |
| `ActivateVersion` | Require `OperatorAuthority`, prior approval, exact ownership, and expected revision. |

The owner-private persistence seams are `Reader` and `VersionLifecycleStore`. They are public to adapters inside the repository, not alternative product entry points. `domain.Driver` and `domain.DriverVersion` are temporary aliases to the capability-owned types; this prevents a second mutable representation while callers migrate.

## Durable lifecycle contract

All three mutations use `workspace_key`, exact `driver_id`, exact `version_id`, and a positive `expected_revision`. Exact IDs reject leading or trailing whitespace; `driver_id` also rejects the colon reserved by the Redis aggregate/index key namespace. Fleet-backed driver revisions begin at one and increment once per fresh transition.

| Command | Durable mutation | Semantic impact |
|---|---|---|
| Approve | Set only `approved_version:<version_id>` to the version source digest. | `workflow_catalog.version_trust_changed.v1` |
| Unapprove | Remove only that approval metadata key. An already-active version remains active but no longer has explicit approval. | `workflow_catalog.version_trust_changed.v1` |
| Activate | Set the exact version as active after durable approval revalidation. | `workflow_catalog.effective_version_changed.v1` |

Redis executes the transition in one Lua operation. Postgres locks the driver row and executes the validation, CAS update, and replay-record insert in one transaction. Both backends preserve unrelated `Driver.Metadata` and use the tuple `workspace_id:driver_id:version_id:expected_revision:action` as the replay identity. The Loom-to-Fleet lifecycle command carries only workspace, driver, version, and expected revision; it has no caller-supplied actor or idempotency fields. FleetDB derives the action from the registered route and constructs the replay tuple at its own trust boundary from the server-controlled path plus the validated body.

A duplicate or lost-response retry returns the original `committed_revision` and `semantic_impact` with `replayed=true`, even if a later valid transition has advanced the current driver. The post-commit Driver in either a fresh or replayed HTTP response is a current durable read and may therefore have a revision greater than `committed_revision`. Workflow Catalog treats that as valid advanced state: it retains the committed operation facts, validates the returned ownership and immutable version identity, and applies action-specific postconditions only when the returned Driver is still at the committed revision. A stale, non-duplicate revision returns a conflict; no path falls back to generic `UpdateDriver`.

FleetDB exposes the dedicated permission `workflow_catalog.version_lifecycle` and the three intent routes:

```text
POST /api/v1/{workspace}/drivers/{driver_id}/versions/{version_id}/approve
POST /api/v1/{workspace}/drivers/{driver_id}/versions/{version_id}/unapprove
POST /api/v1/{workspace}/drivers/{driver_id}/versions/{version_id}/activate
```

The body contains only `expected_revision`; actor and authority are never caller-controlled DTO fields. Stable error codes preserve revision conflict, ownership, not-validated, and not-approved failures across the handwritten Loom transport.

## Authority and entry surfaces

`internal/platform/authority` provides opaque, issuer-sealed, workspace-, action-, class-, and expiry-scoped values. Workflow lifecycle commands accept only `OperatorAuthority`. Execution, session, webhook, zero-value, expired, wrong-action, foreign-issuer, and wrong-workspace authority fail closed, and authority/principal values reject wire serialization.

In open/local mode, `loom serve` creates or reuses a 256-bit token at:

```text
<workspace-runtime-dir>/.loom/operator/operator.token
```

The leaf directory is a real, non-symlink directory no broader than mode `0700`; the token is an exact mode-`0600` regular file containing 64 hexadecimal characters, generated in lowercase by the server. Bearer comparison is constant-time. A valid token produces a one-minute authority bound to the server-derived workspace and one exact operation.

External-auth mode requires both an identity already verified by global JWT middleware and a configured workspace-role resolver. The resolver must authorize that identity as `owner`, `admin`, or `maintainer` for the server-derived workspace before Loom issues the same short-lived operator shape. A resolver error, unknown role, developer/viewer role, missing identity, or mismatched workspace fails closed before FleetDB is called; request headers, paths, and bodies cannot widen the scope.

Phase 2 production enablement is intentionally local/open-mode only. No production workspace-role resolver is wired yet, so an external-auth server defaults the Workflow Catalog slice off, requires no Fleet capability, and registers no catalog routes. Explicitly forcing `LOOM_WORKFLOW_CATALOG_ENABLED=true` without a resolver fails startup with a configuration error. The external resolver contract and denial tests are retained for the later composition slice that supplies a real role source; this phase does not advertise external-auth lifecycle support.

Loom owns these routes:

```text
GET  /api/workspaces/{ws}/workflow-catalog/drivers
GET  /api/workspaces/{ws}/workflows/{name}/versions
POST /api/workspaces/{ws}/workflows/{name}/versions/{versionId}/approve
POST /api/workspaces/{ws}/workflows/{name}/versions/{versionId}/unapprove
POST /api/workspaces/{ws}/workflows/{name}/versions/{versionId}/activate
```

The registered-driver list route is new. The existing static `GET /api/workspaces/{ws}/workflows` builtin-source catalog remains with its current owner. Versions and lifecycle responses preserve the existing UI shape; the Loom HTTP adapter may derive the current revision when an old UI omits `expected_revision`, while the capability command and FleetDB request always receive a positive explicit revision.

The legacy `POST /api/workspaces/{ws}/workflows/{name}/versions` build/register route remains a compatibility surface, but it no longer activates a version. Requests with `activate: true` are rejected and successful creates remain inactive; callers must use the authenticated Workflow Catalog `ActivateVersion` command for that state transition.

The standalone `loom workflow list`, `versions`, `approve`, `unapprove`, and `activate` commands are management-API clients. They require explicit `--server`/`LOOM_SERVER_URL` and `--workspace`/`LOOM_WORKSPACE`, discover auth through `/api/config`, read the local bearer only in open mode, never open a Store, never start `loom serve`, and fail closed when the endpoint or credential is unavailable. Local credential discovery accepts only literal loopback HTTP endpoints with an explicit port, no userinfo/path/query/fragment, and no redirect following; DNS names such as `localhost`, remote hosts, TLS endpoints, and redirect targets are rejected before the durable token is read. Build and run remain on their existing lane; clone, digest, and readiness remain local/file operations.

The packaged Desktop launcher keeps the durable operator credential in the sidecar. Each workspace window receives a 30-second single-use launch code in the URL fragment, exchanges it for a short-lived browser session, and binds that session to the exact workspace. Switching workspaces clears the bearer; a stale recovery route for workspace A is discarded rather than paired with a launch minted for workspace B. A raw loopback browser receives no lifecycle authority, and the remote workspace WebView capability cannot invoke the privileged launcher commands.

### Fleet-first compatibility exception

FleetDB retains one bounded generic Driver `PATCH` lifecycle path while Fleet
deploys before Loom. The request may carry legacy approval metadata and
activation fields in one authenticated operation, so it does not require a
separately committed prior-approval command. It still requires
`workflow_catalog.version_lifecycle`, exact same-driver ownership, passed
validation, and canonical metadata. The Workflow Catalog HTTP and management
CLI lifecycle clients use only the three intent routes above.

| Exception | Owner | Removal issue | Expiry |
|---|---|---|---|
| Authenticated generic Driver `PATCH` approval/activation compatibility | `workflow-catalog-lane` | `MM-2-LEGACY-DRIVER-LIFECYCLE-PATCH` | Phase 5 is the last permitted milestone under the explicit Phase 4 extension below. |

**Phase 4 audit correction and MM-6 extension (recorded after the Phase 4
audit on 2026-07-17):** the Phase 2 wording that “new
Loom never calls this path” was too broad. The new lifecycle HTTP and CLI
clients do not call it, but current native Flue registration still activates
through `internal/driver/approval.go` and `internal/driver/register.go`, with
production entry callers in `internal/workflows/workflows.go` and
`internal/cli/driver/driver_cmd.go`. Re-registering an untrusted bundle also
uses the generic Driver update to demote trust. Removing the Fleet lane in
Phase 4 would therefore break built-in workflow startup and `loom driver
register --activate` rather than merely ending old-version skew.

The exception receives one final migration wave through Phase 5 under
`workflow-catalog-lane`. Phase 5 must move native authoring, activation, and
trust demotion through the Workflow Catalog command/authority surface and the
authenticated management boundary, then remove the lifecycle fields and
approval deltas from FleetDB's generic Driver `PATCH`. Until then, the lane
remains fail-closed behind lifecycle permission, same-driver version
ownership, passed validation, canonical digest/metadata checks, and atomic
approval deltas; Developer authority remains denied. The architecture gate
freezes the current Workflow Catalog-owned `internal/driver` write set at four
rows/four sites and requires both deprecated compatibility markers in the
vendored FleetDB OpenAPI. Marking Phase 5 complete while either side remains
is a hard failure.

The strict “prior durable approval before activation” invariant therefore applies to the new `ActivateVersion` intent route. This compatibility exception is not evidence that generic whole-record mutation remains an allowed module port.

## Composition and version skew

`internal/bootstrap.StoreHandle` retains the one FleetDB client constructed for the Store and exposes it only to composition. `internal/app/serve` wraps that shared client with the narrow catalog adapter, constructs authority/admission, and gives web server composition only a route-registration interface. The capability core never imports the FleetDB client, HTTP, Store, or legacy domain package.

The deployment capability is `workflow_catalog.version_lifecycle.v1`:

- FleetDB defaults `FLEET_WORKFLOW_CATALOG_LIFECYCLE_ENABLED` to true and advertises the key only when the feature is enabled, authentication is enabled with no API-prefix or exact-path skip that can bypass lifecycle authentication, the selected backend implements the full lifecycle store, and all three protected routes register.
- Loom defaults `LOOM_WORKFLOW_CATALOG_ENABLED` to true in local/open mode, derives the required key from that enabled slice, and checks it during Store startup. External auth without a production role resolver defaults the slice off; explicitly forcing it on is a startup error.
- Disabling the Loom slice requires no key and registers no module routes.
- A missing capabilities endpoint, unsupported API revision, or missing key prevents new Loom readiness. There is no generic whole-record mutation fallback.
- FleetDB must land/deploy first because its new routes remain compatible with old Loom while new Loom deliberately rejects an old FleetDB profile.

## Source and product proof

The committed source includes focused tests for the following contracts, and the final gate results below exercise them against the paired FleetDB implementation:

| Layer | Defined coverage |
|---|---|
| Catalog core | Pure queries, ID/name resolution, ownership, defensive copies, lifecycle preconditions, semantic effects, exact-commit versus later-revision response validation, replay behavior, invalid durable results, and default-deny authority/dependency cases. |
| Authority | Every typed class, exact scope/action/expiry, foreign issuer, opaque serialization, 256-bit credential creation/reuse/concurrency, permission and symlink checks, constant-time bearer verification, and wrong workspace. |
| Redis | CAS, metadata preservation, validation/ownership/approval preconditions, legacy revision default, concurrent duplicate replay, and serialization against generic updates. |
| Postgres | The same lifecycle parity against a real database, including replay and concurrent generic update behavior. |
| FleetDB HTTP/auth | Route/config profiles, capability publication including auth skip-path/prefix denial, lifecycle responses that may read a later durable revision, stable error codes, and admin/maintainer allow versus developer/viewer deny. |
| Loom transport/composition | Shared-client retention, exact intent routes, machine-code mapping, enabled/disabled capability requirements, secure local credential composition, external owner/admin/maintainer role resolution, missing-resolver and developer denial, wrong-workspace denial, and missing-dependency failure. |
| Loom HTTP | Read compatibility, approve/activate journey, unapprove-active semantics, legacy create-version activation rejection, stale revision, malformed requests, unauthenticated, non-operator, and wrong-workspace denial. |
| Loom CLI | Explicit endpoint/workspace, no implicit host/Store, unavailable-host and missing-token failure, local bearer, output compatibility, domain exit classes, unauthenticated, and wrong workspace. |

## Slice locality and structural delta

This locality record is measured from the exact committed implementation ranges
Loom `7e8a6dd2d76bf9cddd0d8a6610a3d91046fe1433..84cccb76123d7531a881988ca0b1e9db49e17677`
and FleetDB `8120c788ccc78477a61cfba591fe0445c580ab77..430dce8d9fcc9c48bc9d52613b78403b8aae19d4`.
The count includes shipping, non-generated source and configuration. It excludes
tests and fixtures, docs, architecture tooling/evidence, generated schemas and
the vendored OpenAPI fixture, lockfiles, and test-only scripts/Compose files.

| Slice | Owner and categorized changed production files | Allowed inbound adapters/contracts | Business-rule files outside the owner | Composite Store and direct-write delta |
|---|---|---|---|---|
| Workflow Catalog pilot (Loom) | **Workflow Catalog; 47 files.** Eight owner-root files (five public/core files and three owner-private FleetDB/HTTP adapters); seven typed-authority platform files; nine composition, shared-transport, bootstrap/readiness, and credential-retention files; ten CLI/HTTP/WebUI inbound-adapter files; four legacy alias/compatibility files; and nine frontend/Desktop source or capability/dependency files. | `app/serve` composition; the shared FleetDB client and catalog adapter; bootstrap capability negotiation; Workflow Catalog HTTP; standalone workflow-management CLI; local browser-session exchange; Tauri launcher and Vite workflow UI; bounded legacy domain, driver execution/registration, and old create-version compatibility shims. | **0 files owned by another capability.** Catalog policy is in the five owner core files. One rule-bearing file remains outside the physical module root: `internal/driver/approval.go`, a Workflow Catalog-owned legacy compatibility shim whose Phase 2 diff removes direct approve/unapprove mutations and narrows the remaining activation/trust dependencies. `internal/domain/platform.go` is aliases; `internal/driver/executor.go` and `register.go` are call-site adaptations. All other changed files are declared adapters, composition, platform authority/security, contracts, or UI/native privilege integration. | Store `93 -> 92`; outside-composition Store `82 -> 81`. Type-resolved direct-write inventory stays `233` rows across `258` call sites. The required coupling improvement is the removal of one composite-Store consumer, not a claimed direct-write reduction. |
| Workflow Catalog durable mechanism (FleetDB) | **FleetDB companion; 21 files.** Two command-composition/profile files; five HTTP/auth/OpenAPI files; six model/service/storage-contract files; five Redis lifecycle/compatibility files; and three Postgres lifecycle/migration files. | Three authenticated lifecycle routes; deployment-derived capability manifest; `workflow_catalog.version_lifecycle` permission; source OpenAPI; Redis Lua command; Postgres transaction and migration; bounded old-Loom generic Driver compatibility. | **0 cross-owner product-policy files.** Workflow Catalog remains the product aggregate owner; FleetDB owns durable validation, CAS, replay, transaction, and backend-parity enforcement required by that owner command. Generic Driver changes are the documented Fleet-first compatibility boundary, not a second Loom mutation API. | Loom coupling ratchets are not applicable inside FleetDB. Redis and Postgres implement the same three intent commands and replay tuple; no additional service or module boundary is introduced. |

The 47-file Loom categorization is mutually exclusive:

- owner root (`8`): `internal/modules/workflowcatalog/{api,errors,model,ports,service}.go`, `fleetdb/adapter.go`, and `httpapi/{errors,module}.go`;
- typed authority (`7`): `internal/platform/authority/{admission,authority,browser_session,issuer,opaque,operator_credential}.go` and `httpapi/module.go`;
- composition/transport/readiness (`9`): the three `internal/app/serve/workflow_catalog*.go` files, `internal/bootstrap/{embedded,openstore}.go`, `internal/infra/fleetdb/{capabilities,client,workflow_catalog}.go`, and `internal/cli/issue_backend_workspace.go`;
- inbound adapters (`10`): `internal/cli/root.go`, `internal/cli/local/browser_session.go`, `internal/cli/serve/{serve,serveadapter/workflow_catalog}.go`, `internal/cli/workflow/{management_client,workflow_cmd}.go`, `internal/httpclient/client.go`, and `internal/webui/{app/server_modules.go,handlers/workflows/module.go,server_config.go}`;
- legacy aliases/compatibility (`4`): `internal/domain/platform.go` and `internal/driver/{approval,executor,register}.go`; and
- frontend/Desktop (`9`): `desktop/src-tauri/{Cargo.toml,capabilities/workspace-launcher.json,src/lib.rs}`, `desktop/src/main.ts`, and the five production files changed under `internal/webui/frontend/src` (`api/workflows/{localOperatorSession,workflows}.ts`, `components/WorkflowSourceModal/WorkflowSourceModal.tsx`, `hooks/workflows/useWorkflowSource.ts`, and `main.tsx`).

The 21-file FleetDB categorization is likewise mutually exclusive:

- composition/profile (`2`): `cmd/fleet-db/{main,workflow_catalog}.go`;
- HTTP/auth/OpenAPI (`5`): `internal/api/{errors,platform,workflow_catalog}.go`, `internal/auth/permissions.go`, and `api/openapi.yaml`;
- model/service/storage contracts (`6`): `internal/models/platform.go`, `internal/service/control_plane.go`, and `internal/storage/{errors,keys,platform_types,storage}.go`;
- Redis lifecycle/compatibility (`5`): `internal/storage/{driver_version_lifecycle,platform,redis}.go` and `internal/storage/lua/{driver_version_lifecycle,update_driver}.lua`; and
- Postgres lifecycle/migration (`3`): `internal/storage/postgres/{driver_version_lifecycle,platform}.go` and migration `044_workflow_catalog_version_lifecycle.up.sql`.

The production-file and structural counts are reproducible with:

```sh
git diff --name-only --diff-filter=ACMR 7e8a6dd2..84cccb761 \
  | sort -u | rg '\.go$' \
  | rg -v '(_test\.go$|^internal/archtest/|^internal/infra/fleetdb/testdata/|generated)' \
  | wc -l # 38
git diff --name-only --diff-filter=ACMR 7e8a6dd2..84cccb761 \
  | sort -u | rg '\.(ts|tsx|rs|json|toml)$' \
  | rg -v '(__tests__/|/tests/|\.spec\.ts$|^internal/archtest/|/gen/|Cargo\.lock$)' \
  | wc -l # 9

git -C ../fleet-db-modular-monolith-phase3 diff --name-only --diff-filter=ACMR 8120c788..430dce8d \
  | sort -u | rg '\.go$' | rg -v '(_test\.go$|generated)' | wc -l # 17
git -C ../fleet-db-modular-monolith-phase3 diff --name-only --diff-filter=ACMR 8120c788..430dce8d \
  | sort -u \
  | rg '^(api/openapi\.yaml|internal/storage/lua/.*\.lua|internal/storage/postgres/migrate/migrations/postgres/044_workflow_catalog_version_lifecycle\.up\.sql)$' \
  | wc -l # 4

for revision in 7e8a6dd2 84cccb761; do
  git show "$revision":internal/archtest/testdata/migration-baseline.json \
    | jq '.ratchets.composite_store | [.max_production_files, .max_outside_composition]'
  git show "$revision":internal/archtest/testdata/direct-writes.yaml \
    | awk '/^  - file:/{rows++} /^    count:/{sites += $2} END{print rows, sites}'
done
```

The target-path performance procedure is source-bound to Loom `e96925ae6`
(an ancestor of `84cccb761`) and FleetDB `430dce8d9`. With
`FLEET_DB_REPO` pinned to that companion checkout, the test builds both
binaries, starts a fresh embedded Redis-backed FleetDB and real `loom serve`,
and executes 30 independently seeded authenticated approval commands:

```sh
GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache \
FLEET_DB_REPO=<fleet-db@430dce8d9> \
go test -tags=e2e ./internal/webui/handlers/workflows \
  -run '^TestE2E_WorkflowCatalogPhase2ApprovePerformance$' -count=1 -v
```

The test measures command start through successful management response, uses
nearest-rank percentiles, and places a counter on Loom's FleetDB transport.
Each sample observed exactly six requests: two workspace GETs, two driver GETs,
one version GET, and one lifecycle POST. The 30 raw latency samples and full
environment are retained in `performance-baseline.yaml`; they produce p50
`8.886 ms` and p95 `35.834 ms`.

### Final architecture validation

The following checks passed after the final boundary remediations at Loom implementation head `84cccb761`:

- `GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache go test ./internal/archtest -count=1` — PASS after the architecture-profile file split.
- `GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache go run ./scripts/archcheck check` — PASS with Store `92/92`, outside-composition Store `81/81`, legacy handler imports `91/91`, direct-write rows `233`, capability roots `1`, mutation commands `3`, runtime components `83`, goroutine launch definitions `108`, performance records `6` (`6` measured and `0` deferred), pending decisions `0`, and build profiles `11/11`.
- `GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache ./scripts/check-import-fanout.sh 18` — PASS; `internal/cli/serve` is exactly `18` after moving CLI-to-composition wiring behind the existing `serveadapter` seam.
- `git diff --check` — PASS.
- FleetDB source and Loom vendored OpenAPI snapshots are byte-identical at SHA-256 `4f50d5e0b98cabea2a904e5713bf13c838fd05e0c91d967e1d7a1b6528c9ca19`.

## Completion evidence

The results below are measured or executable proof; no row is inferred only from source shape.

| Evidence | Result |
|---|---|
| FleetDB focused, race, and full gate | **PASS** at `430dce8d9`. The full gate covered Redis and Postgres lifecycle parity, API/auth/config profiles, concurrency/replay, E2E, coverage (`76.9%`), and all package floors. |
| Loom focused and race suites | **PASS.** Workflow Catalog, typed authority, FleetDB adapter, serve composition, HTTP, CLI, bootstrap/readiness, Desktop Rust (`14/14`), frontend Vitest (`16` focused tests), and Playwright (`3/3`) suites passed. |
| Architecture/characterization | **PASS.** The final architecture counts are recorded above; `make test-characterization` passed all `5/5` authoritative rows and the supervisor-disabled contract validator remained green. |
| Loom full gates | **PASS** at final implementation head `84cccb761`. `make check-frontend` passed all six frontend gates. `FLEET_DB_BIN=/tmp/fleet-db-phase2-430dce8d FLEET_DB_REPO=<paired-worktree> GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache make check-go` passed all `16/16` Go gates, the serialized race suite, and `65.5%` coverage against a `60%` threshold. The exact aggregate `make gate` with the same pinned environment then reported Go, frontend, and all quality gates passed. The paired source and binary must remain explicit: an earlier invalid run selected an unrelated sibling spec and stale installed FleetDB binary and was discarded. |
| Contract discipline | **PASS.** The contract guard and generated-code staleness checks passed; companion source and vendored snapshot match at `4f50d5e0…ca19` for Loom `84cccb761` and FleetDB `430dce8d9`. |
| Real route/CLI E2E | **PASS** at final Loom implementation head `84cccb761` against FleetDB `430dce8d9`. `TestE2E_WorkflowCatalogPhase2RealFleetDBLoomHTTPAndCLI` started a fresh real embedded Redis-backed FleetDB plus `loom serve` and exercised list, versions, approve, activate, and unapprove through Loom HTTP and the public management CLI; direct FleetDB access was limited to fixture setup and the final durable-state read. The product-path test itself covers unauthenticated, wrong-workspace, and stale-revision denial; focused CLI suites cover unavailable-host and credential failures. Postgres parity is proven by the companion storage/integration gate rather than mislabelled as the same product-path run. |
| Packaged Desktop UI | **PASS** at product head `e96925ae6`. The actual packaged `Loom Agents.app` sidecars used isolated runtime data; the UI moved `bug-fix-ui-v1` from `PASSED` to `APPROVED` to `ACTIVE`, and the public CLI observed `active=true`, `approved=true`, `effective_trust=trusted`. The packaged `loom-desktop`, `loom`, and `fleet-db` SHA-256 values were respectively `12e1938885b99fcfe24e7251f2f545ccc078eb0a76c6a743a55318b4962fbcfc`, `1db31ee7c29a45bf3515477e6c785c47a8e6f6dec7d40586a137d61cae775823`, and `40650d7f1d39afa956a6b35eb4927b5055cfd6b164682b6dac8b923c6ccc1469`. No Desktop or frontend files changed from `e96925ae6` through `84cccb761`; the later commits changed serve wiring, architecture checks, and performance evidence, and the final-head product E2E above covers that serve-wiring delta. The available computer-use runtime performed this proof; no GPT-5.6 Terra selector was exposed. |
| Browser-session security | **PASS** at product head `e96925ae6`. Playwright passed `3/3` scenarios covering the Desktop fragment exchange journey, denial of lifecycle authority to a raw browser, and clearing workspace-bound authority on workspace switch. These browser-automation results are recorded separately from the packaged-app proof. |
| Performance | **PASS** at product head `e96925ae6`. Thirty authenticated approval samples produced nearest-rank p50 `8.886ms` and p95 `35.834ms`; every sample observed exactly six Loom-to-FleetDB requests (two workspace GETs, two driver GETs, one version GET, one lifecycle POST). `performance-baseline.yaml` records the raw samples and procedure. |
| Deterministic local-mode integration | **PASS.** Checkout-scoped project `loom-mm2-final-local`, run `20260716T054925Z-46850`, and exact tasks `LOCALMODE-2`/`LOCALMODE-3` passed planner/coder artifact checks and were torn down. This proves local composition without external model transmission; it is not relabelled as Codex proof. |
| External model note | **NOT REQUIRED FOR THIS SLICE.** The normative gate requires checkout-bound `make local-mode-verify` plus relevant real entry-surface E2E; it does not require a Codex-backed agent run. No GPT-5.6 Terra selector was exposed, so the packaged UI was tested with the available computer-use runtime without transmitting repository context to an external model. |

The completion update appends a source-bound Phase 2 validation snapshot to `migration-baseline.json`, retains the tightened structural ratchets, and sets `capability-graph.yaml` to `completed_phase: 2`.

The two Phase 1 target-path nulls are already replaced by the measured records in `performance-baseline.yaml`; later phases must not revert them or substitute measurements from a legacy direct-Store path.

## Next phase

Phase 3 starts only after this pilot is complete. Automation will consume only Workflow Catalog's narrow `EffectiveVersionResolver` port, without importing the complete `API`, its adapters, or any catalog mutation surface. The first Automation slice owns TriggerBinding, Event, and Delivery; centralizes actor filtering, hop depth, and idempotency in one admission command; registers cron/webhook delivery components with the runtime host; and retains Workflow Catalog version/trust policy as a separate concern.

---

[Migration overview](README.md) · [Migration plan](03-migration-plan.md) · [Enforcement and gates](04-enforcement-and-gates.md) · [Phase 1 evidence](06-phase-1-decisions-and-evidence.md)
