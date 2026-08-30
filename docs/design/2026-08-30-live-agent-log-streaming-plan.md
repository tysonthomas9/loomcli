# Live agent-log streaming plan

> Revised 2026-08-30 after an adversarial codex vet. The vet confirmed the
> goal and the route placement but broke three original contracts: native
> EventSource reconnect (incompatible with one-time tokens), the
> `byte_size` archive→live handoff (not gap-free across truncation or
> rotation), and running-only stream gating (races the components cannot
> see). Each reversal is recorded inline with its evidence.

## Goal

Live tail of agent logs over SSE in two existing UI surfaces:

1. **Agent detail → Logs tab** (`AgentDetailPanel/AgentLogsTab.tsx`): today it
   prefers the tmux terminal and otherwise shows a static archive snapshot —
   which is exactly the case for background agents (planner, task runner). It
   gains a live mode: scrollback first, then new lines appended as the agent
   works.
2. **Issue detail → task-log phase tabs** (`IssueDetailPanel`, the
   `task-log-{phase}` tabpanels): today a 500ms-polled text blob; the tabs
   gain the same live append.

Grounded on v5 plus PR #548 (SSE writer consolidation): `log.LogStreamer`
already implements the hard part — replay from a byte offset, fsnotify tail,
debounced `log-chunk` events with integer SSE IDs and `byte_offset` payloads,
a `truncated` event on truncation, heartbeats — and after #548 it is on the
shared `realtime.Writer` with error propagation. It is currently unrouted;
this plan gives it routes and consumers.

## Decisions

1. **Two thin routes in the log module.**
   - `GET /api/workspaces/{ws}/agents/{name}/logs/stream`
   - `GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}/stream`
   Query params: `offset=N` (resume cursor, wins when present) or
   `tail_bytes=N` (initial scrollback window from EOF); neither means replay
   from 0. Each handler: auth → validate params → resolve path with the
   **`log` package resolvers only** (`log.GetAgentLogPath`,
   `log.GetTaskLogPath` — the latter is *already exported*; its doc comment
   is stale, fix it in passing. Never the private `handlers/misc`
   duplicates: they root at `os.UserHomeDir()` and ignore
   `LOOM_WORKSPACE_RUNTIME_DIR`/`LOOM_CONFIG_DIR`, so archive and live
   would read different roots on the stack) → `NewLogStreamer(path)` →
   **`defer streamer.Close()`** (the constructor owns an fsnotify watcher;
   `Stream` does not release it — skipping Close leaks a watcher per
   viewer) → `Stream(ctx, w, start)`. All framing stays behind
   `realtime.Writer`; the #548 seam test keeps it honest.

2. **Param validation before path resolution — mandatory, not defensive.**
   Go 1.22+ `ServeMux` delivers `%2F` inside wildcard values decoded, so
   `{name}`/`{id}`/`{phase}` can carry `/`; and `ValidatePathWithinDir`
   fences against the *global* log root, so a traversal that hops
   workspaces still passes it. Reuse the existing validators:
   `service.IsValidAgentName` for `{name}`, and the `handlers/misc`
   `validTaskID`/`validPhase` patterns for `{id}`/`{phase}` (move them
   somewhere both packages can use). Additionally fence the resolved path
   against the **workspace** log dir for these routes, not the global root.
   Reject with 400 pre-stream.

3. **Auth reuses the one-time SSE token pattern — never Bearer — and needs
   an explicit middleware exemption.** EventSource cannot send an
   Authorization header; that exact mistake made the PR-review stream dead
   on arrival. But `isPublicRoute` exempts exactly `/api/events` (plus the
   terminal-WS patterns) — without a matching own-auth exemption for the
   two new normalized paths (`/api/agents/{name}/logs/stream`,
   `/api/tasks/{id}/logs/{phase}/stream`, GET only), the Bearer middleware
   401s them before the handler runs. So: add the two exemptions in
   `auth_routes.go` (mirroring the agent-terminal-WS suffix pattern), and
   the handlers fail closed on `?token=` via the same `realtime.TokenStore`
   (workspace-scoped) minted at the existing `/events/token` endpoint; open
   mode (nil store) needs no token. `TokenStore` today is wired only into
   the subscription module — the log module's registration gains it as a
   dependency. No new token endpoint.
   *Deferred hardening (recorded, not in scope):* tokens carry user +
   workspace but no route/resource audience, and `/events/token` checks
   identity + workspace membership but not workspace role; log routes
   inherit that. An `aud` claim / role check is a follow-up.

4. **One streaming connection does scrollback and tail — no archive
   handoff.** *(Reverses the original `byte_size` decision.)* A separate
   archive fetch handed off by size cannot be gap-free: truncation to a
   smaller file silently clamps the offset to the new EOF, and replacement
   by an equal-or-larger file is undetectable by the size-only rotation
   check — and it would also mix two encodings (archive = JSON string
   lines, live = base64 raw bytes). Instead the client opens **only the
   stream**, with `tail_bytes` for the initial window; the streamer
   computes `start = max(0, size - tail_bytes)` against the *same stat* it
   replays from, then tails — snapshot and live share one file generation
   by construction. No `byte_size` field, no archive prefetch in live
   mode. The line-oriented archive endpoints stay untouched for
   non-live uses.

5. **Missing log file = empty stream, not 404.** A phase that has not
   started or an agent that has not written yet is a normal condition (the
   archive client already maps 404 to empty). `LogStreamer` currently
   errors because replay opens the file eagerly; change it to treat
   not-exist as an empty replay (offset 0) and fall through to the event
   loop — the directory watcher already handles the Create event. Connect
   always succeeds; lines appear when the file does. This avoids a
   404-then-retry-with-fresh-token state machine in the hook. (404 remains
   only for a workspace/agent/task that fails validation or resolution.)

6. **The hook owns reconnection — never the browser's native retry.**
   *(Reverses "browser handles reconnect natively".)* Tokens are single-use
   (the nonce burns on `Validate`) and expire in 30s; a native reconnect
   replays the original URL with the burned token and dies. `useLogStream`
   mirrors `WorkspaceSSEClient`: on error, close the EventSource, fetch a
   fresh token, rebuild the URL with `offset=<last byte_offset>`, reconnect
   with backoff seeded from the server's `retry: 5000` hint. On
   `truncated`: reset local offset and rendered buffer to 0 and keep
   listening (the server continues from the new file's start). A rotation
   that happens *across* a disconnect can still lose or duplicate tail —
   accepted for logs; the `truncated` event covers the connected case.

7. **Bytes are decoded as a stream.** `log-chunk` payloads are base64 raw
   bytes chunked at 32 KiB, which can split a multibyte character; the hook
   feeds decoded bytes through one `TextDecoder` with `{stream: true}`
   rather than decoding chunks independently. ANSI escapes get the same
   treatment the archive view gives them today.

8. **Surface behavior: stream while visible.** *(Reverses running-only
   gating.)* Gating on a "running" signal races startup, completion, and
   late flushes — and `AgentLogsTab`'s `isActive` prop means *tab
   visibility*, not agent state. Instead: whenever the logs surface is in
   archive mode and visible, the stream is open; close on hide/unmount. A
   stopped agent's stream simply replays and idles on heartbeats. tmux
   terminal remains preferred when a session exists. Same rule for task
   phase tabs (which today poll every 500ms; live phases stop polling,
   completed phases can keep the one-shot fetch).

9. **Non-goals.** No tmux replacement; no change to `loom daemon logs -f`;
   no PR-review changes; no writer/frame changes; no new SSE client
   abstraction (the hook is an input to the deferred client-side SSE
   consolidation); no consolidation of the `handlers/misc` duplicate
   DTOs/path helpers beyond what the new routes force (recorded as
   follow-up); no token audience hardening (see decision 3).

## Implementation sequence (vertical slices)

Base on the `refactor/sse-writer-consolidation` branch (this work depends on
#548's streamer). Deliver as one PR stacked on / rebased over #548 when it
merges.

### Slice 1 — agent surface, end to end (kernel included)

1. Streamer contract fixes: missing-file-tolerant replay; `tail_bytes`
   start computation inside replay (same stat). Unit tests: not-exist →
   empty replay then live lines on create; tail window; truncation;
   watcher released on Close.
2. Agent stream route in the log module (`module.go` registers it; module
   gains `TokenStore`): validation → resolution (log package resolvers,
   workspace-dir fence) → `defer Close()` → Stream. Middleware exemption in
   `auth_routes.go` + its table test. Endpoint tests over httptest: retry
   prelude, replay from offset, tail_bytes, live append, truncated event,
   401 without/with-burned token when a store is configured, 400 on
   traversal-shaped params, connect-then-create for a missing file.
3. `useLogStream` hook (token fetch → EventSource → streaming decode →
   manual reconnect at last offset) + AgentLogsTab live mode, unit-tested
   with the mocked EventSource pattern from `sse.test.ts`.
4. Full-stack proof on the running codex local-mode stack: open a working
   agent's Logs tab and watch lines appear without refresh
   (browser-verified, console clean).

### Slice 2 — task run tab

5. Task-phase stream route (same kernel: validators, fence, exemption,
   Close), endpoint tests.
6. Task-log tab live append reusing `useLogStream`; live phase stops the
   500ms poll.
7. Full-stack proof: while a codex agent works a task, its phase tab
   streams.

## OpenAPI

Add both stream paths as `text/event-stream` responses (string schema, as
the existing documented SSE routes do) with `token`/`offset`/`tail_bytes`
params, plus an explicit `LogChunkPayload` schema (`chunk_b64`,
`byte_offset`, `timestamp`) so generated types are meaningful. Run both
generation workflows (Go via the Makefile target, frontend via the
package.json script). No `byte_size` field (decision 4).

## Verification

- `go test ./internal/webui/log ./internal/webui/server/realtime
  ./internal/webui/server/middleware -count=1` plus the packages hosting
  moved validators; `go vet`.
- Frontend: hook unit tests + both surface components;
  `npm run test:unit -- src/api/common/__tests__/sse.test.ts` stays green.
- `make check`, `git diff --check`.
- Live smoke on the codex stack:
  1. raw `curl -N` on the agent stream: `retry: 5000`, tail_bytes window,
     integer-ID `log-chunk` frames, heartbeat;
  2. reconnect: second `curl` with a fresh token and `offset=<last>` —
     continues without gap or duplication (append-only case);
  3. burned token: replaying the first URL 401s;
  4. traversal probe (`%2F` in `{name}`) → 400, nothing streamed;
  5. truncation: rotate a log file, observe `truncated` then fresh chunks;
  6. browser (Opus agent): both surfaces append live, no reload, no
     console errors, EventSource visible in the network log, reconnect
     works after a forced drop.

## Acceptance criteria

- A background agent's Logs tab shows new log lines without reload, with
  end-to-end latency well under the current 500ms poll feel (server-side
  batching is 50ms debounce; transport/decode/render sit on top).
- The active task phase tab does the same.
- Scrollback and live tail come from one connection — no gaps, no
  duplicate lines in the append-only case; truncation resets cleanly.
- Streams authenticate via one-time SSE tokens in auth mode, fail closed
  pre-stream with structured JSON errors, and reconnect with fresh tokens.
- Traversal-shaped path params are rejected before any file access.
- No fsnotify watcher outlives its request.
- No SSE framing outside `realtime/writer.go` (seam test still green).
