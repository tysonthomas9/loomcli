# Phase 3 Automation Decisions and Evidence

- **Status:** Architecture, paired gates, non-UI, and local-browser proof complete; Terra export not recorded
- **Loom base:** `e8b73a2a65f4a1c9fb6cd56653335d8e4a1c7bb7`
- **FleetDB base:** `430dce8d9fcc9c48bc9d52613b78403b8aae19d4`
- **OpenAPI SHA-256:** `7b9abcdce6b47b553687f592253c3ed437b9ec03fe823e758738fb0bb4c8ebeb` for the FleetDB source and Loom vendored snapshot
- **Paired FleetDB binary SHA-256:** `f5e91ea7a35665240ba9edbe2ec95d8d601b11704e6d5265367c9099e2a9ae58`
- **Scope:** Automation ownership, trigger admission, webhook ingestion, cron and delivery retry lifecycle, and custom-driver entry lanes

## Locked decisions

### Ownership and public dependencies

`internal/modules/automation` is the canonical owner of TriggerBinding, Event,
Delivery, matching, actor filtering, hop-depth enforcement, idempotency, cron
policy, and delivery retry policy. It imports only Workflow Catalog's narrow
`EffectiveVersionResolver` and calls Execution through an Automation-owned
intent port. It does not receive Workflow Catalog's complete API, a requested
version resolver, the composite Store, or a FleetDB client.

The legacy `driver_version_id` binding field remains on the compatibility wire,
but admission resolves and snapshots only the Catalog's currently activated,
validated version. A caller-supplied or persisted inactive version cannot select
an automated run target.

### One admission command

Webhook, system, and execution-originated events enter one `AdmitEvent` use
case. The authority class determines origin; request bodies cannot supply
origin, hop depth, signature status, workspace authority, execution identity,
or trust. Actor kind is derived from server-stamped provenance. In particular,
the fixed `driver-run:` and `task-run:` actors emitted through the trusted
system lane are classified as workflow actors, so a system origin cannot bypass
a binding's workflow-actor exclusion. Exclusion wins over an exact actor
allow-list.

FleetDB atomically reserves the Event and every matched Delivery using one
workspace-scoped idempotency key and immutable request fingerprint. Automation
probes that durable reservation before consulting any live binding or Catalog
state. Exact replay therefore returns the original match set even if bindings
or active versions later change or disappear; reuse with different immutable
content is a conflict. Automation then
dispatches each accepted Delivery through an idempotent Execution port and
records the result on that Delivery. The Fleet implementation of that intent
loads the reserved Delivery snapshot, applies subject concurrency, and creates
or replays the DriverRun atomically; it does not rematch a route or trust the
legacy binding version pin. The durable Delivery state is the recovery point
for a lost response or process restart.

For workflow-owned admission, the current Execution owner tuple
(`node_id`, `lease_owner`, and `fencing_token`) is authorization evidence, not
immutable event content. A fresh reservation validates that tuple atomically
against the running DriverRun before it writes the Event or any Delivery. The
tuple is deliberately excluded from the idempotency fingerprint, so the exact
same request can be replayed by a legitimate successor owner after handoff;
changing the run identity or event semantics remains a conflict. A stale owner
using a new idempotency key fails before any durable write.

Admission and claim HTTP DTOs carry payload bytes through an explicit base64
field. A nested JSON value is insufficient because normal JSON marshaling can
compact whitespace and change the byte sequence used by the stored digest.
First response, lost-response replay, retry claim, and dispatch therefore
round-trip the exact original bytes.

Workflow-originated admission re-derives its emitting run and parent event from
`ExecutionAuthority`. Missing parents fail closed, and hop `cap + 1` records no
run. There is no in-memory provenance ledger and no global event bus.

### Webhook and deletion workflows

Inbound webhook transport calls the named `app/webhookingestion` workflow. The
workflow verifies through a connector-owned compatibility verifier that does
not return plaintext credentials, derives `WebhookAuthority`, and calls
Automation admission.

Binding retirement disables admission first. Grant revocation and final delete
are idempotent; an interrupted request can resume from the durable disabled
binding. Automation rejects direct deletion of an enabled binding.

### Runtime lifecycle

`internal/platform/runtime` owns worker launch, cadence, jitter, timeout,
failure backoff, cancellation, isolation, and health. Automation contributes
two non-overlapping components, while Execution's durable outcome and generic
await-notification reconcilers are required base-platform registrations
independent of the Workflow Catalog and Automation feature flags. The stable
audit IDs are:

- `serve-trigger-cron-scheduler`
- `serve-trigger-delivery-sweeper`
- `serve-driver-run-outcomes`
- `serve-await-event-notifications`

The compatibility policies keep the shipped 30-second cron and 15-second
delivery cadences, immediate first passes, zero jitter, lifecycle-only timeout,
and no host-level failure backoff. Delivery's durable record-level retry remains
Automation policy, not runtime-host backoff.

Cron occurrence claim and completion are durable Fleet operations. The runtime
host may repeat or overlap a pass after failure, but the occurrence identity and
claim state survive process restart; the removed in-memory scheduler window is
not accepted as Phase 3 evidence.

Every terminal DriverRun transition atomically records an immutable outcome
snapshot in a Fleet-owned outbox. The outcome reconciler claims due snapshots,
first appends a deterministic base-Execution `run.finished` TriggerEvent, then
notifies generic awaits, and only then optionally publishes the outcome through
Automation before completing the outbox row. The base journal and direct await
notification do not depend on the Automation feature flag. Actor-rejected
generic waiters are successful no-ops and remain pending for another event or
timeout. Failed journal append, await notification, or publication is persisted
with bounded backoff, expired claims are restart-recoverable, and the stable
run/status event identity makes a publish-before-complete crash safe for
at-least-once replay. The run-finished payload is deterministically bounded
before admission, so untrusted summary or error text cannot make the Execution
lifecycle event await-ineligible. Raw system-event emission is not exposed to
web handlers or runtime consumers, so those callers cannot choose another
producer's component identity.

Generic event matching uses the same convergence rule. FleetDB exposes one
service-only `resolve-and-resume` operation that resolves an eligible await and
resumes its suspended DriverRun in one storage transaction or Redis script.
There is no split resolve-then-resume fallback. Pre-commit failure leaves both
objects unchanged; an exact retry after commit or a lost response observes the
already-converged state. A delayed replay from an older await cycle cannot
overwrite the pending-resume marker of a newer cycle. Timeout resolution,
actor filtering, pending-event registration, and the run-outcome compatibility
alias share this atomic path on both Redis and Postgres.

Each accepted generic TriggerEvent atomically creates a leased Fleet-owned
await-notification envelope. The base runtime claims that envelope and invokes
the same atomic resolver, which closes the event-commit/notification crash
window without relying on an in-process callback. Redis registration catch-up
reads the same durable TriggerEvent `payload` field that Postgres and live
dispatch use. An oversized generic event remains durable for audit but is
explicitly await-ineligible: its notification reports the canonical identity,
size, and oversized flag while omitting the body; the reconciler records an
audited successful no-op instead of backing off the entire component, and
catch-up continues scanning so a later eligible event can win. Await payloads
are never silently truncated. Approval payloads are checked against the same
limit before journal append, while the bounded `run.finished` encoding keeps
the base Execution lifecycle lane eligible. Blank actor predicates and
non-canonical event identities are rejected before persistence. The legacy
split resolve/system/resume routes remain only for rolling compatibility and
require an API-key or Unix-socket service identity. Resume additionally proves
the terminal await belongs to the run, names the exact winning event, and
advances a still-running marker only to a higher await ordinal.

Composition awaits for `run.finished` require the complete trusted lifecycle
tuple: system origin, exact `system` actor, canonical `run-finished:*` source
identity, and either the base `execution` source kind or Automation's optional
`internal` copy. The same policy runs before Automation reservation, during
live and outbox dispatch, and in the in-memory, Redis, and Postgres
registration catch-up scans. External and workflow origins cannot occupy the
`system` or `system:*` actor namespace; old rows backfilled during an upgrade
are audited and skipped without poisoning the notification queue, so a later
genuine outcome can still win.

The session-authenticated approval endpoint adds a transport-specific
exact-allowlist for the `approval` event type and rejects reserved session
identities before journal append. Signed webhook input is still normalized by
its adapter, but the shared Automation policy rejects any forged lifecycle
type or actor after verification and before durable admission. Neither a
browser session nor a valid webhook signer can pre-seed a future
child-completion await or resume an already-suspended parent by impersonating
the internal lifecycle lane.

## Compatibility and rollout

FleetDB publishes each capability only when the selected backend, authenticated
route profile, and complete operation set are usable. Generic await convergence
is an always-on Execution dependency, so every new `loom serve` requires
`execution.await_atomic_resume.v1`, including deployments with Workflow Catalog
and Automation disabled. Feature activation adds keys rather than replacing
that base requirement:

| Loom serve profile | Required FleetDB capability keys |
|---|---|
| Base Execution | `execution.await_atomic_resume.v1` |
| Workflow Catalog enabled | Base plus `workflow_catalog.version_lifecycle.v1` |
| Automation enabled | Base plus `automation.trigger_admission.v1` |

The Execution key certifies both the generic atomic route and the run-outcome
compatibility alias with Redis/Postgres parity. The Automation key certifies
atomic reservation and transition operations. New Loom fails readiness against
an older or partially configured FleetDB and never falls back to either the
split await mutation sequence or the legacy trigger-route mutation path.
FleetDB deploys first and remains compatible with old Loom.

Existing trigger-binding, webhook, event, delivery, manual-run, and CLI response
shapes remain stable.

Resumable SSE is not implemented, validated, or claimed by Phase 3. Existing SSE behavior remains unchanged. The work is assigned to a separately reviewed behavior-changing follow-up branch/PR that must define reconnect and replay semantics, persistence and offset ownership, version skew, rollback, and end-to-end proof. Partial SSE plumbing does not count toward Phase 3 completion.

## Architecture activation and legacy disposition

The checked-in capability graph marks both `workflowcatalog` and `automation`
active and advances the architecture ratchet to `completed_phase: 3`. The
mutation ledger contains 17 exact entries: the three retained Workflow Catalog
lifecycle commands and all 14 public Automation mutations covering ordinary
and AgentService-managed binding lifecycle, event admission, manual dispatch,
cron sweeping, and delivery retry. The checked-in contract test validates this
phase/status/count independently of the direct-write inventory; the refreshed
repository-wide coupling counts are recorded below.

`internal/workflows` cannot truthfully expire in Phase 3. It remains a mixed
legacy package with embedded builtin source/digest/materialization and
registration behavior that belongs with Workflow Catalog authoring, plus
trusted global-runner resolution that belongs with the Phase 4 Execution seam.
Phase 3 deliberately delivered custom-driver and trigger lanes through module
APIs without folding that directory rewrite into the Automation behavior
slice. Nine production Go files and seven test files still import the path.

The graph therefore records an explicit reviewed extension rather than deleting
or silently moving the row:

| Field | Decision |
|---|---|
| Owner | `workflow-distribution-lane` |
| Removal issue | `MM-LEGACY-WORKFLOWS` |
| Replacement roots | `internal/modules/workflowcatalog`, `internal/modules/execution` |
| Last permitted milestone | Phase 5 |
| Machine evidence | Exact sorted 16-file `remaining_call_sites` list in `capability-graph.yaml` |

`TestLegacyWorkflowsExtensionMatchesCurrentCallers` scans every checked-in Go
file outside the legacy path and requires the observed imports to equal that
list. The extension is bounded to the two subsequent migration waves allowed
by MM-6; completing Phase 5 with the row still present remains a graph-gate
failure.

## Slice locality and structural delta

The locality snapshot is measured from Loom base
`e8b73a2a65f4a1c9fb6cd56653335d8e4a1c7bb7` plus the Phase 3 pre-commit diff.
It takes the union of tracked changes and non-ignored untracked files,
then excludes tests, architecture tooling, generated OpenAPI fixtures, and
generated workflow bundles. The result is 98 changed non-generated runtime
production Go files. Primary attribution is mutually exclusive: 71 belong to
the Automation extraction and its entry/composition surfaces, and 27 belong to
the required Execution await/outcome companion. This is intentionally a
self-reference-free pre-commit measurement identified by the base plus diff;
final local commit IDs are reported in the handoff rather than embedded here.

| Slice | Owner and changed production files | Allowed inbound adapters/contracts | Business-rule files outside the owner | Composite Store and direct-write delta |
|---|---|---|---|---|
| Automation extraction | Automation; 71 files, including 14 production files under `internal/modules/automation` | Named `app/webhookingestion`, `app/workfloweventing`, `app/systemeventing`, and `app/workflowbinding` workflows; `app/serve` composition; trigger CLI management client; WebUI agents/approvals/driver-api/binding/webhook adapters; Automation FleetDB adapter; authority, runtime-host, legacy-domain aliases, and bounded compatibility shims | **0 cross-capability business-rule files.** Matching, admission, actor/hop/idempotency, binding lifecycle, cron, and delivery retry policy live under the Automation owner. Named workflows own orchestration only; other changed paths are declared adapters, contracts, platform lifecycle, or compatibility shims. | Store `92 -> 89`, outside-composition Store `81 -> 78`. The slice removes 12 direct-write rows and 14 call sites from the standalone trigger CLI and HTTP adapters: `233/258 -> 221/244` rows/sites. |
| Execution await/outcome companion | Execution; 27 files across existing Execution roots and their app/store/FleetDB/memstore adapters | Base-runtime registrations in `app/serve`; `internal/driver` await/outcome policy and reconcilers; `internal/store` contracts; FleetDB and memstore adapters; tracing wrapper; the compatibility await matcher | **0 cross-capability business-rule files.** Nine rule-bearing files remain in Execution's existing `internal/driver` and `internal/trigger` roots rather than prematurely activating `internal/modules/execution`; their physical extraction is Phase 4 scope. The other 18 files are composition, contracts, or persistence adapters. | Store `89 -> 88`, outside-composition Store `78 -> 77`. Five explicitly classified tracing-adapter rows/sites are added for atomic resolve/resume and durable outcome operations: `221/244 -> 226/249` rows/sites. |
| Phase 3 repository result | 98 files | Both declared slices above | **0 cross-capability business-rule files** | Store `92 -> 88`, outside composition `81 -> 77`; direct writes `233/258 -> 226/249`, a net reduction of 7 rows and 9 call sites. |

The 27-file Execution companion set contains nine rule-bearing files under
`internal/driver` and `internal/trigger`; its other 18 files are composition,
domain/store contracts, tracing, or FleetDB/memstore adapters. The remaining
71 files are the primary Automation set. Shared composition files are assigned
to the slice whose policy change they principally host, so no file is counted
twice. The Store sequence and direct-write sequence are attribution steps, not
claims that an intermediate commit exists.

The metric commands are:

```sh
BASE=e8b73a2a65f4a1c9fb6cd56653335d8e4a1c7bb7
{ git diff --name-only --diff-filter=ACMR "$BASE"; git ls-files --others --exclude-standard; } \
  | sort -u \
  | rg '\.go$' \
  | rg -v '(_test\.go$|^internal/archtest/|^internal/infra/fleetdb/testdata/|^internal/workflows/builtin-dist/|generated)' \
  | wc -l

git show "$BASE":internal/archtest/testdata/migration-baseline.json \
  | jq '.ratchets.composite_store | [.max_production_files, .max_outside_composition]'
jq '.ratchets.composite_store | [.max_production_files, .max_outside_composition]' \
  internal/archtest/testdata/migration-baseline.json

git show "$BASE":internal/archtest/testdata/direct-writes.yaml \
  | awk '/^  - file:/{rows++} /^    count:/{sites += $2} END{print rows, sites}'
awk '/^  - file:/{rows++} /^    count:/{sites += $2} END{print rows, sites}' \
  internal/archtest/testdata/direct-writes.yaml
```

## Public-path E2E and measured performance

The post-hardening real-process public acceptance test passed in `42.47s` from
the recorded Phase 3 pre-commit source, including its paired FleetDB build:

```sh
FLEET_DB_REPO=/Users/tyson/codebase/code-agents/rc-2/fleet-db-modular-monolith-phase3 \
GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache \
go test -tags=e2e ./internal/webui/handlers/webhooks \
  -run '^TestE2E_AutomationPhase3RealFleetDBLoomHTTPAndCLI$' -count=1 -v
```

It starts fresh real FleetDB and `loom serve` processes. Direct FleetDB access
is limited to workspace plus validated/approved/active driver-version fixture
setup and final durability reads. The product path uses the public standalone
CLI to create, list, show, update, manually run, and delete a binding; uses a
signed webhook to create the Event, Delivery, and DriverRun; reads Event and
Delivery audit records through the CLI; proves exact webhook redelivery
replays the original run; and proves an anonymous direct HTTP create is
denied. `TestE2E_GitHubWebhookDispatchesDriverRunWithEphemeralStack` also
passed in `8.41s`, retaining the existing deterministic webhook regression
alongside the broader Phase 3 journey.

The pinned-count performance test passed with 30 fresh signed webhook
deliveries:

```sh
FLEET_DB_REPO=/Users/tyson/codebase/code-agents/rc-2/fleet-db-modular-monolith-phase3 \
GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache \
go test -tags=e2e ./internal/webui/handlers/webhooks \
  -run '^TestE2E_AutomationPhase3WebhookPerformance$' -count=1 -v
```

The test starts a fresh embedded Redis-backed FleetDB and real `loom serve`,
seeds and activates one fixed driver version, creates one binding through the
public authenticated CLI, and places a counting reverse proxy only on Loom's
FleetDB transport. For each unique delivery ID it snapshots the counter,
times the complete signed webhook request through the accepted Loom response,
requires a returned run ID, and requires exactly 12 observed Loom-to-FleetDB
requests. Direct fixture writes and post-run assertions are outside each
sample. Aggregates use nearest-rank percentiles.

| Measurement | Result |
|---|---|
| Environment | macOS 15.6.1 (24G90), arm64 Apple M4 Pro (12 cores), 24 GB memory, Go 1.26.0, Node 24.13.1; local real processes, no external model |
| Source identity | Loom base `e8b73a2a65f4` plus the measured Phase 3 pre-commit diff; FleetDB base `430dce8d9fcc` plus the measured companion pre-commit diff. Final local commit IDs are reported in the handoff rather than embedded in this self-reference-free snapshot. |
| Samples | `n=30` fresh unique signed webhook deliveries; the post-hardening named test passed in `20.62s` and retains the measured vector in its command output |
| Nearest-rank latency | p50 `6.314ms`; p95 `19.023ms`; both pass the test's `100ms`/`250ms` budgets |
| FleetDB round trips | Exactly `12` for every sample; this is asserted by a pinned constant, not learned from the first sample or inferred from call sites |

The first observed request trace, retained in order by the test, was:

1. `GET /api/v1/admin/workspaces/GITHUBE2E`
2. `GET /api/v1/admin/workspaces/GITHUBE2E`
3. `GET /api/v1/GITHUBE2E/trigger-bindings`
4. `GET /api/v1/GITHUBE2E/connectors`
5. `GET /api/v1/GITHUBE2E/trigger-bindings/binding-github-pr-opened/webhook-secret`
6. `POST /api/v1/GITHUBE2E/automation/admissions/external/github.pull_request.opened`
7. `GET /api/v1/GITHUBE2E/automation/binding-matches/github.pull_request.opened`
8. `GET /api/v1/GITHUBE2E/drivers/github-pr-review`
9. `GET /api/v1/GITHUBE2E/driver-versions/github-pr-review-v1`
10. `POST /api/v1/GITHUBE2E/automation/admissions/external/github.pull_request.opened`
11. `POST /api/v1/GITHUBE2E/automation/deliveries/automation-delivery-1605122ccccc3394f04d0102ae90be7d/dispatch`
12. `GET /api/v1/GITHUBE2E/awaits`

The counter records method and path; it does not manufacture status metadata.
The test separately requires the public webhook response to be accepted and a
non-empty run ID for every sample.

## Local browser proof and trust-parity correction

The checkout-scoped local UI completed the post-fix product journey. `POST
/api/workspaces/LOCALMODE/operator-sessions/exchange` performed the one-time
local-operator exchange and returned `200`; the browser then created and
activated binding `s1-bug-fix`. The UI `POST
/api/workspaces/LOCALMODE/trigger-bindings/s1-bug-fix/run` returned `202`, and
`GET /api/workspaces/LOCALMODE/trigger-bindings/s1-bug-fix/runs` returned
`200`. DriverRun
`automation-run-107e83e1e7a899fe76a407177cc8a33e` completed on immutable version
`bug-fix-agent-v-b14ca933dc57` with summary `bug-fix: no ready bug to claim`.
The final browser console and error inspection was empty. The retained local
screenshots are:

- `/tmp/loom-mm-phase3-ui/06-agent-history-after-trust-fix.png`
- `/tmp/loom-mm-phase3-ui/07-final-run-now-completed.png`

The first browser attempt had exposed a real storage-policy mismatch: Loom's
Catalog correctly treated a trusted version manifest as trusted without an
explicit approval marker, while Automation's atomic admission and direct-
dispatch guards required the marker unconditionally. The correction makes
Redis and Postgres revalidate the same canonical effective-trust precedence:
a valid per-version approval, then explicit version-manifest trust, then the
legacy Driver trust fallback. An explicit untrusted manifest overrides the
legacy trusted Driver row, while a stale approval cannot override that
manifest.

`TestRedisAutomationCatalogEffectiveTrustParity` and
`TestPostgresAutomationCatalogEffectiveTrustParity` cover both admission and
direct dispatch across trusted manifest, explicit untrusted manifest, valid or
stale approval, and legacy-driver fallback. Redis also fails closed when the
Driver metadata JSON contains a numeric or object-valued approval injected
immediately before the atomic script;
`TestRedisAutomationCatalogMalformedApprovalCannotAuthorizeAtomicScripts`
proves malformed approval data cannot authorize admission or manual dispatch.

GPT-5.6 Terra review is not recorded. Sending either local screenshot to that
external model is an external data export and awaits explicit informed
approval. This is an evidence-policy boundary, not a Loom product failure.

## Supervisor-disabled matrix disposition

Phase 3 does not claim supervisor-disabled parity. The executable manifest
still contains only the intentionally RED `deterministic-plan-coder` row. The
custom-driver and cron/webhook rows anticipated by the migration plan are
explicitly deferred to the Phase 4 Execution/supervisor-replacement lane under
`execution-reliability-lane`: both need the deterministic TS execution leaf,
ordered public-API seeding, and daemon-free local-mode path before they can
assert zero daemon processes and zero control sockets. Phase 3's runtime-host
tests, public webhook E2E, and completed checkout-scoped local-mode proof do not
substitute for those missing matrix rows. The rows must be added and made green
before supervisor deletion; the full matrix remains the Phase 6 gate.

## Completion evidence

The machine validation snapshot records the exact Phase 3 base SHAs and states
that its measured source is the pre-commit diff. It does not invent
self-referential final commit identities; final local commit IDs belong in the
handoff. Every required paired gate below is complete. Only the
approval-dependent external Terra export remains not recorded.

| Proof | Result | Evidence |
|---|---|---|
| Architecture profiles and ratchets | **PASS** | `go run ./scripts/archcheck check` enforces 11/11 build profiles plus the all-files AST pass; Store `88/88`, outside composition `77/77`, handler imports `91/91` (`42 + 49`), direct writes `226` rows/`249` sites, module roots `2`, mutations `17`, runtime components `85`, ticker sites `56`, managed components `58`, goroutine launches `107`, performance records `6/6` measured, and pending decisions `0`. |
| Automation authority, actor/hop/replay, Catalog selection, and managed/unmanaged ABA-CAS suites | **PASS** | Focused Automation, named-workflow, composition, adapter, and WebUI boundary tests pass; wrong authority, forged provenance, replay conflict, stale revision, and owner-collision cases fail closed. |
| Binding retirement and recoverable process-manager faults | **PASS** | `TestManagedBindingDeleteResumesAfterEveryDurableStepFailure`, `TestManagedAgentDeleteResumesAfterParkOrArchiveFailure`, and `TestDeleteDisablesRevokesThenDeletesAndCanResume` cover interruption after each durable step. |
| Redis/Postgres admission, dispatch, cron, outcome, effective trust, and atomic await/resume parity | **PASS post-correction** | The final FleetDB gate exercised both backends, API contracts, real-container E2E, harness, race, and coverage. It includes the UI-discovered effective-trust parity correction for admission and direct dispatch plus Redis malformed-approval fail-closed hardening. |
| FleetDB capability profiles and Loom readiness negotiation | **PASS** | Route/config/auth profiles advertise only usable `workflow_catalog.version_lifecycle.v1`, `automation.trigger_admission.v1`, and `execution.await_atomic_resume.v1`; Loom focused tests require Execution for every serve profile, add Catalog/Automation by feature slice, and fail closed on missing or old backends. |
| Durable outcome and generic await convergence | **PASS** | Outcome and await reconcilers cover journal-before-notify ordering, response-loss replay, restart recovery, actor-rejected no-op, oversized quarantine, late registration, and old-cycle ABA protection. Redis/Postgres use the same atomic resolve-and-resume contract; no split fallback is accepted. |
| Runtime-host ownership and isolation | **PASS** | Runtime-host tests prove inert construction, non-overlap, cadence/jitter, timeout/backoff ownership, panic isolation, bounded stop, and concurrent health snapshots. Automation contributes exactly two components; the outcome and await-notification components are always-on Execution registrations. |
| Standalone trigger CLI is HTTP-only | **PASS** | Management-client tests cover explicit endpoint/workspace discovery, prohibition on implicit host startup, unavailable-host failure, local bearer auth, JSON/text compatibility, and domain exit classes. Source and architecture checks reject a direct Store/FleetDB mutation fallback. |
| HTTP webhook, binding-management, manual-run, and CLI E2E | **PASS post-hardening** | In one escalated run, `TestE2E_AutomationPhase3RealFleetDBLoomHTTPAndCLI` passed in `42.47s` including the paired FleetDB build; `TestE2E_GitHubWebhookDispatchesDriverRunWithEphemeralStack` passed in `8.41s`; and the `n=30` performance E2E passed in `20.62s` with p50 `6.314ms`, p95 `19.023ms`, and exactly 12 FleetDB round trips per sample. |
| Checkout-scoped deterministic local mode | **PASS** | Project `loomcli-mm-phase3` used the exact Loom, FleetDB, and pinned Flue checkouts on ports 8580/8582/8583. The generic planner/coder/session/transcript/diff verifier and signed-webhook verifier passed; webhook redelivery was idempotent and its secret remained redacted. |
| Authenticated local-browser manual-run journey | **PASS** | One-time operator exchange `POST` returned `200`; UI Run now for `s1-bug-fix` returned `202`; run-history `GET` returned `200`. Run `automation-run-107e83e1e7a899fe76a407177cc8a33e` completed on `bug-fix-agent-v-b14ca933dc57` with summary `bug-fix: no ready bug to claim`; final console/error inspection was empty. Screenshots `/tmp/loom-mm-phase3-ui/06-agent-history-after-trust-fix.png` and `/tmp/loom-mm-phase3-ui/07-final-run-now-completed.png` retain the visible proof. |
| GPT-5.6 Terra screenshot review | **NOT RECORDED — not claimed** | Exporting the local screenshots to an external model awaits explicit informed approval. This is an evidence-policy boundary, not a product failure, and it does not negate the completed local-browser product proof. |
| FleetDB `make gate` | **PASS post-correction** | Static/lint, unit/race, Redis/Postgres integration/API/contracts, real-container E2E, harness, and the final quality gate passed at 78.0% coverage; the gate ended with `Quality Gate PASSED`. |
| Loom `make gate` against the exact final FleetDB companion binary | **PASS post-correction** | The aggregate Go and frontend gates passed with `FLEET_DB_BIN=/tmp/fleet-db-modular-monolith-phase3`, the matching companion worktree, and a clean HOME/runtime environment. The paired binary SHA-256 is `f5e91ea7a35665240ba9edbe2ec95d8d601b11704e6d5265367c9099e2a9ae58`. |
| OpenAPI source/vendored checksum match | **PASS** | Both files are byte-identical at SHA-256 `7b9abcdce6b47b553687f592253c3ed437b9ec03fe823e758738fb0bb4c8ebeb`. |
