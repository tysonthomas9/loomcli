# Phase 10 Consolidated Deep Module Goal

- **Status:** Final approved Phase 10 decision; implementation in progress
- **Normative authority:** Sole Phase 10 architecture and delivery plan
- **Implemented:** Stack 10.1 canonical generated HTTP contract seam
- **Decision last amended:** 2026-08-13
- **Baseline:** Phase 9 Wave 9.41, 158 production packages
- **Supersedes:** the
  [Post-Phase-9 WebUI deepening discussion](18-post-phase-9-webui-deepening-plan.md),
  the
  [Phase 10 application deepening discussion](19-phase-10-application-deepening-goal.md),
  the separate six-candidate Phase 10 discussion,
  the later outside-package deepening discussion,
  and the later Source Control/package-unification discussion
- **Supporting decisions:**
  [WebUI is delivery-only](../../adr/0001-webui-is-delivery-only.md),
  [atomic Work Item move](../../adr/0002-atomic-cross-workspace-work-item-move.md),
  [workflow bundle availability](../../adr/0003-separate-workflow-bundle-availability.md),
  [private composed runtime](../../adr/0004-private-composed-serve-runtime.md),
  and [Artifacts evidence policy](../../adr/0005-artifacts-own-evidence-policy.md)

## Decision precedence

This document is the only normative Phase 10 architecture and implementation
plan. If an earlier discussion, session conclusion, stack order, package
forecast, or completion criterion conflicts with this document, this document
wins.

| Record | Final status | Treatment |
|---|---|---|
| This consolidated deep-module goal | **Final and normative** | Use its ownership decisions, twelve-stack manifest, gates, and completion criteria. |
| [Post-Phase-9 WebUI deepening discussion](18-post-phase-9-webui-deepening-plan.md) | **Superseded** | Retain only as historical rationale and its complete question ledger. |
| [Phase 10 application deepening discussion](19-phase-10-application-deepening-goal.md) | **Superseded** | Retain only as historical rationale for the accepted supporting ADRs. |
| Separate six-candidate Phase 10 session discussion | **Superseded** | Its accepted conclusions are incorporated here; its separate stack and unresolved alternatives have no authority. |
| Later outside-package deepening session discussion | **Superseded** | Its deletion-test and package-locality conclusions are incorporated below; its candidate list and package-count targets have no independent authority. |
| Later Source Control/package-unification session discussion | **Superseded** | Its accepted Git consolidation is incorporated into the Source Control decision below; its alternative boundaries and sequencing have no independent authority. |
| Any other Phase 10 planning session or proposal not ratified by an explicit amendment to this document | **Superseded** | It may be retained as research or rationale, but it cannot change ownership, interfaces, stack order, gates, or completion criteria. |

ADRs 0001 through 0005 are accepted supporting decisions, not competing Phase
10 plans. Per-stack evidence records describe what was implemented and proved;
they cannot silently revise this architecture. Any future change to the final
decision requires an explicit amendment here and, when architectural, a new or
superseding ADR.

### Cross-session consolidation

The two later discussions refine this decision; they do not add new stacks or
parallel authorities:

- **Outside-package reduction:** the target is less knowledge spread outside
  capability owners, not the smallest possible package count. Move product
  policy, aggregate behavior, and persistence-shaped interfaces to their
  owners. Retain a package outside `internal/modules` only when deleting it
  would duplicate a real protocol, security, operating-system, runtime, build,
  or independently replaceable mechanism across callers. Stacks 10.3, 10.6,
  and 10.11 perform this work and tighten the exact inventory after each
  deletion.
- **Git package convergence:** consumers receive the single Source Control
  module through Browse, Mutate, and Checkout ports. Local Git mechanics,
  stack persistence, forge publication, and credential brokering remain
  private adapters because they cross distinct earned seams. Consolidation
  removes duplicate public Git/FileOps policy and coordinators; it does not
  flatten those mechanisms into one large implementation package. Stack 10.3
  is the sole delivery authority for this change.

Any conclusion from a superseded discussion that is not restated in this
document is rejected rather than implicitly deferred.

## Goal

Complete Phase 10 as one ordered program of deep-module consolidation. Each
stack must concentrate behavior behind a small owner or application interface,
delete the superseded path in the same PR, and leave the repository green for
the next stack. Package deletion is evidence of improved depth and locality,
not the objective by itself.

The final architecture keeps capability ownership explicit, retains named
cross-capability application workflows, and preserves only adapters that cross
a real protocol, security, runtime, or independently replaceable mechanism
seam. It does not introduce generic service, repository, coordinator, Store,
Ops, dependency-bag, or runtime facades.

Public routes, canonical payloads, status behavior, and product workflows stay
compatible unless this decision explicitly replaces a development-only state
model. Removed internal paths receive no forwarding packages, aliases, dual
writes, deprecated constructors, silent fallbacks, or normalization shims.

## Canonical ownership and interface decisions

### Owner planes and Read Projections

Production `internal/domain`, `internal/store`, and `internal/ops` are retired.
Product state, commands, and queries belong to their capability owner. FleetDB
and memory adapters map at owner seams. Only shared HTTP, process, filesystem,
and backend-format mechanics may remain under platform or infrastructure.

Cross-owner UI reads use named immutable Read Projections in
`internal/app/query`. A Read Projection owns no product state and cannot mint
authority or mutate a participating owner. Ordinary callers consume owner
intent and query interfaces; persistence ports are private to adapters and
composition.

### Canonical HTTP contracts

OpenAPI is the canonical HTTP schema. Generated Go and TypeScript transport
types map explicitly to transport-neutral owner commands and results. Stale or
malformed payloads fail visibly; the server and frontend receive no handwritten
compatibility DTO or normalization path.

### Source Control

Source Control is one deep module presented through three cohesive,
consumer-defined ports:

- **Browse:** list, read, stat, index, search, history, blame, and diff;
- **Mutate:** conditional write, delete, create directory, and move; and
- **Checkout:** materialization, status, repair, branch operations, stack
  lineage and publication, and pull-request operations.

Workspace supplies Repository References and approved local placement. Source
Control owns Git semantics, path normalization, containment and symlink safety,
`.git` and sensitive-path protection, optimistic versions, traversal bounds,
caching, invalidation, mutation locks, recovery, and the complete publication
transaction. Its earned private adapters are local Git, stack persistence,
forge publication, and credential brokering. The old FileOps/GitOps seams and
WebUI file and Source Control coordinators are deleted.

### Artifacts, Run Capture, and Transcript Evidence

Execution owns DriverRun and TaskRun lifecycle. Interaction owns AgentSession
and TerminalSession lifecycle. Artifacts owns durable evidence content and its
policy. Prompts, transcripts, diffs, logs, reports, and finalized scrollback are
separate Artifacts grouped by their lifecycle owner.

A Run Capture is the immutable authorized Read Projection that groups the
available evidence for one run or session. Transcript Evidence is the
transcript facet within that Run Capture. The Run Capture Archive is the
queryable collection of captures; none of these projections is a store or
write authority.

Backend-specific parsing and mechanical redaction are private platform
adapters behind the Artifacts evidence-policy seam. Artifacts owns what must be
redacted, authorization, the durable format, a 63 MiB bounded capture policy,
explicit truncation provenance, finalization, and visible missing, corrupt, or
capture-failed states. Live output is ephemeral observation. Durable reads do
not fall back to local session files.

Evidence capture failure does not rewrite an otherwise successful work
outcome, but it produces a separate durable, UI-visible evidence status. Legacy
session archives receive no reader, migration, or dual write and are never
deleted automatically; an explicit operator cleanup path may remove them.

### Interaction and terminal runtime

Interaction owns AgentSession, TerminalSession, tab identity, terminal
lifecycle, inbox, chat, attachment authorization, and session history. Its
`TerminalRuntime` port is implemented by a private `internal/infra/pty` adapter
that owns process, PTY, tmux, resize, buffering, and shutdown mechanics.

WebUI owns only WebSocket upgrade, framing, ping/pong, disconnect translation,
and short-lived one-use attachment-token encoding. Active-tab preference is
presentation state and cannot infer or mutate Interaction lifecycle.

### Workspace and Repository Admission

Workspace owns its catalog, topology, Repository References, durable workspace
operations, registration state, and backend selection. Source Control owns
repository materialization.

`internal/app/repositoryadmission` owns the recoverable workflow for admitting
one or more repositories during Workspace creation or afterward. FleetDB is
authoritative for durable operation state, exact-intent idempotency, generation
fencing, and replay. A machine-local journal may retain only materialization
and cleanup facts required for crash recovery. The UI polls the durable
admission ID; no process-local job registry remains.

Repository removal is not the inverse command. A later Repository Retirement
workflow must define its own safety and evidence-retention policy.

### Atomic Work Item move

`internal/app/workitemmove` consumes one atomic FleetDB command that creates the
target and closes the source together. It authorizes both workspaces and binds
idempotency to the source ID, exact revision, target workspace, and request ID.
Claimed, assigned, running, or dependency-connected sources cannot move.

The immutable closed source retains history and a clickable `moved_to`; the
open unassigned target records `moved_from`. Exact replay returns the original
target and divergent replay conflicts. The sequential create/comment/close
path and warning-only partial success are deleted.

### Reviewer identity convergence

PR Review owns a versioned reviewer preset. Agents owns one atomic managed-
identity command that converges the shared Role and checkout-specific Agent.
Composition derives a purpose-scoped authority for that exact operation and
does not expose separate low-level Role and Agent issuers.

The shared Role remains; each deterministic checkout Agent is archived
idempotently when its review checkout or session is removed. Existing
development identities are not adopted silently. `internal/app/prreviewer`
and its partial `RoleCommitted` result are deleted.

### Automation event admission

Webhook, workflow, and system event application adapters remain distinct
because they prove different trust origins. Webhooks verify the exact received
bytes, workflow events prove a running Execution parent, and system events
prove a registered producer and action-scoped authority.

After provenance is verified, all origins enter one private Automation
implementation for canonical validation, defensive copying, command
construction, and admission. There is no caller-constructible generic
provenance envelope or interchangeable authority type.

### Workflow Distribution and bundle availability

Workflow Authoring owns the authoring lifecycle. Workflow Distribution locates
source, validates layout, builds, stages, promotes, and verifies immutable
content-addressed bundles while reporting source digest, bundle digest, trust,
and provenance. Filesystem and build-tool integrations are private injectable
adapters; built-in, native, and global-runner layouts are policies rather than
public interfaces. The nested forwarding `workflowdistribution/authoring`
package is deleted.

Workflow Catalog records validation and bundle availability as separate
invariants. The canonical sequence is:

1. persist a pending immutable version with source and expected bundle digests;
2. promote content to its digest-addressed location;
3. verify the promoted digest and provenance;
4. mark the version available; and
5. approve, activate, or execute only an available version.

A pending record is durable recovery state, not executable admission. The
active predecessor remains active until its successor becomes available.
Restart reconciliation may retry bounded transient promotion failures. Digest
mismatch, containment violation, or invalid metadata marks availability
failed; dispatch fails closed on missing or drifted content. Development
Workflow Catalog state receives no backfill and must be recreated explicitly.

### Private composed runtime and delivery-only WebUI

`internal/app/serve` is the sole capability and runtime composition root. It
accepts validated typed configuration and a runtime profile, constructs no
ambient dependencies, and returns one private runtime interface for HTTP,
explicit start, graceful idempotent close, and health. `Start` rolls partial
startup back in reverse order. CLI and Desktop retain environment parsing,
listener and signal mechanics, OS integration, paths, and process policy.

`internal/webui` is delivery-only. Its intended direct children are `app`,
`frontend`, `handlers`, and `server`. It owns HTTP mapping, middleware,
generated DTO mapping, SSE and WebSocket protocols, short-lived transport
tokens, rate limiting, presentation state, route registration, and assets. It
does not construct capabilities, control PTYs, execute Git, resolve local
workspace paths, coordinate recovery, own task claims, or mutate product state
outside owner commands.

The parallel `internal/cli/serve/serveadapter` composition plane and the
screen-oriented WebUI coordinator packages are deleted. FleetDB remains the
only shared task-claim authority.

## Canonical stacked delivery manifest

Each numbered entry is one separately reviewable PR. Logical commits inside a
PR keep paired contracts, owner behavior, consumer migration, deletion, and
documentation legible, but the PR may not leave a live compatibility path.

| Stack | Scope | Required proof before the next stack |
|---|---|---|
| 10.1 | Canonical generated HTTP contract seam | Generation/checksum guard, server/frontend contract tests, explicit stale and malformed rejection |
| 10.2 | Artifacts policy, private parsing adapters, Run Capture, and Transcript Evidence | Authorization, redaction, bounds, truncation, finalization, failure-state, restart, and UI evidence tests; delete Sessions paths |
| 10.3 | Deep Source Control including file operations | Browse/Mutate/Checkout interface tests, traversal/symlink/security races, Git recovery/publication tests, UI file and PR journeys; delete FileOps/GitOps coordinators |
| 10.4 | Interaction and private PTY adapter | Attach/replay fencing, reconnect, resize, buffering, shutdown, history, scrollback, and terminal product journeys; delete session/terminal coordinators |
| 10.5 | Workspace and Repository Admission | Create-plus-admit, later admission, exact replay, concurrent admission, restart, cleanup, failure, and durable UI polling; delete process-local provisioning paths |
| 10.6 | Owner-plane deletion and `app/query` | Owner-by-owner adapter/query tests, cross-owner projection tests, import/deletion guards, and product UI reads; delete production `domain`, `store`, and `ops` planes |
| 10.7 | Atomic Work Item move with paired FleetDB command | Redis/Postgres atomicity, replay/conflict/concurrency, authorization, immutable source, clickable UI history |
| 10.8 | Agents-owned reviewer convergence with paired FleetDB command | Preset drift, concurrent convergence, creation/archive journeys, transcript attribution, retirement guard |
| 10.9 | Automation admission consolidation | Shared owner invariants, all three origin adapters, exact-byte and security-negative paths |
| 10.10 | Workflow Distribution and durable availability with paired FleetDB lifecycle | Transition fault injection, restart reconciliation, digest drift, approval/activation denial, active-predecessor preservation, retirement guard |
| 10.11 | Private composed runtime and delivery-only WebUI | Construction/start/rollback/close tests, route/runtime parity, exact WebUI topology, coordinator and `serveadapter` deletion |
| 10.12 | Final ratchets and packaged product proof | Full Loom and FleetDB gates, exact architecture inventories, and packaged Desktop success/crash/conflict/restart/fail-closed journeys |

Stacks 10.7, 10.8, and 10.10 land additive FleetDB contracts first, followed by
the Loom owner and consumer changes. The enabled Loom profile requires the
paired capability keys; absence fails readiness, with no Loom-side fallback.

Every stack must add or tighten import, retired-root, constructor, direct-write,
generated-contract, and shrink-only inventory guards. Tests exercise behavior
through the same interfaces used by production callers. Product E2E is
required for every user-visible stack; stack 10.12 runs the complete packaged
Desktop matrix rather than substituting API-only evidence.

Implementation evidence is recorded per stack. Stack 10.1 is documented in
[Phase 10.1 generated HTTP contract evidence](21-phase-10-1-generated-http-contract-evidence.md).

## Superseded decisions and alternatives

The following earlier decisions are explicitly replaced:

- **Verify before version registration** is replaced by durable pending
  Workflow Catalog state. Verification remains mandatory before availability,
  approval, activation, or execution.
- **Platform owns parsing and redaction** is replaced by Artifacts-owned
  evidence policy with private platform parsing and mechanical-redaction
  adapters.
- **`internal/app/workspaceprovisioning`** is replaced by the canonical
  `internal/app/repositoryadmission` workflow name.
- **Transcript Evidence as the complete run record** is replaced by Run Capture
  as the grouping projection, with Transcript Evidence as one facet.
- **Separate six-, nine-, and five-entry Phase 10 stacks, plus later
  outside-package and Source Control candidate sequences,** are replaced by
  the twelve-entry manifest in this document.
- **The provisional 156-package forecast** is withdrawn. Each stack records an
  exact shrink-only inventory after its physical shape is reviewed; no package
  count justifies a shallow interface.

The four earlier ADRs are not superseded. They remain accepted constraints and
are incorporated into this goal together with ADR-0005.

## Completion criteria

Phase 10 is complete only when:

- every canonical owner and application interface above is active and used by
  production callers;
- every named legacy plane, coordinator, forwarding package, alias, fallback,
  and partial-success path is absent and guarded against return;
- WebUI is delivery-only and `app/serve` is the sole composition root;
- OpenAPI and generated transport types are the only shared HTTP contract;
- FleetDB capability negotiation and Redis/Postgres parity pass for every
  paired command;
- direct-write, mutation, import, runtime, package, and generated-contract
  inventories are exact and no allowance is widened merely to pass;
- full Loom and FleetDB gates pass; and
- packaged Desktop proof covers success, replay, concurrency, crash, restart,
  conflict, evidence failure, digest drift, and fail-closed behavior.
