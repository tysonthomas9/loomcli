# v5 Integration Regression Closure

- **Status:** Validated
- **Date:** 2026-07-15
- **Branch:** `unified-agents`
- **Integration base:** `f104611123a54ed00f4d6677d9046d3decf118f8`
- **Purpose:** Close regressions and proof gaps exposed after integrating `v5` before modular-monolith extraction begins
- **Migration:** [Modular Monolith Migration](README.md)

## Closure standard

This pass does not claim that the application contains no bugs. It establishes
that there are no known regressions in the reviewed integration seams after:

1. reproducing the reported failure through the registered HTTP module;
2. auditing the adjacent dispatch, identity, lifecycle, workflow, contract, and
   local-stack paths;
3. adding a regression test at the lowest useful boundary and at an integrated
   boundary where behavior crosses packages;
4. running the repository gate; and
5. rebuilding an isolated stack from this physical checkout and exercising the
   repaired flows through the product UI.

A preserved volume, a task found by title, or a session created before the
current run is not accepted as runtime proof.

## Regression ledger

| Area | Classification | Failure or risk | Restored invariant |
|---|---|---|---|
| Unified create dispatch | Merge-introduced | Built-in interactive create reached the record discriminator switch and returned `unsupported agent kind: interactive` | `interactive` and `worker` remain supervised role kinds; `supervised` is normalized before delegation; unknown kinds fail closed |
| Create repository scope | Review-discovered | `loom data create --source-repo` accepted the flag but omitted `source_repo` from the generated API request, creating a task that could not be routed reliably | The OpenAPI request, generated clients, adapter mapping, and raw HTTP transport preserve the repository scope |
| Scripted AgentService storage | Pre-integration branch debt | The in-memory conformance store required `role_name` and dropped driver/version behavior | An AgentService has exactly one behavior reference: role, or driver plus version; DTO and store round-trip both forms |
| Durable agent lifecycle UI | Pre-integration branch debt | Rename/delete could mutate only one trigger binding and leave the durable identity alive or stale | Agent identity rename/archive is record-scoped; schedule and timezone remain binding-scoped; unattached legacy bindings keep compatibility behavior |
| Unified identity namespace | Pre-integration branch debt | Supervised agents, durable records, and unattached binding records could collide, producing ambiguous or unreachable item routes; mixed-case supervised names could bypass the preflight before normalization | Creation normalizes the prospective supervised identity before cross-kind checks, existing collisions return `409` without mutation, and migration only adopts an exact crash-residue fingerprint |
| Stop/restart lifecycle | Pre-existing v5 parity gap | The unified store route hard-stopped on an empty stop request and returned a restart status different from the established CLI/daemon contract | Stop without force queues yield and returns `202`; force stop returns `200`; restart returns `200` |
| Review Loop session linkage | Pre-integration branch debt | A GitHub slug such as `owner/repo#123` was used as a Loom task ID, breaking task-scoped session routes | TaskRun uses the local Loom issue ID; external PR identity remains workflow input |
| Workflow run detail | Pre-integration branch debt | Sparse terminal rows or a transient detail error could erase output/session linkage; multi-step runs exposed one session | Sparse updates preserve enriched detail, detail fetch can be retried, and every linked task session is selectable |
| Issue design persistence | Cross-repo integration gap | Loom's planner sent `design_format`, but the companion FleetDB branch rejected the unknown field and therefore rejected the design body in the same atomic PATCH | FleetDB persists and hydrates issue designs as managed artifacts, treats format as artifact identity while hashing the body for integrity, and supports format-only re-persistence; Loom vendors the matching FleetDB contract |
| Issue design history and revert | Review-discovered | Create responses could omit the resolved format, and a revert could append a durable reference before proving the historical artifact was still readable | Create returns the resolved `design_format`; history/revert preflights and hydrates the referenced artifact before appending an event, so a missing artifact cannot create a broken durable reference |
| Workflow driver readiness | Review-discovered | Prompt-agent creation could mutate role, service, and binding records before discovering that the Flue build toolchain or active driver bundle was unavailable | Driver materialization and active-bundle resolution complete before durable agent mutations; unavailable target bindings return an actionable `503` atomically |
| Unified-agent OpenAPI | Pre-integration branch debt | Registered routes and discriminated response/request shapes were absent or stale | Every route in the unified agent module is documented and guarded; generated Go and TypeScript unions decode the supported kinds |
| Local-mode isolation | Pre-existing v5 proof gap | Multiple checkouts shared the default Compose project and verifier accepted preserved tasks/sessions | Project names are checkout-scoped; volume provenance is checked; proof reads a live run manifest with exact IDs and fresh timestamps |
| Local-mode proof integrity | Review-discovered | Invalid timestamps, host/VM clock skew, injected manifests, or the Codex alias could make proof fail open or prove the wrong backend | Threshold is minted inside the stack, timestamps fail closed, Make reads the live container manifest, and Codex verification requires `backend=codex` |
| Workflow build portability | Review-discovered | A host-installed Flue checkout could exist but lack the Linux target's Rolldown native binding, failing only after the stack started | The workflow profile checks the container architecture and matching Linux/glibc native binding before Compose startup and prints an exact pnpm remediation |
| Supervisor restart ownership | Live-reproduced | Process-derived ownership IDs and a 30-minute task lease fenced a replacement daemon after container restart | A workspace-persisted owner ID survives process/container replacement, NodeID remains process-specific, and the ownership lease uses the two-minute node TTL |
| Pre-spawn ownership | Review-discovered | Ownership could change after queue selection but before process launch | The supervisor re-arbitrates ownership immediately before spawn and refuses to launch after a lost lease |
| Codex restart recovery | Live-reproduced | An active Codex lock without a session ID was treated as a clean cold start, and a failed recovery reset could overwrite a concurrent reassignment | A recent dead no-session lock enters checkpoint recovery for the exact task/run/worktree; release conflicts abort cleanup, and destructive reset requires the exact `in_progress` state and recovering assignee |
| Local-mode title lookup | Review-discovered | Harness lookup could stop at the first issue page or call a route FleetDB does not implement, causing old data to satisfy a run or a fresh task beyond the boundary to disappear | The entrypoint uses the real `/issues/search?q=` service path and exact-title matching; a production-faithful test finds a deferred target after 252 issues, including URL-special characters |
| Delete versus active claim | Live-reproduced | Permanently deleting an issue after an auto-agent claimed it left a real execution pointing at a missing issue; the sidebar then opened an `issue not found` panel | FleetDB delete and claim arbitrate through the same issue lock across Redis and Postgres: an active claim returns the documented `409`, while a winning delete fences new claims until projection and clears the lock with the issue; failed requests clean up through an independent bounded context. The UI directly resolves active IDs omitted by capped or type-filtered lists, retries independent lookups, and only treats an authoritative `404` as missing. |

## Boundary decisions preserved

- This is a regression-closure slice, not the first capability extraction.
- The HTTP module remains an adapter over the current service/store surfaces;
  it does not establish a new cross-capability public API.
- Legacy binding fallback is compatibility behavior. New durable agent behavior
  remains record-owned.
- Identity uniqueness is currently checked across separate stores and is not an
  atomic global uniqueness constraint. Any pre-existing collision fails closed;
  transactional ownership belongs in the future Agents capability/fleet-db
  command boundary.
- A delete whose durable append fails at the same time as lock storage can leave
  its fail-closed reservation until the 24-hour TTL. Cleanup uses an independent
  five-second context and logs `delete-lock-leak`; durable retry ownership for
  compound command reservations belongs in the future FleetDB command boundary.
- Event-store transport failure after a server-side durable commit remains an
  ambiguous outcome: the caller cannot prove whether releasing the reservation
  is correct. Resolving that class requires an idempotent command/outbox receipt,
  not a longer in-process lock defer.
- A permanently stale active-agent status causes the sidebar's missing/error
  direct lookup to retry every five seconds until that status clears. Requests
  are per-ID, independently published, and capped by a ten-second abort, so one
  stale lookup cannot block another active task.
- `WorkflowAgentDetail` has one checked component-boundary exception for a
  concrete `SessionRunDetail` import. The public barrel participates in the
  issue-session graph and produced a Rollup circular-chunk warning; the direct
  edge removes that production-build hazard until transcript presentation is
  extracted as an independent UI package.
- Roles, connectors, trigger bindings, and workflow routes still have broader
  pre-existing OpenAPI coverage debt. This slice closes the unified-agent module
  without inventing contracts for unrelated modules.

## Automated evidence

The closure matrix includes:

- affected Go package tests for handlers, app wiring, memstore, workspace
  identity migration, workflow built-ins, generated API types, and local mode;
- companion FleetDB API, service, artifact, Redis/Postgres storage, migration,
  RPC, and generated-contract tests;
- unified-agent route/spec and discriminator guards;
- Review Loop built-in workflow test;
- full frontend Vitest, production build, lint, architecture, and generated-type
  freshness checks;
- root `make gate` with inherited Loom desktop/runtime variables removed;
- isolated Compose configuration and local-mode verifier tests;
- ownership parity tests in the Fleet-backed supervisor and in-memory control
  store, including same-owner reacquisition, different-owner fencing, an
  immediate pre-spawn recheck, and exact-assignee cold-reset guards;
- a production-faithful local-mode search test that crosses the default page
  boundary with 252 issues and uses the registered handler/service/Fleet
  adapter route;
- FleetDB delete/claim lock-arbitration tests, including canceled and failed
  append cleanup, the shared Redis/Postgres delete-lock storage contract, HTTP
  `409` mapping, and matching published/vendored contracts;
- frontend historical-orphan containment and direct-lookup tests covering
  active bug/chore/epic records omitted by the tree projection, an authoritative
  `404`, transient failure retry, request timeout, and independent completion
  when another active lookup stalls; and
- `git diff --check` plus generated-code staleness checks.

The final root `make gate` completed successfully with all Go and frontend
quality gates passing. The frontend suite was green at 381 files and 8,551
passed tests with one existing skip. The gate also passed race-enabled Go
tests, generated-code freshness, production frontend build, lint, architecture
checks, coverage policy, and the repository line-count ratchet. `git diff
--check` was clean.

## Runtime evidence

The stack was rebuilt from this checkout into an explicit, checkout-scoped
Compose project. Its isolated volumes were deliberately preserved across the
restart proof; exact run IDs, task IDs, and container-minted timestamps prevent
older rows in those volumes from satisfying the verifier. It remains available
for manual inspection at
`http://127.0.0.1:8583/ws/LOCALMODE/kanban`.

| Proof field | Live value |
|---|---|
| Source root | `/Users/tyson/codebase/code-agents/rc-2/loomcli` |
| Checkout ID | `466422ad600c` |
| Compose project | `loomcli-rc2-v5-fresh` |
| Run ID | `20260715T091002Z-regression` |
| Container-minted threshold | `2026-07-15T09:10:14Z` |
| Backend | `codex` (`codex-cli 0.144.4`) |
| Planner task | `LOCALMODE-20` |
| Coder task | `LOCALMODE-21` |
| Host ports | FleetDB `8580`, Loom API `8582`, UI `8583` |

`make local-mode-codex-verify` read that manifest from the running container
and passed every assertion: exact manifest-owned task IDs and run-tagged
titles, fresh task/session timestamps, planner review status with a Markdown
design artifact and non-empty transcript, and coder closed status with a
non-empty transcript plus diff metadata containing the expected local-mode
artifact. The containers remained healthy after the verifier and their logs
contained no Loom panic or fatal event.

### Same-container restart proof

After the first verifier pass, the Loom container was restarted in place while
FleetDB, Redis, the UI, and the isolated volumes remained intact. The entrypoint
read its `.recovery` journal and logged:

```text
resuming proof epoch 20260715T091002Z-regression from 2026-07-15T09:10:14Z
```

It reused exact tasks `LOCALMODE-20` and `LOCALMODE-21` rather than seeding a
new epoch. A second `make local-mode-codex-verify` passed the same manifest,
freshness, design, transcript, and diff assertions after the restart.

### Abrupt restart recovery proof

A dedicated planner issue, `LOCALMODE-15`, was interrupted by hard-stopping the
Loom container while its lock was active and contained a task/run identity but
no Codex session ID. FleetDB, Redis, the UI, and all isolated named volumes
remained available over the same preserved data.

| Recovery field | Before interruption | Replacement daemon |
|---|---|---|
| Container hostname | `67fdd513786c` | `782b72575cd5` |
| Supervisor PID | `383` | `371` |
| Planner PID | `8292` | `446` |
| Durable owner | `loom-supervisor-owner-2972f7e44e8f76adef6eb6be37dae7fa` | unchanged |
| Ownership fence | `118` | `124` |
| Task | `LOCALMODE-15` | `LOCALMODE-15` |
| Lock run ID | `dc3d076541788d941b19474855084862` | unchanged |

The replacement daemon logged `cold-starting interrupted task with checkpoint
fallback` followed by `claimed task for agent ... reason="resume interrupted
task"`; it did not select another ready task. The recovered planner then saved
a 12,814-character design, moved `LOCALMODE-15` to `review`, cleared its
assignee, and signaled completion.

### API and browser journeys

The direct live-API matrix passed for supervised `interactive`, `worker`, and
`supervised` creation; unknown-kind `400` rejection without persistence;
graceful, forced, and restart lifecycle responses; duplicate-identity
rejection; `source_repo` transport; format-only design rejection without
mutation; prompt-agent create/read/rename/delete cleanup; and driver readiness.
On the final rebuilt services, a dedicated epic then proved the delete/claim
boundary end to end: create `201`, direct FleetDB claim `200`, Loom delete while
claimed `409 conflict`, read-after-conflict `200`, owner release `204`, delete
after release `200`, and final read `404`.

The browser pass opened the exact final planner and coder cards. Planner
`LOCALMODE-20` rendered its completed session, transcript, repository, and
Markdown design. Coder `LOCALMODE-21` rendered its completed session,
transcript, and exact diff adding the `LOCALMODE-21` local-mode artifact. No
page exception was reported. The browser still emits the non-blocking router
warning `No HydrateFallback element provided`. Multi-step workflow detail was
covered by component tests but was not available in the seeded live dataset.

## Exit decision

The bounded integration closure is validated. Phase 1 subsequently approved
MM-1 through MM-7 and completed the guardrails without adding a capability
module root. Package moves begin in Phase 2 and remain subject to the approved
graph, characterization gate, overlap checks, and per-slice proof in the
migration index. Future defects outside this ledger become normal backlog work;
a failure in one of these restored invariants reopens this closure slice.

---

[Previous: Enforcement and gates](04-enforcement-and-gates.md) · [Migration index](README.md) · [Phase 1 evidence](06-phase-1-decisions-and-evidence.md)
