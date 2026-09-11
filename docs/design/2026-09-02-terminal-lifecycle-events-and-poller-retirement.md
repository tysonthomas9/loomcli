# Terminal lifecycle events and frontend poller retirement

Date: 2026-09-02

## Goal

The UI should learn that displayed server state changed from the server that
owns the change. Polling is a repair mechanism, not the normal delivery path.

The first slice makes the NavRail terminal count exact during an established
connection. It emits process-local PTY lifecycle events and changes the 30
second count poll in
`internal/webui/frontend/src/hooks/terminal/useWorkspaceSessionCount.ts` to a
hidden-paused five minute safety poll. Later slices remove the remaining data
pollers without turning the workspace SSE stream into a stream of full domain
objects.

## Existing constraints

- `internal/webui/server/realtime/hub.go` has one workspace-scoped browser
  stream and a generic `MutationPayload`. New consumers use `entity_type`,
  `entity_id`, and `action`; `type` remains a compatibility field. See
  `docs/design/generic-sse-envelope.md`.
- SSE framing remains exclusively in `realtime.Writer`; lifecycle producers
  broadcast payloads and never frame events. See
  `docs/adr/0001-sse-framing-single-writer-seam.md`.
- FleetDB mutations have durable cursors and can be replayed after
  `Last-Event-ID`, but the current catch-up path returns one page (at most 100
  events) and swallows backend/catch-up failures. A direct `Hub.Broadcast` gets
  a connection-local event ID, but is not written to FleetDB and is not
  returned by the catch-up query.
- The main terminal process is owned by `(workspace, session)` in
  `internal/webui/terminal/pty_manager.go`. `MultiPTYManager` selects a
  per-workspace manager. `AgentTmuxManager` only attaches browser PTYs to tmux
  sessions created and owned by the CLI daemon.

## Terminal lifecycle event model

### Envelope

All main-PTY lifecycle events use this shape:

| Transition | `type` | `entity_type` | `entity_id` | `action` | Event fields |
|---|---|---|---|---|---|
| New child successfully spawned | `terminal_session_change` | `terminal` | session name | `terminal.pty_started` | `pty_alive=true`, `kind=pty`, `agent=false` |
| Child exits on its own | `terminal_session_change` | `terminal` | session name | `terminal.pty_exited` | `pty_alive=false`, `exit_reason=exited`, `kind=pty`, `agent=false` |
| Manager terminates a child | `terminal_session_change` | `terminal` | session name | `terminal.pty_killed` | `pty_alive=false`, `exit_reason=killed|shutdown`, `kind=pty`, `agent=false` |

`workspace_id` carries the workspace and is the hub routing key. Therefore
`entity_id` is the session name already used by terminal metadata events, not a
duplicated workspace-qualified key. Consumers identify a terminal by
`(workspace_id, entity_id)`. `timestamp` is UTC RFC3339Nano. Lifecycle events
do not set `issue_id`: PTY liveness does not depend on tab metadata or an issue
association.

`kind` describes the runtime (`pty` now, `agent_tmux` for the future daemon
event). `agent` is explicit even when false so consumers do not have to infer
agent ownership from a missing field. These fields are descriptive; the count
hook still refetches the authoritative tab list and applies its non-agent rule.

Every lifecycle action maps to legacy `terminal_session_change`, so the
existing coarse subscription in `useWorkspaceSessionCount` and
`useIssueSessionMap` continues to fire. New code may subscribe to
`entity_type=terminal` and the three actions. Metadata CRUD continues to use
`terminal_metadata` / `terminal.metadata`; issue-link changes continue to use
`terminal_session_change` / `terminal.session_change`.

### Emission and ordering

`PTYManager` accepts one small `PTYLifecycleObserver` interface. It calls the
observer only for real process transitions:

- after `pty.StartWithSize` succeeds and the session is published in the
  manager map, for both `AttachSession` and `EnsureSession`;
- after `killSession` removes and closes a session, with `pty_exited` for the
  drain goroutine's natural-exit path and `pty_killed` for explicit kill,
  grace expiry, idle reaping, or shutdown;
- never when `AttachSession` finds an existing session, and never for an
  idempotent kill of an unknown key.

Observer calls occur outside manager locks. The observer must return quickly
and must not take ownership of the event. `MultiPTYManager` passes the same
observer to each lazily created per-workspace manager, so registration and
lazy construction cannot lose the workspace scope.

The production adapter lives with the terminal service wiring and converts an
observer event to `realtime.MutationPayload`; the PTY implementation does not
know about the hub. A fake observer is the second adapter and is the test
surface for spawn, natural exit, explicit kill, and reattach.

Attach and detach events are not emitted in this slice. They do not change
`pty_alive` and no production UI reads `attached_clients` after bootstrap. If
that field becomes live UI state, use `terminal.pty_attached` and
`terminal.pty_detached` with `attached_clients`, still mapped to
`terminal_session_change`, and coalesce reconnect churn before broadcast.

`AgentTmuxManager` is not a source of underlying agent-session start events: it
creates only a browser-side `tmux attach-session` child. The daemon code that
creates and terminates the underlying tmux session must emit the equivalent
`terminal.pty_started|exited|killed` envelope with `kind=agent_tmux` and
`agent=true`; browser attach/detach must not masquerade as agent process
lifecycle. This is a later slice and does not affect the non-agent NavRail
count.

### Durability and reconnects

Slice 1 events are live-only `Hub.Broadcast` events. They receive an SSE frame
ID, but are absent from FleetDB mutation history, so a reconnecting client can
miss a PTY transition between disconnect and catch-up. Fetch-on-mount,
visibility refetch, and the five minute safety poll repair that gap. Removing
the safety poll requires either a durable runtime-event journal or a reconnect
contract that always invalidates process-local projections after catch-up. The
recommended default is the latter for volatile PTYs: emit a synthetic
`terminal.runtime_snapshot_required` on each successful SSE connection and
refetch, rather than persisting short-lived process state in FleetDB.

## General replacement for polling

### Server mutation contract

Every server-side mutation of state displayed by the UI must do one of:

1. append a durable generic mutation that the workspace SSE bridge replays;
2. broadcast a process-local invalidation envelope and define reconnect repair;
3. write to a dedicated resumable byte/item stream for append-only data.

At the plan baseline, mutation sites without a complete notification contract
include the following. Slice 1 closes the first item. Recovery/cache repair,
raw Redis/Postgres writes, and boot reconciliation may also change a displayed
projection without appending a mutation, so the contract is not universal:

- PTY spawn/exit/kill in `internal/webui/terminal/pty_manager.go`;
- pending-input open, answer, consume, and expiry in
  `internal/cli/daemon/daemon_input.go`;
- session record/transcript writes in `internal/sessions/` (the attach-only
  `BroadcastSessionIssueEvent` in `realtime/hub.go` is not complete);
- task phase-log appends under `tasks/<id>/<phase>.log`;
- worktree file changes that alter git status or diff stats;
- daemon agent-status transitions that change the monitor/status projection;
- workspace job transitions in `internal/webui/svcimpl/workspace_job_store.go`;
- PR-review agent messages and terminal state;
- external pull-request changes observed by GitHub integration;
- usage and observability aggregate refreshes;
- backend install/login/test completion that changes backend health.

A broadcast is an invalidation, not a second source of truth. Handlers keep
their GET endpoint authoritative. Event fan-out work must be O(clients) with a
small payload; expensive reads or aggregate computation happen once before
broadcast, never once per client.

### Shared frontend primitive (slice 2 complete)

`hooks/common/useInvalidatedQuery.ts` exposes:

```ts
useInvalidatedQuery(fetcher, {
  key, enabled, entityTypes, actions, types, debounceMs, safetyPollMs,
  pauseWhenHidden, refetchOnConnect, resetOnKeyChange,
})
```

`fetcher` receives an `AbortSignal` and the result is
`{data, loading, error, refetch}`. A provider-owned
`InvalidatedQueryRegistry` gives equal keys one immutable cached snapshot, one
debounce, one optional safety poll, one visibility listener, and one request;
per-instance registrations keep committed fetchers and count enabled owners.
Disabling the last enabled owner aborts and settles pending work, while a
disabled registration can still issue a one-off `refetch`.

Events are forwarded unfiltered from `EventProvider` and match a non-empty
`entity_type` first (entity and optional action must match; a caller that sets
only `types` matches entity-typed events by coarse type); only entity-less
events use the global `refresh` fallback. The registry records the
provider's `connectionEpoch`, seeded before the mount fetch; every completed
SSE handshake increments the epoch, and a changed epoch debounces one repair
fetch. Hidden events/polls become dirty and visibility always performs one
repair fetch. Blocked issues use `entityTypes: ["issue", "dependency", "label"]`,
`safetyPollMs: 5 * 60_000`, and retain prior data on key changes.

The primitive deepens the repeated shape in `useIssueSessionMap` and
`useWorkspaceSessionCount`, but those hooks do not themselves provide the
primitive's complete visibility or stale-response guarantees. It does not own
endpoint-specific selection or merge rules. Transcript/log append streams and
aggregate snapshot streams use separate primitives.

Slice 2 follow-ups are replay pagination and catch-up failure surfacing in
`fleet_batch_mutations.go` and `backend_subscriber.go`; aligning `/blocked`
with the stream's source-repo scope; fixing `undefined ↔ array` source-repo
rebinding in `useEventProvider.tsx`; and reconciling `BlockedIssue` blocker
details with the wire type; documenting that the `parent_id` filter is
direct-parent matching rather than descendant matching.

Live verification of slice 2 (two browser passes on a local-mode stack) found
three pre-existing transport defects that every later slice inherits and that
gate the zero-poll cutover:

- `internal/webui/subscription/backend_subscriber.go` re-creates a workspace
  subscriber with cursor `0` after the idle deactivation in `multi.go`, so the
  next SSE client triggers a replay of the whole FleetDB mutation stream to
  every connected client.
- That backlog overflows the new client's send buffer, and
  `realtime/hub.go` `broadcastToClients` evicts it; the browser reconnects a
  second later, so a cold load pays two handshakes (and, for invalidated
  queries, a third repair fetch).
- `internal/webui/frontend/src/api/common/sse.ts` `connect()` returns without
  `scheduleReconnect()` when the token exchange fails (a 502 while the server
  restarts); the tab never reconnects, `retryNow()` is inert, and the Monitor
  banner does not reflect it because it follows the agent-status poll. Until
  this is fixed, the safety poll is the only repair after a server restart.

### Data-source classes

- **Invalidate and refetch:** tab metadata/count, blocked issues, task session
  lists, workspace data, agent status, pending input, backend status, jobs.
  Events are small and the existing GET remains authoritative.
- **True stream:** active session transcripts, task phase logs, workflow run
  progress, and PR-review conversation. Send ordered chunks/items with a
  cursor and resume token; do not refetch the growing transcript or full log.
- **Server-computed aggregate:** git status/diff stats, usage, observability
  metrics, and external PR lists. Push a changed/snapshot event after a
  mutation or compute once on a server-owned cadence and broadcast it. N open
  browsers must not each run git, scan transcripts, query telemetry, or invoke
  `gh` on their own cadence.

## Poller inventory

This inventory covers domain-data timers under `src/`, including store and
component-local pollers found from hook consumers. It excludes UI clocks,
debounces, request timeouts, toast timers, stale-age timers, and retry
countdowns.

| Order | Poller | Current interval | Endpoint / source | Event-driven change | Difficulty |
|---:|---|---|---|---|---|
| 1 | `useWorkspaceSessionCount` | 30s -> 5m safety | `GET .../terminal/tabs`; Redis tab metadata + PTY manager | PTY lifecycle envelopes; 5m repair poll | S |
| 2 | `useBlockedIssues` | 5m hidden-paused safety | `GET .../blocked`; FleetDB issues, dependencies, parent propagation, and labels | invalidate on delivered issue/dependency/label actions; retain repair poll until replay/scope gaps close | S |
| 3 | `useTaskSessions` | 10s / 3s active | `GET .../tasks/{id}/sessions`; session index | complete `session.*` events at every record transition | M |
| 4 | `usePendingInput` | 5s | `GET .../agents/{name}/input`; daemon registry | bridge open/answer/consume/expire as `agent.pending_input_changed` | M |
| 5 | `agentStore` via `useStoreContext`; active-run refresh in `AgentsPage` | 30s / 5s active | `GET .../status`; daemon + issue projection | invalidate on complete agent/session/workspace/run mutations | M |
| 5 | `workspaceStore`, `useWorkspace` | 60s | `GET .../workspaces/{ws}`; workspace config/agents | workspace and agent invalidations | M |
| 6 | `useGitStatus`, `AgentStatusBadge` | 5s / 30s | `GET .../agents/{name}/git/status`; worktree | server-owned git watcher/cadence broadcasts aggregate change | H |
| 6 | `useAgentDiffStat`, `useIssueDiffStat` | 60s / 30s | agent/issue `git/diff-stat`; worktree | same git aggregate keyed by agent; issue maps through assignee | H |
| 7 | `useSessionTranscript` | 3s active | `GET .../sessions/{id}/transcript`; transcript file/store | resumable ordered transcript-entry stream | H |
| 8 | `useTaskLogPolling` | 2s default, 500ms consumer | `GET .../logs/{phase}`; phase log file | resumable byte stream using existing log framing seam | H |
| 9 | `useWorkflowRunStreams` fallback | 5s | `GET .../runs/{id}`; FleetDB run | make run stream resumable and snapshot-complete | M |
| 10 | `usePRReviewConversation` | 1.5s | PR-review conversation GET; review run/messages | ordered review-message/run-state stream | H |
| 11 | `useJobPolling` | 1s then 2s | workspace-job GET; in-memory job store | job state/progress invalidation or per-job stream | M |
| 12 | `usePullRequests` | 30s, backs off to 5m | `GET .../pull-requests`; GitHub via `gh` | webhook plus server-owned fallback cadence, one broadcast | H |
| 13 | `useUsage` | 30s | `GET /api/monitor/usage`; session usage | server aggregate snapshot/change event | M |
| 13 | `useObservabilityMetrics` | 30s | `GET /api/observability/metrics`; telemetry aggregate | use/extend `/api/observability/events` to push snapshots | M |
| 14 | `TerminalView` backend-status burst | 5s for 2m | backend health GET; installed CLI/auth | setup process emits `backend.status_changed` | M |

`useWorkspaceHealth`, `useWorkspaceRepos`, and `usePollingWithBackoff` schedule
retries only after transport failure. They cannot be replaced by a server event
while the server is unreachable. Keep them, rename/document them as recovery
probes, and ensure only one probe loop exists per browser. The workspace
creation job loop is not a recovery probe and remains in the retirement list.

## User-testable slices

1. **Terminal count:** open two non-agent tabs, close one, run `exit` in the
   other, and reconnect its WebSocket. The badge changes immediately for real
   spawn/kill/exit and does not increment on reattach; hide the document past a
   timer tick and confirm no safety request until visible.
2. **Blocked projection + shared primitive (slice 2 complete):** add/remove a
   dependency and close a blocker from a second browser tab. The blocked
   summary updates once per delivered event burst, with one `/blocked` request
   shared by equal-key consumers; the five-minute repair poll remains.
3. **Task sessions:** start and finish a task run while its issue panel is open.
   The session row and active marker update with timer polling disabled,
   including a run started by another client.
4. **Pending input:** make an agent ask, answer from a second tab, and let a
   prompt expire. The banner appears/disappears on each transition without a
   five-second request loop.
5. **Workspace and monitor projections:** create/delete an agent and transition
   a run while Home and Agents are open in two tabs. Both update; one SSE
   reconnect triggers one authoritative snapshot.
6. **Git aggregates:** edit/commit/reset in a managed worktree. Status, PR badge,
   agent diff, and issue diff update from one server computation, verified with
   two browser tabs and no multiplied git commands.
7. **Session transcript:** watch an active run. Entries append once, in order;
   disconnect/reconnect resumes without duplicates or a full-transcript fetch.
8. **Task logs:** watch both phases across rotation/truncation and reconnect.
   Chunks resume or reset explicitly; no 500ms/2s snapshot requests remain.
9. **Workflow runs:** disconnect mid-run, reconnect, and observe every state
   transition through terminal state with fallback polling disabled.
10. **PR-review conversation:** send a message and watch streamed assistant
    messages and terminal state across reconnect, without 1.5s GETs.
11. **Workspace jobs:** create a workspace with a slow clone. Progress and the
    final redirect arrive through the job event/stream; refresh resumes safely.
12. **Pull requests:** change a PR externally and locally publish a branch.
    Both tabs update from webhook/server cadence while only the server calls
    GitHub.
13. **Aggregates:** generate usage and telemetry while two dashboards are open.
    Both receive the same server snapshot and client count does not change
    computation/query frequency.
14. **Backend status:** run an install/login/test flow. The picker updates on
    process completion and makes no five-second burst afterward.

Each slice lands server emission and frontend consumption together. A poll is
removed only after tests cover initial fetch, relevant mutation, key change,
hidden state, stale responses, and reconnect repair.

## Risks and controls

- **Missed live events:** keep a minutes-scale hidden-paused safety poll until
  reconnect invalidation or durable replay is proven for that source.
- **Replay gaps:** durable cursors cover FleetDB mutations only. Live-only
  sources must send a reconnect snapshot-required event or have their own
  resumable stream.
- **Multiple browser tabs:** each tab has one EventSource, but broadcasts must
  not cause per-client server computation. Debounce invalidations in each tab
  and compute aggregates once.
- **Unloaded entities:** invalidations are hints. Stores may mark a query key
  dirty without fetching until that entity/query is mounted.
- **Fan-out cost:** payload construction is O(1); hub delivery is O(clients).
  Coalesce noisy filesystem/telemetry changes and never attach transcript/log
  bodies to the generic mutation stream.
- **Event storms and stale responses:** debounce by query key, serialize fetches
  with one trailing invalidation, and discard results from old generations.

## Open questions and recommended defaults

1. **How should volatile process events repair reconnects?** Default to a
   connection-time `terminal.runtime_snapshot_required` invalidation. Do not
   persist PTY process state in FleetDB solely for replay.
2. **Can a main `PTYManager` session represent `TabMetadata.kind=agent`?** The
   manager currently has no authoritative kind. Default its lifecycle payload
   to `kind=pty, agent=false` and keep consumers refetching metadata; add kind
   to the launch interface only if an owning use case exists.
3. **Who emits agent tmux lifecycle?** Default to the daemon that creates and
   kills the underlying tmux session. `AgentTmuxManager` may emit separate
   viewer attach events later, but not process-start events.
4. **Are bounded exit reasons sufficient?** Default to
   `exited|killed|shutdown` for invalidation. Add nullable `exit_code` and
   `signal` only when the UI has a concrete diagnostic use.
5. **Where does the git aggregate cadence live?** Default to one
   workspace-scoped server watcher with debounce and a slow reconciliation
   scan. Do not run git from hub fan-out or per-client handlers.
6. **What is the external PR freshness target without webhooks?** Default to a
   server-owned 60 second active-workspace cadence, backed off when no clients
   subscribe; webhook delivery supersedes it when configured.
7. **Should observability use workspace SSE?** Default to the existing global
   `/api/observability/events` stream because the metrics endpoint is global;
   keep workspace mutation traffic on the workspace stream.
8. **When can safety polls reach zero?** Default gate: replay is paginated and
   catch-up failures are surfaced or repaired, query and stream scopes align,
   a reconnect test proves an authoritative snapshot or cursor replay, a
   two-tab test proves no missed mutation, and production metrics show no
   dropped-broadcast repair reliance.

## Transport repair 1: browser reconnect on fetch-event-source (2026-09-02)

This repair is client-only. `@microsoft/fetch-event-source` owns the retry
loop, timer, and `Last-Event-ID`; the wrapper supplies its 1 second doubling,
30 second capped backoff. A custom fetch exchanges a fresh token per attempt.
Token 401/403 is fatal and enters the error path; a token 404 or `disabled`
response means open mode, so the stream opens without a token. Every other
token, HTTP, network, or stream failure retries. The wrapper
intentionally overrides the server's `retry:` directive so all failures use
one policy. Duplicate `connected` frames are ignored within one connection.

This fixes a dead-tab path in the old client: if token exchange failed while
the server restarted, `connect()` returned without `scheduleReconnect()`,
leaving neither a stream nor a retry.

Live proof stopped and started the Loom container with Podman. The existing
tab reconnected without intervention, each tab held one stream, and each
delivered mutation batch caused one refetch.

This repair does not fix server cursor-0 replay on subscriber (re)activation,
64-slot client-buffer eviction, or the `OnAuthenticated`-before-`RegisterClient`
race. A live `Last-Event-ID` regression remains server work: cursor-0 replay
delivers the oldest frames first; the tab consumed about 64 before hub
eviction, moved its tracked ID about 7.8 hours backward, and every reconnect
then began unbounded catch-up. The client deliberately honors every `id:` line.

`go-sse` was evaluated for the server hub and rejected: publish is synchronous
and its topic model does not fit per-client cursors. The custom provider had
already reimplemented the hub, so this repair leaves that server design alone.

## Transport repair 2a: subscriber starts at the head, ordered handshake (2026-09-03, PR #610)

Server side of the transport repair; companion FleetDB PR BrowserOperator/fleet-db#227 (optional).

What was wrong (all verified live on 2026-09-02): the workspace subscriber kept its cursor in memory and restarted at `0` on every re-activation, replaying the whole FleetDB stream through the hub (2741 events by the evening); loom discarded the `cursor`/`has_more` fields FleetDB returns; the 64-slot client buffer overflowed within milliseconds and the only tab was evicted, so cold loads did two handshakes and the tab's Last-Event-ID regressed by hours; the token route activated the subscriber before any client existed; registration was fire-and-forget; catch-up was one unpaged query with swallowed errors.

What changed: a `MutationPage{Events, Cursor, HasMore}` contract through backend, Fleet client, subscriber, `multi` and handler; the subscriber probes `since=$` (FleetDB #227) and otherwise drains silently from the last known head, capped, with a cap hit failing readiness; a `Ready` barrier that `EnsureActive` waits on outside its mutex; cursor classes `$`, `c1.` tokens and wrapped numeric synthetic ids, anything else an error; the handshake registers the client synchronously, activates, pages the catch-up (10 pages / 5 s) and dedupes catch-up cursors against live frames for the connection; cap, backend error or FleetDB 410 answer HTTP 503 `resync_required` before any frame; the token route no longer activates; `Broadcast` short-circuits with no client; the writer gained a response-controller seam with per-frame flush error propagation.

Proven live on a local-mode stack, both FleetDB flavours (`.scratch/terminal-lifecycle/bv-step2/`): cold load after idle = one activation (head probe with #227, silent drain without it), zero replay pages, zero buffer-full lines, one registration, two blocked-list fetches; an hour-old cursor replays 286 / 322 frames before `connected`; a garbage cursor answers 503 `error`, a cursor older than the retention floor answers 503 `expired`; a restart while a tab is open recovers with a bounded catch-up; one refetch per delivered mutation frame.

Next (PR B, `feat/sse-resync-frame`): in-stream `resync` frames with per-client sequence numbers instead of the 503s and instead of eviction on overflow, writer deadlines, 256-slot buffer, client `onResync` handling and the four consumers that only react to reconnect state.
