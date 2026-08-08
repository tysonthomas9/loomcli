# Phase 8 Consolidation and Evidence

- **Status:** Complete
- **Base:** Phase 7 completion commit `46bb9a8416d291d686cf32c58fa872a097cdba3d`
- **Implementation head:** `35e61b31b879a6f0fb0b1f7f18a8491b187b0f1d`
- **Companion FleetDB:** unchanged at `b71dec551082fc10e1d8ceab11a645f46d308a62`
- **Purpose:** Remove structural fragmentation and duplicated product knowledge
  without weakening capability ownership or ports-and-adapters boundaries.
- **Target:** At most 190 production Go package directories under `internal/`.

## Why Phase 8 exists

Phase 7 completed the ten capability owners and their default-deny dependency
graph, but the completed tree contains 250 production Go package directories:
18 under `internal/modules`, 232 outside those module roots, 115 with one
production Go file, and 141 with one or two production Go files. Package count
was intentionally diagnostic during capability extraction. Leaving it
unratcheted after extraction, however, permits package-per-constructor,
package-per-interface, parallel model, and compatibility-mapper structures to
survive indefinitely.

Phase 8 treats those counts as architecture-shape constraints, not as a
replacement for ownership checks. A reduction is valid only when it removes
duplicated knowledge or a forwarding-only structure while preserving the
approved dependency direction.

## Boundary test

A separate production package must have at least one durable reason to exist:

- a distinct external system or wire protocol;
- a distinct credential, privilege, or cryptographic boundary;
- a distinct runtime, platform, or build-tag boundary;
- an independently replaceable adapter with a stable owner-owned port;
- multiple consumers of a stable abstraction; or
- a documented cycle-breaking seam that cannot be removed by correcting
  dependency direction.

File count alone neither justifies nor disqualifies a package. A one-file
package that only aliases a type, forwards a constructor, or adapts one sibling
package to another sibling package fails this test.

## Required consolidation outcomes

1. **Work Items:** one capability-owned model and one status/type/validation
   policy; no `types -> entity -> backend -> HTTP` internal representation
   chain.
2. **Workspace:** one owner for workspace identity and naming validation.
3. **Source Control:** one stack model and lineage-ordering policy; the local
   stack store implements Source Control ports without a sibling forwarding
   adapter.
4. **Connectors and Automation:** one owner for connector status/provider
   policy and automation delivery-idempotency derivation.
5. **Process security:** one subprocess-environment policy with explicit trust
   profiles; no broad ambient credential fallback.
6. **HTTP transport:** one bounded JSON decoder and one common
   authentication/authority error classifier, with capability-specific error
   mapping kept at the capability adapter.
7. **Composition:** CLI, WebUI, and serve composition use ordinary files in a
   composition package rather than nested forwarding packages.
8. **Legacy buckets:** `internal/domain`, `internal/entity`, `internal/types`,
   and the composite `internal/store` are shrink-only until their capability
   models and ports have moved to their declared owners.

## Ratchets

The checked-in production-package inventory is exact and shrink-only:

- a new production package fails unless its architecture decision is reviewed
  together with a refreshed inventory;
- deleting a package makes the old inventory stale and therefore forces the
  ceiling down in the same change;
- total, module, outside-module, one-file, and one-or-two-file counts cannot
  increase; and
- generated-only packages count as compiled dependency-graph nodes, while
  semantic duplication scans may exclude generated declarations.

Each consolidation stack must also reduce at least one knowledge-level debt:
duplicate models, duplicate policy lists, duplicate validation, duplicate
mapping hops, parallel mutation paths, or forwarding-only packages.

## Delivery waves

| Wave | Scope | Required proof |
|---|---|---|
| 8.1 | Exact shape ratchet and low-risk forwarding-package collapse | Focused architecture and affected-package tests |
| 8.2 | Work Items and Workspace canonical models/policies | Characterization tests plus WebUI/CLI contract tests |
| 8.3 | Security, HTTP, Source Control, Connectors, and Automation policy consolidation | Negative authorization, credential, replay, and validation tests |
| 8.4 | Legacy model/store bucket retirement and remaining composition collapse | All architecture profiles and full Go/frontend gates |
| 8.5 | Exact packaged/local product acceptance | Existing Phase 7 journey matrix with transcript, diff, restart, and fail-closed evidence |

## Completion gate

Phase 8 completes only when:

- production package directories under `internal/` are 190 or fewer;
- the exact shape inventory and every existing capability/import/ownership
  architecture check pass;
- no consolidation replaces the removed packages with a generic `common`,
  `shared`, `models`, or `services` business-logic bucket;
- the required consolidation outcomes above have one canonical owner each;
- focused tests, the aggregate gate, and relevant frontend tests pass; and
- the packaged/local acceptance matrix proves unchanged user-visible behavior,
  durable transcripts/diffs, restart recovery, and unavailable-backend
  fail-closed behavior.

## Completion result

Phase 8 removed 61 production package directories and closed below the hard
ceiling without introducing a generic business-logic bucket. Relative to the
Phase 7 base, the implementation changes 493 files with 11,121 insertions and
19,865 deletions: a net reduction of 8,744 lines.

| Shape measure | Phase 7 | Phase 8 | Change |
|---|---:|---:|---:|
| Production packages under `internal/` | 250 | 189 | -61 |
| Packages under `internal/modules/` | 18 | 17 | -1 |
| Packages outside `internal/modules/` | 232 | 172 | -60 |
| One-file packages | 115 | 67 | -48 |
| One-or-two-file packages | 141 | 89 | -52 |

`production-package-shape.yaml` records the exact 189-package inventory and
all five ceilings. The guard rejects additions, stale deleted rows, or any
increase in total, module, outside-module, one-file, or one-or-two-file counts.
It permits a mechanical refresh only when the package set shrinks.

## Delivered consolidation

| Area | Result |
|---|---|
| Work Items and Workspace | Canonical model, status, validation, identity, and naming policy live with their capability owners; the dead representation chain and forwarding policy packages are gone. |
| FleetDB adapters | Capability transports and operation fragments are folded into owner adapters while the shared low-level FleetDB client remains one infrastructure boundary. |
| Source Control | Stack lineage and ordering policy are colocated with the Source Control owner; local persistence remains an adapter behind owner ports. |
| Interaction and Usage | UI persistence and usage projection logic are consolidated without merging their aggregates. |
| CLI and WebUI composition | Forwarding-only serve, file, automation-route, projection, and request-policy packages are ordinary files in their owning composition or adapter package. |
| HTTP and authority | Bounded decoding and authentication/authority classification are shared platform mechanisms; capability-specific error mapping remains at each adapter. |
| Runtime security | One trust-profile subprocess environment policy owns filtering. Trusted local task runners preserve the closed backend executable-override set; remote workflows remain fail-closed. |
| Packaged command resolution | Host bridge, bundled runner, and terminal paths pin the packaged sibling `loom` executable, including login-shell startup, instead of allowing an older global binary to win `PATH`. |

The consolidation preserves all ten declared capability roots. The final
architecture guard reports Store `15/0`, 26 legacy handler exceptions, 98
direct-write rows, 107 mutation commands, 71 runtime components, 80 goroutine
launch definitions, six measured performance rows, zero pending architecture
decisions, and all 11 build profiles plus the all-files AST pass. Visible
measured candidate passes peaked at 1,195.3 MiB and 1,236.4 MiB; the exact final
aggregate gate also passed the same 2,048 MiB architecture-memory ceiling.

## Product acceptance

The packaged Desktop workspace `PHASE8-PROOF-20260808` exercised all eight
local creation templates with two successful real-Codex runs per template:
Behavior Planner, Coder, Bug Triage, Documentation Review, Local Review,
interactive Lead, interactive PR Review, and interactive Custom. GitHub-backed
mutations were not required for this Loom-only consolidation.

The targeted reliability rows additionally proved:

- configured and live agent projections render once, using durable ID or name
  identity, and switching agents preserves transcript loading;
- all agents, tasks, run history, transcripts, diffs, and repository state
  survive a packaged Desktop restart;
- task `PHASE8-PROOF-20260808-11` resolves bare `loom data` calls to the
  packaged sibling binary, rejects deletion while claimed with HTTP 409, and
  recovers the same owner-fenced run after the native Codex child is killed;
- task `PHASE8-PROOF-20260808-12` with an intentionally missing Codex binary
  fails in 0.4 seconds as `local_backend_unavailable`, records zero tokens and
  no transcript/diff, and remains Blocked for review;
- after restoring normal executable selection, task
  `PHASE8-PROOF-20260808-13` completes via `local-cli-codex` in 2m16s, saves
  its design, moves to Review, and exposes the durable transcript under run
  `automation-run-7998b4d097ff1cef69992806bf1ae3cb`; and
- the exact final gated tree `0709f1ebc` package rebuild starts on a fresh service port,
  reloads all 13 durable tasks and eight unique agent rows, and displays the
  completed canary transcript with every autonomous agent still disabled.

## Validation record

| Check | Exact result |
|---|---|
| Loom `make gate` on tree `0709f1ebc` | PASS: Go, frontend, lint, architecture, characterization, supervisor-disabled validation, race, coverage, and aggregate gate. Published implementation head `35e61b31b` has the same production tree plus bounded test-only CI synchronization. |
| FleetDB `make gate` at `b71dec551` | PASS; Phase 8 changes no FleetDB source or OpenAPI contract |
| Frontend Vitest | 8,744 passed, 1 skipped; focused durable-agent projection test 3/3 |
| Frontend production build | PASS |
| Driver/runtime/terminal focused Go suites | PASS |
| Natural-exit convergence race regression | PASS for 50 consecutive race-enabled repetitions; full terminal race package PASS |
| Exact Desktop package build | PASS on production tree `0709f1ebc`, unchanged by published test-only head `35e61b31b`; generated workflow output remained uncommitted and the source tree remained clean |

The aggregate gate initially exposed two useful release blockers: a function
length violation in the new runner setup and a reproducible terminal
natural-exit convergence race. The first was extracted into a dedicated
environment-preparation helper. The second was fixed in production by fencing
durable convergence while it is in flight, with a deterministic regression
that issues an idempotent kill during the hook. Both fixes are included in the
green implementation head above.

The stacked CI runs also reproduced a pre-existing harness flaw under parallel
race load: the 100,000-key Redis setup used one unbounded pipeline and could
hit the client write deadline before the measured close operation began. The
published implementation head seeds the same keys in bounded 5,000-command
pipelines without changing the eight-second close budget or production code.
The exact test passed 10/10 under `-race` while the full localredis race suite
ran concurrently. The terminal retry-exhaustion regression also waits for the
in-flight convergence fence to clear before asserting repair; that boundary
passed 100/100 under `-race` plus the full terminal race suite.

---

[Migration overview](README.md) · [Phase 7 evidence](14-phase-7-decisions-and-evidence.md)
