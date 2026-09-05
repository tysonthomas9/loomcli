# Fixed SSE replay boundary

The authoritative SSE handler now captures one readable mutation head per replay
pass and uses FleetDB's fixed `(since, through]` interval for every page. A source
that keeps growing cannot extend that pass beyond its original head. `connected`
follows completion of the initial interval; subsequent wakeups or heartbeat
reconciliation capture a new head for the next pass.

This consumes the API in [FleetDB PR #272](https://github.com/BrowserOperator/fleet-db/pull/272),
commit `b623ea823bb1d46d15afd0d46f1e3571cb42ba43`. That producer bounds raw storage
pages before filters, preserving the terminal cursor even when the head event
is not visible. Deploy the producer contract before this consumer. A bounded
request never falls back to an unbounded page or timestamp cursor.

## Contracts and changes

- Fleet's bounded client sends `since`, `through`, `limit`, and `timeout=0`.
  It requires all response fields, rejects malformed token envelopes and
  contradictory terminal pages, and preserves typed cursor-expiry errors.
- The v2 head selector is now encoded as `c1.JA`. Previously Loom sent literal
  `$`, which FleetDB's v2 opaque-cursor decoder rejects. Both head probing and
  authoritative head reads now use the producer's actual wire contract.
- The subscriber, workspace routing, appstores, and subscription module carry
  the bounded callback through to the SSE handler. Missing capability is an
  error. Caller cancellation and subscriber retirement cancel reads; results
  from a replaced subscriber are rejected before leaving that request.
- Every replay pass freezes its head. The reader compares opaque identities
  only: terminal `has_more=false` must mean `cursor==through`, and a page that
  reaches the fence cannot claim more work. Validation precedes frame writes.
- A fresh request validates its empty bounded interval before checkpointing its
  head. An expired or failed read therefore cannot acknowledge a fresh head.
  Resumed requests preserve their last successfully written event/checkpoint.
- Hub durable payloads remain wakeups only. Existing source-repo filtering,
  per-page yielding, heartbeats, and write-failure checkpoint rules remain in
  force. No frontend runtime code changes in this PR.

The new authoritative contract accepts opaque cursors and the explicit origin
`0`. Legacy numeric Last-Event-ID values fail closed with a no-ID resync, no
bounded HTTP request, no connected frame, and no new checkpoint. Existing
unbounded methods retain their older normalization behavior; it is not used to
coerce a bounded replay checkpoint.

## Evidence

Testing coordinates: integration/unit depth, deterministic sources and local
HTTP fixtures, isolated provisioning, positive and adversarial cases, targeting
the Fleet HTTP client through the Loom SSE handler. This is not actual FleetDB
storage, a deployed stack, paid agent execution, or browser evidence.

The fixed-fence tests grow the source during pagination, preserve a head that
does not sort lexically after preceding cursors, deliver a filtered terminal
checkpoint, and capture a new head only for the next pass. Other cases reject
terminal disagreement and missing capability before writes, retain checkpoints
on expiry, and resume after a partial writer failure within the same fence.

Existing HTTP replay tests now assert explicit head and bounded requests through
the real Fleet client. The 201-event regression retains its before-connected
ordering and queued-overlap/live-sentinel assertions. A separate regression
checks numeric-cursor rejection rather than silently converting it. Client
tests reject incomplete, null, contradictory, and nonprogressing responses and
verify head encoding against a strict opaque-head fixture.

Subscriber tests cover context propagation, caller cancellation, retirement,
missing capability, and replacement during a read. App SSE tests continue to
exercise the production callback wiring. Independent cross-reviews covered the
reader, HTTP client, and subscriber seams.

Commands use `GOCACHE=/private/tmp/loom-sse-integration-go-cache`; logs are under
`/private/tmp/sse-stack-review/loom-replay-fence*` and the companion focused logs.

```sh
go test -race -p 1 ./internal/backend/fleet ./internal/webui/server/realtime ./internal/webui/subscription ./internal/webui/appstores ./internal/webui/app
go build ./...
go vet ./internal/backend/fleet ./internal/webui/server/realtime ./internal/webui/subscription ./internal/webui/appstores ./internal/webui/app
golangci-lint run ./internal/backend/fleet/... ./internal/webui/server/realtime/... ./internal/webui/subscription/... ./internal/webui/appstores/... ./internal/webui/app/...
```

The integrated affected race run passed. Final post-review client and realtime
race runs passed in 1.634s and 3.590s; the complete subscriber suite passed in
3.709s and app suite in 13.210s (then reused by the final integrated run).
Build and vet passed; scoped lint reported zero issues, with its existing
unknown `norawexec` directive warning. Independent final review found no blocker.
Initial runs exposed old literal-head and numeric-cursor fixture expectations;
those were updated to the strict wire contract and rerun. No full repository
gate, frontend suite, or browser proof is claimed by these results.

## Remaining goal and rollout limits

This fixes the moving replay target; it does not certify refreshed query state.
Expired-cursor recovery still needs a committed fence captured before the
required queries refresh, explicit acknowledgment after success, and attempt
invalidation on expiry or scope change. Until then, reconnect can repeat the
same expired checkpoint. The full streaming goal remains incomplete.

Subscriber identity is checked per request, not pinned across head capture and
all pages. Replacing a subscriber between requests can select a new source.
Fleet validates fence membership, but existing cursor tokens lack durable
workspace/incarnation identity; Redis ID collisions or recreation require a
stronger source lease or generation contract. This PR does not claim to close
that race, pin retention, or prove completeness before a retained origin `0`.

Paired fetch-SSE browser proof and cross-repository CI remain required. The
backend and handler fixtures are not a substitute for those proofs. No merge
or deployment is included. Rollback this consumer before removing FleetDB's
bounded API; there is no schema migration or frontend bundle change.
