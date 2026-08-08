# Phase 8 Consolidation and Evidence

- **Status:** In progress
- **Base:** Phase 7 completion commit `46bb9a8416d291d686cf32c58fa872a097cdba3d`
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

Evidence is appended here as each stacked change is committed and validated;
an implementation commit is not treated as proof for a command that was not
run against that exact tree.

---

[Migration overview](README.md) · [Phase 7 evidence](14-phase-7-decisions-and-evidence.md)
