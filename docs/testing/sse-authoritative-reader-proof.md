# One authoritative cursor chain per SSE connection

## Production change

The workspace mutation handler now uses one connection-local reader whenever its
page source is configured. Initial replay and subsequent delivery call the same
page reader with the last successfully written cursor. Durable hub payloads only
wake that reader: their payloads, order and cursor strings cannot reach the wire.
The hub coalesces these wakeups outside its payload/retry queues, so saturation,
late replay copies and reversed notifications cannot move the reader backward.

Registration precedes source selection. A request with Last-Event-ID retains
that exact cursor. A fresh request resolves the source's `$` head selector and
rejects missing or unresolved heads, events in the head response, or has_more.
It checkpoints that head as subscribe-from, independently of the shared
subscriber's possibly stale activation head. This does not certify an initial
query snapshot. The existing source's empty-stream `0` and incarnation contracts
still determine empty-stream behavior.

Each page is validated before any writes: bounded size, actual source IDs,
progress and within-page cursor consistency. A mutation advances the connection
cursor only after a successful frame write. A filtered tail advances through a
checkpoint only after all matching records were written. A writer failure stops
delivery at the successful prefix. Opaque cursors are compared for identity only.
A bounded recent page history rejects short cycles without limiting the lifetime
or total event count of a healthy connection.

Page limits are scheduling boundaries, not permission to discard records and
jump ahead. Pagination yields to cancellation, heartbeat and transient work.
`connected` follows the first terminal page. The same reader handles wakeups
thereafter; heartbeat reconciliation reads the source even if notifications are
entirely missing. A wakeup arriving during the final empty read remains pending.
Transient notifications remain no-ID hints. Their overflow requests refresh
without choosing a cursor from discarded payloads.

Source errors and retention expiry emit no-ID resync and stop normal delivery.
They do not publish a retention floor, discard an undelivered prefix while
checkpointing past it, or claim connected. A network write failure simply ends
the connection. The old buffered replay algorithm is superseded by this reader.

## Evidence to run

- Reader: exact opaque order, filtered checkpoints, backend failure, malformed
  pages, partial mutation/checkpoint write failure, suffix retry and bounded
  cycle history across a sustained healthy source.
- Hub: durable wakeups bypass saturated queues, stay workspace scoped, and do
  not forward durable payloads to authoritative clients.
- Handler: fresh head ignores stale activation head, page-budget continuation,
  missed-wakeup reconciliation, source error/expiry retains cursor and prevents
  connected, and a wakeup during the final empty source read triggers a reread.
- Adapter/HTTP replay: existing 201-event proof now obtains its live sentinel
  from the authoritative source instead of accepting a broadcast payload.

These are deterministic storage-fake and local HTTP proofs, not paired browser,
real FleetDB projection feed or storage-server restart evidence.

## Remaining contracts and rollout limits

This draft does not complete snapshot recovery. The frontend still initiates
refresh without acknowledging that every affected query succeeded. After expiry,
reconnect keeps the old checkpoint and can repeat expiry until a successful
snapshot/reset contract is implemented. This deliberately exposes the missing
recovery transition instead of silently advancing past unobserved state.

The follow-on [fixed replay boundary](sse-fixed-replay-fence-proof.md) now consumes
FleetDB's bounded raw-page contract, so continuously advancing source pages
cannot extend the initial pass beyond its captured head. Snapshot acknowledgment
and cross-pass source incarnation still require separate contracts.

The current page source remains FleetDB's mutation API. The staged committed
projection prefix must replace raw append visibility before notifications can
certify projected query effects. Projection manager integration, receipt
retention/incarnation handling, query freshness and dependency invalidation,
real storage-server restart tests, and paired browser proofs remain required.
No merge or deployment is part of this change.

## Recorded local validation

The integrated race run passed realtime (8.981s), subscriptions (4.013s), and
app (13.687s). Scoped Go lint reported zero issues. Generated TypeScript and Go
API freshness checks passed. The real-library frontend cursor tests from the
parent remain prior-revision evidence; frontend runtime code is unchanged here.

The handoff test passed normally. A temporary Go overlay disabled only durable
wakeups; that negative control failed with `wake during empty read was lost`
after three seconds, before the default periodic reconciliation interval.
The overlay is outside the repository and is not part of the implementation.
Independent reader/hub/handler reviews found no blocker within the stated scope.
