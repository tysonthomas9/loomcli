# Modular Monolith Migration

- **Status:** Phase 9 package consolidation complete through Wave 9.41; the
  modular monolith is ratcheted to 158 production packages with no
  `internal/backend` compatibility tree
- **Date:** 2026-08-12
- **Scope:** `loom serve`, the operator CLI entry surfaces, the Vite frontend counterpart, and the fleet-db contracts those capabilities depend on
- **Provenance:** [Phase 0 integration baseline](00-phase-0-baseline.md), final [Phase 1 evidence](06-phase-1-decisions-and-evidence.md) at Loom `7e8a6dd2`, [Phase 2 evidence](07-phase-2-decisions-and-evidence.md) at Loom `84cccb761` with FleetDB `430dce8d9`, [Phase 3 evidence](08-phase-3-decisions-and-evidence.md) at core implementation commits Loom `7f95b9bf1` and FleetDB `f1c4e1119`, final [Phase 4 evidence](09-phase-4-decisions-and-evidence.md) at Loom `53cbe2577` with FleetDB `afb688768`, and the appended reliability-validation record at Loom `67c45972f` with FleetDB `9ffa69f60`
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

Package count is not a substitute for ownership or public-API compliance. The
pre-guardrail source snapshot had 165 Go packages, while the Phase 7 completion
tree had 250 production package directories and 115 one-file packages. Phase 8
adds shrink-only shape ratchets and removes duplicated knowledge and
forwarding-only packages while preserving the capability graph. The completed
tree has 189 production packages and 67 one-file packages. Phase 9 continues
from that exact shape toward 160 packages by deleting residual horizontal
models, repositories, and shallow composition seams without merging capability
owners.

Through Wave 9.41, Phase 9 has retired the residual `internal/types` plane,
duplicate Connectors and Artifacts repository/model layers, forwarding-only
owner adapters, runtime and authentication compatibility paths, horizontal
handler dependencies, three shallow vocabulary packages, and the ambient
runtime-context package. It also folds native-transcript dispatch into Sessions,
deletes the forwarding-only Codex parser package, and rejects unknown transcript
backends instead of falling back to Claude. Wave 9.15 deletes the remaining
shallow Claude and OpenCode parser packages and their unused public helpers.
Wave 9.16 deletes the forwarding-only local-session store adapter, replaces its
persistence-shaped calls with Sessions-owned archive intents, and folds the
nested event-store package into that implementation. Subsequent waves remove
more runtime compatibility, handler persistence access, and shallow
composition. Wave 9.40 moves the remaining lifecycle, detail, list, claim,
comment, event, and dependency behavior behind Work Items-owned ports, then
deletes the horizontal `IssueBackend`, its duplicate projections and errors,
and its WebUI and driver proxy layers. At the Wave 9.40 checkpoint the exact
shape was 158 production packages: 15 under `internal/modules`, 143 outside
module roots, 42 one-file packages, and 60 one-or-two-file packages. The
external `issue_backend` JSON and
configuration key remains a stable operator contract; it no longer names an
internal runtime interface or compatibility facade. Wave 9.41 then removes the
empty legacy location itself: concrete FleetDB and Loom HTTP adapters now live
under `internal/modules/workitems/{fleetdb,httpapi}`, while reusable HTTP and
generated-wire mechanisms live under `internal/platform`. Total topology stays
at 158 packages, with 17 under capability modules, 141 outside module roots, 42
one-file packages, and 60 one-or-two-file packages.

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
| [10-local-open-mode-authority-revision.md](10-local-open-mode-authority-revision.md) | How does local open mode work without operator credential ceremony while OIDC remains authoritative for shared deployments? |
| [11-phase-5-decisions-and-evidence.md](11-phase-5-decisions-and-evidence.md) | Which Phase 5 seams, ownership invariants, gates, and exact-head acceptance proofs closed the phase? |
| [12-phase-5-real-codex-proof.md](12-phase-5-real-codex-proof.md) | How did the packaged-Desktop real-Codex matrix close with 20 local passes and four operator waivers? |
| [13-phase-6-decisions-and-evidence.md](13-phase-6-decisions-and-evidence.md) | Which supervisor paths were deleted, what replaced them, and which exact gates prove Phase 6? |
| [14-phase-7-decisions-and-evidence.md](14-phase-7-decisions-and-evidence.md) | Which remaining capability and frontend boundaries closed, and what exact packaged-product matrix completes the migration? |
| [15-phase-8-consolidation-and-evidence.md](15-phase-8-consolidation-and-evidence.md) | How is post-extraction fragmentation removed without weakening ports-and-adapters boundaries? |
| [16-phase-9-package-consolidation.md](16-phase-9-package-consolidation.md) | Which residual horizontal planes and shallow packages will be retired next, and what has the first slice proved? |
| [17-phase-9-direct-write-and-outside-package-catalog.md](17-phase-9-direct-write-and-outside-package-catalog.md) | Which direct persistence calls remain, and why does each production package outside `internal/modules/` still exist? |

## Scope boundaries

“Modular monolith” applies to the capability core hosted by `loom serve`. The approved target requires one product-mutation topology: standalone operator CLI commands become management-API clients rather than opening the composite Store directly. Under MM-7, those commands discover an explicitly configured endpoint, never start a host implicitly, fail closed when it is unavailable, use the server's configured open or OIDC trust mode, and migrate command family by command family while preserving output and exit-code compatibility. Local bootstrap/file-only commands remain platform tools outside product aggregate ownership.

| Artifact | Migration decision |
|---|---|
| fleet-db | Remains a separate service and owner of atomic durable transitions |
| Vite web UI | Remains one application; gains vertical feature modules |
| Desktop launcher | Remains a thin Tauri lifecycle/native adapter |
| `@loom/sdk` | Remains one published package with its current coarse exports |
| TypeScript workflow drivers | Remain the extension boundary; no Go plugin system |
| Coding backend processes | Remain execution processes behind module-owned runtime ports |

The migration does **not** introduce microservices, multiple `go.mod` files, feature npm packages, microfrontends, or a generic in-process event bus.

## Phase 0 through current Phase 7 status

The required base integration is complete and its validated code heads are pushed. The Phase 1 branch starts from Loom `122d4d79`, where `origin/v5` at `95e97289` is an ancestor; companion FleetDB is `8120c788`. The original divergence counts, semantic conflict ledger, gate results, and local-mode proof remain frozen in the [Phase 0 baseline](00-phase-0-baseline.md), while the machine-readable baseline records the refreshed heads and structural ratchets.

Phase 0 and Phase 1 are complete. The final Phase 1 Loom head is `7e8a6dd2`; it approved the capability graph, completed the direct-write, authority, transaction, runtime, and performance inventories, enforced all 11 declared build profiles plus all-files AST checks, added the characterization gate, and productized the supervisor-disabled proof contract. It also closed the stale-task default/clock and session-heartbeat reliability items and added fail-fast FleetDB capability negotiation for slices that declare required keys.

Phase 2 is complete with an active `internal/modules/workflowcatalog` root. Loom implementation head `84cccb761` and FleetDB `430dce8d9` implement the read API, typed operator commands, shared FleetDB adapter, management HTTP and CLI adapters, local credential mechanism, and Redis/Postgres lifecycle contract. The paired gates, checksum `4f50d5e0…ca19`, real route/CLI E2E, packaged Desktop journey, checkout-scoped deterministic local-mode integration, and measured p50/p95 plus round trips pass. The graph is ratcheted to `completed_phase: 2`, and the machine baseline includes the immutable Phase 2 validation snapshot. See the [Phase 2 evidence](07-phase-2-decisions-and-evidence.md).

Phase 3 source implementation establishes active `internal/modules/automation`, named webhook and system-event application workflows, durable Redis/Postgres admission, cron, retry, manual-dispatch, run-outcome, and generic await-notification operations, plus four named runtime-host registrations across Automation and Execution. Its security closeout makes generic await resolution and parent-run resumption one Execution-owned Redis/Postgres command, reserves the complete `run.finished` provenance lane across admission/live dispatch/historical catch-up, and negotiates the always-required `execution.await_atomic_resume.v1` deployment key before runtime loops start. The architecture graph is ratcheted to `completed_phase: 3`, and the mutation ledger records all 14 public Automation mutations alongside the three Workflow Catalog lifecycle commands. Architecture and focused Go checks, public route/CLI and signed-webhook E2Es, the pinned-count performance proof, the OpenAPI checksum, checkout-scoped generic plus webhook local-mode verifiers, the authenticated local-browser create/activate/manual-run/history journey, and the targeted Playwright local-operator suite pass. That UI run also drove the Redis/Postgres effective-trust parity correction and malformed-approval hardening. The final post-correction FleetDB gate passes at 78.0% coverage, and the final Loom aggregate gate passes against the exact paired FleetDB binary and matching source under a clean HOME/runtime environment. GPT-5.6 Terra review is separately not recorded because exporting the local screenshots to an external model awaits explicit informed approval; this is not a product failure. The baseline retains both the self-reference-free pre-commit measurement and a post-commit audit bound to Loom `7f95b9bf1` and FleetDB `f1c4e1119`.

Phase 4 is complete. It activates the Execution and minimal Artifacts
capability roots and moves the selected TaskRun and DriverRun owner-command
paths behind the paired FleetDB contract. The current architecture check
passes all 11 profiles plus the all-files AST check with four active roots, 60
required command-ID namespaces, Store `82/71`, 90 handler exceptions, 251
primary direct-write rows across 273 sites, 86 runtime components, and 103
goroutine definitions. The namespace prefixes group the ledger entries; they do not
replace the per-entry aggregate and coordinating-owner declarations. The
separate `internal/driver` ratchet freezes its remaining 10 rows across 11
sites without relabeling their owners. The paired OpenAPI snapshots are
byte-identical at SHA-256
`26ed930bc527c3815742c8b4c7a0ba5267bdc91c585ddc9f78483d9373303482`.
FleetDB and Loom aggregate gates pass against the exact paired FleetDB source
and binary. The source-bound supervisor-disabled row passes with fresh
planner/coder tasks, persisted design, transcripts and diff, zero automatic
agent definitions, daemon processes, or daemon sockets, and clean teardown.
The 30-sample artifact-backed design path measured p50 `11.796 ms`, p95
`14.784 ms`, and exactly three Loom-to-FleetDB requests per sample. The exact
packaged Desktop at Loom `53cbe2577` completed a real GPT-5.6 Terra
planner/coder journey and the unavailable-backend fail-closed journey, after
which Codex was restored as the project default. The immutable final validation
snapshot records those results at Loom `53cbe2577` and FleetDB `afb688768`.
Physical supervisor deletion remains Phase 6 work. At that immutable
validation snapshot, Phase 5 had not started. See the
[Phase 4 record](09-phase-4-decisions-and-evidence.md).

Phase 5 source ownership is complete. Agents, Interaction, minimal Source
Control and Connectors, durable AgentProvisioning, and Workflow Catalog
authoring seams are active. The all-`internal` Interaction sole-writer scan now
finds zero direct `AgentSession`, `TerminalSession`, `AgentLease`, or inbox
mutation sites outside Interaction and persistence adapters, and the graph is
ratcheted to `completed_phase: 5`. The Interaction inbox delivery adapter
receives only a narrow enqueuer and derives a fresh, registered
`serve-interaction-inbox-delivery` system authority for the exact enqueue; it
never receives the issuer. Inbox retry completion is bound to the exact claimed
attempt, so a delayed completion cannot clear a successor claim. The
typed 11-profile direct-write inventory now records 260 rows across 269 sites.
The increase is classifier coverage from moving FleetDB transport interfaces
into owner-specific packages; it exposes previously latent, type-resolved calls
rather than adding persistence behavior. The inventory includes the
owner-private Agents prompt-repair primitive.
The exact structural ratchets now record 78 composite-Store files, 66 outside
composition, and 87 legacy handler-import exceptions. The remaining
composition-edge adapters are explicit transitional bridges; the owner modules
do not receive the composite Store.
It includes the three remaining mixed `internal/driver` rows in the primary
scan, with their Phase 6 expiry preserved; the former Interaction-owned driver
rows are gone. The exact runtime ledger now names 90 components, 61 of
them managed, after adding separately scheduled Workspace
repository-admission restart recovery and lease renewal without changing the
54 ticker definitions. Recording the bounded Phase 5 ownership-analysis
worker plus the guarded repository-admission materialization watchdog and
renewal worker brings the exact all-source goroutine-launch ledger to 105. The
[Phase 5 record](11-phase-5-decisions-and-evidence.md) separates this
source-level closure from the packaged Desktop proof. The FleetDB and Loom
gates are green, the repaired-package regression matrix passes, and the four
GitHub-backed rows are explicitly waived by the operator. The exact
24-row acceptance matrix and its 20-pass/4-waiver disposition are recorded
in the [real-Codex proof record](12-phase-5-real-codex-proof.md).

The current reliability closeout is committed through Loom `67c45972f` with
FleetDB `9ffa69f60`. Core hardening at `ee971be22` adds healthy-parent,
owner-fenced stale-child TaskRun
recovery; atomic first-class Repo create/update/delete and replay protection;
mandatory workflow-actor exclusion for `internal.issue.*` bindings; and
fail-closed runner credential scanning, redaction, and exact-SHA publication.
The current UI commit adds repository-free workspace creation so the blocked
repository-admission path is reachable from a fresh packaged product.
The paired OpenAPI snapshots match at SHA-256
`ebf2ec68fd5751fbb59747c7b3db7b66fe4f7f80f30cb7eead9b6b3fd35ccb9e`, and
FleetDB's full gate and the earlier Loom clean-HOME gate against this paired
hardening source pass. The refreshed package proves repo-free Blocked/no-run
admission, repository selection, planning, a GPT-5.6 Terra coder transcript and
diff, patch-back, and Closed convergence. The checkout-scoped Podman verifier
and raw browser prove fresh planner/coder design, transcripts and diff plus zero
supervisor artifacts. The appended
`phase4-reliability-validation-67c45972f` snapshot binds that exact product
proof, the passing exact-head architecture checks, and the bounded-memory Loom
aggregate gate rather than inferring any result from the prior UI-independent
gate.

Phase 6 source and daemon-free runtime acceptance are complete at Loom
`02daec339` with FleetDB `51b8a493`. The legacy supervisor, daemon IPC/control,
RPC, WebUI daemon integration, AgentCommand, DaemonProfile, and compatibility
paths are physically retired. Canonical Agents desired state, Agent lifecycle,
WorkerProfile placement, Execution recovery, Interaction sessions, and explicit
prompt-agent arbitration replace them. The exact 7-row matrix rejects disabled
or skipped tests and passes inside the fresh supervisor-disabled Podman proof,
which also verifies planner/coder designs, transcripts, diff, and zero daemon
artifacts. Both full gates pass and the paired contract hashes to
`816b0b0c…7ef0`. The signed exact-head package passes binary verification and
completed a fresh UI-created real-Codex planner/coder canary: task
`PHASE5-REPAIRED-20260801-23` persisted its design, both transcripts, one-file
diff, local branch, exit-0 runs, and Review convergence without a GitHub
mutation. See the [Phase 6 record](13-phase-6-decisions-and-evidence.md).

Phase 7 completes the modular-monolith migration. All ten capability roots are
active; Work Items, Workspace, Source Control, Connectors, and Artifacts own
their remaining product surfaces; and Observability plus Source Control are
full frontend feature slices. The final architecture inventory records Store
`61/51`, 29 broad handler exceptions, zero legacy service-handler imports, 107
mutation commands, 102 primary direct writes, 71 runtime components, 80
goroutine launches, and all six performance rows measured. The legacy
`webui/service` and `svcimpl` business-logic centers are physically empty; the
remaining delivery coordination lives in focused packages. The signed packaged
Desktop selected 16 passing real-Codex executions across the eight local
creation templates, with four GitHub execution rows explicitly waived and
three legacy Advanced templates retired.
Product proof also corrected the Observability cache so modular DriverRun
metrics survive restart. See the
[Phase 7 record](14-phase-7-decisions-and-evidence.md).

Phase 8 completes the post-extraction consolidation at Loom `35e61b31b` with
the unchanged FleetDB companion `b71dec551`. Starting from exact Phase 7 commit
`46bb9a841`, it reduces production package directories from 250 to 189,
outside-module packages from 232 to 172, one-file packages from 115 to 67, and
one-or-two-file packages from 141 to 89. The exact shrink-only inventory,
default-deny capability graph, ownership ledgers, all 11 build profiles,
full Loom and FleetDB gates, 16-run packaged template matrix, crash recovery,
unavailable-backend fail-closed row, exact-package restart, and transcript
persistence all pass. No generic replacement business-logic bucket or FleetDB
contract change was introduced. See the
[Phase 8 plan and evidence](15-phase-8-consolidation-and-evidence.md).

Phase 9 is active from the Phase 8 documentation head `1fc9d887c`. Wave 9.1
removes the unused `internal/types` product-model plane, moves live
classification policy to its owner or enforcing consumer, and makes the
FleetDB adapter project private wire records directly to the existing backend
compatibility DTOs. Wave 9.2 then makes the sole-consumer IssueBackend E2E
suite test-only and folds diff-path traversal validation into its source-control
consumer while deleting an unused sensitive-path classifier. Wave 9.3 moves
the remaining connector models and persistence contracts into their declared
owner, makes FleetDB and memstore implement that port directly, and deletes
the horizontal domain, composite-repository, placeholder, and mapping-only
catalog layers. Wave 9.4 then makes Artifacts the sole model and port owner,
has FleetDB and memstore implement those ports directly, injects owner queries
through the Artifacts capability, and physically removes the horizontal
Artifact model, repository, upload, and mapping-only catalog surfaces without
a fallback facade. Together the waves reduce the exact production shape from
189 to 184 packages, outside-module packages from 172 to 167, one-file
packages from 67 to 63, and one-or-two-file packages from 89 to 85. Wave 9.5
removes the forwarding-only Artifacts FleetDB subpackage, lets the composition
bridge implement the owner ports directly, and tightens the exact shape again
to 183 packages and 84 one-or-two-file packages without changing the ten
capability owners. Wave 9.6 then removes the duplicate Connectors grant
transport/adapter plane and the Git-only runtime-failing compatibility
constructor. FleetDB and memstore now implement the Connectors grant owner port
directly, reducing the exact shape to 182 packages, 62 one-file packages, and
83 one-or-two-file packages. Wave 9.7 then physically removes 11 dead
source-compatibility functions or values, deletes the obsolete test-only Driver
stale-recovery implementation, characterizes the Execution owner instead, and
guards against handwritten deprecated production APIs returning. It removes
499 net lines without changing the package count or widening import fanout.
The 25 `legacy handler imports` remain live allowlisted compatibility edges,
not historical labels, and the Driver still has an active shared-token/header
authentication fallback. Wave 9.8 deletes that Driver fallback and requires
signed run-scoped identity throughout the runtime. Wave 9.9 then routes Git
graph, blocked-list, health statistics, readiness, and config-label delivery
through narrow Work Items or presentation ports, deletes the dead workspace
backend setter and config env fallback, and tightens the exact live handler
imports from 25 to 22. Wave 9.10 then removes PR-review's two generated-backend
DTO imports and composite-store import, makes the HTTP adapter own its wire
shapes, injects a two-method Workspace query port, and composes Connector
dispatch, management, and credential sealing once at the application root. It
physically deletes the old positional constructor and forwarding-only route
composition function, lowers live handler imports from 22 to 19, and tightens
PR-review import fanout from 18 to 15. Wave 9.11 then absorbs the sole live
runtime-preflight policy into its Workflows consumer, injects backend readiness
through a consumer-owned port, deletes the horizontal package and global test
hook, and ratchets the exact shape to 181 production packages, 166 packages
outside modules, 61 one-file packages, and 82 one-or-two-file packages. The
session-store facade remains a known migration target only because bypassing it
would create 24 forbidden direct persistence writes; the later deletion wave
must migrate the complete port and remove the facade together. Wave 9.12
deletes three one-file vocabulary seams (`authmode`, `backendnames`, and
`cli/backendapi`) and ratchets the exact shape to 178 packages. Wave 9.13 then
deletes the process-global runtime-context package and ambient event context
provider, requires explicit caller context throughout CLI, session, event,
monitor, and worker paths, and moves automode prompt construction to its
composition callers. That slice ratchets the exact shape again to 177 packages,
162 outside modules, 57 one-file packages, and 78 one-or-two-file packages
without a replacement shim. Wave 9.14 deletes the shallow native-transcript
dispatcher and Codex wrapper, makes Sessions select the recorded backend
parser, and replaces the unknown
backend's silent Claude fallback with an explicit error. It ratchets the exact
shape to 175 packages, 160 outside modules, 55 one-file packages, and 76
one-or-two-file packages. Wave 9.15 then deletes the remaining Claude and
OpenCode transcript parser packages, keeps only owner-private parsing behavior
in Sessions, and ratchets the exact shape to 173 packages, 158 outside modules,
53 one-file packages, and 74 one-or-two-file packages. Wave 9.16 then deletes the
forwarding-only session-store adapter and the separate session event-store
package, moves archive and event-log policy behind the Sessions owner, and
ratchets the exact shape to 171 packages, 156 outside modules, 51 one-file
packages, and 72 one-or-two-file packages. Wave 9.17 deletes the terminal
session-name command protocol and frontend name-derived backend/agent
classification, requires server-derived durable launch envelopes before
WebSocket attachment, and removes the dead connect-seeding callback. It leaves
the exact package count unchanged while eliminating the known executable
compatibility path. Wave 9.18 deletes the neutral Driver run-types bridge,
makes Driver own its invocation contract, leaves Sandbox with placement policy
and launcher evidence only, and deletes Driver's stale import-fanout exception.
It ratchets the exact shape to 170 packages, 155 outside modules, 50 one-file
packages, and 71 one-or-two-file packages. Wave 9.19 then deletes the shallow
local-node-config wrapper and lets Bootstrap own the runtime-provider
operations for its existing machine-local state cache. It ratchets the exact
shape to 169 packages, 154 outside modules, 49 one-file packages, and 70
one-or-two-file packages. Wave 9.20 deletes the Agents bootstrap application
facade and horizontal store adapter, moves the
monotonic PromptFile repair into the Agents owner over its revisioned Role
port, requires injected owner commands for workspace creation and startup
repair, and deletes the legacy Agents constructor and repair-specific
persistence interface. It ratchets the exact shape to 167 packages, 152
outside modules, 47 one-file packages, and 68 one-or-two-file packages while
lowering direct persistence to 86 rows/95 sites and workspacemgr fanout to 14
with no exception. Wave 9.21 deletes the Automation composition adapters that
reconstructed binding-secret, grant-cleanup, and Agent-identity behavior over
horizontal stores. Trigger-binding routes now consume the Connectors-owned
binding lifecycle and canonical Agents identity queries, the real FleetDB and
memstore adapters implement the owner persistence seam, and `agentmodules`
fanout tightens from 38 to 37 without increasing the Agents handler ceiling.
The exact package and direct-write shapes remain unchanged. Wave 9.22 removes
the approval endpoint's composite Store/AwaitStore access and the
trigger-binding endpoint's DriverRunStore/filter/ordering access. Both now
consume exact read ports backed by the existing WebUI read-projection module;
there is no handler fallback to the removed persistence paths. The live handler
allowance falls from 19 to 17, while the exact package shape becomes
`167 / 15 / 152 / 46 / 68` because `readprojection` is no longer a one-file
package. Direct persistence remains `86 / 95`. Wave 9.23 then deletes the
TriggerBinding secret plane across FleetDB and Loom. Connectors becomes the
sole inbound-signing authority, the verifier consumes a narrow Automation
route query and fails closed without a Connector, and the old field, route,
permission, persistence column, CLI flag, compatibility verifier, and fallback
resolver are absent. The handler allowance falls again to 16 and FleetDB
client call sites fall from 238 to 236 without changing package shape or
direct-write counts. Wave 9.24 deletes three shallow package roots:
Daytona's real host adapter is folded through `serveadapter`, workspace Git
materialization is folded into its sole `workspacemgr` owner, and the
fake-harness executable moves under Go-standard `testdata`. Exact package
shape falls to `164 / 15 / 149 / 43 / 65`; retired-root guards prevent all
three old paths from returning, with no owner, port, or external adapter
removed. Wave 9.25 deletes four more shallow package roots and six forwarding
constructors, moving repository admission, local journal recovery, task
worktree/lineage, and CLI test support into their actual consumers. Exact shape
falls to `160 / 15 / 145 / 42 / 61`, while direct persistence remains
`94 / 112`. Wave 9.26 then removes the executable compatibility behavior still
hidden inside that topology: the parallel trigger-route and Automation runtime
planes, direct-Store Driver lifecycle implementations, duplicate Workflow
Catalog authoring, journal-only task dispatch, in-memory cursor substitution,
the executable-sibling Node layout, and repository-basename alias matching. It
deletes 41 files and 12,639 net lines without creating a replacement package;
the exact shape stays 160 and all architecture and aggregate gates pass against
paired FleetDB `9c1859a`. Wave 9.27 removes the remaining handler imports of
the horizontal Store, backend, and FleetDB planes across Agents, Roles,
TaskRun, Driver API, Terminal, and Workflows. Those handlers now consume exact
capability or presentation ports with real adapters composed once at the
application seam and no persistence fallback. Handler legacy imports fall from
16 to 0, composite Store files from 15 to 14, direct persistence from
`94 / 112` to `90 / 108`, and runtime inventory from `71 / 80` to `70 / 79`;
the exact package shape remains `160 / 15 / 145 / 42 / 61`. The fully pinned
aggregate gate passes against FleetDB source and binary `9c1859a`. The
remaining waves delete residual horizontal model/projection planes and shallow
packages. Wave 9.28 then deletes the one-consumer AgentProvisioning owner-adapter
package and makes its action-specific translations private implementation of
the existing serve composition root. Exact package shape tightens to
`159 / 15 / 144 / 42 / 60` and serve fanout to 30, while direct persistence,
handler imports, and runtime inventory remain at their lower Wave 9.27
ratchets. The fully pinned aggregate gate remains green. Wave 9.29 removes the
realtime mutation sub-plane from the horizontal Work Items backend contract,
including timestamp fallbacks and API no-op methods, and gives Work Items the
narrow durable mutation-stream port. The broad lifecycle/query/command
interface then contracts from 26 to 20 methods in Wave 9.30 by deleting unused
children, defer/undefer, label, and polymorphic batch compatibility operations.
Wave 9.31 makes FleetDB `external_ref` part of canonical atomic issue creation,
then deletes Loom's probe/retry/patch fallback and alternate wrapped repository
response decoder. Wave 9.32 deletes Work Items' union create result and
post-create read fallback; create now returns one owner-owned canonical summary.
Its 20 real lifecycle/query/command methods remain the next deletion target; an empty capability-graph
`legacy_paths` list alone is not completion proof. See the
[Phase 9 plan](16-phase-9-package-consolidation.md).

## Approved architecture decisions

| ID | Outcome |
|---|---|
| MM-1 | Adopt the ten coarse capability owners, including Artifacts and the Workspace/Source Control split. |
| MM-2 | In local `open` mode, derive exact workspace/action-scoped typed operator authority server-side with the stable `local-open-operator` subject and no client credential; require the existing OIDC identity/role resolver for shared deployments. |
| MM-3 | Advertise a capability key only when the active Redis or Postgres deployment has parity; missing support fails readiness with no fallback. |
| MM-4 | Use Workflow Catalog approve/unapprove/activate as the first complete backend pilot after a behavior-neutral read seam. |
| MM-5 | Place public capability roots at `internal/modules/<capability>` with optional owner-private subpackages. |
| MM-6 | Expire compatibility facades after at most two migration waves unless an explicit reviewed extension records an owner. |
| MM-7 | Use an explicitly configured management endpoint, no implicit host startup, fail-closed unavailability, the server's open/OIDC trust mode, and family-by-family compatibility rollout. |

These outcomes are enforced by `capability-graph.yaml` and `migration-baseline.json`. Changing one requires a reviewed graph/baseline change; an individual slice must not reverse it through incidental package movement.

The [local open-mode authority revision](10-local-open-mode-authority-revision.md)
revises MM-2 so the single-user UI works without launch codes or a durable
operator credential while leaving the existing OIDC/cloud path unchanged. The
historical Phase 1 through Phase 4 evidence remains immutable.

---

[All migrations](../README.md) · Next: [Current-state evidence](01-current-state.md) · [Phase 8 consolidation](15-phase-8-consolidation-and-evidence.md) · [Phase 9 consolidation](16-phase-9-package-consolidation.md)
