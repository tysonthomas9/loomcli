# PR-Review — Conversation Transport Follow-ups

Branch: `feat/pr-review` · Scope: `internal/webui/handlers/prreview/*`, `internal/webui/frontend/src/hooks/workspace/usePRReviewConversation.ts`

## 1. Background — how the reviewer conversation works today

The PR reviewer is a normal interactive-role terminal agent (codex or a claude/gemini harness)
stood up by `ensureReviewer` (`reviewer.go`). That launch model is sound and is **not** in scope here.

What this doc targets is how the reviewer's **conversation** is surfaced to the web UI:

- The frontend hook `usePRReviewConversation.ts` polls `GET /conversation` on a fixed **1.5 s
  `setInterval`** (`usePRReviewConversation.ts:11,183`).
- `getReviewerConversation` (`stream.go:209`) calls `readReviewerSnapshot`, which on every request:
  - **codex**: re-dials a *fresh* codex app-server connection, reads the whole thread with all
    turns, then closes it (`readReviewerThread`, `stream.go:28-37`);
  - **harness (claude)**: re-reads and re-parses the transcript **from disk** under
    `~/.claude/projects` (`harness_read.go`).
- It returns the **entire** conversation every time — `flattenReviewerMessages` emits all turns,
  the poll path carries **no cursor** (`stream.go:219-223`).
- A second transport exists but is unused under auth: `GET /stream` (`streamReviewer`,
  `stream.go:57`) is a full SSE implementation whose own comment says it "only works over
  loopback — EventSource can't send the auth Bearer header" (`stream.go:206-208`). The frontend
  never opens it.

## 2. Problem statement

1. **Dead transport carried in tree.** `/stream` + `runReviewerStream` + `pollReviewerStream` +
   `writeReviewerStatus` (~150 LOC) are maintained but unreachable in the authed product path.
2. **The live path is the expensive one.** 1.5 s × {fresh codex dial + full-thread read, or full
   disk re-read + re-parse} + full-snapshot resend. Cost grows with conversation length, per open
   panel, indefinitely. Notably the SSE path *does* keep a `seen` cursor — the poll path discarded
   that idea.
3. **The "must poll" justification is only partly forced — the repo has an authed-streaming
   primitive, but no per-entity conversation stream uses it yet.** Nuance (corrected after codex
   vetting — see §9):
   - `realtime.TokenStore` — one-time HMAC SSE auth tokens (`sse_token.go`) — is real and **wired
     for the workspace `/events` stream** (`subscription/module.go:42-57`, `server_app.go:234`).
     The `WorkspaceSSEClient` consumes it by manually reconnecting and fetching a fresh token each
     time (`api/common/sse.ts:140-182,328-367`). So an authed-SSE pattern **does exist and is used**
     — for `/events`.
   - BUT the per-run/per-entity streaming features do **not** use it: `useWorkflowRunStreams.ts`
     explicitly **falls back to polling when an auth token is set** ("EventSource cannot send
     Authorization headers", `:21-25,237-240`). So workflows make the *same* poll-under-auth choice
     pr-review did. My earlier claim that workflows are an authed-EventSource precedent was WRONG.
   - The reviewer's own PTY *does* stream over an **authed WebSocket** via a one-time query token
     (`terminalConnection.ts:19-58`, validated at `handlers/terminal/ws.go:225-242`). So the
     WS-with-query-token pattern is a genuine in-repo precedent for authed streaming of reviewer
     data; the SSE-with-query-token pattern is proven only for `/events`, not for per-entity feeds.
   - Net: streaming under auth is *achievable* here with established primitives, but it is **not** a
     free "just reuse what workflows do" — it requires new auth plumbing (see §5.B0).
4. **Boundary reach-ins.** To reconstruct conversation state the runtime layer should own, prreview
   scrapes `claudecode`'s on-disk `~/.claude/projects` layout, type-asserts through its own reader
   seam (`harness_read.go:135`), and string-matches `leadcontrol`'s runtime-metadata key prefixes
   (`reviewer.go:525`). These are symptoms, not root causes.

## 3. Goals / non-goals

**Goals**
- Remove wasted per-poll work and dead code.
- Move to authed streaming using the transport pattern already established in this repo.
- Give the runtime layer ownership of "read this session's conversation" so prreview stops
  reverse-engineering other packages' internals.

**Non-goals**
- Changing the reviewer *launch*/interactive-role model.
- Changing PR list/diff fetching (connector-based, already consistent).
- Redaction policy changes (keep `redact.String` semantics).

## 4. Track A — v1 hardening (small, safe, ship first)

Independently landable; no API contract break for existing clients.

### A1. Resolve the dead SSE endpoint — pick ONE
- **A1a (delete):** remove `/stream` and its exclusive helpers (`streamReviewer`,
  `runReviewerStream`, `pollReviewerStream`, `writeReviewerStatus`, stream-only
  `reviewerStreamStatus`). Keep `readReviewerSnapshot`/`flatten…` — the poll path uses them.
  ⚠️ **Not pure dead-code cleanup** (corrected via vetting): `/stream` is registered
  (`module.go:103`), in the OpenAPI spec (`api/openapi.yaml:2926-2955`) and generated types, and
  covered by backend tests (`handlers_test.go:1441-1513`, `harness_read_test.go:309-336`). Deleting
  it is an API-surface + generated-types + test change. Budget for that, or don't call it "safe."
- **A1b (keep as Track B seed):** do NOT delete — carry it into Track B and auth it (see §5.B0).
  Do not keep both a dead SSE endpoint and the poll indefinitely.
- *Decision needed:* delete now (accepting the API/test churn) vs. keep as the streaming seed.

### A2. Make the poll incremental
- Add an **optional** opaque cursor (`after=<turnID/itemID>`) to `GET /conversation`. Contract:
  omit `after` → full snapshot (back-compat with today's clients); with `after` → only newer
  messages. Return an explicit response cursor **and** a `reset` signal so the client can
  distinguish "no new messages" from "full snapshot is empty" from "server rotated, re-sync"
  (this conflation is easy to get wrong — flagged in vetting).
- The SSE path's per-connection `seen` set is an in-memory dedupe, **not** a resumable client
  cursor (`stream.go:95-99,174-195`) — design the cursor fresh, don't assume it's already modeled.
- Preserve the "reconnecting snapshot has no messages → keep last good" behavior
  (`usePRReviewConversation.ts:96-101`).

### A3. Stop re-dialing codex every poll
- `readReviewerThread` opens+closes a codex app-server connection per call (`stream.go:28-36`).
  Pooling one client per reviewer endpoint is the goal — BUT (corrected via vetting) the current
  client is **not concurrency-safe**: `CodexClient.Call` does write-then-read-until-own-ID with no
  per-ID demux or mutex (`codex_client.go:281-311`); two concurrent callers on one connection steal
  each other's responses. So this is **"serialize or demux required,"** not "confirm thread-safety."
  Options: (a) a per-endpoint mutex serializing `Call` (simplest; fine at poll cadence), or (b) an
  ID-keyed response mailbox/demux (needed if we later want concurrent viewers to share reads).
- Bound by the existing `reviewerPollTimeout`.

### A4. (fold-in) Efficiency items from the earlier review that touch this path
- `ensureReviewer` builds the workspace twice (`reviewer.go:166-177` + `membership.go:78`) — thread
  the matched `WorkspaceRepo` + `data.Path` out of the first `BuildWorkspaceDataForKey`.
- Per-repo credential re-seal is loop-invariant (`seed.go:49,72` driven by `list.go:81-92`) — hoist
  token+sealer+`sealed` to once per list request.

## 5. Track B — streaming rework (the right end state)

### B0. Auth plumbing (prerequisite — do not skip)
A tokenized reviewer SSE stream will be **rejected by the global auth middleware** unless the route
is handled like `/events`: added to the public-route/SSE exceptions (`middleware/auth.go:137-147,
181-191`, `middleware/auth_routes.go:47-57`) AND validating its own one-time `TokenStore` token at
the handler. Alternatively, carry reviewer deltas over the **existing authed WS transport** the
reviewer terminal already uses (`terminalConnection.ts` `&token=`, `ws.go:225-242`) — a proven
in-repo pattern — rather than standing up a new SSE route. Also handle: one-time token consumed on
first connect means **native `EventSource` auto-reconnect fails** (the URL token is spent);
`WorkspaceSSEClient` works around this by manually reconnecting and minting a fresh token each time
(`sse.ts:140-182,328-367`) — reuse that client, don't hand-roll EventSource. Note `realtime.Hub` is
**not** a drop-in: it's a workspace-mutation hub (`MutationPayload`, ws/source-repo filters,
catch-up over stored mutations, `hub.go:40-59,191-205`) — usable for a "conversation changed"
invalidation ping, not for carrying message deltas without extending its event model.

### B1. Runtime-owned conversation API
- Define, in the runtime layer, an incremental read seam: "messages for session S since cursor C"
  (+ state). codex impl lives in `leadcontrol` (it owns `CodexThread`/`ReadThreadWithTurns` and the
  runtime-metadata keys); harness impl lives behind the `harnessTranscriptReader` seam so prreview
  never type-asserts to `*claudecode.Reader` nor re-derives `~/.claude/projects`.
- prreview consumes only this seam. This retires the `harness_read.go:135` assertion and the
  `reviewer.go:525` key-prefix matching (the latter becomes a `leadcontrol` responsibility, e.g.
  `RuntimeIdentityMetadataKeys()`).

### B2. Stream over the authed realtime transport
- Serve conversation deltas over the authed transport chosen in B0 (authed SSE via `TokenStore` +
  `WorkspaceSSEClient`, **or** the reviewer WS). NOTE: `useWorkflowRunStreams.ts` is **not** a
  precedent to mirror — it polls under auth. The real SSE-auth precedent is `/events` +
  `WorkspaceSSEClient` (`sse.ts`).
- Frontend `usePRReviewConversation` switches from `setInterval` poll to the streaming subscription,
  preserving the hook's existing staleness (`requestSeqRef`) and stale-subject (409) handling.
- ⚠️ Streaming removes *frontend* polling but **not source read amplification**: today's `/stream`
  still runs `readReviewerSnapshot` once per viewer per second (`stream.go:95-127`). Real cost
  reduction requires B1's shared/incremental reader (one read per PR, fanned out to N viewers), not
  just changing the client transport.

### B3. Retire the poll
- Once B2 is in, `/conversation` becomes a one-shot initial-snapshot fetch (or is removed if the
  stream sends an initial batch). No 1.5 s interval.

## 6. Sequencing

1. Track A (A1 decision → A2 → A3), plus A4 as an easy adjacent win. Ships value immediately,
   low risk, no new transport.
2. Track B after A, as a deliberate follow-up. B1 first (seam), then B2 (transport), then B3
   (retire poll). A2's cursor is forward-compatible with B1's cursor.

## 7. Risks & open questions

- **Structured vs. raw:** the terminal WS carries raw PTY bytes; the panel needs structured,
  redacted messages. B2 is a *new structured channel*, not reuse of the PTY bytes — real work.
- **Codex client is not concurrency-safe** (A3/B1): `CodexClient.Call` has no per-ID demux/mutex
  (`codex_client.go:281-311`). Pooling requires serialize-or-demux; multiple viewers sharing one
  client will corrupt reads without it.
- **Cursor stability spans BOTH backends:** codex turn/item IDs *and* harness IDs — the latter are
  reconstructed from provider/session/event IDs plus ordinals on every full parse
  (`harness_read.go:193-224`), so ordering/stability under incremental reads must be defined for the
  harness path too, not just codex.
- **Redaction must stay server-side** after moving the seam: every snapshot path redacts before
  serving today (`stream.go:151-153`); B1's runtime API must not leak un-redacted text to prreview.
- **One-time token vs. EventSource reconnect** (B0): a spent URL token breaks native EventSource
  auto-reconnect; must use the manual-reconnect + fresh-token client (`sse.ts`).
- **Auth-middleware gate** (B0): any new SSE route is Bearer-only-gated unless explicitly made
  public + self-validating (`auth_routes.go:47-57`).
- **API/OpenAPI/test surface** (A1a): `/stream` is spec'd + tested; deleting or changing it ripples
  into `openapi.yaml`, generated types, and prreview tests.
- **Scale sensitivity:** if this is low-traffic v1, Track A alone may suffice for a while; Track B's
  payoff scales with concurrent viewers × conversation length. **Per-viewer read amplification is
  the real cost driver** and is only fixed by B1's shared reader, not by the transport swap.
- **Loopback/dev (open-auth) mode:** `TokenStore` is nil when `ExtAuthURL` is empty
  (`server.go:82`); whatever replaces `/stream` must still work in open-auth dev.

## 8. Key file references

- `internal/webui/handlers/prreview/stream.go` — `streamReviewer` (dead SSE), `getReviewerConversation` (poll), `readReviewerSnapshot`, `readReviewerThread`
- `internal/webui/handlers/prreview/harness_read.go` — disk transcript read; `*claudecode.Reader` assertion (~:135)
- `internal/webui/handlers/prreview/reviewer.go` — `ensureReviewer`, runtime-metadata prefixes (~:525)
- `internal/webui/handlers/prreview/module.go` — route registration (`:97-104`)
- `internal/webui/frontend/src/hooks/workspace/usePRReviewConversation.ts` — 1.5 s poll
- `internal/webui/frontend/src/hooks/workflows/useWorkflowRunStreams.ts:21-25,237-240` — counter-example: polls under auth (NOT an authed-EventSource precedent)
- `internal/webui/frontend/src/api/common/sse.ts:140-182,328-367` — the real authed-SSE precedent: `WorkspaceSSEClient` (manual reconnect + fresh one-time token)
- `internal/webui/server/realtime/sse_token.go`, `subscription/module.go:42-57`, `server_app.go:234` — sse-token infra, wired for `/events`
- `internal/webui/server/middleware/auth.go:137-147,181-191`, `auth_routes.go:47-57` — global auth gate + public-route/SSE exceptions
- `internal/leadcontrol/codex_client.go:281-311` — `Call` is not concurrency-safe (no per-ID demux)
- `internal/leadcontrol/codex_metadata.go:14-23`, `harness_metadata.go:16-30` — the real owners of the runtime-metadata keys prreview string-matches
- `internal/webui/frontend/src/components/TerminalView/instances/terminalConnection.ts:19-58`, `handlers/terminal/ws.go:225-242` — authed WS via `&token=` (in-repo streaming precedent)

## 9. Vetting changelog (codex gpt-5.5 @ xhigh, claims re-verified against source)

Codex reviewed v1 of this doc; its two most consequential corrections were independently confirmed
in-tree before folding in:
1. **Corrected false claim** — `useWorkflowRunStreams.ts` is NOT an authed-EventSource precedent; it
   falls back to polling when a token is set (`:21-25,237-240`). §2.3, §5.B2, §8 rewritten. The
   authed-SSE pattern that IS real is `/events` + `WorkspaceSSEClient`.
2. **Corrected A3 feasibility** — the codex client can't be pooled/shared as-is; `Call` lacks
   demux/mutex (`codex_client.go:281-311`). A3 reframed to "serialize or demux required."
3. Added §5.B0 (auth plumbing prerequisite), reset/cursor contract to A2, API/test-break caveat to
   A1a, and the missed risks (harness cursor stability, redaction seam, one-time-token reconnect,
   per-viewer read amplification, `realtime.Hub` not a drop-in).

Codex's verdict: *"sound direction, but not sound to execute as written"* — the amendments above
close the gaps it identified. Remaining judgment calls left open for the author: A1a delete-now vs.
A1b keep-as-seed, and SSE-token vs. reviewer-WS as the Track B transport.
