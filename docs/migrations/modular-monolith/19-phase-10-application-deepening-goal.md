# Phase 10 Application Deepening Goal

- **Status:** Superseded by the
  [Phase 10 consolidated deep-module goal](20-phase-10-consolidated-deep-module-goal.md)
- **Date:** 2026-08-12
- **Baseline:** Phase 9 Wave 9.41, 158 production packages
- **Scope:** Work Item move, PR-reviewer identity, event admission, workflow
  authoring, and `loom serve` composition
- **Related plan:** [Post-Phase-9 WebUI deepening](18-post-phase-9-webui-deepening-plan.md)
- **Decisions:** [atomic Work Item move](../../adr/0002-atomic-cross-workspace-work-item-move.md),
  [workflow bundle availability](../../adr/0003-separate-workflow-bundle-availability.md),
  and [private composed runtime](../../adr/0004-private-composed-serve-runtime.md)

> This document is retained as the complete application-deepening discussion.
> It is not the implementation authority. The consolidated Phase 10 goal
> incorporates its accepted ADRs, resolves overlap with the WebUI and six-
> candidate discussions, and replaces its five-stack manifest and provisional
> package forecast.

## Goal

Deepen five application seams that remain after capability extraction. Each
change must reduce what callers need to know, concentrate policy and failure
handling behind one interface, and delete the superseded path. Package deletion
is evidence, not the objective.

The target keeps named cross-capability workflows and independently
replaceable adapters. It does not merge capability owners or replace the
current packages with a generic application, service, repository, event, or
runtime facade.

Current development state is not production data. Existing Agent and Workflow
Catalog records require no migration or compatibility path. Incompatible state
must fail visibly and may be removed only through an explicit operator reset;
Loom must never delete workspaces, sessions, or runtime evidence automatically.

## Approved application seams

| Area | Deepened module | Required deletion or contraction |
|---|---|---|
| Cross-workspace Work Item move | `internal/app/workitemmove` over one atomic FleetDB command | Delete the sequential create/comment/close implementation and warning-only partial-success behavior |
| PR-reviewer identity | Agents-owned atomic managed-identity convergence; PR Review owns the versioned preset | Delete `internal/app/prreviewer` and its partial `RoleCommitted` result |
| Event admission | Automation-owned canonical admission implementation behind origin-specific application adapters | Delete duplicated normalization, copying, and command-construction behavior without merging provenance seams |
| Workflow authoring | `internal/app/workflowauthoring` owns the lifecycle; Workflow Distribution owns staging mechanics | Fold `internal/infra/workflowdistribution/authoring` into `internal/infra/workflowdistribution` and delete forwarding exports |
| Serve composition | One private composed runtime in `internal/app/serve` | Absorb and delete `internal/cli/serve/serveadapter`; remove exported capability factories and nil fallbacks |

`internal/app/agentprovisioning` and `internal/app/connectorgrants` remain. Both
pass the deletion test: removing either would scatter durable recovery,
generation fencing, or least-privilege replacement policy across callers.

## Stack A — atomic Work Item move

`internal/app/workitemmove` owns a consumer-defined atomic-command port. A new
`internal/app/workitemmove/fleetdb` adapter implements it around the paired
FleetDB command, and `internal/app/serve` injects the adapter. Workspace and
Work Items expose only the queries and authorities needed to validate the
request; neither receives the low-level FleetDB transport.

The operation has these invariants:

- source and target belong to the same FleetDB deployment;
- the caller is authorized in both workspaces;
- source ID, exact source revision, target workspace, and request ID form the
  idempotency intent;
- exact replay returns the original target, while a changed source revision or
  target conflicts;
- claimed, assigned, running, or dependency-connected Work Items cannot move;
- the target receives a target-workspace ID and starts `open` and unassigned;
- user-maintained fields move, but claims, runs, sessions, assignees, transient
  status, comments, and activity do not;
- the source becomes immutable and `closed`, retains its history, and records a
  clickable `moved_to` reference;
- the target records `moved_from`; and
- FleetDB either creates the target and closes the source together or changes
  neither.

The HTTP response retains `source_id` and `target_id`; warning-only partial
success is removed. The UI shows a prominent move banner on the source and
rejects edits, comments, claims, and reopening.

## Stack B — Agents-owned reviewer convergence

PR Review owns a stable reviewer-preset identity plus explicit revision or
fingerprint. The preset describes the role name, prompt selector, Agent kind,
desired state, and runtime metadata. Agents owns validation and one atomic
managed-identity command that converges the shared workspace Role and the
checkout-specific Agent together.

Composition derives one purpose-scoped system authority for this exact
operation. It does not expose separate Role and Agent authorities or a generic
Agents issuer. The shared Role remains after a review; each deterministic
checkout-specific Agent is archived idempotently when its review checkout or
session is removed.

No existing reviewer identity is adopted or migrated. Conflicting development
records may be explicitly removed and recreated. The implementation deletes
`internal/app/prreviewer`, its constants, its three public interfaces, and the
partial persistence-step result.

## Stack C — Automation-owned admission implementation

`internal/app/webhookingestion`, `internal/app/workfloweventing`, and
`internal/app/systemeventing` remain named application adapters because they
represent different trust origins. They keep distinct verification and typed
authority seams:

- webhook verification covers the exact received bytes before JSON
  normalization or validation;
- workflow events require a verified running Execution parent and its typed
  authority; and
- system events require a registered internal producer and action-scoped
  system authority.

After provenance is verified, all three enter one private Automation
implementation for canonical content validation, defensive copying, command
construction, and admission. There is no generic caller-constructible
provenance envelope or interchangeable authority type.

Origin-specific verification and authority failures remain distinct until the
handoff. After handoff, canonical Automation errors apply. Shared invariant
tests move to the Automation interface; each adapter retains tests for
provenance, authority derivation, exact-byte behavior, and mapping.

## Stack D — durable workflow bundle availability

`internal/app/workflowauthoring` owns one lifecycle for built-in, native, and
global-runner sources. Filesystem layout, packaged source, build toolchain,
staging, and content promotion remain adapter mechanics in
`internal/infra/workflowdistribution`; the nested `authoring` package and its
forwarding aliases are deleted.

Workflow Catalog records validation and bundle availability as different
invariants. Availability has explicit pending, available, and failed states:

1. author a pending version with immutable source and bundle digests;
2. promote content to its digest-addressed location;
3. verify the promoted digest;
4. mark the version available; and
5. approve or activate only an available version.

A currently active version remains active until its successor becomes
available. Dispatch fails closed when an available version's content is
missing or drifted. Restart reconciliation retries bounded transient
filesystem or process failures and marks digest mismatch, containment
violation, or invalid staged metadata terminally failed. Unreferenced staged or
promoted bundles are purged only after a bounded retention period.

Current Workflow Catalog development data receives no availability backfill.
A fresh state is required for this stack.

## Stack E — one private composed runtime

`internal/app/serve` becomes the only capability and runtime composition root.
It absorbs the composition behavior currently split through
`internal/cli/serve/serveadapter`, then deletes that package and its exported
capability construction surface.

The host supplies validated typed configuration and an explicit runtime
profile. Composition does not read ambient environment variables, infer a
profile from nil dependencies, or start work during construction. The returned
runtime exposes only HTTP handling, explicit start, graceful close, and health:

- construction validates and assembles without starting goroutines or
  listeners;
- `Start` begins registered runtime work and rolls partial startup back in
  reverse order; and
- `Close` is graceful and idempotent.

CLI and Desktop retain listener binding, signal handling, OS integration,
environment parsing, paths, and process policy. Tests build the production
runtime with substituted adapters instead of calling private capability
constructors.

This stack depends on the composition and delivery work in the
[WebUI deepening plan](18-post-phase-9-webui-deepening-plan.md). It must follow
or incorporate that plan's route and server-composition cleanup rather than
creating a second WebUI composition path.

## Paired contracts and compatibility

The Work Item move, reviewer convergence, and workflow-availability changes
use paired FleetDB contracts. FleetDB capability keys are mandatory for the
enabled Loom profile; missing support fails readiness. There is no Loom-side
fallback sequence, deprecated endpoint, dual write, or compatibility facade
for the unfinished stack.

Paired contract changes land FleetDB-first within each logical stack, followed
by the Loom owner and consumer changes. A previous Loom binary may be used for
rollback only when it remains compatible with the additive portion of the
paired FleetDB deployment; the new Loom never weakens its capability-key
requirements.

## Stacked delivery

| Stack | Scope | Required proof before the next stack |
|---|---|---|
| A | Atomic Work Item move and paired FleetDB command | Owner-interface behavior, Redis/Postgres atomicity, replay/conflict/concurrency, HTTP/UI behavior, focused architecture checks |
| B | Agents-owned reviewer convergence | Paired command contract, preset drift, concurrency, creation/archive UI journeys, retirement guard |
| C | Automation admission consolidation | Shared owner invariants plus all three provenance adapters and security-negative paths |
| D | Workflow availability lifecycle and Workflow Distribution fold | Transition fault injection, restart reconciliation, digest drift, approval/activation denial, retirement guard |
| E | Private composed runtime and `serveadapter` deletion | Bootstrap-interface tests, start rollback, graceful close, route/runtime parity, retirement guard |

Each stack is a separately reviewable PR. Run the aggregate Loom and FleetDB
gates at every paired contract milestone. The final stack additionally requires
packaged Desktop product proof for atomic move and clickable history, reviewer
identity/transcript attribution and retirement, all three event origins,
authoring restart and fail-closed digest drift, and runtime startup, rollback,
health, and shutdown.

## Package topology

The conservative planned package result is:

| Change | Packages |
|---|---:|
| Phase 9 Wave 9.41 baseline | 158 |
| Add `internal/app/workitemmove/fleetdb` | +1 |
| Delete `internal/app/prreviewer` | -1 |
| Delete `internal/infra/workflowdistribution/authoring` | -1 |
| Delete `internal/cli/serve/serveadapter` | -1 |
| **Planned maximum after all five stacks** | **156** |

The exact package inventory is refreshed only after the final physical shape is
reviewed. The current 25-file `app/serve` ceiling is not preserved
artificially: forwarding files are deleted, cohesive composition is absorbed,
and a new shrink-only exact inventory records the justified result.

## Completion criteria

This goal is complete only when:

- all five replacement interfaces and implementations are active;
- the sequential move, partial reviewer result, duplicated admission behavior,
  nested authoring adapter, and exported serve factories are absent;
- retired paths, constructors, aliases, DTOs, and fallbacks are covered by
  cannot-return architecture guards;
- paired FleetDB capability negotiation and Redis/Postgres parity pass;
- tests exercise behavior through the interfaces callers use;
- direct-write, mutation, runtime, import, and package inventories are exact
  and no allowance is widened to make the change pass;
- full Loom and FleetDB gates pass; and
- packaged product proof covers the approved success, replay, crash, conflict,
  restart, and fail-closed journeys.
