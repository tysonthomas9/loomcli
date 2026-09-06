# SSE recovery read proof

Status: recovery acknowledgment remains proposed. The internal [certified issue client and captured-source capability](../testing/sse-certified-issue-source-proof.md) are implemented, without a Loom REST bridge or frontend consumer. The current frontend resync checkpoint repair is separate. This plan does not authorize resetting a resume checkpoint after ordinary REST refreshes.

## Required guarantee

After an expired cursor, advancing to a captured head `H` is safe only if the client has replaced or invalidated every relevant cached view using reads that include the committed effects through `H`, for the same source incarnation and scope. It must subsequently replay mutations strictly after `H`.

This is a causal lower bound, not necessarily one global point-in-time snapshot. Different queries may include newer committed effects. Each logical query still needs a complete, coherent result: independently paginating a changing collection must not omit a record because offsets moved.

A successful HTTP response, a completed frontend promise, a current SSE connection, and equality between two opaque cursor strings are not read proofs.

## Verified current behavior

The following findings are from the Loom source-binding worktree and Fleet replay-fence worktree reviewed for this plan.

| Seam | Current guarantee | Missing for recovery acknowledgment |
| --- | --- | --- |
| [QueryRecoveryCoordinator](../../internal/webui/frontend/src/hooks/common/queryRecovery.ts) | Fresh participant requests, cancellation, dynamic membership and revision rechecks. Concurrent refresh calls join the active attempt. | No source identity, incarnation, target head, or verified response certificate. Its own comment explicitly excludes checkpoint authorization. |
| [EventProvider](../../internal/webui/frontend/src/hooks/common/useEventProvider.tsx) | Resync schedules registered query refreshes; completion does not reset the SSE checkpoint. | No reset/acknowledgment protocol. Preserve this behavior until the full proof exists. |
| [Bound mutation source](../../internal/webui/subscription/mutation_source.go) and [authoritative session](../../internal/webui/server/realtime/authoritative_handler.go) | One subscriber entry is captured for an SSE connection. Reads check retirement before and after source access. Replay uses a fixed source fence. | REST requests do not inherit this binding. Entry-pointer identity is process-local and is not a durable backend epoch. Reconnect selects again. |
| [Issue list service](../../internal/webui/service/issue_list.go) | Resolves a Fleet backend or daemon pool according to workspace and deployment; errors propagate. Fleet composite Kanban reads separately request issues, blocked, ready, and deferred views. | Recovery cannot silently select another backend or daemon. Separate reads have no common read certificate or logical snapshot contract. |
| Fleet `internal/storage/postgres/projection_page.go` and `projection_mutations.go` | Enrolled mutation heads and pages validate receipt-backed applied progress and source identity. Fixed replay fences cap source rows before filtering. | These are event delivery proofs, not certificates attached to ordinary entity queries. |
| Fleet `internal/service/issue_service.go` and `internal/storage/postgres/issue.go` | Issue service delegates to the configured reader. PostgreSQL list reads count and then fetch rows. | No required recovery fence is passed to these reads; count and result are separate statement snapshots in ordinary use. |
| [Skills store](../../internal/webui/frontend/src/stores/skillsStore.ts) | Ordinary loaded catalogs can return from cache; strict catalog recovery requests a fresh read. | Cache entries have no committed-source certificate. An unmounted catalog must not later reuse a pre-recovery loaded result as certified current. |
| [Skills handlers](../../internal/webui/handlers/skills/handlers.go) | Catalog, capability, and file responses use the canonical Store seam and validate payload integrity. | Payload validity and file revision do not establish ordering against a Fleet committed head. Global catalog and capability scope must be defined separately. |
| [Expanded file tree](../../internal/webui/frontend/src/hooks/common/useScopedFileTree.ts), [task sessions](../../internal/webui/frontend/src/hooks/terminal/useTaskSessions.ts), and [session count](../../internal/webui/frontend/src/hooks/terminal/useWorkspaceSessionCount.ts) | Register active view refreshes, using cancellation and fresh request ownership. | Files, PTYs, Git, and session stores are not automatically certified by a PostgreSQL projection receipt. |

On an enrolled PostgreSQL primary, an uncached read whose snapshot starts after the transaction that committed `H` will see that transaction's effects. This is useful, but current REST contracts do not establish all those conditions across routing, composite reads, caches, and responses. A separate successful head probe followed by an independently routed query is insufficient.

For Redis and unenrolled PostgreSQL, the current mutation head represents raw source admission rather than an applied-effect prefix. A fresh query after that head can still precede projection. These modes must not issue the proposed acknowledgment proof.

## Proposed protocol and ownership

### 1. Define source identity before defining a durable ticket

The first implementation must identify an enrolled PostgreSQL authority, workspace, lane incarnation, and committed head. Loom must additionally bind the request to its selected backend registration and authorization/repository scope.

Do not serialize the current subscriber pointer and call it a durable identity. Backend registration epochs across replacement and process restart are not yet a defined protocol. Until they are, a server-local recovery attempt may be explicitly short-lived, bound to a process and registration, and rejected on replacement or restart. A durable or cross-instance ticket requires a separately specified stable authority identity and verification mechanism.

Proposed ticket contents, pending that decision:

- Unique attempt identity and expiration.
- Authority identity and workspace.
- PostgreSQL lane incarnation and exact canonical committed head `H`.
- Query scope and scope revision: repository filters, permissions, and relevant view parameters.
- Versioned manifest identifying which view families the attempt can certify.

An opaque or signed ticket is not itself authorization. Every use must recheck access and identity. Unrecognized, expired, mismatched, or unverifiable tickets fail visibly; there is no fallback to an ordinary read.

### 2. Prove the database read

Add an enrolled-only PostgreSQL read operation that opens an owned transaction snapshot, validates the ticket's incarnation and exact source membership, verifies receipt-backed applied progress covers `H`, and runs the logical query through the scoped storage executor. The check and data read must use that same snapshot.

Do not implement this as a pool-level head query followed by a separate service read. A stale repeatable-read snapshot must fail the fence check. A replica may participate only when it can validate and read the same proof in its own snapshot; default the initial implementation to the primary.

For the first issue view, cover the entire returned logical result, including any count, pagination, parent enrichment, blocked/ready/deferred computation, and permission/repository filtering. Prefer a scoped composite operation over several independently routed REST calls. An empty result requires the same proof as a nonempty one.

Return a certificate bound to the ticket, response scope, and verified read position. It attests to the data actually read, not a head fetched afterward. Ordinary endpoints retain their existing behavior; proof-required requests use an explicit strict path.

### 3. Bind Loom REST reads and caches

Resolve one backend registration for the attempt and retain it through all head, read, and verification operations. Reject retirement before and after a request, following the existing bound-source pattern. Recheck before accepting acknowledgment. Never fall back to a daemon or replacement Fleet backend for a proof-required request.

Propagate cancellation and the recovery ticket through the frontend API, Loom service, and Fleet request. Preserve the certificate through conversion; do not invent one from request parameters.

Inventory caches by query family. A proof-required read must bypass an uncertified cache or use an entry with a matching identity/scope and adequate verified position. HTTP caches and conditional responses require the same rule. On recovery start, mark dormant workspace caches stale with a scope/source recovery generation so future mounts cannot reuse old loaded state. Refreshing only currently mounted participants is insufficient for cached state reused later.

Do not claim every external view is covered by a PG ticket. Map durable actions to affected view families and distinguish projection-backed entity state from file contents, Git state, sessions, global catalogs, and capabilities. External families need an appropriate authority/version and ordering contract, or an explicit exclusion that cannot hide a lost durable invalidation. Do not enable general checkpoint acknowledgment while this mapping is incomplete.

### 4. Coordinate a new proof attempt

Extend the coordinator with a proof-specific attempt carrying the ticket and required coverage manifest. This is a new attempt identity, not an alias for today's signal-only `refresh()`.

Requests begun before the ticket must not satisfy it. Repeated requests for the same ticket may join. A different head, source, incarnation, or scope cancels/supersedes the old attempt rather than joining its promise. Each participant must return a verified matching certificate after its result is accepted by the current store generation.

Preserve dynamic enrollment and membership revision rechecks. An unmounted participant leaves the active set but its cached state remains invalidated. An empty active set proves only that no active registered participants remain; it does not certify missing view families or authorize acknowledgment by itself.

### 5. Acknowledge and resume

Only after the manifest is satisfied may the client acknowledge this exact attempt and `H`. Acknowledgment must reject stale workspace/repository/source generations, expired tickets, and changed incarnations. It must not substitute a newer head.

Choose acknowledgment ownership explicitly. For an in-memory transport checkpoint,
prefer a client-local atomic acceptance step after matching certificates and scope
checks; a new server acknowledgment endpoint or persisted ticket is not inherently
required. Add server-side acknowledgment state only if the selected authority or
cross-instance retry contract needs it. Neither option relaxes the read proof or
backend incarnation validation required on resumed delivery.

Define the loss/retry behavior before implementing acknowledgment: repeated acknowledgment of the same accepted attempt is idempotent; disconnect or lost acknowledgment response must permit a safe retry or a new proof attempt. Until accepted acknowledgment is known, retain the previous resume checkpoint. Reconnect after acceptance resumes after `H`, so commits arriving during refresh remain replayable.

The retention protection or restart behavior during a recovery attempt is also explicit: if `H` becomes invalid before resume, reject and restart recovery. Never advance to a newly discovered retention floor without a new proof.

## Delivery order

1. Keep the frontend resync checkpoint repair independent: no refresh completion or resync payload authorizes a reset.
2. Define authority/epoch and coverage semantics, including process-local versus durable attempt lifetime. Record the chosen identity contract before adding ticket persistence.
3. Implement one enrolled-PG issue read certificate through the actual service and storage path, with production-equivalent failure tests. No public reset endpoint yet.
4. Bind Loom proof-required REST requests and invalidate active/dormant caches by recovery generation. Add coordinator certificate handling and supersession tests.
5. Complete the durable-action/view coverage manifest and all required view families. Explicitly gate unsupported sources and families.
6. Add acknowledgment/resume only after the complete matrix below passes. Choose local versus server-owned acknowledgment based on the required authority/retry semantics, then prove the actual HTTP/SSE/frontend sequence, including lost responses and concurrent commits.

## Acceptance matrix

| Requirement | Failure schedule / proof | Required result |
| --- | --- | --- |
| Committed source only | Pause raw/Redis projection after append; request proof | Unsupported/error; no ticket that can authorize acknowledgment |
| Actual read lower bound | Hold old PostgreSQL snapshot, commit `H`, then attempt certified read in old snapshot | Reject; no certificate for stale rows |
| Atomic effect evidence | Fail effect, receipt, or applied-prefix write | Cannot certify that event's position |
| Source identity | Replace Loom backend with equal cursor values between head and query, between pages, and before ack | Old attempt rejects; replacement is never silently selected |
| Incarnation | Recreate/reset backend or restart a process-local ticket owner | Ticket invalidates unless the explicitly implemented durable identity contract proves continuity |
| Complete logical query | Concurrent inserts/deletes while reading count/pages/derived Kanban state | Coherent complete snapshot or visible error; no omission certified as complete |
| Empty results | Lagging or wrong-source query returns `[]` with HTTP 200 | No matching proof, so no acknowledgment |
| Cache provenance | Return pre-ticket browser/server/HTTP cache; remount a previously inactive skills catalog | Stale cache cannot complete the attempt or reappear as certified current |
| Fresh attempt | Preexisting request finishes after recovery starts; ticket B arrives during ticket A | Old request/A completion cannot satisfy B |
| Dynamic participants | Mount/enable a query while another participant is pending; change required membership after an aggregate finishes | New member joins/re-runs before completion; missing manifest coverage blocks |
| Scope cancellation | Workspace A→B→A or repository/permission change during reads | Old results and acknowledgment remain rejected despite matching visible names |
| Partial failure | One participant errors, aborts, returns malformed data, or mismatched proof | Preserve accepted checkpoint; no partial success acknowledgment |
| Concurrent source growth | Capture `H`, commit `H+1` during refresh | Ack remains exactly `H`; subsequent replay delivers `H+1` |
| Lost acknowledgment | Drop request/response or reconnect around ack | Idempotent retry/new proof; no silent cursor advancement |
| Retention | Remove the recovery boundary before resumed replay | Explicit expiry and new recovery; no floor substitution |
| External surfaces | A durable action invalidates an external/local view without an ordering proof | Coverage gate prevents claiming whole-client recovery |

## Scope of this document

This is an implementation plan based on inspected seams. It does not implement a ticket, epoch, certified read, cache-generation protocol, or acknowledgment endpoint. Existing registered-query recovery remains useful and intentionally weaker. Fixed replay fences and connection-bound mutation sources are prerequisites, not substitutes for read certification.
