# Modular Monolith Migration

- **Status:** Phase 6 source and daemon-free runtime acceptance are complete at
  `completed_phase: 6`; the fresh packaged-Desktop UI and real-Codex canary
  await macOS unlock
- **Date:** 2026-08-03
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
| [10-local-open-mode-authority-revision.md](10-local-open-mode-authority-revision.md) | How does local open mode work without operator credential ceremony while OIDC remains authoritative for shared deployments? |
| [11-phase-5-decisions-and-evidence.md](11-phase-5-decisions-and-evidence.md) | Which Phase 5 seams, ownership invariants, gates, and exact-head acceptance proofs closed the phase? |
| [12-phase-5-real-codex-proof.md](12-phase-5-real-codex-proof.md) | How did the packaged-Desktop real-Codex matrix close with 20 local passes and four operator waivers? |
| [13-phase-6-decisions-and-evidence.md](13-phase-6-decisions-and-evidence.md) | Which supervisor paths were deleted, what replaced them, and which exact gates prove Phase 6? |

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

## Phase 0 through current Phase 6 status

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
`816b0b0c…7ef0`. The signed exact-head package passes binary verification; its
fresh UI/real-Codex canary is the sole remaining acceptance item because macOS
is locked. See the [Phase 6 record](13-phase-6-decisions-and-evidence.md).

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

[All migrations](../README.md) · Next: [Current-state evidence](01-current-state.md) · [Phase 6 evidence](13-phase-6-decisions-and-evidence.md)
