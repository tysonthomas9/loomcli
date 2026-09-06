# Browser recovery coverage and publication contract

The authenticated handle bridge supplies a source-bound issue read. Browser offer decoding checks the active stream's workspace/repository scope and the handle's native shape. Neither proves that the browser's caches represent the returned snapshot. General checkpoint acknowledgment remains disabled.

## Verified gaps

| Consumer | Current production seam | What is missing before certified publication |
| --- | --- | --- |
| Ready, kanban and graph | `stores/issueStore.ts` shares one `issuesMap` across modes; `hooks/common/useStoreContext.tsx` registers one ordinary issue refresh | Mode/filter-specific interpretation. The native manifest lacks graph dependency edges; treating missing edges as empty would lose relationships. |
| Issue-store writers | `runFetchIssues` merges by timestamps and preserves recent local creations; mutations, optimistic updates and rollback timers write separately | Command callbacks now require exact entry/scope ownership, and ordinary recovery refuses unresolved or overlapping commands. The [writer inventory](sse-issue-store-publication.md) records the remaining single publication generation and strict replacement seam. Timestamp ordering still cannot certify snapshot membership, deletions or cleared fields. |
| Native records | `api/issues/issues.ts` normalizes alias precedence; `fetchGraphIssues` reconstructs selected fields and defaults | A strict native decoder and explicit representation conversion must preserve metadata and reject alias conflicts. Existing compatibility conversion is not a certificate decoder. |
| Blocked results | `hooks/issues/useBlockedIssues.ts` uses independently cached keys with workspace, parent, priority, type, assignee, limit and repositories | Exact filtered projection or invalidation under the same recovery generation. Publishing issueStore alone does not update these entries. |
| Selected detail | App uses `hooks/issues/useIssueDetail.ts`, with local detail state and separate requests | The selected request is now an ordinary recovery participant with fresh reads, cancellation and selection revisions. This is not certified coverage: detail dependencies, dependents and comments are outside the issue manifest. App list changes invalidate the intended selection through full detail reads; timestamps do not authorize partial patches. |
| Detail comments and history | `components/IssueDetailPanel/IssueDetailPanel.tsx` owns comments and events locally | Panel history now has its own bounded ordinary recovery participant, tied to the intended selection, with explicit failure and cancellation. Confirmed local comments are a selection-scoped overlay retired when a detail read includes them. Neither is certified coverage; the native manifest still omits comments and history, and a successful issue-store refresh proves neither collection. |
| Dormant consumers | Mode changes reuse the issue store; generic query entries are removed after the final registration disposes; App's selected detail hook remains mounted | Inventory actual retained state rather than assuming every registry entry persists. Mode switches, previous-data refs and local stores must not publish older uncertified content as recovered. |

FleetDB's `fleet.issue-workspace.v2` contains complete issue rows, total, ready, blocked and deferred collections. It contains no graph edges, detail relationship collections, comments or history. The v2 read binds its cursor to the producer transaction and requires the database writer/provenance protocol. It cannot satisfy the table above by itself; time-based readiness refresh also remains necessary.

The [native preparation and HTTP API](../testing/sse-native-recovery-preparation-proof.md) now implement the off-store read/validation seam. They remain disconnected from EventProvider and cache publication.

## Chosen next implementation

1. Prepare the native manifest off-store. Preserve original records and metadata, validate scope/handle echo/manifest/through and derived-record consistency, and record explicit supported coverage. Do not fabricate missing graph edges or detail collections.
2. Add a recovery attempt owned by the browser client and current committed workspace/repository scope. When the protocol can consume an offer, suspend the automatic retry loop synchronously; initial expiry occurs before `connected`. Cancellation, manual reconnect, sign-out, unmount and scope changes must retire the attempt. An old A→B→A completion cannot be accepted because visible names match again.
3. Introduce a single publication generation controlling issue-store requests, mutations, optimistic writes and rollback timers. Build replacements off-store; reject superseded or unresolved-command state before a single publish. Keep optimistic data as a separate overlay or explicitly defer acceptance while commands remain unresolved. Never use the ordinary timestamp merge as proof.
4. Extend the certified source with required graph/detail collections, or keep those consumers explicitly unavailable for certified acceptance until separate reads prove them. Bind blocked/detail/cache invalidation to the same generation, including dormant state that can be reused later.
5. Complete the durable-action/view mapping, durable Fleet incarnation checks and every required family before acknowledgment. Only then atomically accept the exact recovery boundary and resume after it. An issue-only certificate never completes the whole-client barrier.

## Transport distinctions

`WorkspaceSSEClient.connectionGeneration` identifies a library retry loop, not each HTTP connection within that loop. Existing guards reject callbacks from retired loops; a decoded handle retained by another component still needs its own attempt ownership. Do not use `onConnected` to initiate expiry recovery, because the failed initial replay never emits it.

The offer decoder validates against the immutable repository filter used for that loop's URL. A no-op `connect(..., newRepos)` call can change saved configuration without changing the current wire subscription; saved configuration therefore cannot validate that stream's offer. Repository filters are subscription metadata, not authorization; the Fleet response is workspace-wide.

The decoder only exposes valid offers to resync subscribers. It does not pause retries, fetch certificates, publish caches or advance checkpoints. Enabling partial automatic consumption now would either starve slow recovery with repeated reconnects or pause the event stream without a complete acknowledgment path.

## Regression requirements

- Late ordinary response, optimistic timeout or mutation callback after certified publication cannot restore pre-recovery data.
- Workspace/repository A→B→A rejects old attempt success, failure and cleanup. An abandoned concurrent render must not retire the still-committed workspace.
- Removed issues and cleared metadata remain removed; graph relationships and detail collections cannot be silently defaulted to empty.
- Filtered blocked caches and dormant views cannot reuse older content as certified.
- Foreign, expired, malformed or mismatched handle responses publish nothing and preserve the accepted SSE checkpoint.
- A slow certified read survives suspended retry timers; duplicate offers join only the identical attempt. Manual reconnect invalidates it.
- Actual paired Fleet/browser proof must include HTTP route/authentication, immutable source binding, stored acceptance generation, exact resume cursor and replayed post-boundary events. Package fixtures alone do not establish these runtime properties.
