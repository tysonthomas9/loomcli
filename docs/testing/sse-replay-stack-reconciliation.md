# SSE replay reconciliation with the transport stack

This integrates the remaining code-level replay fixes from PR #626
(`51b0dd2d7`) onto the existing transport stack, through PR #639
(`301732c17`). It preserves the stack's paged backend interface, bounded catch-up,
registration acknowledgement, explicit resync frames, strict cursor handling,
and fetch-based client.

| Original #626 concern | Result on the stack |
| --- | --- |
| Replay stopped after one page | Already superseded by `MutationPage` and bounded handler pagination. Keep the newer interface. |
| Registration raced replay | Already superseded by cancellable registration acknowledgement before subscriber activation and replay. |
| Queued live copies duplicated replay | Existing exact-cursor replay dedup remains; the new 201-event proof exercises queued first/last duplicates. |
| HTTP open announced readiness early | Fixed here: only `connected` changes client readiness and resets retry backoff. |
| Pagination could stall or cycle | Fixed here before cursor advancement or event publication. Startup/replay are bounded; live polling uses bounded recent-cursor history. |
| Filtering prevented cursor progress | Successful replay emits a final `checkpoint` when its scanned cursor exceeds the last delivered mutation. |
| Later-page failures were hidden | Keep the stack's explicit resync contract, which supersedes #626's partial-prefix-and-close behavior. |

## Current wire contract

The server registers the client before activation and replay. Replay uses the
existing page/time limits. Successful replay writes mutations, then a filtered
tail checkpoint if needed, then `connected`. Empty terminal idle pages may retain
their input cursor. Pages carrying events or claiming more work must advance;
repeated earlier page cursors fail visibly.

Failed or capped replay emits a resync instruction and then `connected`, using
the existing stack contract. Its buffered partial mutations are not published.
The client requests authoritative query refresh on resync; receiving `connected`
means the transport handshake completed, not that every asynchronous query
refresh has succeeded. Query freshness after failed refresh remains separate
work in the overall architecture goal.

`checkpoint` carries an opaque SSE ID and `{}` data. It advances resume state
without a domain mutation or readiness callback. HTTP headers do not reset
backoff: repeated failures during replay continue the retry sequence until a
`connected` frame arrives. Generation checks prevent reentrant callbacks from
changing a replacement connection.

## Evidence classes

The new Go test uses the actual Fleet HTTP adapter, three 100/100/1 response
pages, hub, and SSE handler. Its controlled HTTP server is a deterministic
fixture, not a running FleetDB storage service. The final page waits for queued
live duplicates and a later sentinel; assertions require exactly 201 ordered
replay mutations before `connected`, with no duplicate replay IDs afterward.

Other deterministic tests cover all-filtered and trailing-filtered checkpoint
progress, empty idle pages, missing/stalled/cyclic page cursors, rejected live
pages leaving the subscriber checkpoint unchanged, and sustained healthy traffic
continuing beyond the bounded recent-history window. Frontend tests use the
actual fetch-event-source parser and cover 201 replay frames, interrupted replay,
checkpoint resume headers, and reentrant connection callbacks. Provider tests
assert that HTTP open still leaves the UI connection state pending.

Run the affected code-level checks from the Loom checkout:

```sh
go test -race -p 1 ./internal/backend/fleet ./internal/webui/subscription \
  ./internal/webui/server/realtime ./internal/webui/app -count=1
cd internal/webui/frontend
npm run test:unit -- src/api/common/__tests__/sse.test.ts \
  src/hooks/common/__tests__/useEventProvider.test.tsx \
  src/components/ConnectionStatus/__tests__/ConnectionStatus.test.tsx
```

The old #626 browser proof wrapped native `EventSource`. This stack uses fetch
SSE, so that instrumentation and its old successful 1,001-event full-replay
expectation must not be reused as current evidence. A replacement persistent-page
proof must interrupt fetch SSE, preserve the exact Last-Event-ID header, assert
real API writes and application updates without reload, and include a failing
delivery-disabled control. Bounded overflow must prove resync/refetch, not demand
unlimited replay. That browser proof remains unverified on this branch.

## Remaining architecture work

This is not a claim that every SSE bug is fixed. The larger goal still requires
a committed-source feed, durable retention/incarnation handling, an ordered live
delivery frontier across retry queues, cursor-free transient hints, a stable
snapshot boundary during replay, dependency-aware invalidation and query-freshness
recovery, storage-server restart tests, and paired Loom/FleetDB browser proof.
The live cursor-cycle guard detects only cycles within its bounded recent window;
it does not establish arbitrary global cursor ordering.

## Recorded local validation

All 409 frontend unit-test files passed (8,822 tests), including the 136 targeted
client/provider/connection-status tests. Typecheck, changed-source lint and
formatting passed. Generated TypeScript and Go API freshness checks passed.
The Fleet backend, subscription and realtime packages passed with the race
detector (2.618, 4.024 and 5.290 seconds). The application package initially
exposed a fixture without durable mutation IDs; after supplying its actual page
cursor on the fixture events, that package passed with the race detector in
13.798 seconds. Changed Go code passed lint. Independent review found no
remaining blocker in this increment's scope.
