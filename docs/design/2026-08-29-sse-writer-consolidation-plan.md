# Complete SSE writer consolidation plan

## Decision

Complete the consolidation at the existing
`internal/webui/server/realtime.Writer` seam. Every production Go SSE frame
must be encoded and flushed by that module. Stream-specific code will continue
to own authentication, cursor selection, polling, heartbeat cadence, response
headers, write deadlines, and domain payloads.

This is grounded to the latest remote `v5` tip verified on 2026-08-29:
[`90a3ae716a373b33f9c23a0eb6142e1ceb7db0cf`](https://github.com/tysonthomas9/loomcli/tree/90a3ae716a373b33f9c23a0eb6142e1ceb7db0cf).

The implementation should reuse PR #182's useful migration, but go further in
two places: make bypassing the writer hard and detectable by hiding its
underlying writer/flusher (backed by the drift guard in step 7 — a tripwire,
not a proof), and propagate frame-write failures instead of silently dropping
them.

PR #182 disposition (decided 2026-08-29): reference only — no cherry-pick and
no rebase. Its SSE commit (`53a55b006`) targets a stale epic-stack base and
stops short of this plan (public writer fields, swallowed errors); implement
fresh off the v5 tip. (Correction 2026-08-29: an earlier note claimed the
commit predates the otel handshake spans — false; its parent already has
them.) Close #182 as superseded once this plan and the preflight
re-implementation land, with its connector-redaction commit dispositioned
separately.

## Why this seam

SSE transport formatting is one module with one concrete HTTP adapter. The
callers should know only which protocol concept they are sending: a resumable
event, a non-resumable event, a retry directive, or a comment. They should not
know line ordering, blank-line termination, multiline-data encoding, or when
to flush.

The deletion test supports this placement: removing `realtime.Writer` would
recreate SSE formatting and flushing logic in realtime updates, driver watches,
workflow streams, log streams, and PR-review streams. Keeping the seam there
therefore provides real leverage and locality.

Do not add a hypothetical transport interface around the writer. There is one
production adapter (`http.ResponseWriter`), while tests already have the second
useful adapter in `httptest.ResponseRecorder`.

## Current v5 inventory

| Stream | Current frames and cursor contract | Current writer use | Required migration |
| --- | --- | --- | --- |
| Workspace realtime (`/events`) | `retry`, ID-less `connected`, opaque-ID `mutation`, heartbeat comments; resumes with FleetDB cursor | Mixed: mutations/retry/comments use `Writer`; `connected` writes through public `sw.W` | Add ID-less event support and remove direct access to the underlying writer |
| Driver epic watch | integer-ID `snapshot`, `taskRun`, and `closed`; heartbeat comments; resumes with `Last-Event-ID` or `afterSeq` | Already uses `Writer` | Preserve IDs/cursor ordering; adopt the PR #182 retry prelude at `realtime.RetryMs` (decided 2026-08-29) |
| Workflow run stream | ID-less `event` and `error`; store pagination uses the private `after` cursor | Manual `http.Flusher` plus `fmt.Fprintf` | Move both frame types to ID-less writer calls; do not accidentally add browser resume IDs |
| Log stream | `retry`, integer-ID `log-chunk` and `truncated`, heartbeat comments | Entirely manual framing/flushing | Thread `*realtime.Writer` through the log implementation and return write failures |
| PR-review stream | opaque-ID `status` and `message`, heartbeat comment | Already uses `Writer` | Regression-only; its string IDs prove the opaque-ID interface remains necessary |
| Flue integration fake | integer-ID `snapshot` and `taskRun` | Manual test helper | Use the production writer so the fake cannot drift from the real wire contract |

Primary source pointers at the pinned tip:

- [writer](https://github.com/tysonthomas9/loomcli/blob/90a3ae716a373b33f9c23a0eb6142e1ceb7db0cf/internal/webui/server/realtime/writer.go)
- [workspace realtime handler](https://github.com/tysonthomas9/loomcli/blob/90a3ae716a373b33f9c23a0eb6142e1ceb7db0cf/internal/webui/server/realtime/handler.go)
- [driver watch](https://github.com/tysonthomas9/loomcli/blob/90a3ae716a373b33f9c23a0eb6142e1ceb7db0cf/internal/webui/handlers/driverapi/watch.go)
- [workflow stream](https://github.com/tysonthomas9/loomcli/blob/90a3ae716a373b33f9c23a0eb6142e1ceb7db0cf/internal/webui/handlers/workflows/module.go)
- [log streamer](https://github.com/tysonthomas9/loomcli/blob/90a3ae716a373b33f9c23a0eb6142e1ceb7db0cf/internal/webui/log/streamer.go)
- [PR-review stream](https://github.com/tysonthomas9/loomcli/blob/90a3ae716a373b33f9c23a0eb6142e1ceb7db0cf/internal/webui/handlers/prreview/stream.go)

## Target writer interface

Keep the protocol-shaped public methods:

```go
type Writer struct {
	w       io.Writer
	flusher http.Flusher
}

func NewWriter(http.ResponseWriter) (*Writer, error)
func (*Writer) WriteRetry(milliseconds int) error
func (*Writer) WriteEvent(id int64, event, data string) error
func (*Writer) WriteEventID(id, event, data string) error
func (*Writer) WriteEventNoID(event, data string) error
func (*Writer) WriteComment(text string) error
```

`w` and `flusher` must be private. All four frame variants should delegate to
one private frame encoder that performs one write followed by one flush. This
keeps the public interface descriptive while putting the protocol mechanics in
one implementation.

The private encoder must:

- emit fields in stable order and terminate every frame with one blank line;
- encode every line of multiline data as its own `data:` field, splitting on
  all three SSE line terminators (`\r\n`, `\r`, `\n`) since a lone carriage
  return breaks framing exactly like a newline;
- reject carriage returns or newlines in event names and IDs, preventing frame
  injection; also reject NUL (U+0000) in IDs — the WHATWG algorithm silently
  ignores such an `id` field — and reject negative retry values, since `retry`
  is only honored when its value is ASCII digits;
- encode multiline comments as separate comment lines, symmetric with data
  (decided 2026-08-29): the encoder is total over text payloads and reserves
  rejection for identity fields;
- return the write error and avoid pretending a failed frame was delivered;
- flush after a successful write. Note `http.Flusher.Flush()` returns no
  error, so "propagate failures" means write errors; a connection that dies
  at flush time surfaces on the next write. The writer is not
  concurrency-safe and its doc comment must say so — every current stream
  serializes writes within one handler loop.

Do not add JSON marshaling to this interface. JSON is a domain-payload concern,
and several callers already have meaningful marshaling failure paths. The
writer should own SSE framing, not become a generic serialization module.

## Preserved contracts and non-goals

This is a transport refactor, so these behaviors must remain unchanged unless
called out as a separate decision:

- Realtime mutations keep opaque FleetDB IDs; driver-watch and log events keep
  integer IDs; workflow events remain ID-less. Note the preserved IDs are wire
  fields, not all replay cursors: only realtime and driver-watch honor a
  returned `Last-Event-ID`. Log replay is keyed by byte offset and PR-review
  ignores `Last-Event-ID` entirely (fresh per-connection `seen` state), so
  their IDs are preserved verbatim without implying resumability.
- A non-resumable event (see `CONTEXT.md`, Streaming Language) must not
  overwrite a browser's `Last-Event-ID`. "Control frame" is a misnomer for
  this class: workflow `event`/`error` frames are non-resumable yet carry
  domain payloads.
- The workflow event named `error` remains a server event even though browsers
  also use `error` for transport failure; the existing frontend intentionally
  handles both paths.
- Each stream retains its current heartbeat cadence. Consolidating encoding
  does not require coupling all streams to one interval.
- Authentication, one-time SSE tokens, workspace scoping, filters, polling,
  replay queries, and lifecycle ownership remain in their current modules.
- Response headers and long-lived write-deadline policy remain handler-owned;
  they differ by stream and are not frame encoding.
- No frontend wire-shape or generated OpenAPI change is expected.
- No new generic `Frame` type, stream runner, hub abstraction, or mock-only
  port is introduced.

## Deferred follow-up: frontend consumption (2026-08-29)

This plan consolidates emission only; the consuming side is intentionally
untouched and remains unconsolidated. Three independent SSE parsers exist
today:

- `src/api/common/sse.ts` (`WorkspaceSSEClient`): full EventSource client
  with one-time token fetch and `Last-Event-ID` resume, mounted app-wide by
  `useEventProvider.tsx` for the workspace realtime feed;
- `src/hooks/workflows/useWorkflowRunStreams.ts`: a second, bare EventSource
  implementation with restart-on-error and deliberately no resume;
- `sdk/driver.js`: the Node SDK's hand-rolled fetch parser for the driver
  epic watch (honors `retry:` hints, sends `Last-Event-ID`).

A later client-side consolidation should extract the shared
connect/reconnect/parse lifecycle so the browser has one SSE client, mirroring
what this plan does for the server. That work is not blocked by, and must not
be bundled into, this consolidation.

Consumer inventory also found two producers with no consumer at the v5 tip:
the PR-review stream route is registered but nothing in the frontend or SDK
calls it, and `log.LogStreamer` is not routed at all (the log viewer uses the
JSON archive endpoint plus the tmux WebSocket). Step 5 still migrates the log
streamer so the seam is complete, but whether to instead delete these dormant
producers is a separate scope decision deferred with the consumer work.

## Implementation sequence

### 1. Lock the protocol at the writer seam

Add table-driven golden tests in `realtime/writer_test.go` before migrating
callers. Cover integer ID, opaque ID, ID-less event, retry, comment, flush, and
unsupported flusher. Add focused cases for multiline data, forbidden newline
in ID/event, NUL in an ID, a negative retry value, trailing and consecutive
empty multiline-data segments, and an underlying write failure.

These tests become the primary test surface for framing. Endpoint tests should
assert only the stream-specific sequence, names, IDs, and payloads.

### 2. Deepen `realtime.Writer`

Add `WriteEventNoID`, introduce the private frame encoder, and make the writer
and flusher fields private. Preserve the existing public methods so the already
consolidated driver-watch and PR-review callers need little or no change.

Do not expose a raw escape hatch. If a future SSE field is needed, add it as a
protocol concept with a golden test.

### 3. Remove the workspace-realtime bypass

Replace the direct `connected` write in `realtime.Handler` with
`WriteEventNoID`. Preserve the exact ordering:

1. catch-up mutations;
2. retry directive;
3. ID-less `connected` event;
4. live loop and heartbeat comments.

Keep the existing live tests that prove retry precedes `connected` and that
resume/catch-up IDs remain intact.

### 4. Migrate workflow streaming

Construct one `realtime.Writer`, replace the local `writeSSE(io.Writer, ...)`
helper with an error-returning helper over `*realtime.Writer`, and emit both
`event` and `error` with `WriteEventNoID`.

This changes flush cadence observably: today the loop flushes once per fetched
page (up to 100 events), while the writer flushes per frame. Accepted — per-
frame flushing only lowers delivery latency and the pages are small in
practice; do not add a batch API for this.

Stop the loop immediately on marshaling or write failure. Stopping on marshal
failure is a small behavior change (today `writeSSE` ignores it and emits an
empty-data frame) and is intended. Marshal-error policy stays per-stream: the
realtime handler's deliberate skip-and-continue in `writeSSEEvent` is existing
behavior and must not be "fixed" to terminate; only write failures terminate
every stream. Keep the store's `after` cursor internal; emitting it as an SSE
ID would silently change browser reconnect semantics and is outside this
consolidation.

### 5. Migrate log streaming end to end

Pass `*realtime.Writer` through replay, live reads, truncation handling, and
heartbeat emission. Replace every manual retry/comment/event write. Change
`sendLogChunk`, `sendTruncatedEvent`, and their callers to return errors, and
terminate the stream when the client write fails.

Retain the current log heartbeat interval and event-ID source. Keep the local
`logHeartbeatInterval` constant; do not reuse PR #182's swap to
`realtime.HeartbeatInterval`, which is equal today (30s) but couples log
cadence to the shared constant against this plan's non-goals. Do not replace
byte offsets with SSE IDs: `ByteOffset` is payload state, while `NextEventID()`
is the existing SSE identity contract.

### 6. Finish remaining producers

- Keep driver-watch writes on `Writer`; add the retry prelude at
  `realtime.RetryMs` (decided 2026-08-29) with a raw-frame test asserting the
  retry frame precedes the snapshot. The only consumer, `sdk/driver.js`,
  already honors server `retry:` hints; the hint overrides both the client
  default 2000ms and any caller-supplied `reconnectMs` (that override is the
  SDK's documented contract). Update `watch_test.go`'s frame parser first —
  its `nextFrame` currently fails the test on any `retry:` line (reuse PR
  #182's parser change) — and add an SDK test proving the override.
- Move the Flue integration fake to `Writer`, including the same retry
  prelude, so fake and production emit an identical handshake sequence.
- Verify PR-review still compiles with private writer internals and preserves
  opaque message/status IDs; extend an endpoint test to assert the literal
  `id:` lines, which no existing PR-review test does.
- Search all production Go files for direct SSE framing. The only literal
  protocol encoding should remain inside `realtime/writer.go`; tests may retain
  literals as expected wire fixtures.

### 7. Add drift prevention

Implement the check as an AST-based Go test (decided 2026-08-29): a
`writer_seam_test.go` in the realtime package that walks production packages
with stdlib `go/parser` and fails on any string literal containing SSE field
prefixes (`id:`, `event:`, `data:`, `retry:` — no trailing space required,
since SSE accepts `event:foo` — or a leading `:` comment frame) outside
`internal/webui/server/realtime/writer.go`, skipping `_test.go` files. This
runs under `check-go` (and therefore `make check`, CI, and the pre-push hook)
with no Makefile wiring. It is a tripwire, not an enforcement proof:
concatenated literals, formatted field names, or raw writes to the original
`http.ResponseWriter` can evade it. The real guarantees are the private
fields plus review; the test catches the honest mistakes.

A text grep was rejected because production doc comments legitimately quote
frames (`driverapi/watch.go` documents `"event: snapshot"`); the AST sees only
real string literals, so those never false-positive. Keep the check narrow: it
prevents bypassing this seam, not ordinary strings that merely discuss events.

## Verification

Run focused Go tests first:

```text
go test ./internal/webui/server/realtime \
  ./internal/webui/handlers/driverapi \
  ./internal/webui/handlers/workflows \
  ./internal/webui/handlers/prreview \
  ./internal/webui/log \
  ./internal/driver -count=1
```

Then run the frontend consumer tests that depend on these wire contracts:

```text
cd internal/webui/frontend
npm run test:unit -- src/api/common/__tests__/sse.test.ts
```

(`src/hooks/workflows/useWorkflowRunStreams.ts` has no unit tests; a path
filter naming that directory makes vitest exit non-zero on zero matches. The
hook's behavior is exercised by the workflow E2E flows, not unit tests.)

Also run the SDK suite, since the retry prelude changes driver reconnect
semantics and `make check` does not cover the SDK:

```text
cd sdk && npm test
```

Finally run `make check`, `git diff --check`, and the repo's clean-environment
Go gate described in `AGENTS.md`. Deterministic HTTP/SSE integration tests
remain the merge evidence class; no live external backend or paid-service
proof is required. In addition, run the one-time full-stack smoke below before
opening the PR (decided 2026-08-29).

### Full-stack smoke (pre-PR, one time)

Bring up a podman local-mode stack on the implementation branch, on an
unclaimed port block with a session-owned compose project (never the default
`loomcli-local-mode` project; check `podman ps` first). Requires the sibling
fleet-db checkout; a stale `~/go/bin/fleet-db` is a known local-mode breaker.
Script the frame assertions (grep over captured `curl -N` output) in
`loom-aug/.scratch/sse-smoke/`; the script is environment-specific and stays
uncommitted. Tear down only the session-owned compose project afterwards.

1. Realtime handshake bytes: attach `curl -N` to
   `/api/workspaces/{ws}/events` with a token and assert the live frame
   order — `retry: 5000`, then `event: connected` with data and no `id:`
   line, then a `: heartbeat` comment after ~30s.
2. Live mutation end to end: with the stream attached, mutate real state via
   the API and assert a `mutation` frame arrives with an opaque FleetDB
   cursor as its `id:`.
3. Resume/catch-up: record that cursor, disconnect, mutate again while
   disconnected, reconnect with the cursor, and assert the missed mutation is
   replayed before the `retry`/`connected` pair — the ordering contract
   proven against real FleetDB replay.
4. Driver watch, both layers: raw `curl -N` asserting `retry: 5000` precedes
   the `snapshot` frame, then a small Node script using the real
   `sdk/driver.js` to watch an epic, drop the connection, and confirm resume
   via `Last-Event-ID`.
5. Browser UI live update: with the stack seeded, verify in a real browser
   that the workspace view updates on a terminal-driven mutation without a
   refresh and the console shows no EventSource errors.

Deliberately skipped live: the workflow stream (E2E-covered consumer,
ID-less pass-through wire) and the PR-review/log streams (no consumers to
attach).

## Acceptance criteria

- No production Go code outside `realtime/writer.go` formats SSE protocol
  lines or flushes an SSE frame directly.
- All existing stream event names, IDs, cursor/replay behavior, payloads,
  heartbeat cadence, headers, and pre-stream JSON errors remain compatible.
- Non-resumable events are provably ID-less, so they cannot advance reconnect
  state.
- Writer failures terminate the affected stream rather than being ignored.
  This means write errors — `http.Flusher` cannot report flush failures — and
  a terminal frame (`closed`, final `error`) may drop its own write error
  since the stream is ending either way.
- Writer golden tests cover every supported frame form and malformed-field
  rejection.
- Endpoint tests cover realtime retry/connected ordering, driver-watch resume,
  workflow ID-less event/error frames, log retry/chunk/truncation behavior, and
  PR-review opaque IDs.
- A repository check guards against manual SSE framing reappearing (a
  tripwire for honest mistakes, per step 7 — not an enforcement proof).
- The pre-PR full-stack smoke passed all five checks against a live
  local-mode stack.

## Recommended commit shape

Deliver as one PR based on the `v5` tip containing both commits (decided
2026-08-29); a stacked pair adds ceremony without review value at this size.
The in-flight `refactor/status-vocabulary-single-source` branch overlaps this
plan's file set only at `prreview/stream.go` and `sdk/driver.js`, neither of
which this plan modifies, so landing order against that branch does not
matter.

Use two commits so review can separate the seam from behavior-preserving
migration:

1. `realtime: deepen the shared SSE writer` — private fields, private encoder,
   ID-less support, golden/error tests.
2. `webui: route all SSE producers through realtime.Writer` — caller
   migrations, endpoint tests, Flue fake, and drift-prevention check.

The driver-watch retry directive (decided above) must be called out explicitly
in the second commit message because it is a small observable behavior
addition — the SDK reconnect delay moves from 2000ms to 5000ms — not merely
consolidation.
