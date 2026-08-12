# Post-Phase-9 WebUI Deepening Plan

- **Status:** Superseded by the
  [Phase 10 consolidated deep-module goal](20-phase-10-consolidated-deep-module-goal.md)
- **Date:** 2026-08-12
- **Scope:** Remaining shallow modules under `internal/webui`, the capability
  and application seams that replace them, and the architecture gates that
  prove WebUI is delivery-only
- **Decision:** [ADR-0001: WebUI is a delivery-only module](../../adr/0001-webui-is-delivery-only.md)

> This document is retained as the complete WebUI architecture discussion and
> decision ledger. It is not the implementation authority. The consolidated
> Phase 10 goal resolves its naming, evidence-policy, dependency-order, and
> delivery-stack decisions; ADR-0001 remains accepted.

## Goal

Deepen the Artifacts, Source Control, Interaction, Workspace, and application
query modules so WebUI depends only on narrow interfaces and transport-neutral
results. Delete screen-oriented coordinator modules rather than moving their
interfaces under new names.

The target is not an arbitrary total package count. The deletion test is the
acceptance rule: deleting a retained module must cause its complexity to
reappear across callers. The intended direct children of `internal/webui` are:

```text
internal/webui/
  app/        # HTTP server and route assembly
  frontend/   # bundled frontend assets
  handlers/   # capability-oriented HTTP adapters
  server/     # DTO, middleware, handler utilities, realtime protocols
```

## Approved seams

### Run Capture and Artifacts

- A Run Capture is a Read Projection, not a mutable aggregate or independent
  store.
- Execution owns DriverRun and TaskRun lifecycle. Interaction owns AgentSession
  and TerminalSession lifecycle. Artifacts owns captured content.
- Prompt, transcript, diff, log, and report content are separate finalized
  Artifacts grouped by their lifecycle owner.
- Bounded local staging may exist only inside the Artifacts adapter while a
  capture is in flight. Durable truth is the finalized Artifact.
- Legacy session archive paths and formats receive no reader, migration,
  dual-write, or compatibility facade. Existing directories are not deleted at
  startup; an explicit operator cleanup path may remove them.
- Backend-neutral transcript parsing and redaction move to platform ownership.

### Source Control

Source Control presents three cohesive interfaces:

- **Browse:** list, read, stat, index, search, history, blame, and diff.
- **Mutate:** conditional write, delete, mkdir, and move.
- **Checkout:** status, enumeration, repair, branch operations, publication,
  and pull-request operations.

Workspace supplies opaque catalog references and approved local placement
through narrow ports. Source Control owns path normalization, symlink and
containment safety, `.git` protection, sensitive-path enforcement, optimistic
versions, traversal bounds, caching, invalidation, mutation locking, and Git
semantics. WebUI maps HTTP preconditions to Source Control versions and maps
typed errors back to HTTP.

### Interaction and terminal runtime

- Interaction owns durable AgentSession, TerminalSession, tab identity,
  terminal lifecycle, inbox, chat, and session history.
- A `TerminalRuntime` port isolates process, PTY, tmux, attachment, resize,
  ring-buffer, and shutdown mechanics in `internal/infra/pty`.
- WebUI owns only WebSocket upgrade, frames, ping/pong, disconnect handling,
  and short-lived attachment-token encoding.
- Active-tab selection remains presentation state in WebUI and cannot infer or
  mutate Interaction lifecycle.
- Setup terminals are an Interaction use case; an injected backend catalog
  resolves an allowlisted backend/action pair to a launch specification.

### Workspace and application queries

- Workspace owns catalog, topology, repository registration, durable workspace
  operations, and backend selection.
- Source Control owns repository materialization.
- `internal/app/workspaceprovisioning` coordinates multi-step workspace and
  repository admission without owning participating aggregates.
- `internal/app/query` composes cross-owner Read Projections from Workspace,
  Work Items, Agents, Execution, Interaction, Source Control, and Artifacts.
- Consumers request exact projections. Neither WebUI nor application queries
  receive `store.Store` or construct `ops.WorkspaceData` compatibility views.

### Composition and delivery

- `internal/app/serve` is the only capability and runtime composition root.
- WebUI receives transport configuration, delivery-runtime mechanisms, and
  capability-grouped route dependencies. It does not receive a generic `Deps`,
  registry, resource bag, or service locator.
- Route modules receive their smallest consumer-owned interface.
- OpenAPI is the canonical HTTP schema; generated DTOs are mapped explicitly to
  transport-neutral capability commands and results.
- FleetDB remains the only shared task-claim authority. Supported Fleet routes
  become thin HTTP adapters over owner capabilities; the WebUI Redis claim
  store and timeout authority are deleted.

## Replacement and deletion map

| Delete from `internal/webui` | Replacement seam |
|---|---|
| `agentmodules` | capability-grouped route assembly in `webui/app` |
| `storeadapter` | Workspace queries, local-placement port, `app/query` |
| `filecoord` | Source Control Browse, Mutate, and Checkout interfaces |
| `sessioncoord` | owner queries plus Run Capture Read Projection |
| `terminal` | Interaction plus `infra/pty` and WebSocket delivery |
| `agentcoord` | Agents, Interaction, Source Control, and Run Capture |
| `workspacecoord` | Workspace plus `app/workspaceprovisioning` |
| `sourcecontrolcoord` | Source Control plus cross-owner application query |
| `readprojection` | `internal/app/query` |
| `coordinator`, `hooks` | typed lifecycle composition in `app/serve` |
| `fleet` | `handlers/fleet` over canonical owner capabilities |
| `localredis` | `infra/localredis` plus owner-specific adapters |
| `subscription` | `webui/server/realtime` |
| `apperrors` | centralized HTTP error mapping in `server/handler` |
| `editor` | `infra/editor` behind a narrow launcher interface |

## Green implementation stack

1. Move transcript parsing and redaction to platform ownership.
2. Introduce Artifacts-backed Run Capture and delete `internal/sessions`.
3. Deepen Source Control and delete file and Git coordinator modules.
4. Deepen Interaction, introduce the PTY adapter, and delete session and
   terminal coordinator modules.
5. Introduce Workspace placement and provisioning ports, then delete
   `storeadapter` and `workspacecoord`.
6. Introduce `internal/app/query` and delete cross-owner WebUI projections and
   `agentcoord`.
7. Simplify server composition, absorb route grouping, and delete
   `agentmodules`.
8. Relocate Fleet delivery, local Redis, editor, subscriptions, lifecycle
   hooks, and HTTP error mapping.
9. Enable the final import gate, run the full gate, and capture product proof.

Every stack entry must compile and pass its owning tests. No entry may leave a
forwarding package, alias, dual-write path, deprecated constructor, generic
registry, or composite Store behind.

## Architecture gates

The final default-deny import test enforces:

- `internal/webui/**` may import public capability interfaces, application
  queries, generated transport types, and delivery mechanisms.
- It may not import `internal/store`, `internal/ops`, `internal/bootstrap`,
  persistence adapters, FleetDB transports, PTY implementations, or local
  filesystem state.
- Capabilities and application workflows may not import `internal/webui/**`.
- WebUI handlers cannot directly control PTYs, execute Git, resolve local
  workspace paths, coordinate retries or admissions, or mutate product state
  outside an owner command.

Each moved behavior retains tests at its owning interface: path and symlink
security, optimistic-version races, Git history and repair, terminal reconnect
and replay rejection, session fencing, Run Capture authorization, restart and
crash recovery, and unchanged HTTP schemas. Final acceptance additionally
requires whole-server route/middleware coverage and real product-path evidence
for file browsing, terminal interaction, and run-history/transcript display.

## Complete decision ledger

This ledger maps every decision accepted during the architecture grilling. A
decision is not closed merely by moving files: its target seam and proof must
both be present.

### Round 1: scope and outcome

- **Q1 — Deepening scope.** Pursue all four remaining opportunities:
  composition (`agentmodules` and `webui/app`), the global state seam
  (`storeadapter` and coordinators), file operations (`filecoord`), and
  Interaction (`sessioncoord` and `terminal`). **Target:** the owning modules
  named below. **Proof:** every listed shallow package is either deleted or
  justified by the deletion test.
- **Q2 — WebUI responsibility.** `internal/webui` is delivery-only: HTTP
  mapping, authentication middleware, SSE/WebSocket framing, frontend assets,
  route registration, and presentation state. **Target:** `webui/{app,handlers,
  server,frontend}`. **Proof:** the default-deny import gate in this document.
- **Q3 — Success measure.** Success is determined by depth and the deletion
  test, not an arbitrary total package count. **Target:** every retained module
  must concentrate complexity behind a smaller interface. **Proof:** deleting
  it conceptually redistributes meaningful policy or mechanics across callers.
- **Q4 — Product compatibility.** Preserve public routes, payloads, status
  behavior, and user-visible workflows while changing internal ownership.
  **Target:** generated HTTP contracts and delivery adapters. **Proof:** contract
  tests and product-path regression evidence.

### Round 2: initial seam placement

- **Q5 — Agent route composition.** Absorb `agentmodules` route registration
  into `webui/app`; capability construction remains in `app/serve`. **Target:**
  capability-grouped route assembly. **Proof:** `agentmodules` is deleted and
  route precedence tests remain green.
- **Q6 — Store adapter replacement.** Replace `storeadapter` consumer by
  consumer; each caller requests an exact Workspace projection. Machine-local
  paths sit behind a Workspace-owned placement port. **Target:** Workspace and
  `app/query`. **Proof:** no consumer accepts the composite Store or
  `ops.WorkspaceData`.
- **Q7 — File-operation owner.** Source Control owns file browsing and
  mutation; Workspace supplies opaque catalog references through a narrow port.
  **Target:** Source Control. **Proof:** `filecoord` is deleted without moving
  its interface wholesale.
- **Q8 — Session and terminal owner.** Interaction owns its public lifecycle
  interface; PTY mechanics remain an adapter. `sessioncoord` does not survive
  as a public facade. **Target:** Interaction, Artifacts, Execution, and
  `infra/pty`. **Proof:** callers use owner-specific interfaces.

### Round 3: interfaces, order, and tests

- **Q9 — Composition inputs.** Group dependencies by capability and separate
  platform/server configuration from delivery dependencies. Do not replace the
  current broad structs with another giant struct. **Target:** `app/serve` and
  `webui/app`. **Proof:** no service locator, generic registry, or 48-field
  dependency object remains.
- **Q10 — Consumer-owned interfaces.** Define cohesive command/query
  interfaces at owner roots, grouped by shared authority and invariants rather
  than one interface per method. **Target:** capability roots. **Proof:** each
  delivery adapter receives only the operations it consumes.
- **Q11 — Migration order.** Replace `storeadapter`, deepen Source Control,
  deepen Interaction, collapse `agentmodules`, then enforce the delivery-only
  import rule. **Target:** the green stack, refined by Q65. **Proof:** every
  intermediate commit builds and passes its owning tests.
- **Q12 — Session relocation.** Relocate `internal/sessions` immediately in the
  migration stack; do not retain a transitional public adapter. **Target:**
  platform parsing/redaction, Artifacts, Execution, and Interaction. **Proof:**
  the old root package is deleted in the same stack.
- **Q13 — Test replacement.** Replace shallow-package tests with owner-interface
  behavior tests, adapter contracts, HTTP mapping tests, and product E2E.
  **Target:** each new seam. **Proof:** security, restart, concurrency, and
  product behavior remain covered before old tests are removed.

### Round 4: decomposing the legacy Sessions package

- **Q14 — Split by concern.** Artifacts owns captured content; Interaction owns
  AgentSession and TerminalSession; Execution owns DriverRun and TaskRun;
  platform owns transcript parsing and neutral redaction. **Target:** those four
  owners. **Proof:** no owner imports another merely to read the old archive.
- **Q15 — Canonical language.** A **Run Capture** is immutable evidence for one
  Execution run or Interaction session; the collection is the **Run Capture
  Archive**. **Target:** the glossary and application query language. **Proof:**
  code and docs stop using “session archive” as the generic cross-owner term.
- **Q16 — Disk compatibility.** Do not preserve the legacy
  `runtimeDir/sessions/<id>` layout or schema. No reader, migration, or
  dual-write is required. **Target:** clean Artifacts-backed storage. **Proof:**
  no compatibility code or tests remain.
- **Q17 — Intent-specific archive interfaces.** Do not create a general archive
  interface. Use capture/finalize, query/diagnose, cleanup/compact, and parse
  transcript interfaces. **Target:** Artifacts, `app/query`, management, and
  platform seams. **Proof:** no generic filesystem-style archive CRUD surface.
- **Q18 — Atomic relocation stack.** First move parsing/redaction; then migrate
  all remaining Sessions callers to Run Capture owners and delete Sessions.
  No aliases, forwarders, dual writes, or deprecated constructors. **Target:**
  stack entries 1 and 2. **Proof:** each entry is independently green.

### Round 5: Run Capture lifecycle

- **Q19 — Durable source of truth.** Existing Artifacts storage is the only
  durable evidence store. Run Capture is a projection, not a new database,
  table, aggregate, or archive store. **Target:** Artifacts plus `app/query`.
  **Proof:** no Run Capture persistence port exists.
- **Q20 — Evidence granularity.** Prompt, transcript, diff, logs, and reports
  are separate finalized Artifacts linked to the same owner. **Target:**
  Artifacts types and owner references. **Proof:** independent authorization,
  redaction, size limits, and reads remain possible.
- **Q21 — In-flight staging.** Bounded local staging may exist only inside the
  Artifacts adapter. Finalization uploads evidence and removes staging; startup
  recovery resumes/finalizes or marks abandoned staging failed. **Target:** the
  machine-local Artifacts adapter. **Proof:** crash/restart tests and no durable
  caller-visible staging archive.
- **Q22 — Cleanup and repair.** Cleanup invokes Run Capture retention/garbage
  collection through an Artifacts management interface. Doctor queries owners
  and Artifacts and repairs through authorized commands, never raw paths.
  **Target:** management adapters. **Proof:** commands retain their intent and
  exit behavior without filesystem access.
- **Q23 — Legacy files.** Stop reading and writing old session directories but
  never delete them automatically at startup. Provide explicit operator cleanup
  if removal is desired. **Target:** startup and cleanup behavior. **Proof:** no
  implicit destructive path exists.
- **Q24 — Write authority.** Run Capture has no write authority. Execution or
  Interaction authorizes the owner reference; Artifacts validates and persists
  evidence. **Target:** owner-fenced Artifact commands. **Proof:** cross-owner
  or unowned capture writes fail closed.
- **Q25 — Stack shape.** Use separate green changes for platform parsing,
  Artifacts capture capability, caller migration/deletion, Source Control,
  Interaction, and WebUI composition. **Target:** Q65 stack. **Proof:** no
  transitional release treats both archives as canonical.

### Round 6: Source Control and file operations

- **Q26 — Module shape.** Deepen the existing Source Control module rather than
  creating public file packages. Present cohesive Browse, Mutate, and Checkout
  interfaces. **Target:** `modules/sourcecontrol`. **Proof:** large
  implementation behind three narrow caller surfaces.
- **Q27 — Scopes.** Retain workspace, repository, and agent product scopes, but
  resolve them through an opaque `WorkspaceLayout` port. **Target:** Source
  Control consuming Workspace placement. **Proof:** Source Control neither
  reads Workspace persistence nor constructs paths from conventions.
- **Q28 — File security.** Source Control owns normalization, traversal and
  symlink rejection, containment, `.git` protection, versions, and race
  fencing. Authentication mints an explicit access grant; context-hidden file
  capabilities are removed. **Target:** Source Control and auth delivery.
  **Proof:** adversarial path and unmintable-grant tests.
- **Q29 — Git mechanics.** Source Control owns Git semantics; a private
  machine-local adapter executes commands. Do not export a generic Git helper
  or route operations through `internal/ops`. **Target:** Source Control adapter
  seam. **Proof:** all Git callers cross owner interfaces.
- **Q30 — Checkout repair.** Repair is a Source Control command that resolves
  authoritative references and invokes bounded provisioning mechanics. WebUI
  only maps input/output. **Target:** Checkout interface. **Proof:** repair
  authorization, validation, and failure classification tests.
- **Q31 — Bounds and caching.** Source Control owns traversal/search bounds,
  singleflight, cache invalidation, and mutation locking. Limits are injected
  configuration rather than WebUI constants. **Target:** Source Control
  implementation. **Proof:** bound, invalidation, cancellation, and race tests.
- **Q32 — Errors.** Source Control returns typed validation, not-found,
  forbidden, conflict, partial-result, and stale-version errors. **Target:**
  Source Control root. **Proof:** it imports no HTTP error package and handlers
  map each kind correctly.
- **Q33 — Transport neutrality.** Source Control types carry no JSON tags or
  HTTP precondition terminology. WebUI maps generated DTOs and headers to
  domain versions. **Target:** generated DTO and handler seam. **Proof:** public
  schema remains byte/behavior compatible.
- **Q34 — Source Control deletion gate.** Delete `filecoord`; remove path, Git,
  cache, repair, and concurrency policy from handlers; stop using `ops.FileOps`
  as the business interface. **Target:** completed Source Control slice.
  **Proof:** the old package and imports are absent and moved tests pass.

### Round 7: Interaction and terminals

- **Q35 — Cross-owner session reads.** Execution, Interaction, and Artifacts
  expose owner queries; `internal/app/query` composes Run Capture and other UI
  views. **Target:** owner roots and one application query package. **Proof:**
  `sessioncoord` and `webui/readprojection` are deleted.
- **Q36 — PTY adapter.** Interaction consumes a `TerminalRuntime` port;
  `internal/infra/pty` implements process, tmux, attachment, resize, buffering,
  and shutdown mechanics. **Target:** Interaction seam and PTY adapter.
  **Proof:** PTY code owns no session policy.
- **Q37 — WebSocket role.** Interaction authorizes and coordinates attachment;
  WebUI owns upgrade, framing, ping/pong, and disconnect translation only.
  **Target:** Interaction attach interface and realtime adapter. **Proof:**
  WebSocket handlers cannot spawn, kill, or select processes.
- **Q38 — Tab identity.** Interaction's TerminalSession state is canonical;
  tab metadata is its projection, not an independent Redis identity. **Target:**
  Interaction. **Proof:** a tab cannot outlive or diverge from its owner record.
- **Q39 — Active-tab preference.** Keep `active_tab` as delivery presentation
  state. It may persist narrowly but cannot infer liveness or mutate
  Interaction. **Target:** `webui/app`. **Proof:** active-tab code imports no
  runtime adapter or owner store.
- **Q40 — Terminal tokens.** Interaction authorizes an attachment; realtime
  delivery signs and validates a short-lived, one-use transport token carrying
  only that coordinate. Remove generic token methods from agent/terminal
  facades. **Target:** Interaction plus realtime auth. **Proof:** expiry,
  replay, workspace, user, and attachment-scope tests.
- **Q41 — Setup terminals.** Interaction owns `StartSetupSession`; an injected
  backend catalog maps an allowlisted backend/action pair to a launch
  specification. **Target:** Interaction and backend catalog port. **Proof:**
  arbitrary shell input cannot cross the interface.
- **Q42 — History and scrollback.** Interaction owns session history; Artifacts
  owns finalized transcript and scrollback content; Run Capture assembles them.
  Execution transcripts never route through Interaction. **Target:** owner
  queries. **Proof:** ownership and transcript authorization tests.
- **Q43 — Interaction transport neutrality.** Interaction and `app/query`
  return typed transport-neutral results; WebUI owns JSON and status mapping.
  **Target:** owner and delivery seams. **Proof:** neither imports WebUI DTOs or
  error types.
- **Q44 — Interaction deletion gate.** Delete `sessioncoord`, `terminal`, and
  `readprojection`; make Interaction canonical, Artifacts the content owner, and
  PTY mechanics adapter-only. **Target:** completed Interaction slice. **Proof:**
  reconnect, replay, isolation, resize, shutdown, scrollback, transcript, and
  run-history tests remain green.

### Round 8: composition and remaining coordinators

- **Q45 — Composition root.** `internal/app/serve` alone constructs capability
  modules, persistence adapters, and process runtimes. **Target:** `app/serve`.
  **Proof:** `webui/app.NewServer` constructs delivery resources only.
- **Q46 — Server inputs.** Replace broad `ServerConfig` with transport config,
  delivery runtime, and capability-grouped route dependencies. **Target:**
  WebUI assembly interface. **Proof:** no generic dependency bag or service
  locator replaces it.
- **Q47 — Route interfaces.** Each handler module consumes its smallest
  interface; `webui/app` groups and registers them while preserving precedence.
  **Target:** handler configurations and app assembly. **Proof:** focused route
  registration tests.
- **Q48 — Delete `agentmodules`.** Move only route grouping into `webui/app`;
  do not preserve its 48-field dependency object. **Target:** app assembly.
  **Proof:** package and equivalent giant struct are absent.
- **Q49 — Delete `storeadapter`.** Workspace owns catalog/topology and local
  placement; `app/query` composes multi-source views. **Target:** Workspace and
  application query seams. **Proof:** WebUI accepts no Store and builds no
  legacy WorkspaceData.
- **Q50 — Delete `agentcoord`.** Agent naming belongs to Agents, terminal and
  stop behavior to Interaction, evidence to Run Capture, Git/PR behavior to
  Source Control, and tokens to realtime delivery. **Target:** those owners.
  **Proof:** no replacement agent facade recombines them.
- **Q51 — Delete `workspacecoord`.** Workspace owns durable lifecycle and
  backend selection; Source Control owns materialization;
  `app/workspaceprovisioning` owns multi-step orchestration. Async operation
  status is durable, not process-local. **Target:** Workspace, Source Control,
  and named application workflow. **Proof:** process-local job registry and
  coordinator DTOs are absent.
- **Q52 — Delete `sourcecontrolcoord`.** Source Control owns Git reads and
  mutations; issue-to-agent diff resolution is a named application query.
  **Target:** Source Control and `app/query`. **Proof:** package deletion and no
  Work Items dependency inside Source Control.
- **Q53 — Typed workspace runtime.** Move workspace resource lifecycle from
  WebUI coordinators/hooks to typed `app/serve` composition. Use the platform
  runtime host for periodic global reconciliation; eliminate the string-keyed
  resource bag. **Target:** `app/serve` and platform runtime. **Proof:** typed
  registration/rollback/shutdown tests and no `map[string]any` registry.
- **Q54 — Delivery-owned mechanisms.** WebUI may own middleware, routing,
  SSE/WebSocket protocols, short-lived transport tokens, rate limiting,
  presentation preferences, assets, and JSON/status translation. **Target:**
  the four surviving WebUI areas. **Proof:** none owns product lifecycle or
  machine runtime policy.
- **Q55 — Construction tests.** Put capability composition tests in
  `app/serve`, route tests in `webui/app`, contract tests at narrow handler
  interfaces, and retain one whole-server route/middleware test plus product
  E2E. **Target:** test suites by seam. **Proof:** giant construction mocks are
  deleted rather than translated.
- **Q56 — Composition deletion gate.** Handlers accept no Store, FileOps, or
  GitOps; WebUI constructs no capabilities; `app/serve` is visibly the sole
  composition root. **Target:** completed composition slice. **Proof:** import
  scan, constructor scan, and package deletion checks.

### Round 9: final WebUI classification

- **Q57 — Fleet.** FleetDB is the sole shared claim authority. Preserve needed
  Fleet routes as thin owner adapters under `handlers/fleet`; delete WebUI
  Redis claim, timeout, and registry authority. **Target:** Execution and Work
  Items interfaces plus delivery handlers. **Proof:** contention/fencing tests
  exercise one authority and no `fleet:*` claim implementation remains in
  WebUI.
- **Q58 — Local Redis.** Move the generic embedded Redis/snapshot adapter to
  `infra/localredis`; replace tab/session stores through their owners; retain
  only narrow presentation state in WebUI. Non-session Redis snapshot
  compatibility is preserved. **Target:** infrastructure and owner adapters.
  **Proof:** `webui/localredis` is deleted without losing embedded FleetDB
  persistence.
- **Q59 — Realtime subscriptions.** Absorb Work Item mutation subscription and
  SSE broadcasting into `webui/server/realtime`. **Target:** realtime delivery
  module. **Proof:** `webui/subscription` is deleted and reconnect/cursor tests
  pass.
- **Q60 — HTTP errors.** Absorb `apperrors` into centralized HTTP error mapping
  under `server/handler`; owners return typed errors. **Target:** delivery error
  mapper. **Proof:** one mapping table covers every capability error kind.
- **Q61 — Editor adapter.** Move OS editor detection/launching to
  `infra/editor`; handlers consume a narrow launcher interface. **Target:**
  infrastructure adapter and delivery handler. **Proof:** WebUI executes no OS
  process directly.
- **Q62 — Canonical contracts.** OpenAPI is canonical; generated request and
  response DTOs map explicitly to owner types. Reject stale/malformed payloads
  rather than normalizing them. **Target:** generated transport package and
  handlers. **Proof:** generation/checksum guard and server/frontend contract
  tests.
- **Q63 — Default-deny imports.** Allow WebUI to import public capability
  interfaces, application queries, generated DTOs, and delivery mechanisms;
  deny stores, ops, bootstrap, persistence transports, PTYs, and filesystem
  state. Deny reverse capability-to-WebUI imports. **Target:** architecture
  test. **Proof:** positive graph and deliberate negative fixtures.
- **Q64 — Final WebUI shape.** Retain direct children `app`, `frontend`,
  `handlers`, and `server`; delete every other listed top-level WebUI package.
  **Target:** filesystem topology. **Proof:** exact direct-child ratchet plus
  the deletion test for survivors.
- **Q65 — Final implementation stack.** Execute the nine green entries in this
  document in order, with no forwarding packages, aliases, dual writes,
  deprecated constructors, generic registries, or composite Store. **Target:**
  stacked implementation changes. **Proof:** focused gates at every entry and a
  final full gate/product proof.
- **Q66 — Durable documentation.** Record Run Capture in the canonical
  glossary, the delivery-only WebUI decision in an ADR, the dependency
  direction and deletion tests here, and the no-disk-compatibility decision.
  **Target:** glossary, ADR-0001, and this plan. **Proof:** all three documents
  cross-reference the same ownership language.
