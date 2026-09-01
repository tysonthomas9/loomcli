# Issue-detail realtime invalidation research

Status: research notes. No implementation decision is recorded here.

Question (2026-09-01): does an agent/API-created comment failing to appear in
an already-open issue detail panel reveal a broader architecture problem, or a
local SSE bug?

## Conclusion

**Yes, it is architectural in scope, but the broken seam is domain
invalidation rather than SSE transport.** The workspace stream reliably carries
generic mutation envelopes, and live delivery and reconnect replay preserve the
same fields. What the envelope does not express is the set of issue aggregates
whose materialized views became stale. The frontend then refreshes only the
slim issue-list projection, while the panel and full-page detail view retain a
separate full-detail snapshot containing comments, dependencies, dependents,
and activity.

The missing comment is the simplest manifestation. Labels, dependency edges,
dependents, and the issue Journey/event list can also remain stale. A dependency
mutation proves that adding only a legacy `issue_id` is not a complete design:
one edge changes the detail view of both endpoint issues, while FleetDB's event
identifies the source issue in `entity_id` and the other endpoint only in
`metadata.depends_on_id`.

The latest `v5` SSE Writer commit is not causal. It moved byte framing behind a
single writer and explicitly left payloads, cursors, polling, and stream
lifecycle behavior with their existing owners.

## Revisions and method

- LoomCLI remote `v5` was verified with `git ls-remote` at
  `ce4df56fb9372628743fed8f7e3e76c377f2c0a9` (`Consolidate all SSE frame
  encoding behind realtime.Writer (#548)`, 2026-08-30). All LoomCLI citations
  below refer to that exact tree, even where the research file is being written
  on another branch.
- FleetDB remote `main` was verified at
  `000793b28a475366ecaaebe7442a41fbd2ca1710` (2026-08-29) in an isolated
  checkout. The AFT run's paired local FleetDB checkout was
  `2a336c0b1e228ceba849a3b0d1a71c6ec4a75ccc`; the relevant comment, label, and
  dependency event construction has the same semantics in both revisions.
- Sources are repository code, repository architecture notes/ADRs, tests, and
  the observed focused AFT regression at
  `tests/aft/reports/run-2026-09-01T22-48-27-645Z.json`. No secondary sources
  were used.

## Intended architecture and invariants

The generic SSE design deliberately makes the workspace stream carry entity
events rather than issue-only messages. It defines `entity_type`, `entity_id`,
and `action` as the generic envelope, retains `type` and `issue_id` only for
legacy compatibility, and assigns invalidation decisions to consumers
(`docs/design/generic-sse-envelope.md:5-17 @ ce4df56fb`). It also requires live
hub broadcasts and reconnect catch-up to project the same envelope
(`docs/design/generic-sse-envelope.md:21-32 @ ce4df56fb`).

The data-access interface intentionally has two projections:

- `IssueBackend.List` is slim.
- `IssueBackend.Get` is full detail and includes dependencies, dependents, and
  comments (`internal/backend/issuebackend.go:22-29 @ ce4df56fb`).

The web UI preserves that split. List conversion explicitly leaves relational
fields zero-valued (`internal/webui/service/issue_list.go:402-406 @
ce4df56fb`), while full-detail conversion emits stable arrays for labels,
dependencies, dependents, and comments
(`internal/webui/service/issue_backend_helpers.go:203-210 @ ce4df56fb`).

Those choices require an additional invariant which is not currently modeled:

> Every accepted issue-scoped mutation must identify every affected issue
> aggregate, and every cached projection of those aggregates must either apply
> the mutation or invalidate itself.

The generic entity envelope answers “what entity event occurred?” It does not
always answer “which cached issue aggregates are now stale?” Those are different
questions for child and relationship entities.

## End-to-end trace

### 1. FleetDB emits an issue-owned child event

FleetDB uses the owning issue ID as `entity_id` for comments and labels. A
comment event is `entity_type=comment`, `entity_id=cmd.IssueID`, and includes
the comment ID/text in metadata (`fleet-db:
internal/service/issue_service.go:1277-1287 @ 000793b2`). A label event follows
the same ownership shape (`fleet-db: internal/service/issue_service.go:1058-1069
@ 000793b2`).

A dependency edge is more important architecturally: FleetDB puts the source
issue in `entity_id` and the other endpoint in `metadata.depends_on_id`
(`fleet-db: internal/service/issue_service.go:958-968 @ 000793b2`). The
projector updates relationship state for both endpoints for related edges and
source/target blocking sets for blocking edges (`fleet-db:
internal/projection/handlers.go:755-825 @ 000793b2`). Thus a dependency event
can invalidate two detail aggregates.

Child events do not provide a reliable parent revision signal. Comment
projection appends to the comments list without updating the issue hash
(`fleet-db: internal/projection/handlers.go:1010-1029 @ 000793b2`). Label
projection changes the issue's labels field but does not update `updated_at`
(`fleet-db: internal/projection/handlers.go:895-936 @ 000793b2`). Dependency
projection changes edge/index structures and, for parent-child edges,
`parent_id`, but not a general parent revision
(`fleet-db: internal/projection/handlers.go:755-825 @ 000793b2`).

### 2. The Loom Fleet adapter drops impact information

FleetDB's mutation response preserves `metadata`
(`fleet-db: internal/api/event_types.go:10-37 @ 000793b2`), and Loom's private
wire struct receives it (`internal/backend/fleet/convert.go:490-506 @
ce4df56fb`). But `backend.MutationData` has neither metadata nor an affected
issue set (`internal/backend/types.go:183-204 @ ce4df56fb`). Conversion copies
the generic entity fields and populates `IssueID` only for `entity_type=issue`
(`internal/backend/fleet/convert.go:546-562 @ ce4df56fb`). Consequently:

- comments and labels lose their directly affected issue target as an
  issue-scoped field, even though `entity_id` happens to contain it;
- dependencies lose `depends_on_id`, so the second affected issue cannot be
  recovered downstream;
- action-specific consumers would have to know FleetDB's overloaded child
  `entity_id` convention to infer even the first target.

The existing adapter test treats this as intended behavior: it asserts that
comment and label mutations do **not** populate `IssueID`
(`internal/backend/fleet/fleet_test.go:2235-2274 @ ce4df56fb`).

### 3. Live and catch-up delivery preserve the same incomplete contract

For live delivery, the backend subscriber converts every `MutationData` to a
realtime payload and broadcasts it (`internal/webui/subscription/backend_subscriber.go:209-214
@ ce4df56fb`). For reconnect catch-up, the same backend mutation is projected
through `BackendMutationToRPCEvent`
(`internal/webui/subscription/multi.go:274-290 @ ce4df56fb`). Both adapters copy
`EntityType`, `EntityID`, `Action`, and the empty `IssueID` without deriving
aggregate impact (`internal/webui/server/realtime/backend_adapter.go:19-37,40-63
@ ce4df56fb`).

The SSE handler sends catch-up mutations before the connected frame and live
mutations from the client channel through the same frame writer
(`internal/webui/server/realtime/handler.go:134-160,179-193,201-219 @
ce4df56fb`). The browser parses the mutation and forwards it unchanged
(`internal/webui/frontend/src/api/common/sse.ts:372-389 @ ce4df56fb`), and
`EventProvider` only filters and fans it out
(`internal/webui/frontend/src/hooks/common/useEventProvider.tsx:184-212 @
ce4df56fb`). This confirms the observed bug is downstream of transport and
affects replay as well as live delivery.

### 4. The issue store invalidates only the list projection

The issue store classifies `issue`, `dependency`, `dep`, `comment`, and `label`
as issue-list projection invalidators, but only `entity_type=issue` mutations
are applied to a local issue (`internal/webui/frontend/src/stores/issueStoreHelpers.ts:294-334
@ ce4df56fb`). Child events therefore schedule a debounced `refetch()`
(`internal/webui/frontend/src/stores/issueStore.ts:105-147 @ ce4df56fb`). That
method re-runs the active list/kanban/graph fetch, not `GET` for the selected
issue (`internal/webui/frontend/src/stores/issueStore.ts:413-424 @
ce4df56fb`).

This behavior is internally consistent for board projections, but cannot
refresh comments or full dependency/dependent arrays because the list
projection intentionally does not carry them.

### 5. Detail is a separate cache with no mutation-invalidation interface

`useIssueDetail` owns an independent `IssueDetails` snapshot, fetched only when
`fetchIssue(id)` is called (`internal/webui/frontend/src/hooks/issues/useIssueDetail.ts:60-115
@ ce4df56fb`). Its update interface merges scalar issue fields and labels while
preserving full-detail-only data such as comments, dependencies, and dependents
(`internal/webui/frontend/src/hooks/issues/useIssueDetail.ts:125-146 @
ce4df56fb`; the preservation is explicitly tested in
`internal/webui/frontend/src/hooks/issues/__tests__/useIssueDetail.test.ts:703-767
@ ce4df56fb`).

`App` attempts to synchronize the detail snapshot from the list store by
comparing title, status, priority, type, assignee, owner, and `updated_at`; it
then invokes only the scalar merge interface
(`internal/webui/frontend/src/App.tsx:589-621 @ ce4df56fb`). It does not compare
comments, dependencies, dependents, or labels, and it never calls
`fetchIssue` in response to a mutation.

The panel introduces two more caches:

- `localComments`, initialized from the detail prop and locally appended only
  when the same panel submits a human comment
  (`internal/webui/frontend/src/components/IssueDetailPanel/IssueDetailPanel.tsx:762-780,808-814
@ ce4df56fb`);
- `events`, fetched when issue ID or parent `updated_at` changes
  (`internal/webui/frontend/src/components/IssueDetailPanel/IssueDetailPanel.tsx:769-806
@ ce4df56fb`).

This explains why same-panel human comments appear while agent/API/other-tab
comments do not, and why child activity can remain stale when FleetDB does not
advance parent `updated_at`. The full-page issue view receives the same
`issueDetails` snapshot from `useWorkspaceViewData`, so it shares the stale
detail problem (`internal/webui/frontend/src/views/IssueDetailPage.tsx:63-81,209-225
@ ce4df56fb`).

### 6. Notification and read-model visibility have no shared cursor

Fixing aggregate targeting is necessary but does not by itself guarantee that
the next detail fetch contains the triggering mutation. FleetDB first appends
an event, then attempts inline projection. If projection fails, it retries once
and still returns success while logging that the background projector will
catch up (`fleet-db: internal/service/issue_service.go:75-110 @ 000793b2`). The
mutation endpoint reads the event store and returns an event-store cursor
(`fleet-db: internal/api/mutations.go:24-34,128-155 @ 000793b2`), so a client
can receive mutation cursor `C` while the issue, comment, or dependency read
projection is still behind `C`.

FleetDB projectors do track a workspace projection cursor after successful
projection (`fleet-db: internal/projection/pg_projector.go:220-244 @
000793b2`; the Redis projector exposes the same interface at
`internal/projection/projector.go:303-310`). That cursor is not returned by the
normal issue or comment read endpoints: `GET /issues/{id}` emits only the issue,
and the comments endpoint emits comments plus a count
(`fleet-db: internal/api/issues.go:256-270; internal/api/comments.go:66-83 @
000793b2`). Loom then composes one full detail snapshot from separate issue,
dependency, and comment requests, treating relationship-request failures as an
empty degraded result (`internal/backend/fleet/fleet.go:330-360 @ ce4df56fb`).

The missing read contract is therefore:

> A detail refresh caused by mutation cursor `C` must return a snapshot
> projected at or beyond `C`, or explicitly report that projection is still
> behind.

Without a projection cursor on the composed detail response—or a server-side
`min_cursor=C` read—the issue module cannot distinguish an up-to-date empty
comments list from a stale empty comments list. Debouncing and repeated
best-effort refetches can reduce symptoms, but they cannot prove convergence.

## Failure scope

| Mutation family     | List/board behavior                                                                                           | Open detail behavior                                                                                                                                 |
| ------------------- | ------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Issue scalar/status | Usually appears correct because `issue_id` is present and the list issue carries the changed scalar/revision. | Scalar merge updates the panel; this happy path masks the missing detail invalidation seam.                                                          |
| Comment             | Schedules a list refetch.                                                                                     | Comment array and Journey/event list can remain stale; local optimistic submit masks it for the initiating panel.                                    |
| Label               | Refetched list can receive the new label.                                                                     | `App` does not compare labels and FleetDB does not advance `updated_at`, so detail can remain stale.                                                 |
| Dependency          | Board projection can update after refetch.                                                                    | Full dependencies/dependents are absent from the list; both endpoint details can be stale, and the target endpoint is lost when metadata is dropped. |
| Reconnect catch-up  | Replays the same events.                                                                                      | Replays the same incomplete impact contract through the same consumers, so reconnect is not a convergence guarantee for detail.                      |

This is broader than comments because the missing abstraction is aggregate
impact, and the missing consumer interface is detail invalidation. It is not a
general failure of SSE delivery.

## Why the SSE Writer commit is not causal

The `v5` tip's ADR says the Writer owns framing while stream handlers retain
auth, cursors, polling, heartbeat cadence, headers, deadlines, and payloads
(`docs/adr/0001-sse-framing-single-writer-seam.md:3-13 @ ce4df56fb`). The commit
does not change the Fleet mutation converter, `MutationData`, payload adapters,
subscriber, EventProvider, issue store, or detail hook. Its only workspace
realtime production edit is the handler's use of the centralized writer; the
payload is still marshaled immediately before `WriteEventID`
(`internal/webui/server/realtime/handler.go:237-267 @ ce4df56fb`).

Therefore the Writer change may improve framing correctness and error
propagation, but it neither created nor repairs the missing aggregate-impact
and detail-invalidation contracts.

## Test gaps that allowed the composition failure

The tests cover each local interface but not their composition:

- Fleet conversion tests explicitly lock in empty `IssueID` for comment/label
  child events (`internal/backend/fleet/fleet_test.go:2235-2274 @ ce4df56fb`).
- The generic dependency store test checks only that a list refetch occurs and
  expects no mutation-count increment; its fixture uses `entity_id="dep-1"`,
  unlike FleetDB production events where `entity_id` is the source issue ID
  (`internal/webui/frontend/src/stores/__tests__/issueStore.test.ts:1007-1029
  @ ce4df56fb`).
- The comment store test is legacy-shaped: it supplies `issue_id` and omits
  `entity_type`, so it exercises local timestamp mutation rather than the
  production child-entity path
  (`internal/webui/frontend/src/stores/__tests__/issueStore.test.ts:1193-1211
  @ ce4df56fb`).
- The `App` sync test covers only a fresher scalar/status list issue
  (`internal/webui/frontend/src/__tests__/App.test.tsx:2216-2245 @
  ce4df56fb`).
- Live and catch-up SSE tests use issue-shaped `issue_id` fixtures; they verify
  transport ordering and delivery, not a production-shaped child mutation
  reaching an already-open detail view
  (`internal/webui/app/sse_live_test.go:255-311,438-503 @ ce4df56fb`).

The interleaved AFT comments journey is the first composed assertion of the
actual user invariant: after an external comment mutation, the already-open
detail UI must show the comment without navigation or reload.

## Recommended architecture

Introduce one deep mutation-impact module at the adapter seam. Its interface
should accept a backend mutation and return both the generic entity event and
canonical invalidation targets. Consumers should not reverse-engineer
FleetDB's action/metadata conventions.

A suitable wire invariant is an explicit, generated field such as
`affected_issue_ids: string[]`:

- issue mutation: `[entity_id]`;
- comment/label/metadata mutation: `[entity_id]`;
- dependency mutation: unique values of `[entity_id,
  metadata.depends_on_id]`;
- non-issue-scoped mutation: `[]`.

Do not redefine `entity_id` as the affected aggregate ID: it already describes
the event entity family, and relationship events can affect more than one
aggregate. Do not make frontend callers maintain an action-to-impact switch:
Loom's Fleet adapter currently has the source metadata and is the seam with the
required locality.

The frontend issue module should then expose two distinct effects:

1. **Projection invalidation** — debounce/refetch list, kanban, or graph data.
2. **Detail invalidation** — increment a per-issue detail revision for every
   affected issue ID. An open panel or full-page view observes only its issue's
   revision and re-fetches the authoritative full detail and event list.

The smallest implementation can add the per-issue revision to the existing
issue store and call the existing race-safe `fetchIssue`. The stronger eventual
shape is a single issue-detail cache module shared by panel and full-page views,
with optimistic same-tab comments reconciled by stable comment ID when the
authoritative snapshot arrives.

The authoritative detail read must also become cursor-aware. Either FleetDB
should accept `min_cursor=C` and wait boundedly for the projection, or the
composed detail response should include the minimum projection cursor shared by
all of its constituent reads. The issue-detail module can then retry with
bounded backoff until the returned cursor is at least the mutation cursor. A
timeout must surface as stale/degraded rather than silently accepting an older
snapshot.

## Phased remediation

1. **Make the regression contractual.** Add production-shaped comment, label,
   and dependency fixtures plus the open-detail React and focused AFT tests.
2. **Repair aggregate targeting and visible invalidation.** Atomically add
   `affected_issue_ids` to the backend mutation contract and its producer and
   consumers; add per-issue detail revisions and authoritative refetches. This
   is the smallest releaseable fix for the ordinary no-lag path.
3. **Consolidate issue replica ownership.** Move summary, detail, nested-resource
   invalidation, optimistic reconciliation, and latest-wins fetching behind one
   issue module. Delete `App`'s field-comparison glue and panel-local freshness
   policy once consumers use that interface.
4. **Close the projection-consistency gap.** Carry the mutation cursor into a
   cursor-aware detail read, add bounded wait/retry behavior, and fault-inject
   projector lag to prove read-your-event convergence.

## Acceptance gates

1. Adapter tests use real FleetDB-shaped comment, label, and dependency events
   and assert the complete affected-issue set, including both dependency
   endpoints.
2. Live and reconnect/catch-up contract tests prove byte-equivalent impact
   fields for the same logical event.
3. Store tests separately prove list projection invalidation and per-issue
   detail revision behavior.
4. React integration tests open issue A, inject external comment/label/dep
   events, and assert exactly one authoritative detail refresh; a dependency
   test also opens issue B and verifies its dependent view invalidates.
5. Projection-lag tests deliver mutation cursor `C` before projection catches
   up and prove the client never accepts a detail snapshot older than `C` as
   converged.
6. The focused AFT comments journey passes with the panel left open and shows
   each agent mutation before the next step proceeds.
7. The full deterministic AFT screenshot suite passes, followed by one real
   local multi-client check proving cross-tab convergence and reconnect replay.

## Final assessment

The architecture already has good seams for durable events, live/catch-up
transport, and slim versus full read models. The missing deep module is the one
that translates an entity mutation into aggregate cache impact. Without that
interface, state-convergence knowledge is split across the Fleet adapter,
generic SSE envelope, list store, `App`, detail hook, and panel-local caches.
The resulting locality failure is why all individual tests can pass while the
user-visible composition remains stale. Even after locality is repaired, the
event cursor/read-projection cursor gap must close before the system can claim
deterministic convergence during projector lag.
