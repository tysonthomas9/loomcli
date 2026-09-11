# Bind SSE to one subscriber identity

Fixed replay cursors did not fix source selection. Previously each head or page
request looked up the current workspace subscriber independently. Replacing
that subscriber between calls could combine one source's captured head with
another source's pages. Identical cursor values, including Redis IDs reused by
another stream, could hide the switch.

The authoritative handler now opens one `MutationSource` after subscriber
activation and retains it for the entire connection. Its narrow interface owns
head reads and bounded page reads. Replay and subsequent live passes use the
same object; they never call the factory again. The production module/app
wiring replaces the two independent page callbacks with this factory.

`MultiWorkspaceSubscriber.OpenMutationSource` captures the exact registry entry.
Before and after each read it checks that the entry is still registered, the
manager is open, and the caller context is valid. Calls always use that captured
subscriber. Missing or replaced entries fail with an empty result; they cannot
select a replacement. No registry lock is held during backend I/O.

Head reads now require a cursor-capable backend and share the existing bounded
page lifetime guard. Subscriber retirement and caller cancellation cancel both
types of request. Post-read checks reject even a backend that ignores abort and
returns a successful response late. The factory's short setup context is used
only during opening; canceling it after return does not invalidate the source.

## Evidence

Testing coordinates: unit/integration depth; deterministic sources and handler
fixtures; isolated provisioning; positive, failure, and negative-control cases;
targeting the actual Loom handler, subscriber registry, and app wiring. No
deployed FleetDB, browser, or paid agent execution is claimed.

The handler/registry regression replaces the source inside the response writer,
after the previous registry post-read check and before the next page or live
pass. Old and replacement sources advertise the same fence. Both cases require
zero replacement reads, exactly one accepted SSE ID, and a no-ID resync. An
incomplete initial replay cannot emit connected. This exercises the original
selection gap rather than relying on mismatched cursors to cause an error.

A temporary Go overlay deliberately restores per-read registry reselection.
Both cases fail: the replacement receives a read when its counter must remain
zero. The overlay lives outside the repository and is not part of the change.
The unmodified regression passes under the race detector.

Other tests count exactly one factory invocation across pages and live passes,
reject nil/error/canceled factory results before checkpointing, and cover
in-flight replacement, source retirement, strict head capability, canceled
head reads, and manager closure. Existing fixed-fence and 201-event replay tests
retain their assertions through test-only source adapters.

Validation uses `GOCACHE=/private/tmp/loom-sse-integration-go-cache` and logs under
`/private/tmp/sse-stack-review/source-binding*`.

```sh
go test -race -p 1 ./internal/webui/server/realtime ./internal/webui/subscription ./internal/webui/appstores ./internal/webui/app
go test -race ./internal/webui/subscription -run '^TestBoundSourceHandler' -count=1 -v
go build ./...
go vet ./internal/webui/server/realtime ./internal/webui/subscription ./internal/webui/appstores ./internal/webui/app
golangci-lint run ./internal/webui/server/realtime/... ./internal/webui/subscription/... ./internal/webui/appstores/... ./internal/webui/app/...
```

Two independent cross-reviews found no blocker in the source wrapper, handler,
app wiring, and regression evidence. Full repository gates, frontend tests,
and paired browser proof are outside this validation scope.

Final affected race suites passed: realtime 3.943s, subscription 3.754s
(reused by the final integrated run), and app 13.279s; appstores compiled with
no tests. The focused handler proof passed in 1.396s. Build and vet passed;
scoped lint reported zero issues. The negative-control run exited 1 with the
expected replacement-read counter failure in both subtests.

## Remaining goal

This binds the current connection to a subscriber; it does not establish durable
source incarnation across reconnects. A reconnect can open a new subscriber,
and the existing opaque cursor still needs backend-owned workspace/incarnation
validation to detect source recreation. Stream recreation behind an unchanged
subscriber object also requires that durable contract.

A page already validated against the old source may finish writing after the
entry is retired. The next read fails; no replacement is adopted. This is source
continuity, not atomic exclusion between retirement and every frame write.
An idle retired connection detects the change on its next wakeup or heartbeat
read; this does not promise immediate disconnect at retirement.

Retained-cursor recovery still needs a committed fence before query refresh and
explicit acknowledgment after all required refreshes succeed. Reset re-expiry,
remaining projection work, cross-repository CI, and paired fetch-SSE browser
proof remain part of the active goal. No merge or deployment is included.
Rollback reintroduces per-call selection; there is no migration or data rewrite.
